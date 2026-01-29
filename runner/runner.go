package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/suggestions"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testfilter"
)

// Runner executes test and build commands.
type Runner struct {
	opts *Options

	// Computed state
	gitRoot        string
	scanRoot       string
	cacheDir       string
	reportsDir     string
	projects       []*project.Project
	solutions      []*project.Solution
	graph          map[string][]string
	forwardGraph   map[string][]string
	projectsByPath map[string]*project.Project
	db             *cache.DB
}

// New creates a new Runner with the given options.
func New(opts *Options) *Runner {
	return &Runner{opts: opts}
}

// Run executes the command.
func (r *Runner) Run(ctx context.Context) error {
	// Setup terminal
	term.SetVerbose(r.opts.Verbose)
	term.SetQuiet(r.opts.Quiet)
	if r.opts.NoProgress {
		term.SetProgress(false)
	}

	// Auto-quiet for informational dotnet args (e.g. --list-tests, --help)
	if shouldAutoQuiet(r.opts.DotnetArgs) {
		term.SetQuiet(true)
		r.opts.PrintOutput = true
	}

	// Find git root
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	r.gitRoot, err = git.FindRootFrom(cwd)
	if err != nil {
		return fmt.Errorf("finding git root: %w", err)
	}

	// Scan root: current dir if local, otherwise git root
	r.scanRoot = r.gitRoot
	if r.opts.Local {
		r.scanRoot = cwd
	}

	// Setup cache
	r.cacheDir = r.opts.CacheDir
	if r.cacheDir == "" {
		r.cacheDir = filepath.Join(r.gitRoot, ".donotnet")
	}
	os.MkdirAll(r.cacheDir, 0755)
	r.reportsDir = filepath.Join(r.cacheDir, "reports")

	cachePath := filepath.Join(r.cacheDir, "cache.db")
	r.db, err = cache.Open(cachePath)
	if err != nil {
		return fmt.Errorf("opening cache: %w", err)
	}
	defer r.db.Close()

	// Find projects and solutions
	r.projects, err = project.FindProjects(r.scanRoot, r.gitRoot)
	if err != nil {
		return fmt.Errorf("finding projects: %w", err)
	}
	term.Verbose("Found %d projects", len(r.projects))

	r.solutions, err = project.FindSolutions(r.scanRoot, r.gitRoot)
	if err != nil {
		return fmt.Errorf("finding solutions: %w", err)
	}
	term.Verbose("Found %d solutions", len(r.solutions))

	// Build dependency graphs
	r.graph = project.BuildDependencyGraph(r.projects)
	r.forwardGraph = project.BuildForwardDependencyGraph(r.projects)

	// Build project lookup
	r.projectsByPath = make(map[string]*project.Project)
	for _, p := range r.projects {
		r.projectsByPath[p.Path] = p
	}

	// Handle per-test coverage build (separate flow from normal test/build)
	if r.opts.CoverageBuild {
		var testProjects []*project.Project
		for _, p := range r.projects {
			if p.IsTest {
				testProjects = append(testProjects, p)
			}
		}
		if len(testProjects) == 0 {
			term.Dim("No test projects found")
			return nil
		}
		coverage.BuildPerTestCoverageMaps(coverage.BuildOptions{
			GitRoot:      r.gitRoot,
			Projects:     testProjects,
			ForwardGraph: r.forwardGraph,
			MaxJobs:      r.opts.EffectiveParallel(),
			Granularity:  coverage.ParseGranularity(r.opts.CoverageGranularity),
			Ctx:          ctx,
			Cache:        newTestListCache(r.db, r.gitRoot, r.forwardGraph),
		})
		return nil
	}

	// Always fetch dirty files (used for test filtering even without VCS mode)
	dirtyFiles := git.GetDirtyFiles(r.gitRoot)
	if len(dirtyFiles) > 0 {
		term.Verbose("Dirty files: %d", len(dirtyFiles))
	}

	// Get VCS state
	var vcsChangedFiles []string
	useVcsFilter := r.opts.VcsChanged || r.opts.VcsRef != ""

	if useVcsFilter {
		if r.opts.VcsRef != "" {
			vcsChangedFiles, err = git.GetChangedFiles(r.gitRoot, r.opts.VcsRef)
			if err != nil {
				return err
			}
			if len(vcsChangedFiles) == 0 {
				term.Dim("No changes vs %s", r.opts.VcsRef)
				return nil
			}
			term.Verbose("VCS filter: changes vs %s (%d files)", r.opts.VcsRef, len(vcsChangedFiles))
		} else {
			vcsChangedFiles = dirtyFiles
			if len(vcsChangedFiles) == 0 {
				term.Dim("No uncommitted changes")
				return nil
			}
			term.Verbose("VCS filter: uncommitted changes (%d files)", len(vcsChangedFiles))
		}
	}

	// Compute args hash (include coverage flag so coverage runs get separate cache keys)
	hashInput := append([]string{r.opts.Command}, r.opts.DotnetArgs...)
	if r.opts.Coverage {
		hashInput = append(hashInput, "--coverage")
	}
	argsHash := HashArgs(hashInput)

	// Find changed projects
	changed := r.findChangedProjects(argsHash, vcsChangedFiles, useVcsFilter)
	term.Verbose("Changed projects: %d", len(changed))

	// Find affected projects
	affected := project.FindAffectedProjects(changed, r.graph, r.projects)

	// Filter to target projects
	var targetProjects []*project.Project
	var cachedProjects []*project.Project

	for _, p := range r.projects {
		// Filter by type for test command
		if r.opts.Command == "test" && !p.IsTest {
			continue
		}

		if !affected[p.Path] {
			cachedProjects = append(cachedProjects, p)
			continue
		}
		targetProjects = append(targetProjects, p)
	}

	// Handle --failed: filter to only previously-failed projects with per-test filters
	if r.opts.Failed {
		failedEntries := r.db.GetFailed(argsHash)
		if len(failedEntries) == 0 {
			term.Info("--failed: No previously failed projects found in cache.")
			term.Info("         This happens when tests haven't been run yet, or all tests passed.")
			term.Info("         Running all affected tests instead.\n")
		} else {
			failedPaths := make(map[string]bool)
			r.opts.FailedTestFilters = make(map[string]string)
			for _, entry := range failedEntries {
				failedPaths[entry.ProjectPath] = true
				if p, ok := r.projectsByPath[entry.ProjectPath]; ok {
					filter := getFailedTestFilter(entry.Output, r.reportsDir, p.Name)
					if filter != "" {
						r.opts.FailedTestFilters[entry.ProjectPath] = filter
						term.Verbose("  %s: filtering to %d failed tests", p.Name, strings.Count(filter, "|")+1)
					}
				}
			}

			var failedProjects []*project.Project
			for _, p := range targetProjects {
				if failedPaths[p.Path] {
					failedProjects = append(failedProjects, p)
				}
			}

			if len(failedProjects) == 0 {
				term.Info("--failed: Previously failed projects no longer exist or are not affected.")
				term.Info("         Running all affected tests instead.\n")
			} else {
				filterCount := 0
				for range r.opts.FailedTestFilters {
					filterCount++
				}
				if filterCount > 0 {
					term.Info("Running %d previously failed project(s) with test-level filtering", len(failedProjects))
				} else {
					term.Info("Running %d previously failed project(s)", len(failedProjects))
				}
				targetProjects = failedProjects
				cachedProjects = nil
			}
		}
	}

	// Find untested projects (non-test projects with no test project referencing them)
	// and add them as build-only targets so we at least verify compilation.
	if r.opts.Command == "test" {
		untestedProjects := project.FindUntestedProjects(r.projects, r.forwardGraph)
		if len(untestedProjects) > 0 {
			buildArgsHash := HashArgs(append([]string{"build"}, filterBuildArgs(r.opts.DotnetArgs)...))
			r.opts.BuildOnlyProjects = make(map[string]bool)
			var untestedNames []string
			for _, p := range untestedProjects {
				if !affected[p.Path] {
					continue
				}
				// Re-check cache with build-specific hash
				relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
				contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
				key := cache.MakeKey(contentHash, buildArgsHash, p.Path)
				if !r.opts.Force && r.db.Lookup(key) != nil {
					cachedProjects = append(cachedProjects, p)
					continue
				}
				r.opts.BuildOnlyProjects[p.Path] = true
				targetProjects = append(targetProjects, p)
				untestedNames = append(untestedNames, p.Name)
			}
			if len(untestedNames) > 0 {
				term.Warnf("%d project(s) have no tests, will build instead: %s", len(untestedNames), strings.Join(untestedNames, ", "))
			}
		}
	}

	// Set up test filter for non-watch mode (same filtering as watch mode).
	// Skip when --force is used since that means "run everything".
	if r.opts.Command == "test" && len(dirtyFiles) > 0 && !r.opts.Force && !r.opts.Watch {
		testCovMaps := loadAllTestCoverageMaps(r.cacheDir)
		if len(testCovMaps) > 0 {
			term.Verbose("Loaded per-test coverage for %d project(s)", len(testCovMaps))
		}
		tf := testfilter.NewTestFilter()
		tf.SetCoverageMaps(testCovMaps)
		tf.SetHeuristics(testfilter.ParseHeuristics(r.opts.Heuristics))

		// Map dirty files to their owning projects
		for _, f := range dirtyFiles {
			for _, p := range r.projects {
				if strings.HasPrefix(f, p.Dir+"/") || strings.HasPrefix(f, p.Dir+string(os.PathSeparator)) {
					tf.AddChangedFile(p.Path, f)
					break
				}
			}
		}
		r.opts.TestFilter = tf
	}

	// Show suggestions (unless suppressed) — before watch/cached paths that return early
	if !r.opts.NoSuggestions {
		suggestions.Print(suggestions.Run(r.projects))
		if r.opts.Command == "test" {
			suggestions.PrintOnce(suggestions.CheckCoverage(r.gitRoot, r.opts.StalenessCheck))
		}
	}

	// Watch mode: run initial build/test if needed, then start watching
	if r.opts.Watch {
		if len(targetProjects) > 0 {
			r.runProjects(ctx, targetProjects, cachedProjects, argsHash)
		} else if !r.opts.Quiet {
			term.Dim("No affected projects to %s (%d cached)%s", r.opts.Command, len(cachedProjects), formatExtraArgs(r.opts.DotnetArgs))
		}
		return r.runWatch(ctx, r.projects, argsHash)
	}

	if len(targetProjects) == 0 {
		if !r.opts.Quiet {
			term.Dim("No affected projects to %s (%d cached)%s", r.opts.Command, len(cachedProjects), formatExtraArgs(r.opts.DotnetArgs))
			for _, p := range cachedProjects {
				term.CachedLine(p.Name)
			}
			term.Summary(0, 0, len(cachedProjects), 0, true)
		}

		// Print cached outputs if requested
		if r.opts.PrintOutput && len(cachedProjects) > 0 {
			sorted := make([]*project.Project, len(cachedProjects))
			copy(sorted, cachedProjects)
			sort.Slice(sorted, func(i, j int) bool {
				return sorted[i].Name < sorted[j].Name
			})
			term.Println()
			for _, p := range sorted {
				relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
				contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
				key := cache.MakeKey(contentHash, argsHash, p.Path)
				if result := r.db.Lookup(key); result != nil && len(result.Output) > 0 {
					term.Printf("=== %s ===\n%s\n", p.Name, string(result.Output))
				}
			}
		}

		return nil
	}

	success := r.runProjects(ctx, targetProjects, cachedProjects, argsHash)
	if !success {
		return fmt.Errorf("%s failed", r.opts.Command)
	}

	return nil
}

// findChangedProjects returns projects that have changes.
// Projects are checked concurrently since content hash computation involves filesystem I/O.
func (r *Runner) findChangedProjects(argsHash string, vcsChangedFiles []string, useVcsFilter bool) map[string]bool {
	changed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range r.projects {
		wg.Add(1)
		go func(p *project.Project) {
			defer wg.Done()

			// If using VCS filter, check if project has VCS changes
			if useVcsFilter {
				relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
				projectVcsFiles := project.FilterFilesToProject(vcsChangedFiles, relevantDirs)
				if len(projectVcsFiles) == 0 {
					return
				}
				term.Verbose("  vcs candidate: %s (%d files)", p.Name, len(projectVcsFiles))
			}

			// Check cache
			if r.projectChanged(p, argsHash) {
				mu.Lock()
				changed[p.Path] = true
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()

	return changed
}

// projectChanged checks if a project needs to be rebuilt/retested.
func (r *Runner) projectChanged(p *project.Project, argsHash string) bool {
	relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
	contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
	key := cache.MakeKey(contentHash, argsHash, p.Path)

	if r.opts.Force {
		term.Verbose("  forced: %s (key=%s)", p.Name, key)
		return true
	}

	if result := r.db.Lookup(key); result != nil {
		if r.opts.PrintOutput && p.IsTest && len(result.Output) == 0 {
			term.Verbose("  cache miss (no output): %s (key=%s)", p.Name, key)
			return true
		}
		term.Verbose("  cache hit: %s (key=%s)", p.Name, key)
		return false
	}

	term.Verbose("  cache miss: %s (key=%s)", p.Name, key)
	return true
}

// runProjects runs the command on the given projects using a parallel worker pool
// with dependency-ordered scheduling.
func (r *Runner) runProjects(ctx context.Context, targets, cached []*project.Project, argsHash string) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle Ctrl+C — must be before solution path which returns early
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals...)
	go func() {
		<-sigChan
		term.Warn("\nInterrupted, killing processes...")
		cancel()
	}()
	defer signal.Stop(sigChan)

	// Build args string for caching
	argsForCache := strings.Join(append([]string{r.opts.Command}, r.opts.DotnetArgs...), " ")

	// Pre-compute build-specific hash and cache string for build-only projects
	var buildArgsHash, buildArgsForCache string
	var filteredBuildArgs []string
	if len(r.opts.BuildOnlyProjects) > 0 {
		filteredBuildArgs = filterBuildArgs(r.opts.DotnetArgs)
		buildArgsHash = HashArgs(append([]string{"build"}, filteredBuildArgs...))
		buildArgsForCache = strings.Join(append([]string{"build"}, filteredBuildArgs...), " ")
	}

	// Separate build-only projects from test projects
	var testProjects []*project.Project
	var buildOnlyList []*project.Project
	for _, p := range targets {
		if r.opts.BuildOnlyProjects != nil && r.opts.BuildOnlyProjects[p.Path] {
			buildOnlyList = append(buildOnlyList, p)
		} else {
			testProjects = append(testProjects, p)
		}
	}

	// Check if we can build/test at solution level
	if !r.opts.NoSolution && len(testProjects) > 1 {
		// Single solution containing all test projects
		if sln := project.FindCommonSolution(testProjects, r.solutions, r.gitRoot); sln != nil {
			if len(buildOnlyList) == 0 {
				return r.runSolutionCommand(ctx, sln, testProjects, cached, argsHash, argsForCache)
			}
		}

		// Grouped solution builds
		var slnGroups map[*project.Solution][]*project.Project
		var remaining []*project.Project
		if r.opts.ForceSolution {
			slnGroups, remaining = project.GroupProjectsBySolution(testProjects, r.solutions, r.gitRoot)
		} else {
			slnGroups, remaining = project.FindCompleteSolutionMatches(testProjects, r.solutions, r.gitRoot)
		}

		remaining = append(remaining, buildOnlyList...)

		if len(slnGroups) > 0 {
			return r.runSolutionGroups(ctx, slnGroups, remaining, cached, argsHash, argsForCache)
		}
	}

	numWorkers := r.opts.EffectiveParallel()
	if numWorkers <= 0 {
		numWorkers = runtime.GOMAXPROCS(0)
	}
	if numWorkers > len(targets) {
		numWorkers = len(targets)
	}

	if !r.opts.Quiet {
		r.printStartMessage(targets, cached, numWorkers)
	}

	// Calculate max project name length for alignment
	maxNameLen := 0
	for _, p := range targets {
		if len(p.Name) > maxNameLen {
			maxNameLen = len(p.Name)
		}
	}

	// Print cached projects if requested
	if r.opts.ShowCached && len(cached) > 0 {
		for _, p := range cached {
			term.CachedLine(p.Name)
		}
	}

	startTime := time.Now()

	// Job queue
	jobs := make(chan *project.Project, len(targets))
	results := make(chan runResult, len(targets))
	status := make(chan statusUpdate, 100)

	// Stop signal for when failure is detected
	stopNewJobs := make(chan struct{})
	var stopOnce sync.Once
	signalStop := func() {
		stopOnce.Do(func() { close(stopNewJobs) })
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		go func() {
			for p := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if !r.opts.KeepGoing {
					select {
					case <-stopNewJobs:
						return
					default:
					}
				}

				result := r.runSingleProject(ctx, p, argsHash, argsForCache, buildArgsHash, buildArgsForCache, filteredBuildArgs, status, signalStop)

				select {
				case <-ctx.Done():
					return
				case results <- result:
				}
			}
		}()
	}

	// Build dependency tracking for target set
	targetSet := make(map[string]bool)
	for _, p := range targets {
		targetSet[p.Path] = true
	}

	pendingDeps := make(map[string]map[string]bool)
	for _, p := range targets {
		deps := make(map[string]bool)
		for _, depPath := range r.forwardGraph[p.Path] {
			if targetSet[depPath] {
				deps[depPath] = true
			}
		}
		pendingDeps[p.Path] = deps
	}

	// Send ready jobs (no pending deps)
	pending := make(map[string]*project.Project)
	jobsSent := 0
	for _, p := range targets {
		if len(pendingDeps[p.Path]) == 0 {
			jobs <- p
			jobsSent++
		} else {
			pending[p.Path] = p
		}
	}

	jobsClosed := false
	closeJobsIfDone := func() {
		if !jobsClosed && jobsSent == len(targets) {
			close(jobs)
			jobsClosed = true
		}
	}
	closeJobsIfDone()

	clearStatus := func() {
		term.ClearLine()
	}

	showStatus := func(projectName, line string) {
		elapsed := time.Since(startTime).Round(time.Second)
		termWidth := getTerminalWidth()
		prefix := fmt.Sprintf("  [%s] %s: ", elapsed, projectName)
		maxLen := termWidth - len(prefix) - 3

		cleanLine := term.StripAnsi(line)
		if len(cleanLine) > maxLen && maxLen > 0 {
			line = line[:min(maxLen, len(line))] + "..."
		}
		term.Status("%s%s", prefix, line)
	}

	// Track last status for heartbeat
	var lastProject, lastLine string
	var lastMu sync.Mutex

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Collect results
	succeeded := 0
	testSucceeded := 0
	buildSucceeded := 0
	completed := 0
	var failures []runResult
	var allResults []runResult
	directPrinted := make(map[string]bool)

	for completed < len(targets) {
		select {
		case <-ticker.C:
			lastMu.Lock()
			if lastProject != "" {
				showStatus(lastProject, lastLine+" ...")
			} else {
				elapsed := time.Since(startTime).Round(time.Second)
				term.Status("  [%s] waiting...", elapsed)
			}
			lastMu.Unlock()

		case s := <-status:
			lastMu.Lock()
			lastProject = s.project.Name
			lastLine = s.line
			lastMu.Unlock()
			showStatus(s.project.Name, s.line)

		case res := <-results:
			completed++
			allResults = append(allResults, res)

			if r.opts.Quiet {
				// Mark cache (unless skipped by filter)
				if !res.skippedByFilter {
					now := time.Now()
					cacheArgsHash := argsHash
					cacheArgsForCache := argsForCache
					if res.buildOnly {
						cacheArgsHash = buildArgsHash
						cacheArgsForCache = buildArgsForCache
					}
					relevantDirs := project.GetRelevantDirs(res.project, r.forwardGraph)
					contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
					key := cache.MakeKey(contentHash, cacheArgsHash, res.project.Path)
					r.db.Mark(key, now, res.success, []byte(res.output), cacheArgsForCache)
				}
				if res.success {
					succeeded++
				} else {
					failures = append(failures, res)
				}
				// Unblock waiting projects
				for path, p := range pending {
					delete(pendingDeps[path], res.project.Path)
					if len(pendingDeps[path]) == 0 {
						delete(pending, path)
						jobs <- p
						jobsSent++
					}
				}
				closeJobsIfDone()
				continue
			}

			clearStatus()

			// Handle projects skipped by user filter
			if res.skippedByFilter {
				succeeded++
				testSucceeded++
				for path, p := range pending {
					delete(pendingDeps[path], res.project.Path)
					if len(pendingDeps[path]) == 0 {
						delete(pending, path)
						jobs <- p
						jobsSent++
					}
				}
				closeJobsIfDone()
				continue
			}

			skipIndicator := term.SkipIndicator(res.skippedBuild, res.skippedRestore)
			paddedName := fmt.Sprintf("%-*s", maxNameLen, res.project.Name)
			durationStr := fmt.Sprintf("%7s", res.duration.Round(time.Millisecond))

			var stats, suffix string
			if res.buildOnly {
				if term.IsPlain() {
					suffix = "  (no tests)"
				} else {
					suffix = fmt.Sprintf("  %s(no tests)%s", term.ColorDim, term.ColorReset)
				}
			} else {
				stats = extractTestStats(res.output)
			}

			if res.success {
				succeeded++
				if res.buildOnly {
					buildSucceeded++
				} else {
					testSucceeded++
				}
				term.ResultLine(true, skipIndicator, paddedName, durationStr, stats, suffix)

				// Touch project.assets.json mtime
				projectPath := filepath.Join(r.gitRoot, res.project.Path)
				assetsPath := filepath.Join(filepath.Dir(projectPath), "obj", "project.assets.json")
				now := time.Now()
				os.Chtimes(assetsPath, now, now)

				// Mark cache
				cacheArgsHash := argsHash
				cacheArgsForCache := argsForCache
				if res.buildOnly {
					cacheArgsHash = buildArgsHash
					cacheArgsForCache = buildArgsForCache
				}
				relevantDirs := project.GetRelevantDirs(res.project, r.forwardGraph)
				contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
				key := cache.MakeKey(contentHash, cacheArgsHash, res.project.Path)
				r.db.Mark(key, now, true, []byte(res.output), cacheArgsForCache)

				// Mark transitive dependencies
				for _, depPath := range project.GetTransitiveDependencies(res.project.Path, r.forwardGraph) {
					if dep, ok := r.projectsByPath[depPath]; ok {
						depRelevantDirs := project.GetRelevantDirs(dep, r.forwardGraph)
						depContentHash := ComputeContentHash(r.gitRoot, depRelevantDirs)
						depKey := cache.MakeKey(depContentHash, cacheArgsHash, dep.Path)
						r.db.Mark(depKey, now, true, nil, cacheArgsForCache)
					}
				}
			} else {
				failures = append(failures, res)
				// Mark failure in cache
				cacheArgsHash := argsHash
				cacheArgsForCache := argsForCache
				if res.buildOnly {
					cacheArgsHash = buildArgsHash
					cacheArgsForCache = buildArgsForCache
				}
				relevantDirs := project.GetRelevantDirs(res.project, r.forwardGraph)
				contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
				key := cache.MakeKey(contentHash, cacheArgsHash, res.project.Path)
				r.db.Mark(key, time.Now(), false, []byte(res.output), cacheArgsForCache)

				alreadyPrinted := false
				select {
				case <-stopNewJobs:
					alreadyPrinted = true
					directPrinted[res.project.Name] = true
				default:
				}

				term.ResultLine(false, skipIndicator, paddedName, durationStr, stats, suffix)

				if !r.opts.KeepGoing {
					if !alreadyPrinted {
						term.Printf("\n%s\n", res.output)
					}
					cancel()
					totalDuration := time.Since(startTime).Round(time.Millisecond)
					term.Summary(succeeded, len(targets), len(cached), totalDuration, false)
					return false
				}
			}

			// Unblock waiting projects
			for path, p := range pending {
				delete(pendingDeps[path], res.project.Path)
				if len(pendingDeps[path]) == 0 {
					delete(pending, path)
					jobs <- p
					jobsSent++
				}
			}
			closeJobsIfDone()

		case <-ctx.Done():
			return false
		}
	}

	clearStatus()
	totalDuration := time.Since(startTime).Round(time.Millisecond)

	// Print failure output
	if len(failures) > 0 {
		unprintedFailures := 0
		for _, f := range failures {
			if !directPrinted[f.project.Name] {
				unprintedFailures++
			}
		}
		if unprintedFailures > 0 {
			term.Printf("\n--- Failure Output ---\n")
			for _, f := range failures {
				if !directPrinted[f.project.Name] {
					term.Printf("\n=== %s ===\n%s\n", f.project.Name, f.output)
				}
			}
		}
	}

	// Show summary
	if buildSucceeded > 0 || (len(r.opts.BuildOnlyProjects) > 0 && len(failures) > 0) {
		testTotal := len(targets) - len(r.opts.BuildOnlyProjects)
		buildTotal := len(r.opts.BuildOnlyProjects)

		success := len(failures) == 0
		color := term.ColorGreen
		if !success {
			color = term.ColorRed
		}

		if term.IsPlain() {
			if len(cached) > 0 {
				term.Printf("\n%d/%d tests succeeded, %d/%d builds succeeded, %d cached (%s)\n",
					testSucceeded, testTotal, buildSucceeded, buildTotal, len(cached), totalDuration)
			} else {
				term.Printf("\n%d/%d tests succeeded, %d/%d builds succeeded (%s)\n",
					testSucceeded, testTotal, buildSucceeded, buildTotal, totalDuration)
			}
		} else {
			if len(cached) > 0 {
				term.Printf("\n%s%d/%d tests succeeded%s, %s%d/%d builds succeeded%s, %s%d cached%s (%s)\n",
					color, testSucceeded, testTotal, term.ColorReset,
					color, buildSucceeded, buildTotal, term.ColorReset,
					term.ColorCyan, len(cached), term.ColorReset, totalDuration)
			} else {
				term.Printf("\n%s%d/%d tests succeeded%s, %s%d/%d builds succeeded%s (%s)\n",
					color, testSucceeded, testTotal, term.ColorReset,
					color, buildSucceeded, buildTotal, term.ColorReset, totalDuration)
			}
		}
	} else {
		term.Summary(succeeded, len(targets), len(cached), totalDuration, len(failures) == 0)
	}

	// Print all outputs if requested
	if r.opts.PrintOutput {
		type outputEntry struct {
			name   string
			output string
		}
		var outputs []outputEntry

		for _, res := range allResults {
			if res.output != "" {
				outputs = append(outputs, outputEntry{res.project.Name, res.output})
			}
		}

		for _, p := range cached {
			relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)
			contentHash := ComputeContentHash(r.gitRoot, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)
			if result := r.db.Lookup(key); result != nil && len(result.Output) > 0 {
				outputs = append(outputs, outputEntry{p.Name, string(result.Output)})
			}
		}

		if len(outputs) > 0 {
			sort.Slice(outputs, func(i, j int) bool {
				return outputs[i].name < outputs[j].name
			})
			term.Println()
			for _, o := range outputs {
				term.Printf("=== %s ===\n%s\n", o.name, o.output)
			}
		}
	}

	return len(failures) == 0
}

// runSingleProject runs the command on a single project and returns the result.
func (r *Runner) runSingleProject(ctx context.Context, p *project.Project, argsHash, argsForCache, buildArgsHash, buildArgsForCache string, filteredBuildArgs []string, status chan<- statusUpdate, signalStop func()) runResult {
	projectStart := time.Now()
	projectPath := filepath.Join(r.gitRoot, p.Path)
	extraArgs := r.opts.DotnetArgs

	// Check if this is a build-only project
	isBuildOnly := r.opts.BuildOnlyProjects != nil && r.opts.BuildOnlyProjects[p.Path]
	projectCommand := r.opts.Command
	if isBuildOnly {
		projectCommand = "build"
	}

	args := []string{projectCommand, projectPath, "--property:WarningLevel=0"}

	// Auto-detect if we can skip restore/build
	hasNoRestore := false
	hasNoBuild := false
	skippedBuild := false
	skippedRestore := false
	for _, arg := range extraArgs {
		if arg == "--no-restore" {
			hasNoRestore = true
		}
		if arg == "--no-build" {
			hasNoBuild = true
		}
	}

	if !r.opts.FullBuild {
		relevantDirs := project.GetRelevantDirs(p, r.forwardGraph)

		if projectCommand == "test" && !hasNoBuild {
			if canSkipBuild(projectPath, relevantDirs) {
				args = append(args, "--no-build")
				hasNoBuild = true
				skippedBuild = true
				term.Verbose("  [%s] skipping build (up-to-date)", p.Name)
			} else {
				term.Verbose("  [%s] cannot skip build: source files newer than DLL", p.Name)
			}
		}
		if !hasNoRestore && !hasNoBuild {
			if canSkipRestore(projectPath, relevantDirs) {
				args = append(args, "--no-restore")
				skippedRestore = true
				term.Verbose("  [%s] skipping restore (up-to-date)", p.Name)
			} else if term.IsVerbose() {
				projectDir := filepath.Dir(projectPath)
				assetsPath := filepath.Join(projectDir, "obj", "project.assets.json")
				assetsInfo, assetsErr := os.Stat(assetsPath)
				projectInfo, projErr := os.Stat(projectPath)
				if assetsErr != nil {
					term.Verbose("  [%s] cannot skip restore: %s not found", p.Name, assetsPath)
				} else if projErr != nil {
					term.Verbose("  [%s] cannot skip restore: cannot stat .csproj", p.Name)
				} else {
					term.Verbose("  [%s] cannot skip restore: assets (%s) older than .csproj (%s)",
						p.Name, assetsInfo.ModTime().Format("15:04:05"), projectInfo.ModTime().Format("15:04:05"))
				}
			}
		}
	}

	// Test filtering
	var filteredTests bool
	var testClasses []string
	var argsBeforeOurFilter []string
	var userFilter string
	var skipTestsDueToUserFilter bool

	// Extract user filter
	existingFilterIdx := -1
	for i, arg := range extraArgs {
		if arg == "--filter" && i+1 < len(extraArgs) {
			existingFilterIdx = i + 1
			break
		} else if strings.HasPrefix(arg, "--filter=") {
			existingFilterIdx = i
			break
		}
	}
	if existingFilterIdx >= 0 {
		if strings.HasPrefix(extraArgs[existingFilterIdx], "--filter=") {
			userFilter = strings.TrimPrefix(extraArgs[existingFilterIdx], "--filter=")
		} else {
			userFilter = extraArgs[existingFilterIdx]
		}
	}

	if projectCommand == "test" && r.opts.TestFilter != nil {
		filterResult := r.opts.TestFilter.GetFilter(p.Path, r.gitRoot, userFilter)

		if filterResult.ExcludedByUserFilter {
			skipTestsDueToUserFilter = true
			term.Verbose("  [%s] skipping: %s", p.Name, filterResult.Reason)
		} else if filterResult.CanFilter {
			argsBeforeOurFilter = make([]string, len(args))
			copy(argsBeforeOurFilter, args)

			if existingFilterIdx >= 0 {
				combinedFilter := fmt.Sprintf("(%s)&(%s)", filterResult.TestFilter, userFilter)
				args = append(args, "--filter", combinedFilter)
				filteredTests = true
				testClasses = filterResult.TestClasses
				term.Verbose("  [%s] filtering to: %s (combined with user filter)", p.Name, strings.Join(testClasses, ", "))
				if strings.HasPrefix(extraArgs[existingFilterIdx], "--filter=") {
					extraArgs = append(extraArgs[:existingFilterIdx], extraArgs[existingFilterIdx+1:]...)
				} else {
					extraArgs = append(extraArgs[:existingFilterIdx-1], extraArgs[existingFilterIdx+1:]...)
				}
			} else {
				args = append(args, "--filter", filterResult.TestFilter)
				filteredTests = true
				testClasses = filterResult.TestClasses
				term.Verbose("  [%s] filtering to: %s", p.Name, strings.Join(testClasses, ", "))
			}
		} else {
			term.Verbose("  [%s] running all tests: %s", p.Name, filterResult.Reason)
		}
	}

	// Skip if all tests excluded by user filter
	if skipTestsDueToUserFilter {
		return runResult{project: p, success: true, output: "skipped: all tests excluded by user filter", skippedByFilter: true}
	}

	// Apply failed test filters
	if projectCommand == "test" && r.opts.FailedTestFilters != nil {
		if filter, ok := r.opts.FailedTestFilters[p.Path]; ok && filter != "" {
			args = append(args, "--filter", filter)
			filteredTests = true
			testClasses = []string{"previously failed"}
		}
	}

	// Add TRX logger if reports enabled
	var trxPath string
	if !r.opts.NoReports && projectCommand == "test" {
		os.MkdirAll(r.reportsDir, 0755)
		trxPath = filepath.Join(r.reportsDir, p.Name+".trx")
		args = append(args, "--logger", "trx;LogFileName="+trxPath)
	}
	_ = trxPath // trxPath used for TRX report generation

	// Add coverage collection if enabled
	if r.opts.Coverage && projectCommand == "test" {
		args = append(args, "--collect:XPlat Code Coverage")
	}

	// Use filtered args for build-only projects
	if isBuildOnly {
		args = append(args, filteredBuildArgs...)
	} else {
		args = append(args, extraArgs...)
	}

	cmd := exec.CommandContext(ctx, "dotnet", args...)
	setupProcessGroup(cmd)

	var output bytes.Buffer
	lineWriter := &statusLineWriter{
		project:   p,
		status:    status,
		buffer:    &output,
		onFailure: signalStop,
	}
	cmd.Stdout = lineWriter
	cmd.Stderr = lineWriter
	cmd.Dir = r.gitRoot
	if term.IsPlain() {
		cmd.Env = os.Environ()
	} else {
		cmd.Env = append(os.Environ(),
			"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
			"TERM=xterm-256color",
		)
	}

	term.Verbose("  [%s] dotnet %s", p.Name, term.ShellQuoteArgs(args))

	err := cmd.Run()
	duration := time.Since(projectStart)
	outputStr := output.String()

	// Retry with restore if needed
	if err != nil && skippedRestore && needsRestoreRetry(outputStr) {
		term.Verbose("  [%s] retrying with restore (missing NuGet packages)", p.Name)

		retryArgs := make([]string, 0, len(args))
		for _, arg := range args {
			if arg != "--no-restore" {
				retryArgs = append(retryArgs, arg)
			}
		}

		output.Reset()
		projectStart = time.Now()
		retryCmd := exec.CommandContext(ctx, "dotnet", retryArgs...)
		setupProcessGroup(retryCmd)
		retryCmd.Stdout = lineWriter
		retryCmd.Stderr = lineWriter
		retryCmd.Dir = r.gitRoot
		retryCmd.Env = cmd.Env

		err = retryCmd.Run()
		duration = time.Since(projectStart)
		outputStr = output.String()
		skippedRestore = false
	}

	// Retry without test filter if no matches
	filterError := strings.Contains(outputStr, "No test matches the given testcase filter")
	filterFormatError := strings.Contains(outputStr, "Incorrect format for TestCaseFilter")
	if filteredTests && (filterError || filterFormatError) {
		if filterFormatError {
			term.Warnf("  [%s] filter format error, retrying without our filter", p.Name)
		} else {
			term.Warnf("  [%s] heuristic filter matched 0 tests, retrying without it", p.Name)
		}
		term.Verbose("    tried: %s", strings.Join(testClasses, ", "))

		if term.IsVerbose() {
			listCmd := exec.CommandContext(ctx, "dotnet", "test", projectPath, "--list-tests", "--no-build")
			listOutput, listErr := listCmd.Output()
			if listErr == nil {
				lines := strings.Split(string(listOutput), "\n")
				var testNames []string
				inTestList := false
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "The following Tests are available:") {
						inTestList = true
						continue
					}
					if inTestList && line != "" && !strings.HasPrefix(line, "Test run") {
						testNames = append(testNames, line)
					}
				}
				if len(testNames) > 0 {
					term.Verbose("    actual tests in project (%d):", len(testNames))
					for _, name := range testNames {
						term.Verbose("      %s", name)
					}
				}
			}
		}

		retryArgs := make([]string, len(argsBeforeOurFilter))
		copy(retryArgs, argsBeforeOurFilter)
		if userFilter != "" {
			retryArgs = append(retryArgs, "--filter", userFilter)
		}
		retryArgs = append(retryArgs, extraArgs...)

		output.Reset()
		projectStart = time.Now()
		retryCmd := exec.CommandContext(ctx, "dotnet", retryArgs...)
		setupProcessGroup(retryCmd)
		retryCmd.Stdout = lineWriter
		retryCmd.Stderr = lineWriter
		retryCmd.Dir = r.gitRoot
		retryCmd.Env = cmd.Env

		term.Verbose("  [%s] dotnet %s", p.Name, term.ShellQuoteArgs(retryArgs))

		err = retryCmd.Run()
		duration = time.Since(projectStart)
		outputStr = output.String()
		filteredTests = false
		testClasses = nil
	}

	// Save console output if reports enabled
	if !r.opts.NoReports {
		consolePath := filepath.Join(r.reportsDir, p.Name+".log")
		os.WriteFile(consolePath, []byte(outputStr), 0644)
	}

	return runResult{
		project:        p,
		success:        err == nil,
		output:         outputStr,
		duration:       duration,
		skippedBuild:   skippedBuild,
		skippedRestore: skippedRestore,
		filteredTests:  filteredTests,
		testClasses:    testClasses,
		buildOnly:      isBuildOnly,
	}
}

// printStartMessage prints the initial status line for the run.
func (r *Runner) printStartMessage(targets, cached []*project.Project, numWorkers int) {
	testCount := 0
	buildOnlyCount := 0
	for _, p := range targets {
		if r.opts.BuildOnlyProjects != nil && r.opts.BuildOnlyProjects[p.Path] {
			buildOnlyCount++
		} else {
			testCount++
		}
	}

	var statusLine string
	if buildOnlyCount > 0 && testCount > 0 {
		if term.IsPlain() {
			statusLine = fmt.Sprintf("Testing %d projects + building %d untested (%d workers)", testCount, buildOnlyCount, numWorkers)
		} else {
			statusLine = fmt.Sprintf("Testing %s%d projects%s + building %s%d untested%s (%d workers)",
				term.ColorGreen, testCount, term.ColorReset,
				term.ColorYellow, buildOnlyCount, term.ColorReset,
				numWorkers)
		}
	} else if buildOnlyCount > 0 {
		statusLine = fmt.Sprintf("Building %d projects (%d workers)", buildOnlyCount, numWorkers)
	} else {
		statusLine = fmt.Sprintf("Running %s on %d projects (%d workers)", r.opts.Command, len(targets), numWorkers)
	}
	if len(cached) > 0 {
		if term.IsPlain() {
			statusLine += fmt.Sprintf(", %d cached", len(cached))
		} else {
			statusLine += fmt.Sprintf(", %s%d cached%s", term.ColorCyan, len(cached), term.ColorReset)
		}
	}

	displayArgs := filterDisplayArgs(r.opts.DotnetArgs)
	if len(displayArgs) > 0 {
		argsStr := strings.Join(displayArgs, " ")
		if term.IsPlain() {
			statusLine += ", " + argsStr
		} else {
			statusLine += ", " + term.ColorYellow + argsStr + term.ColorReset
		}
	}

	term.Printf("%s...\n", statusLine)

	// Print per-project filters if --failed mode has filters
	if len(r.opts.FailedTestFilters) > 0 {
		term.Dim("Filtering to previously failed tests:")
		for projectPath, filter := range r.opts.FailedTestFilters {
			projectName := filepath.Base(filepath.Dir(projectPath))
			prettyPrintFilter(projectName, filter)
		}
		term.Println()
	}

	// Print per-project test filter preview
	if r.opts.TestFilter != nil {
		var previewUserFilter string
		for i, arg := range r.opts.DotnetArgs {
			if arg == "--filter" && i+1 < len(r.opts.DotnetArgs) {
				previewUserFilter = r.opts.DotnetArgs[i+1]
				break
			} else if strings.HasPrefix(arg, "--filter=") {
				previewUserFilter = strings.TrimPrefix(arg, "--filter=")
				break
			}
		}

		var hasFilters bool
		var hasSkipped bool
		for _, p := range targets {
			if r.opts.BuildOnlyProjects != nil && r.opts.BuildOnlyProjects[p.Path] {
				continue
			}
			result := r.opts.TestFilter.GetFilter(p.Path, r.gitRoot, previewUserFilter)
			if result.ExcludedByUserFilter {
				if !hasSkipped {
					hasSkipped = true
				}
				term.Dim("%s: skipped (all tests excluded by filter: %s)", p.Name, strings.Join(result.ExcludedTraits, ", "))
			} else if result.CanFilter {
				if !hasFilters {
					term.Dim("Filtering tests based on changed files:")
					hasFilters = true
				}
				prettyPrintFilter(p.Name, result.TestFilter)
				if strings.Contains(result.Reason, "TestFileOnly") || strings.Contains(result.Reason, "not referenced") {
					term.Verbose("    (%s)", result.Reason)
				}
			}
		}
		if hasFilters || hasSkipped {
			term.Println()
		}
	}
}

// runWatch is implemented in watch.go
