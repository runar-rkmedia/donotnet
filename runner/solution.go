package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// runSolutionCommand runs a dotnet command on the entire solution instead of individual projects.
// This avoids parallel build conflicts when projects share dependencies.
func (r *Runner) runSolutionCommand(ctx context.Context, sln *project.Solution, projects, cached []*project.Project, argsHash, argsForCache string) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startTime := time.Now()

	if !r.opts.Quiet {
		var parts []string
		parts = append(parts, fmt.Sprintf("Running %s on solution %s", r.opts.Command, filepath.Base(sln.RelPath)))
		parts = append(parts, fmt.Sprintf("%d projects", len(projects)))
		if len(cached) > 0 {
			parts = append(parts, fmt.Sprintf("%d cached", len(cached)))
		}
		displayArgs := filterDisplayArgs(r.opts.DotnetArgs)
		if len(displayArgs) > 0 {
			argsStr := strings.Join(displayArgs, " ")
			if term.IsPlain() {
				parts = append(parts, argsStr)
			} else {
				parts = append(parts, term.ColorYellow+argsStr+term.ColorReset)
			}
		}
		term.Printf("%s...\n", strings.Join(parts, ", "))
	}

	// Build command args
	slnPath := filepath.Join(r.gitRoot, sln.RelPath)
	args := []string{r.opts.Command, slnPath, "--property:WarningLevel=0", "-clp:ErrorsOnly"}
	if r.opts.Coverage && r.opts.Command == "test" {
		args = append(args, "--collect:XPlat Code Coverage")
	}
	args = append(args, r.opts.DotnetArgs...)

	cmd := exec.CommandContext(ctx, "dotnet", args...)
	setupProcessGroup(cmd)

	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.Dir = r.gitRoot

	if term.IsPlain() {
		cmd.Env = os.Environ()
	} else {
		cmd.Env = append(os.Environ(),
			"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
			"TERM=xterm-256color",
		)
	}

	term.Verbose("  dotnet %s", term.ShellQuoteArgs(args))

	err := cmd.Run()
	duration := time.Since(startTime)
	outputStr := output.String()
	success := err == nil

	if err != nil {
		term.Verbose("  command error: %v", err)
		if outputStr == "" {
			outputStr = fmt.Sprintf("Command failed: %v\n", err)
		}
	}

	// Save console output if reports enabled
	if !r.opts.NoReports {
		consolePath := filepath.Join(r.reportsDir, filepath.Base(sln.RelPath)+".log")
		os.WriteFile(consolePath, []byte(outputStr), 0644)
	}

	stats := extractTestStats(outputStr)

	if !r.opts.Quiet {
		if success {
			if stats != "" {
				term.Printf("  %s✓%s %s %s  %s\n", term.ColorGreen, term.ColorReset, filepath.Base(sln.RelPath), duration.Round(time.Millisecond), stats)
			} else {
				term.Printf("  %s✓%s %s %s\n", term.ColorGreen, term.ColorReset, filepath.Base(sln.RelPath), duration.Round(time.Millisecond))
			}
		} else {
			if stats != "" {
				term.Printf("  %s✗%s %s %s  %s\n", term.ColorRed, term.ColorReset, filepath.Base(sln.RelPath), duration.Round(time.Millisecond), stats)
			} else {
				term.Printf("  %s✗%s %s %s\n", term.ColorRed, term.ColorReset, filepath.Base(sln.RelPath), duration.Round(time.Millisecond))
			}
			term.Printf("\n%s\n", outputStr)
		}
	}

	// Mark cache for all projects in the solution
	now := time.Now()
	for _, p := range projects {
		relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
		contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
		key := cache.MakeKey(contentHash, argsHash, p.Path)
		r.db.Mark(key, now, success, nil, argsForCache)
	}

	if !r.opts.Quiet {
		succeeded := 0
		if success {
			succeeded = len(projects)
		}
		term.Summary(succeeded, len(projects), len(cached), duration.Round(time.Millisecond), success)
	}

	return success
}

// runSolutionGroups runs multiple solution builds in parallel, then runs remaining projects.
func (r *Runner) runSolutionGroups(ctx context.Context, slnGroups map[*project.Solution][]*project.Project, remaining []*project.Project, cached []*project.Project, argsHash, argsForCache string) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startTime := time.Now()

	// Count total projects
	totalSolutionProjects := 0
	for _, projs := range slnGroups {
		totalSolutionProjects += len(projs)
	}
	totalProjects := totalSolutionProjects + len(remaining)

	if !r.opts.Quiet {
		var parts []string
		parts = append(parts, fmt.Sprintf("Running %s on %d solutions (%d projects)", r.opts.Command, len(slnGroups), totalSolutionProjects))
		if len(remaining) > 0 {
			parts = append(parts, fmt.Sprintf("+%d individual", len(remaining)))
		}
		if len(cached) > 0 {
			parts = append(parts, fmt.Sprintf("%d cached", len(cached)))
		}
		displayArgs := filterDisplayArgs(r.opts.DotnetArgs)
		if len(displayArgs) > 0 {
			argsStr := strings.Join(displayArgs, " ")
			if term.IsPlain() {
				parts = append(parts, argsStr)
			} else {
				parts = append(parts, term.ColorYellow+argsStr+term.ColorReset)
			}
		}
		term.Printf("%s...\n", strings.Join(parts, ", "))
	}

	// Run solution builds in parallel
	type slnResult struct {
		sln      *project.Solution
		projects []*project.Project
		success  bool
		output   string
		duration time.Duration
	}

	slnResults := make(chan slnResult, len(slnGroups))
	var wg sync.WaitGroup

	numWorkers := r.opts.EffectiveParallel()
	if numWorkers <= 0 {
		numWorkers = runtime.GOMAXPROCS(0)
	}
	if numWorkers > len(slnGroups) {
		numWorkers = len(slnGroups)
	}

	slnJobs := make(chan struct {
		sln   *project.Solution
		projs []*project.Project
	}, len(slnGroups))

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range slnJobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				slnStart := time.Now()
				slnPath := filepath.Join(r.gitRoot, job.sln.RelPath)
				args := []string{r.opts.Command, slnPath, "--property:WarningLevel=0", "-clp:ErrorsOnly"}
				if r.opts.Coverage && r.opts.Command == "test" {
					args = append(args, "--collect:XPlat Code Coverage")
				}
				args = append(args, r.opts.DotnetArgs...)

				cmd := exec.CommandContext(ctx, "dotnet", args...)
				setupProcessGroup(cmd)

				var output strings.Builder
				cmd.Stdout = &output
				cmd.Stderr = &output
				cmd.Dir = r.gitRoot

				if term.IsPlain() {
					cmd.Env = os.Environ()
				} else {
					cmd.Env = append(os.Environ(),
						"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
						"TERM=xterm-256color",
					)
				}

				term.Verbose("  dotnet %s", term.ShellQuoteArgs(args))
				err := cmd.Run()

				slnResults <- slnResult{
					sln:      job.sln,
					projects: job.projs,
					success:  err == nil,
					output:   output.String(),
					duration: time.Since(slnStart),
				}
			}
		}()
	}

	for sln, projs := range slnGroups {
		slnJobs <- struct {
			sln   *project.Solution
			projs []*project.Project
		}{sln, projs}
	}
	close(slnJobs)

	go func() {
		wg.Wait()
		close(slnResults)
	}()

	// Collect solution results
	slnSucceeded := 0
	slnFailed := 0
	var failedOutputs []string

	for res := range slnResults {
		if !r.opts.NoReports {
			consolePath := filepath.Join(r.reportsDir, filepath.Base(res.sln.RelPath)+".log")
			os.WriteFile(consolePath, []byte(res.output), 0644)
		}

		stats := extractTestStats(res.output)

		if !r.opts.Quiet {
			if res.success {
				if stats != "" {
					term.Printf("  %s✓%s %s %s  %s\n", term.ColorGreen, term.ColorReset, filepath.Base(res.sln.RelPath), res.duration.Round(time.Millisecond), stats)
				} else {
					term.Printf("  %s✓%s %s %s\n", term.ColorGreen, term.ColorReset, filepath.Base(res.sln.RelPath), res.duration.Round(time.Millisecond))
				}
			} else {
				if stats != "" {
					term.Printf("  %s✗%s %s %s  %s\n", term.ColorRed, term.ColorReset, filepath.Base(res.sln.RelPath), res.duration.Round(time.Millisecond), stats)
				} else {
					term.Printf("  %s✗%s %s %s\n", term.ColorRed, term.ColorReset, filepath.Base(res.sln.RelPath), res.duration.Round(time.Millisecond))
				}
				failedOutputs = append(failedOutputs, fmt.Sprintf("=== %s ===\n%s", filepath.Base(res.sln.RelPath), res.output))
			}
		}

		now := time.Now()
		for _, p := range res.projects {
			relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
			contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)
			r.db.Mark(key, now, res.success, nil, argsForCache)
		}

		if res.success {
			slnSucceeded += len(res.projects)
		} else {
			slnFailed += len(res.projects)
			if !r.opts.KeepGoing {
				cancel()
				term.Printf("\n%s\n", res.output)
				term.Summary(slnSucceeded, totalProjects, len(cached), time.Since(startTime).Round(time.Millisecond), false)
				return false
			}
		}
	}

	select {
	case <-ctx.Done():
		return false
	default:
	}

	// Run remaining individual projects
	if len(remaining) > 0 {
		if !r.opts.Quiet {
			term.Printf("\nRunning %d individual projects...\n", len(remaining))
		}
		projSuccess := r.runProjects(ctx, remaining, nil, argsHash)
		if !projSuccess {
			return false
		}
		slnSucceeded += len(remaining)
	}

	if len(failedOutputs) > 0 && r.opts.KeepGoing && !r.opts.Quiet {
		term.Printf("\n--- Solution Failure Output ---\n")
		for _, o := range failedOutputs {
			term.Printf("\n%s\n", o)
		}
	}

	if !r.opts.Quiet && len(remaining) == 0 {
		term.Summary(slnSucceeded, totalProjects, len(cached), time.Since(startTime).Round(time.Millisecond), slnFailed == 0)
	}

	return slnFailed == 0
}
