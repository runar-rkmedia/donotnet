package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/devplan"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testresults"
)

func getBuildInfo() (version, vcsRevision, vcsTime, vcsModified string) {
	version = "dev"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			vcsRevision = s.Value[:min(7, len(s.Value))]
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			vcsModified = s.Value
		}
	}
	return
}

func versionString() string {
	version, rev, vcsTime, modified := getBuildInfo()
	parts := []string{"donotnet", version}
	if rev != "" {
		parts = append(parts, rev)
	}
	if modified == "true" {
		parts = append(parts, "(modified)")
	}
	if vcsTime != "" {
		parts = append(parts, "built", vcsTime)
	}
	return strings.Join(parts, " ")
}

// Project and Solution are type aliases to the project package types.
type Project = project.Project
type Solution = project.Solution

var (
	flagListAffected  = flag.String("list-affected", "", "List affected projects: all, tests, or non-tests")
	flagVerbose       = flag.Bool("v", false, "Verbose output")
	flagCacheDir      = flag.String("cache-dir", "", "Cache directory path (default: .donotnet in git root)")
	flagDir           = flag.String("C", "", "Change to directory before running")
	flagVersion       = flag.Bool("version", false, "Show version and build info")
	flagParallel      = flag.Int("j", 0, "Number of parallel workers (default: GOMAXPROCS)")
	flagLocal         = flag.Bool("local", false, "Only scan current directory, not entire git repo")
	flagKeepGoing     = flag.Bool("k", false, "Keep going on errors (don't stop on first failure)")
	flagShowCached    = flag.Bool("show-cached", false, "Show cached projects in output")
	flagNoReports     = flag.Bool("no-reports", false, "Disable saving test reports (TRX and console output)")
	flagVcsChanged    = flag.Bool("vcs-changed", false, "Only test projects with uncommitted changes")
	flagVcsRef        = flag.String("vcs-ref", "", "Only test projects changed vs specified ref (e.g., 'main', 'origin/main', 'HEAD~3')")
	flagCacheStats    = flag.Bool("cache-stats", false, "Show cache statistics")
	flagCacheClean    = flag.Int("cache-clean", -1, "Remove cache entries older than N days (-1 = disabled)")
	flagForce         = flag.Bool("force", false, "Run all projects, ignoring cache (still updates cache on success)")
	flagWatch         = flag.Bool("watch", false, "Watch for file changes and rerun affected projects")
	flagFullBuild     = flag.Bool("full-build", false, "Disable auto --no-build/--no-restore detection")
	flagPrintOutput   = flag.Bool("print-output", false, "Print stdout from all projects (sorted by name) after completion")
	flagQuiet         = flag.Bool("q", false, "Quiet mode - suppress progress output, only show final results")
	flagColor         = flag.String("color", "auto", "Color output mode: auto, always, never")
	flagNoProgress    = flag.Bool("no-progress", false, "Disable progress output (default when not a TTY)")
	flagParseCoverage = flag.String("parse-coverage", "", "Parse a Cobertura coverage XML file and print covered files as JSON")
	flagCoverage      = flag.Bool("coverage", false, "Collect code coverage during test runs (adds --collect:\"XPlat Code Coverage\")")
	flagListTests          = flag.Bool("list-tests", false, "List all tests in affected test projects as JSON")
	flagBuildTestCoverage  = flag.Bool("build-test-coverage", false, "Build per-test coverage map for test projects (slow, runs each test individually)")
	flagHeuristics         = flag.String("heuristics", "default", "Test filter heuristics: default, none, or comma-separated names (use -list-heuristics to see options)")
	flagListHeuristics     = flag.Bool("list-heuristics", false, "List available test filter heuristics")
	flagFailed             = flag.Bool("failed", false, "Only run projects that failed in the previous run")
	flagDumpCache          = flag.String("dump-cache", "", "Dump cached output for a project (by name or path)")
	flagDevPlan            = flag.Bool("dev-plan", false, "Show job scheduling plan based on dependencies and exit")
	flagNoSolution         = flag.Bool("no-solution", false, "Disable solution-level builds, always build individual projects")
	flagSolution           = flag.Bool("solution", false, "Force solution-level builds even for single projects")
	flagNoSuggestions      = flag.Bool("no-suggestions", false, "Disable performance suggestions")
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `donotnet - Fast affected project detection for .NET

Do not run what you don't need to. Tracks file changes to determine which
projects need rebuilding/retesting. Uses git-aware caching for speed and
accuracy across branches and stashes.

%s

Usage: donotnet [flags] [command] [-- dotnet-args...]

Commands:
  test      Run 'dotnet test' on affected test projects (auto-marks on success)
  build     Run 'dotnet build' on affected projects (auto-marks on success)

Flags:
`, versionString())
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  donotnet test                     # Run tests on affected projects
  donotnet build                    # Build affected projects
  donotnet test -- --no-build       # Run tests without building
  donotnet -list-affected=tests     # List affected test projects
  donotnet -list-affected=all       # List all affected projects
  donotnet -C /path/to/repo test    # Run in different directory
  donotnet -vcs-changed test        # Test projects with uncommitted changes
  donotnet -vcs-ref=main test       # Test projects changed vs main branch
  donotnet -force test              # Run all tests, ignoring cache
  donotnet -failed test             # Rerun only previously failed tests
  donotnet -watch test              # Watch for changes and rerun tests
  donotnet -watch -coverage test    # Watch mode with coverage-based selection
  donotnet -cache-stats             # Show cache statistics
  donotnet -cache-clean=30          # Remove entries older than 30 days
`)
	}
}

// isNonBuildFile returns true if the file should be ignored for cache hashing
// (files that don't affect compilation output)
// getFailedTestFilter parses the cached output and/or TRX file to extract failed tests
// and build a filter string for dotnet test.
// Returns empty string if no specific tests can be identified (will rerun entire project).
func getFailedTestFilter(cachedOutput []byte, reportsDir, projectName string) string {
	var failedTests []testresults.FailedTest

	// Try TRX file first (more reliable)
	trxPath := filepath.Join(reportsDir, projectName+".trx")
	if info, err := os.Stat(trxPath); err == nil && info.Size() > 0 {
		if tests, err := testresults.ParseTRXFile(trxPath); err == nil && len(tests) > 0 {
			term.Verbose("  [%s] found %d failed tests in TRX", projectName, len(tests))
			failedTests = tests
		} else if err != nil {
			term.Verbose("  [%s] TRX parse error: %v", projectName, err)
		} else {
			term.Verbose("  [%s] TRX has no failed tests", projectName)
		}
	} else {
		term.Verbose("  [%s] no TRX file at %s", projectName, trxPath)
	}

	// Fall back to parsing stdout if TRX didn't give us results
	if len(failedTests) == 0 && len(cachedOutput) > 0 {
		failedTests = testresults.ParseStdout(string(cachedOutput))
		if len(failedTests) > 0 {
			term.Verbose("  [%s] found %d failed tests in stdout", projectName, len(failedTests))
		} else {
			term.Verbose("  [%s] no failed tests found in stdout (%d bytes)", projectName, len(cachedOutput))
			// Show first few lines for debugging
			lines := strings.Split(string(cachedOutput), "\n")
			for i, line := range lines {
				if i >= 10 {
					term.Verbose("  [%s]   ... (%d more lines)", projectName, len(lines)-10)
					break
				}
				if len(line) > 100 {
					term.Verbose("  [%s]   %q...", projectName, line[:100])
				} else {
					term.Verbose("  [%s]   %q", projectName, line)
				}
			}
		}
	}

	if len(failedTests) == 0 {
		return ""
	}

	return testresults.BuildFilterString(failedTests)
}

// ============================================================================
// Main Logic
// ============================================================================

func main() {
	// Check for unknown flags and suggest corrections before flag.Parse() exits
	if checkForUnknownFlags() {
		os.Exit(2)
	}
	flag.Parse()

	if *flagVersion {
		term.Println(versionString())
		return
	}

	// Handle -parse-coverage flag
	if *flagParseCoverage != "" {
		report, err := coverage.ParseFile(*flagParseCoverage)
		if err != nil {
			term.Errorf("parsing coverage file: %v", err)
			os.Exit(1)
		}

		// Build output structure
		output := struct {
			SourceDirs   []string `json:"source_dirs"`
			CoveredFiles []string `json:"covered_files"`
			AllFiles     []string `json:"all_files"`
		}{
			SourceDirs:   report.SourceDirs,
			CoveredFiles: make([]string, 0, len(report.CoveredFiles)),
			AllFiles:     make([]string, 0, len(report.AllFiles)),
		}
		for f := range report.CoveredFiles {
			output.CoveredFiles = append(output.CoveredFiles, f)
		}
		for f := range report.AllFiles {
			output.AllFiles = append(output.AllFiles, f)
		}
		sort.Strings(output.CoveredFiles)
		sort.Strings(output.AllFiles)

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			term.Errorf("encoding JSON: %v", err)
			os.Exit(1)
		}
		return
	}

	// Handle -list-heuristics flag
	if *flagListHeuristics {
		c, r, g, y, d := term.Color(term.ColorCyan), term.Color(term.ColorReset), term.Color(term.ColorGreen), term.Color(term.ColorYellow), term.Color(term.ColorDim)
		red := term.Color(term.ColorRed)
		term.Printf("%sDefault heuristics%s (enabled with -heuristics=default):\n\n", c, r)
		for _, h := range AvailableHeuristics {
			term.Printf("  %s%-20s%s %s%s%s\n", g, h.Name, r, d, h.Description, r)
		}
		term.Printf("\n%sOpt-in heuristics%s (must be explicitly enabled):\n\n", c, r)
		for _, h := range OptInHeuristics {
			term.Printf("  %s%-20s%s %s%s%s\n", y, h.Name, r, d, h.Description, r)
		}
		term.Printf("\n%sUsage:%s\n", d, r)
		term.Printf("  -heuristics=%sdefault%s                      Default heuristics only\n", g, r)
		term.Printf("  -heuristics=%snone%s                         Disable all heuristics\n", red, r)
		term.Printf("  -heuristics=%sdefault%s,%sExtensionsToBase%s     Defaults + specific opt-in\n", g, r, y, r)
		term.Printf("  -heuristics=%sdefault%s,%s-DirToNamespace%s      Defaults minus specific one\n", g, r, red, r)
		term.Printf("  -heuristics=%sNameToNameTests%s              Only specific heuristics\n", g, r)
		return
	}

	// Handle -build-test-coverage flag (needs gitRoot and projects, handled later)

	// Validate -list-affected value
	if *flagListAffected != "" && *flagListAffected != "all" && *flagListAffected != "tests" && *flagListAffected != "non-tests" {
		term.Errorf("-list-affected must be 'all', 'tests', or 'non-tests'")
		os.Exit(1)
	}

	if *flagDir != "" {
		if err := os.Chdir(*flagDir); err != nil {
			term.Errorf("cannot change to directory %s: %v", *flagDir, err)
			os.Exit(1)
		}
	}

	// Parse subcommand and extra args
	args := flag.Args()
	var command string
	var dotnetArgs []string
	var foundSeparator bool
	for i, arg := range args {
		if arg == "--" {
			dotnetArgs = args[i+1:]
			foundSeparator = true
			break
		}
		if command == "" && (arg == "test" || arg == "build") {
			command = arg
		}
	}

	// Check for common mistake: dotnet flags without -- separator
	if !foundSeparator && command != "" {
		for _, arg := range args {
			if arg == command {
				continue
			}
			if looksLikeDotnetFlag(arg) {
				term.Errorf("flag %q looks like a dotnet flag but appears before '--'", arg)
				term.Error("Use: donotnet %s -- %s", command, strings.Join(args[1:], " "))
				os.Exit(1)
			}
		}
	}

	// Show help if no command and no action flags
	hasActionFlag := false
	switch {
	case command != "":
		hasActionFlag = true
	case *flagListAffected != "":
		hasActionFlag = true
	case *flagCacheStats:
		hasActionFlag = true
	case *flagCacheClean >= 0:
		hasActionFlag = true
	case *flagListTests:
		hasActionFlag = true
	case *flagBuildTestCoverage:
		hasActionFlag = true
	case *flagDumpCache != "":
		hasActionFlag = true
	}
	if !hasActionFlag {
		flag.Usage()
		os.Exit(0)
	}

	// Add coverage collection if --coverage flag is set
	if *flagCoverage && command == "test" {
		dotnetArgs = append(dotnetArgs, "--collect:XPlat Code Coverage")
	}

	// Auto-enable quiet mode and print-output for informational commands
	if shouldAutoQuiet(dotnetArgs) {
		*flagQuiet = true
		*flagPrintOutput = true
	}

	gitRoot, err := git.FindRoot()
	if err != nil {
		term.Errorf("%v", err)
		os.Exit(1)
	}

	// Scan root: current dir if -local, otherwise git root
	scanRoot := gitRoot
	if *flagLocal {
		scanRoot, err = os.Getwd()
		if err != nil {
			term.Errorf("%v", err)
			os.Exit(1)
		}
	}

	cacheDir := *flagCacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(gitRoot, ".donotnet")
	}
	// Ensure cache directory exists
	os.MkdirAll(cacheDir, 0755)
	cachePath := filepath.Join(cacheDir, "cache.db")
	reportsDir := filepath.Join(cacheDir, "reports")

	// Handle cache management commands
	if *flagCacheStats || *flagCacheClean >= 0 {
		db, err := cache.Open(cachePath)
		if err != nil {
			term.Errorf("opening cache: %v", err)
			os.Exit(1)
		}
		defer db.Close()

		if *flagCacheClean >= 0 {
			maxAge := time.Duration(*flagCacheClean) * 24 * time.Hour
			deleted, err := db.DeleteOldEntries(maxAge)
			if err != nil {
				term.Errorf("cleaning cache: %v", err)
				os.Exit(1)
			}
			term.Printf("Deleted %d entries older than %d days\n", deleted, *flagCacheClean)
		}

		if *flagCacheStats {
			stats := db.GetStats()
			term.Printf("Cache statistics:\n")
			term.Printf("  Database: %s\n", cachePath)
			term.Printf("  Size: %d bytes (%.2f KB)\n", stats.DBSize, float64(stats.DBSize)/1024)
			term.Printf("  Total entries: %d\n", stats.TotalEntries)
			if stats.TotalEntries > 0 {
				term.Printf("  Oldest entry: %s\n", stats.OldestEntry.Format(time.RFC3339))
				term.Printf("  Newest entry: %s\n", stats.NewestEntry.Format(time.RFC3339))
			}
		}
		return
	}

	// Find all csproj files (paths always relative to git root for consistent cache keys)
	projects, err := project.FindProjects(scanRoot, gitRoot)
	if err != nil {
		term.Errorf("finding projects: %v", err)
		os.Exit(1)
	}

	// Find all solution files
	solutions, err := project.FindSolutions(scanRoot, gitRoot)
	if err != nil {
		term.Errorf("finding solutions: %v", err)
		os.Exit(1)
	}
	term.Verbose("Found %d solutions", len(solutions))

	// Set verbose, color and progress mode on terminal
	term.SetVerbose(*flagVerbose)
	switch *flagColor {
	case "never":
		term.SetPlain(true)
	case "always":
		term.SetPlain(false)
	case "auto":
		// Default behavior - plain mode is auto-set based on TTY detection
	default:
		term.Errorf("invalid --color value %q (use: auto, always, never)", *flagColor)
		os.Exit(1)
	}
	if *flagNoProgress {
		term.SetProgress(false)
	}
	term.Verbose("Found %d projects", len(projects))

	// Build dependency graphs
	graph := project.BuildDependencyGraph(projects)
	forwardGraph := project.BuildForwardDependencyGraph(projects)

	// Build project lookup map
	projectsByPath := make(map[string]*Project)
	for _, p := range projects {
		projectsByPath[p.Path] = p
	}

	// Open cache database
	db, err := cache.Open(cachePath)
	if err != nil {
		term.Errorf("opening cache: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run suggestions (once per session, before main output)
	if !*flagNoSuggestions && command != "" {
		suggestions := RunSuggestions(projects)
		PrintSuggestions(suggestions)
	}

	// Get git state
	commit := git.GetCommit(gitRoot)
	dirtyFiles := git.GetDirtyFiles(gitRoot)
	argsHash := hashArgs(append([]string{command}, dotnetArgs...)) // include command in hash

	// Pre-compute build-specific hash for untested projects (filters out test-specific args)
	buildArgs := filterBuildArgs(dotnetArgs)
	buildArgsHash := hashArgs(append([]string{"build"}, buildArgs...))

	term.Verbose("Git commit: %s", commit)
	if len(dirtyFiles) > 0 {
		term.Verbose("Dirty files: %d", len(dirtyFiles))
		for _, f := range dirtyFiles {
			term.Verbose("  %s", f)
		}
	} else {
		term.Verbose("Working tree clean")
	}

	// Handle -dump-cache flag
	if *flagDumpCache != "" {
		// Find project by name or path
		var targetProject *Project
		for _, p := range projects {
			if p.Name == *flagDumpCache || p.Path == *flagDumpCache || strings.HasSuffix(p.Path, "/"+*flagDumpCache) {
				targetProject = p
				break
			}
		}
		if targetProject == nil {
			term.Errorf("Project not found: %s", *flagDumpCache)
			term.Println("\nAvailable projects:")
			for _, p := range projects {
				term.Printf("  %s (%s)\n", p.Name, p.Path)
			}
			os.Exit(1)
		}

		// Build cache key
		relevantDirs := project.GetRelevantDirs(targetProject, forwardGraph)
		contentHash := computeContentHash(gitRoot, relevantDirs)
		key := cache.MakeKey(contentHash, argsHash, targetProject.Path)

		// Show current lookup info
		currentArgs := append([]string{command}, dotnetArgs...)
		term.Printf("Project: %s\n", targetProject.Name)
		term.Printf("Path: %s\n", targetProject.Path)
		term.Printf("Cache key: %s\n", key)
		term.Printf("Content hash: %s\n", contentHash)
		term.Printf("Args: %v (hash: %s)\n", currentArgs, argsHash)

		// Scan for all cache entries matching this project (any argsHash)
		term.Println("\nCache entries for this project:")
		found := 0
		db.View(func(k string, entry cache.Entry) error {
			entryContentHash, entryArgsHash, projectPath := cache.ParseKey(k)
			if projectPath == targetProject.Path {
				term.Printf("\n[%d] Key: %s\n", found+1, k)
				term.Printf("    Content hash: %s %s\n", entryContentHash, map[bool]string{true: "(matches)", false: "(different)"}[entryContentHash == contentHash])
				term.Printf("    Args hash: %s\n", entryArgsHash)
				if entry.Args != "" {
					term.Printf("    Args: %s\n", entry.Args)
				}
				term.Printf("    Success: %v\n", entry.Success)
				term.Printf("    LastRun: %s\n", time.Unix(entry.LastRun, 0).Format(time.RFC3339))
				term.Printf("    Output: %d bytes\n", len(entry.Output))
				if len(entry.Output) > 0 && !entry.Success {
					term.Println("\n    --- Cached Output ---")
					term.Println(string(entry.Output))
					term.Println("    --- End Output ---")
				}
				found++
			}
			return nil
		})
		if found == 0 {
			term.Println("No cache entries found for this project.")
		}
		return
	}

	// Determine which projects are changed
	var vcsChangedFiles []string
	useVcsFilter := *flagVcsChanged || *flagVcsRef != ""

	// Default to -vcs-changed behavior when using -list-affected without a VCS filter
	// This gives more intuitive results (what changed) vs cache-based (what needs to run)
	if *flagListAffected != "" && !useVcsFilter {
		useVcsFilter = true
		*flagVcsChanged = true
		term.Info("Using uncommitted changes to determine affected projects (use -vcs-ref=<ref> to compare against a branch)")
	}

	if useVcsFilter {
		if *flagVcsRef != "" {
			// Get diff vs specified ref
			var err error
			vcsChangedFiles, err = git.GetChangedFiles(gitRoot, *flagVcsRef)
			if err != nil {
				term.Errorf("%v", err)
				os.Exit(1)
			}
			if len(vcsChangedFiles) == 0 {
				term.Dim("No changes vs %s", *flagVcsRef)
				return
			}
			term.Verbose("VCS filter: changes vs %s (%d files)", *flagVcsRef, len(vcsChangedFiles))
		} else {
			// Use uncommitted changes (same as dirtyFiles)
			vcsChangedFiles = dirtyFiles
			if len(vcsChangedFiles) == 0 {
				term.Dim("No uncommitted changes")
				return
			}
			term.Verbose("VCS filter: uncommitted changes (%d files)", len(vcsChangedFiles))
		}
	}

	// Find changed projects
	changed := findChangedProjects(db, projects, gitRoot, argsHash, forwardGraph, vcsChangedFiles, useVcsFilter)

	term.Verbose("Changed projects: %v", changed)

	// Find affected projects (changed + dependents)
	affected := project.FindAffectedProjects(changed, graph, projects)

	// Filter to relevant projects and track cached
	var targetProjects []*Project
	var cachedProjects []*Project
	for _, p := range projects {
		// Filter by project type based on command or -list-affected
		if command == "test" && !p.IsTest {
			continue
		}
		if *flagListAffected == "tests" && !p.IsTest {
			continue
		}
		if *flagListAffected == "non-tests" && p.IsTest {
			continue
		}
		// Track as cached if not affected
		if !affected[p.Path] {
			cachedProjects = append(cachedProjects, p)
			continue
		}
		targetProjects = append(targetProjects, p)
	}

	// Handle --failed: filter to only projects that failed in their last run
	var failedTestFilters map[string]string
	if *flagFailed {
		failedEntries := db.GetFailed(argsHash)

		if len(failedEntries) == 0 {
			term.Info("--failed: No previously failed projects found in cache.")
			term.Info("         This happens when tests haven't been run yet, or all tests passed.")
			term.Info("         Running all affected tests instead.\n")
			// Continue with normal execution (don't filter to failed)
		} else {
			// Build map of project path -> test filter and project path -> exists
			failedPaths := make(map[string]bool)
			failedTestFilters = make(map[string]string)
			for _, entry := range failedEntries {
				failedPaths[entry.ProjectPath] = true
				// Look up the project to get its name for TRX file lookup
				if p, ok := projectsByPath[entry.ProjectPath]; ok {
					filter := getFailedTestFilter(entry.Output, reportsDir, p.Name)
					if filter != "" {
						failedTestFilters[entry.ProjectPath] = filter
						term.Verbose("  %s: filtering to %d failed tests", p.Name, strings.Count(filter, "|")+1)
					}
				}
			}

			var failedProjects []*Project
			for _, p := range projects {
				if command == "test" && !p.IsTest {
					continue
				}
				if failedPaths[p.Path] {
					failedProjects = append(failedProjects, p)
				}
			}

			if len(failedProjects) == 0 {
				term.Info("--failed: Previously failed projects no longer exist or are not affected.")
				term.Info("         Running all affected tests instead.\n")
				// Continue with normal execution (don't filter to failed)
			} else {
				filterCount := 0
				for range failedTestFilters {
					filterCount++
				}
				if filterCount > 0 {
					term.Info("Running %d previously failed project(s) with test-level filtering", len(failedProjects))
				} else {
					term.Info("Running %d previously failed project(s)", len(failedProjects))
				}
				targetProjects = failedProjects
				cachedProjects = nil // Don't show cached count with --failed
			}
		}
	}

	// Handle -list-tests flag
	if *flagListTests {
		// Filter to test projects only
		var testProjects []*Project
		for _, p := range targetProjects {
			if p.IsTest {
				testProjects = append(testProjects, p)
			}
		}
		if len(testProjects) == 0 {
			// If no affected, list all test projects
			for _, p := range projects {
				if p.IsTest {
					testProjects = append(testProjects, p)
				}
			}
		}
		if len(testProjects) == 0 {
			term.Dim("No test projects found")
			return
		}
		listAllTests(gitRoot, testProjects)
		return
	}

	// Handle -build-test-coverage flag
	if *flagBuildTestCoverage {
		var testProjects []*Project
		for _, p := range projects {
			if p.IsTest {
				testProjects = append(testProjects, p)
			}
		}
		if len(testProjects) == 0 {
			term.Dim("No test projects found")
			return
		}
		buildTestCoverageMaps(gitRoot, testProjects, *flagParallel)
		return
	}

	// Find untested projects (non-test projects not referenced by any test project)
	// These will be built instead of tested to at least verify they compile
	var untestedProjects []*Project
	var untestedAffected []*Project
	var untestedCached []*Project
	if command == "test" {
		untestedProjects = project.FindUntestedProjects(projects, forwardGraph)
		// Filter to only affected untested projects, checking cache with build-specific hash
		for _, p := range untestedProjects {
			if affected[p.Path] {
				// Re-check cache with build-specific hash (not test hash)
				relevantDirs := project.GetRelevantDirs(p, forwardGraph)
				contentHash := computeContentHash(gitRoot, relevantDirs)
				key := cache.MakeKey(contentHash, buildArgsHash, p.Path)
				if !*flagForce && db.Lookup(key) != nil {
					// Build result is cached
					untestedCached = append(untestedCached, p)
					cachedProjects = append(cachedProjects, p)
				} else {
					untestedAffected = append(untestedAffected, p)
				}
			}
		}
		if len(untestedAffected) > 0 {
			var names []string
			for _, p := range untestedAffected {
				names = append(names, p.Name)
			}
			term.Warnf("%d project(s) have no tests, will build instead: %s", len(untestedAffected), strings.Join(names, ", "))
		}
	}

	// Handle commands
	if command != "" {
		// Watch mode
		if *flagWatch {
			// Run initial test/build first
			if len(targetProjects) > 0 {
				runDotnetCommand(command, targetProjects, dotnetArgs, gitRoot, db, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir, nil, nil, solutions, nil)
			} else {
				term.Dim("No affected projects to %s (%d cached)%s", command, len(cachedProjects), formatExtraArgs(dotnetArgs))
			}

			// Start watch mode
			// Build coverage map for test projects
			var covMap *coverage.Map
			if command == "test" {
				covMap = buildCoverageMap(gitRoot, projects)
				if covMap != nil {
					term.Verbose("Coverage map: %d test projects with coverage, %d files mapped",
						len(covMap.TestProjectToFiles), len(covMap.FileToTestProjects))
					if len(covMap.MissingTestProjects) > 0 {
						term.Verbose("  Missing coverage: %d projects", len(covMap.MissingTestProjects))
					}
					if len(covMap.StaleTestProjects) > 0 {
						term.Verbose("  Stale coverage: %d projects", len(covMap.StaleTestProjects))
					}
				}
			}

			// Load per-test coverage maps for test filtering
			testCovMaps := loadAllTestCoverageMaps(gitRoot)
			if len(testCovMaps) > 0 {
				term.Verbose("Loaded per-test coverage for %d project(s)", len(testCovMaps))
			}

			testFilter := NewTestFilter()
			testFilter.SetCoverageMaps(testCovMaps)
			testFilter.SetHeuristics(ParseHeuristics(*flagHeuristics))

			watchCtx := &watchContext{
				command:          command,
				dotnetArgs:       dotnetArgs,
				gitRoot:          gitRoot,
				db:               db,
				projects:         projects,
				solutions:        solutions,
				graph:            graph,
				forwardGraph:     forwardGraph,
				projectsByPath:   projectsByPath,
				reportsDir:       reportsDir,
				argsHash:         argsHash,
				testFilter:       testFilter,
				coverageMap:      covMap,
				testCoverageMaps: testCovMaps,
			}
			runWatchMode(watchCtx)
			return
		}

		// Handle --dev-plan: show scheduling plan for all relevant projects and exit
		if *flagDevPlan {
			// Use all relevant projects (affected + cached) to show complete plan
			allRelevant := append(targetProjects, cachedProjects...)
			planProjects := make([]*devplan.Project, len(allRelevant))
			for i, p := range allRelevant {
				planProjects[i] = &devplan.Project{Path: p.Path, Name: p.Name}
			}
			plan := devplan.ComputePlan(planProjects, forwardGraph)
			colors := devplan.DefaultColors()
			if term.IsPlain() {
				colors = devplan.PlainColors()
			}
			plan.Print(os.Stdout, colors)
			return
		}

		if len(targetProjects) == 0 {
			if !*flagQuiet {
				term.Dim("No affected projects to %s (%d cached)%s", command, len(cachedProjects), formatExtraArgs(dotnetArgs))
				// Print all cached projects like a summary
				for _, p := range cachedProjects {
					term.CachedLine(p.Name)
				}
				term.Summary(0, 0, len(cachedProjects), 0, true)
			}

			// Print cached outputs if requested
			if *flagPrintOutput && len(cachedProjects) > 0 {
				// Sort by name for deterministic output
				sorted := make([]*Project, len(cachedProjects))
				copy(sorted, cachedProjects)
				sort.Slice(sorted, func(i, j int) bool {
					return sorted[i].Name < sorted[j].Name
				})
				term.Println()
				for _, p := range sorted {
					// Look up cached output from bbolt
					relevantDirs := project.GetRelevantDirs(p, forwardGraph)
					contentHash := computeContentHash(gitRoot, relevantDirs)
					key := cache.MakeKey(contentHash, argsHash, p.Path)
					if result := db.Lookup(key); result != nil && len(result.Output) > 0 {
						term.Printf("=== %s ===\n%s\n", p.Name, string(result.Output))
					}
				}
			}
			return
		}

		// Combine test projects and build-only projects into a single run
		var allProjects []*Project
		var buildOnlyMap map[string]bool

		if len(untestedAffected) > 0 {
			buildOnlyMap = make(map[string]bool)
			for _, p := range untestedAffected {
				buildOnlyMap[p.Path] = true
			}
			allProjects = append(allProjects, untestedAffected...)
		}
		allProjects = append(allProjects, targetProjects...)

		// Create test filter for non-watch mode (same filtering as watch mode)
		var testFilter *TestFilter
		if command == "test" && len(dirtyFiles) > 0 {
			testFilter = NewTestFilter()

			// Load per-test coverage maps for test filtering
			testCovMaps := loadAllTestCoverageMaps(gitRoot)
			if len(testCovMaps) > 0 {
				term.Verbose("Loaded per-test coverage for %d project(s)", len(testCovMaps))
			}
			testFilter.SetCoverageMaps(testCovMaps)
			testFilter.SetHeuristics(ParseHeuristics(*flagHeuristics))

			// Map dirty files to their owning projects
			for _, f := range dirtyFiles {
				for _, p := range projects {
					if strings.HasPrefix(f, p.Dir+"/") || strings.HasPrefix(f, p.Dir+"\\") {
						testFilter.AddChangedFile(p.Path, f)
						break
					}
				}
			}
		}

		if len(allProjects) > 0 {
			success := runDotnetCommand(command, allProjects, dotnetArgs, gitRoot, db, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir, testFilter, failedTestFilters, solutions, buildOnlyMap)
			if !success {
				os.Exit(1)
			}
		}
		return
	}

	// List affected projects (no command, -list-affected was specified)
	var listType string
	switch *flagListAffected {
	case "tests":
		listType = "test"
	case "non-tests":
		listType = "non-test"
	default:
		listType = ""
	}

	if len(targetProjects) == 0 {
		if listType != "" {
			term.Dim("No affected %s projects", listType)
		} else {
			term.Dim("No affected projects")
		}
		return
	}

	if listType != "" {
		term.Success("Affected %s projects:", listType)
	} else {
		term.Success("Affected projects:")
	}
	for _, p := range targetProjects {
		term.Println(p.Path)
	}
}

func findChangedProjects(db *cache.DB, projects []*Project, root string, argsHash string, forwardGraph map[string][]string, vcsChangedFiles []string, useVcsFilter bool) map[string]bool {
	changed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range projects {
		wg.Add(1)
		go func(p *Project) {
			defer wg.Done()

			// If using VCS filter, first check if this project has VCS changes
			if useVcsFilter {
				relevantDirs := project.GetRelevantDirs(p, forwardGraph)
				projectVcsFiles := project.FilterFilesToProject(vcsChangedFiles, relevantDirs)
				if len(projectVcsFiles) == 0 {
					// No VCS changes for this project, skip entirely
					return
				}
				term.Verbose("  vcs candidate: %s (%d files)", p.Name, len(projectVcsFiles))
				// Fall through to cache check
			}

			if projectChanged(db, p, root, argsHash, forwardGraph) {
				mu.Lock()
				changed[p.Path] = true
				mu.Unlock()
			}
		}(p)
	}

	wg.Wait()
	return changed
}

func projectChanged(db *cache.DB, p *Project, root string, argsHash string, forwardGraph map[string][]string) bool {
	// Get relevant directories for this project
	relevantDirs := project.GetRelevantDirs(p, forwardGraph)

	// Compute content hash for this project and its dependencies
	contentHash := computeContentHash(root, relevantDirs)

	// Build cache key
	key := cache.MakeKey(contentHash, argsHash, p.Path)

	// Skip cache check if force flag is set
	if *flagForce {
		term.Verbose("  forced: %s (key=%s)", p.Name, key)
		return true
	}

	// Check cache
	if result := db.Lookup(key); result != nil {
		// If --print-output is set, require cached output to exist (only for test projects)
		if *flagPrintOutput && p.IsTest && len(result.Output) == 0 {
			term.Verbose("  cache miss (no output): %s (key=%s)", p.Name, key)
			return true // Treat as changed - need to re-run to capture output
		}
		term.Verbose("  cache hit: %s (key=%s)", p.Name, key)
		return false // Not changed - cache hit
	}

	term.Verbose("  cache miss: %s (key=%s)", p.Name, key)
	return true // Changed - no cache entry
}

// shouldAutoQuiet returns true if the dotnet args indicate an informational command
// where the user cares about the output, not the progress
func shouldAutoQuiet(args []string) bool {
	infoArgs := map[string]bool{
		"--list-tests": true, // list available tests
		"--version":    true, // show version
		"--help":       true, // show help
		"-h":           true, // show help (short)
		"-?":           true, // show help (alt)
	}
	for _, arg := range args {
		if infoArgs[arg] {
			return true
		}
	}
	return false
}

func hashArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return fmt.Sprintf("%x", h[:8])
}

// filterBuildArgs removes test-specific arguments that shouldn't be passed to dotnet build
func filterBuildArgs(args []string) []string {
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Skip --filter and its value
		if arg == "--filter" {
			i++ // skip the next arg (the filter value)
			continue
		}
		if strings.HasPrefix(arg, "--filter=") {
			continue
		}
		// Skip --blame flags (test-specific)
		if arg == "--blame" || arg == "--blame-hang" || arg == "--blame-crash" {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

type runResult struct {
	project        *Project
	success        bool
	output         string
	duration       time.Duration
	skippedBuild   bool
	skippedRestore bool
	filteredTests  bool     // true if only specific tests were run
	testClasses    []string // test classes that were run (if filtered)
	buildOnly      bool     // true if this was a build-only job (no tests)
	viaSolution    bool     // true if this was run as part of a solution build
}

type statusUpdate struct {
	project *Project
	line    string
}

type statusLineWriter struct {
	project    *Project
	status     chan<- statusUpdate
	buffer     *bytes.Buffer
	lineBuf    []byte
	onFailure  func() // Called once when failure detected
	directMode bool
	mu         sync.Mutex
}

var failurePatterns = []string{
	"Failed!",        // Test run failed
	"] Failed ",      // Individual test failed
	"Error Message:", // Test error details
	"Build FAILED",   // MSBuild failure
}

var failureRegex = regexp.MustCompile(`Failed:\s*[1-9]\d*[,\s]`) // "Failed: N," where N > 0

// Regex to extract test stats: "Failed: X, Passed: Y, Skipped: Z, Total: N"
var testStatsRegex = regexp.MustCompile(`Failed:\s*(\d+),\s*Passed:\s*(\d+),\s*Skipped:\s*(\d+),\s*Total:\s*(\d+)`)

func extractTestStats(output string) string {
	match := testStatsRegex.FindStringSubmatch(output)
	if match == nil {
		return ""
	}
	return formatTestStats(match[1], match[2], match[3], match[4])
}

func formatTestStats(failed, passed, skipped, total string) string {
	// Plain mode - no colors
	if term.IsPlain() {
		return fmt.Sprintf("Failed: %2s  Passed: %3s  Skipped: %2s  Total: %3s", failed, passed, skipped, total)
	}

	failedN, _ := strconv.Atoi(failed)
	passedN, _ := strconv.Atoi(passed)
	skippedN, _ := strconv.Atoi(skipped)
	totalN, _ := strconv.Atoi(total)

	// Failed: dim if 0, red otherwise
	var failedStr string
	if failedN == 0 {
		failedStr = fmt.Sprintf("%sFailed: %2s%s", term.ColorDim, failed, term.ColorReset)
	} else {
		failedStr = fmt.Sprintf("%sFailed: %2s%s", term.ColorRed, failed, term.ColorReset)
	}

	// Passed: green if passed+skipped=total (all accounted for)
	var passedStr string
	if passedN+skippedN == totalN {
		passedStr = fmt.Sprintf("%sPassed: %3s%s", term.ColorGreen, passed, term.ColorReset)
	} else {
		passedStr = fmt.Sprintf("Passed: %3s", passed)
	}

	// Skipped: dim if 0, yellow otherwise
	var skippedStr string
	if skippedN == 0 {
		skippedStr = fmt.Sprintf("%sSkipped: %2s%s", term.ColorDim, skipped, term.ColorReset)
	} else {
		skippedStr = fmt.Sprintf("%sSkipped: %2s%s", term.ColorYellow, skipped, term.ColorReset)
	}

	totalStr := fmt.Sprintf("Total: %3s", total)

	return fmt.Sprintf("%s  %s  %s  %s", failedStr, passedStr, skippedStr, totalStr)
}

func (w *statusLineWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	w.buffer.Write(p)

	w.mu.Lock()
	directMode := w.directMode
	w.mu.Unlock()

	// In direct mode, just print to stderr
	if directMode {
		term.Write(p)
		return n, nil
	}

	// Process line by line
	w.lineBuf = append(w.lineBuf, p...)
	for {
		idx := bytes.IndexByte(w.lineBuf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(w.lineBuf[:idx]))
		w.lineBuf = w.lineBuf[idx+1:]

		if line != "" {
			select {
			case w.status <- statusUpdate{project: w.project, line: line}:
			default: // Don't block
			}

			// Check for failure patterns
			isFailure := false
			for _, pattern := range failurePatterns {
				if strings.Contains(line, pattern) {
					isFailure = true
					break
				}
			}
			if !isFailure && failureRegex.MatchString(line) {
				isFailure = true
			}

			if isFailure {
				w.mu.Lock()
				if !w.directMode {
					w.directMode = true
					// Clear status line and print header
					if term.IsPlain() {
						term.Status("  FAIL %s\n\n", w.project.Name)
					} else {
						term.Status("  %s✗%s %s\n\n", term.ColorRed, term.ColorReset, w.project.Name)
					}
					// Print buffered output
					term.Write(w.buffer.Bytes())
					if w.onFailure != nil {
						w.onFailure()
					}
				}
				w.mu.Unlock()
			}
		}
	}
	return n, nil
}

// runSolutionCommand runs a dotnet command on the entire solution instead of individual projects
// This avoids parallel build conflicts when projects share dependencies
func runSolutionCommand(command string, sln *Solution, projects []*Project, extraArgs []string, root string, db *cache.DB, argsHash string, forwardGraph map[string][]string, projectsByPath map[string]*Project, cachedProjects []*Project, reportsDir string, failedTestFilters map[string]string) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals...)
	go func() {
		<-sigChan
		term.Warn("\nInterrupted, killing processes...")
		cancel()
	}()
	defer signal.Stop(sigChan)

	startTime := time.Now()

	// Build args string for caching
	argsForCache := strings.Join(append([]string{command}, extraArgs...), " ")

	if !*flagQuiet {
		// Build status line
		var parts []string
		parts = append(parts, fmt.Sprintf("Running %s on solution %s", command, filepath.Base(sln.RelPath)))
		parts = append(parts, fmt.Sprintf("%d projects", len(projects)))
		if len(cachedProjects) > 0 {
			parts = append(parts, fmt.Sprintf("%d cached", len(cachedProjects)))
		}

		// Add extra args if any
		displayArgs := filterDisplayArgs(extraArgs)
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
	slnPath := filepath.Join(root, sln.RelPath)
	args := []string{command, slnPath, "--property:WarningLevel=0"}
	args = append(args, extraArgs...)

	cmd := exec.CommandContext(ctx, "dotnet", args...)
	setupProcessGroup(cmd)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.Dir = root

	if term.IsPlain() {
		cmd.Env = os.Environ()
	} else {
		cmd.Env = append(os.Environ(),
			"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
			"TERM=xterm-256color",
		)
	}

	term.Verbose("  dotnet %s", strings.Join(args, " "))

	err := cmd.Run()
	duration := time.Since(startTime)
	outputStr := output.String()
	success := err == nil

	// Add error details if command failed
	if err != nil {
		term.Verbose("  command error: %v", err)
		if outputStr == "" {
			outputStr = fmt.Sprintf("Command failed: %v\n", err)
		}
	}

	// Save console output if reports enabled
	if !*flagNoReports {
		consolePath := filepath.Join(reportsDir, filepath.Base(sln.RelPath)+".log")
		os.WriteFile(consolePath, []byte(outputStr), 0644)
	}

	// Extract stats from output
	stats := extractTestStats(outputStr)

	if !*flagQuiet {
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
		relevantDirs := project.GetRelevantDirs(p, forwardGraph)
		contentHash := computeContentHash(root, relevantDirs)
		key := cache.MakeKey(contentHash, argsHash, p.Path)
		db.Mark(key, now, success, nil, argsForCache)
	}

	if !*flagQuiet {
		succeeded := 0
		if success {
			succeeded = len(projects)
		}
		term.Summary(succeeded, len(projects), len(cachedProjects), duration.Round(time.Millisecond), success)
	}

	return success
}

// runSolutionGroups runs multiple solution builds in parallel, then builds remaining projects.
// Used when some projects can be built via solutions and others must be built individually.
func runSolutionGroups(command string, slnGroups map[*Solution][]*Project, remaining []*Project, extraArgs []string, root string, db *cache.DB, argsHash string, forwardGraph map[string][]string, projectsByPath map[string]*Project, cachedProjects []*Project, reportsDir string, testFilter *TestFilter, failedTestFilters map[string]string, solutions []*Solution, buildOnlyProjects map[string]bool) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals...)
	go func() {
		<-sigChan
		term.Warn("\nInterrupted, killing processes...")
		cancel()
	}()
	defer signal.Stop(sigChan)

	startTime := time.Now()
	argsForCache := strings.Join(append([]string{command}, extraArgs...), " ")

	// Count total projects across all solutions + remaining
	totalSolutionProjects := 0
	for _, projs := range slnGroups {
		totalSolutionProjects += len(projs)
	}
	totalProjects := totalSolutionProjects + len(remaining)

	if !*flagQuiet {
		var parts []string
		parts = append(parts, fmt.Sprintf("Running %s on %d solutions (%d projects)", command, len(slnGroups), totalSolutionProjects))
		if len(remaining) > 0 {
			parts = append(parts, fmt.Sprintf("+%d individual", len(remaining)))
		}
		if len(cachedProjects) > 0 {
			parts = append(parts, fmt.Sprintf("%d cached", len(cachedProjects)))
		}
		displayArgs := filterDisplayArgs(extraArgs)
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
		sln      *Solution
		projects []*Project
		success  bool
		output   string
		duration time.Duration
	}

	slnResults := make(chan slnResult, len(slnGroups))
	var wg sync.WaitGroup

	numWorkers := *flagParallel
	if numWorkers <= 0 {
		numWorkers = runtime.GOMAXPROCS(0)
	}
	if numWorkers > len(slnGroups) {
		numWorkers = len(slnGroups)
	}

	slnJobs := make(chan struct {
		sln   *Solution
		projs []*Project
	}, len(slnGroups))

	// Start solution workers
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
				slnPath := filepath.Join(root, job.sln.RelPath)
				args := []string{command, slnPath, "--property:WarningLevel=0"}
				args = append(args, extraArgs...)

				cmd := exec.CommandContext(ctx, "dotnet", args...)
				setupProcessGroup(cmd)

				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				cmd.Dir = root

				if term.IsPlain() {
					cmd.Env = os.Environ()
				} else {
					cmd.Env = append(os.Environ(),
						"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
						"TERM=xterm-256color",
					)
				}

				term.Verbose("  dotnet %s", strings.Join(args, " "))
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

	// Send all solution jobs
	for sln, projs := range slnGroups {
		slnJobs <- struct {
			sln   *Solution
			projs []*Project
		}{sln, projs}
	}
	close(slnJobs)

	// Wait for workers to finish
	go func() {
		wg.Wait()
		close(slnResults)
	}()

	// Collect solution results
	slnSucceeded := 0
	slnFailed := 0
	var failedOutputs []string

	for r := range slnResults {
		// Save console output if reports enabled
		if !*flagNoReports {
			consolePath := filepath.Join(reportsDir, filepath.Base(r.sln.RelPath)+".log")
			os.WriteFile(consolePath, []byte(r.output), 0644)
		}

		stats := extractTestStats(r.output)

		if !*flagQuiet {
			if r.success {
				if stats != "" {
					term.Printf("  %s✓%s %s %s  %s\n", term.ColorGreen, term.ColorReset, filepath.Base(r.sln.RelPath), r.duration.Round(time.Millisecond), stats)
				} else {
					term.Printf("  %s✓%s %s %s\n", term.ColorGreen, term.ColorReset, filepath.Base(r.sln.RelPath), r.duration.Round(time.Millisecond))
				}
			} else {
				if stats != "" {
					term.Printf("  %s✗%s %s %s  %s\n", term.ColorRed, term.ColorReset, filepath.Base(r.sln.RelPath), r.duration.Round(time.Millisecond), stats)
				} else {
					term.Printf("  %s✗%s %s %s\n", term.ColorRed, term.ColorReset, filepath.Base(r.sln.RelPath), r.duration.Round(time.Millisecond))
				}
				failedOutputs = append(failedOutputs, fmt.Sprintf("=== %s ===\n%s", filepath.Base(r.sln.RelPath), r.output))
			}
		}

		// Update cache for all projects in the solution
		now := time.Now()
		for _, p := range r.projects {
			relevantDirs := project.GetRelevantDirs(p, forwardGraph)
			contentHash := computeContentHash(root, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)
			db.Mark(key, now, r.success, nil, argsForCache)
		}

		if r.success {
			slnSucceeded += len(r.projects)
		} else {
			slnFailed += len(r.projects)
			if !*flagKeepGoing {
				cancel()
				term.Printf("\n%s\n", r.output)
				term.Summary(slnSucceeded, totalProjects, len(cachedProjects), time.Since(startTime).Round(time.Millisecond), false)
				return false
			}
		}
	}

	// If all solutions failed or were cancelled, we're done
	select {
	case <-ctx.Done():
		return false
	default:
	}

	// Run remaining individual projects if any
	if len(remaining) > 0 {
		if !*flagQuiet {
			term.Printf("\nRunning %d individual projects...\n", len(remaining))
		}
		// Use the standard project runner for remaining projects (pass nil for solutions to avoid re-checking)
		projSuccess := runDotnetCommand(command, remaining, extraArgs, root, db, argsHash, forwardGraph, projectsByPath, nil, reportsDir, testFilter, failedTestFilters, nil, buildOnlyProjects)
		if !projSuccess {
			return false
		}
		slnSucceeded += len(remaining)
	}

	// Print any failed solution outputs
	if len(failedOutputs) > 0 && *flagKeepGoing && !*flagQuiet {
		term.Printf("\n--- Solution Failure Output ---\n")
		for _, o := range failedOutputs {
			term.Printf("\n%s\n", o)
		}
	}

	if !*flagQuiet && len(remaining) == 0 {
		// Only print summary if we didn't run remaining projects (which prints its own summary)
		term.Summary(slnSucceeded, totalProjects, len(cachedProjects), time.Since(startTime).Round(time.Millisecond), slnFailed == 0)
	}

	return slnFailed == 0
}

func runDotnetCommand(command string, projects []*Project, extraArgs []string, root string, db *cache.DB, argsHash string, forwardGraph map[string][]string, projectsByPath map[string]*Project, cachedProjects []*Project, reportsDir string, testFilter *TestFilter, failedTestFilters map[string]string, solutions []*Solution, buildOnlyProjects map[string]bool) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build args string for caching
	argsForCache := strings.Join(append([]string{command}, extraArgs...), " ")

	// Pre-compute build-specific hash and cache string for build-only projects
	// Filter out test-specific args like --filter that don't apply to build
	var buildArgsHash, buildArgsForCache string
	var filteredBuildArgs []string
	if len(buildOnlyProjects) > 0 {
		filteredBuildArgs = filterBuildArgs(extraArgs)
		buildArgsHash = hashArgs(append([]string{"build"}, filteredBuildArgs...))
		buildArgsForCache = strings.Join(append([]string{"build"}, filteredBuildArgs...), " ")
	}

	// Separate build-only projects - they should always run individually, not via solutions
	var testProjects []*Project
	var buildOnlyList []*Project
	for _, p := range projects {
		if buildOnlyProjects != nil && buildOnlyProjects[p.Path] {
			buildOnlyList = append(buildOnlyList, p)
		} else {
			testProjects = append(testProjects, p)
		}
	}

	// Check if we can build/test at solution level instead of individual projects
	// This avoids parallel build conflicts when projects share dependencies
	//
	// Default behavior: use solution only if ALL projects in that solution need building
	// With --solution: use solution if 2+ projects in that solution need building
	// With --no-solution: never use solutions
	// Note: only test projects use solution optimization; build-only projects run individually
	if !*flagNoSolution && len(testProjects) > 1 {
		// First try: single solution containing all test projects
		if sln := project.FindCommonSolution(testProjects, solutions, root); sln != nil {
			// If we have build-only projects, can't use single solution path - need to run them too
			if len(buildOnlyList) == 0 {
				return runSolutionCommand(command, sln, testProjects, extraArgs, root, db, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir, failedTestFilters)
			}
		}

		// Second try: find solutions where ALL their projects need building (default)
		// or where 2+ projects need building (with --solution flag)
		var slnGroups map[*Solution][]*Project
		var remaining []*Project
		if *flagSolution {
			slnGroups, remaining = project.GroupProjectsBySolution(testProjects, solutions, root)
		} else {
			slnGroups, remaining = project.FindCompleteSolutionMatches(testProjects, solutions, root)
		}

		// Add build-only projects to the remaining list
		remaining = append(remaining, buildOnlyList...)

		if len(slnGroups) > 0 {
			// Run solution builds in parallel, then remaining projects (including build-only)
			return runSolutionGroups(command, slnGroups, remaining, extraArgs, root, db, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir, testFilter, failedTestFilters, solutions, buildOnlyProjects)
		}
	}

	// Handle Ctrl+C to kill all running processes
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals...)
	go func() {
		<-sigChan
		term.Warn("\nInterrupted, killing processes...")
		cancel()
	}()
	defer signal.Stop(sigChan)

	numWorkers := *flagParallel
	if numWorkers <= 0 {
		numWorkers = runtime.GOMAXPROCS(0) // default to CPU count
	}
	if numWorkers > len(projects) {
		numWorkers = len(projects)
	}

	if !*flagQuiet {
		// Count test vs build-only projects
		testCount := 0
		buildOnlyCount := 0
		for _, p := range projects {
			if buildOnlyProjects != nil && buildOnlyProjects[p.Path] {
				buildOnlyCount++
			} else {
				testCount++
			}
		}

		// Build status line
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
			statusLine = fmt.Sprintf("Running %s on %d projects (%d workers)", command, len(projects), numWorkers)
		}
		if len(cachedProjects) > 0 {
			if term.IsPlain() {
				statusLine += fmt.Sprintf(", %d cached", len(cachedProjects))
			} else {
				statusLine += fmt.Sprintf(", %s%d cached%s", term.ColorCyan, len(cachedProjects), term.ColorReset)
			}
		}

		// Add condensed extra args if any (excluding internal flags), colored
		displayArgs := filterDisplayArgs(extraArgs)
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
		if len(failedTestFilters) > 0 {
			term.Dim("Filtering to previously failed tests:")
			for projectPath, filter := range failedTestFilters {
				projectName := filepath.Base(filepath.Dir(projectPath))
				prettyPrintFilter(projectName, filter)
			}
			term.Println()
		}

		// Print per-project filters from testFilter (based on changed files)
		if testFilter != nil {
			var hasFilters bool
			for _, p := range projects {
				if buildOnlyProjects != nil && buildOnlyProjects[p.Path] {
					continue // skip build-only projects
				}
				result := testFilter.GetFilter(p.Path, root)
				if result.CanFilter {
					if !hasFilters {
						term.Dim("Filtering tests based on changed files:")
						hasFilters = true
					}
					prettyPrintFilter(p.Name, result.TestFilter)
				}
			}
			if hasFilters {
				term.Println()
			}
		}
	}

	// Calculate max project name length for alignment
	maxNameLen := 0
	for _, p := range projects {
		if len(p.Name) > maxNameLen {
			maxNameLen = len(p.Name)
		}
	}

	// Print cached projects at the top if requested
	if *flagShowCached && len(cachedProjects) > 0 {
		for _, p := range cachedProjects {
			term.CachedLine(p.Name)
		}
	}

	startTime := time.Now()

	// Job queue
	jobs := make(chan *Project, len(projects))
	results := make(chan runResult, len(projects))
	status := make(chan statusUpdate, 100)

	// Stop signal for when failure is detected (doesn't kill running process)
	stopNewJobs := make(chan struct{})
	var stopOnce sync.Once
	signalStop := func() {
		stopOnce.Do(func() { close(stopNewJobs) })
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		go func() {
			for p := range jobs {
				// Check if cancelled or stopped (unless keep-going)
				select {
				case <-ctx.Done():
					return
				default:
				}
				if !*flagKeepGoing {
					select {
					case <-stopNewJobs:
						return
					default:
					}
				}

				projectStart := time.Now()
				projectPath := filepath.Join(root, p.Path)

				// Check if this is a build-only project (no tests)
				isBuildOnly := buildOnlyProjects != nil && buildOnlyProjects[p.Path]
				projectCommand := command
				if isBuildOnly {
					projectCommand = "build"
				}

				args := []string{projectCommand, projectPath, "--property:WarningLevel=0"}

				// Auto-detect if we can skip restore/build (unless user already specified or disabled)
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

				if !*flagFullBuild {
					// Get all relevant directories (project + transitive dependencies)
					relevantDirs := project.GetRelevantDirs(p, forwardGraph)

					// --no-build is only valid for test, not build
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
							// Log why we couldn't skip restore
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

				// Check if we can filter to specific tests (only in test command with testFilter)
				var filteredTests bool
				var testClasses []string
				if projectCommand == "test" && testFilter != nil {
					filterResult := testFilter.GetFilter(p.Path, root)
					if filterResult.CanFilter {
						// Check if user already has a --filter arg - if so, combine with AND
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
							// Combine filters: (our filter) & (user filter)
							var existingFilter string
							if strings.HasPrefix(extraArgs[existingFilterIdx], "--filter=") {
								existingFilter = strings.TrimPrefix(extraArgs[existingFilterIdx], "--filter=")
							} else {
								existingFilter = extraArgs[existingFilterIdx]
							}
							combinedFilter := fmt.Sprintf("(%s)&(%s)", filterResult.TestFilter, existingFilter)
							args = append(args, "--filter", combinedFilter)
							filteredTests = true
							testClasses = filterResult.TestClasses
							term.Verbose("  [%s] filtering to: %s (combined with user filter)", p.Name, strings.Join(testClasses, ", "))
							// Remove the original --filter from extraArgs so we don't add it twice
							if strings.HasPrefix(extraArgs[existingFilterIdx], "--filter=") {
								extraArgs = append(extraArgs[:existingFilterIdx], extraArgs[existingFilterIdx+1:]...)
							} else {
								// Remove both --filter and its value
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

				// Apply failed test filters (from --failed flag)
				if projectCommand == "test" && failedTestFilters != nil {
					if filter, ok := failedTestFilters[p.Path]; ok && filter != "" {
						args = append(args, "--filter", filter)
						filteredTests = true
						testClasses = []string{"previously failed"}
					}
				}

				// Add TRX logger if reports enabled
				var trxPath string
				if !*flagNoReports && projectCommand == "test" {
					os.MkdirAll(reportsDir, 0755)
					trxPath = filepath.Join(reportsDir, p.Name+".trx")
					args = append(args, "--logger", "trx;LogFileName="+trxPath)
				}

				// Use filtered args for build-only projects (no --filter, --blame, etc.)
				if isBuildOnly {
					args = append(args, filteredBuildArgs...)
				} else {
					args = append(args, extraArgs...)
				}

				cmd := exec.CommandContext(ctx, "dotnet", args...)
				setupProcessGroup(cmd)

				// Custom writer that captures output and sends status updates
				var output bytes.Buffer
				lineWriter := &statusLineWriter{
					project:   p,
					status:    status,
					buffer:    &output,
					onFailure: signalStop,
				}
				cmd.Stdout = lineWriter
				cmd.Stderr = lineWriter
				cmd.Dir = root
				if term.IsPlain() {
					// In plain mode, don't force ANSI - let dotnet auto-detect
					cmd.Env = os.Environ()
				} else {
					// Force ANSI colors when in a TTY
					cmd.Env = append(os.Environ(),
						"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
						"TERM=xterm-256color",
					)
				}

				term.Verbose("  [%s] dotnet %s", p.Name, strings.Join(args, " "))

				err := cmd.Run()
				duration := time.Since(projectStart)
				outputStr := output.String()

				// Check if we need to retry with restore
				// This happens when we skipped restore but packages are missing from cache
				if err != nil && skippedRestore && needsRestoreRetry(outputStr) {
					term.Verbose("  [%s] retrying with restore (missing NuGet packages)", p.Name)

					// Rebuild args without --no-restore
					retryArgs := make([]string, 0, len(args))
					for _, arg := range args {
						if arg != "--no-restore" {
							retryArgs = append(retryArgs, arg)
						}
					}

					// Run again with restore
					output.Reset()
					projectStart = time.Now()
					retryCmd := exec.CommandContext(ctx, "dotnet", retryArgs...)
					setupProcessGroup(retryCmd)
					retryCmd.Stdout = lineWriter
					retryCmd.Stderr = lineWriter
					retryCmd.Dir = root
					retryCmd.Env = cmd.Env

					err = retryCmd.Run()
					duration = time.Since(projectStart)
					outputStr = output.String()
					skippedRestore = false // We did restore this time
				}

				// Save console output if reports enabled
				if !*flagNoReports {
					consolePath := filepath.Join(reportsDir, p.Name+".log")
					os.WriteFile(consolePath, []byte(outputStr), 0644)
				}

				select {
				case <-ctx.Done():
					return
				case results <- runResult{project: p, success: err == nil, output: outputStr, duration: duration, skippedBuild: skippedBuild, skippedRestore: skippedRestore, filteredTests: filteredTests, testClasses: testClasses, buildOnly: isBuildOnly}:
				}
			}
		}()
	}

	// Build dependency tracking for projects in target set
	// This prevents parallel build conflicts where both a project and its dependency
	// try to build the same files simultaneously
	targetSet := make(map[string]bool)
	for _, p := range projects {
		targetSet[p.Path] = true
	}

	// For each project, track which deps in target set are still incomplete
	pendingDeps := make(map[string]map[string]bool) // project -> set of pending dep paths
	for _, p := range projects {
		deps := make(map[string]bool)
		for _, depPath := range forwardGraph[p.Path] {
			if targetSet[depPath] {
				deps[depPath] = true
			}
		}
		pendingDeps[p.Path] = deps
	}

	// Split into ready (no pending deps) and pending projects
	pending := make(map[string]*Project)
	jobsSent := 0
	for _, p := range projects {
		if len(pendingDeps[p.Path]) == 0 {
			jobs <- p
			jobsSent++
		} else {
			pending[p.Path] = p
		}
	}

	// Close jobs channel only when all jobs have been sent
	jobsClosed := false
	closeJobsIfDone := func() {
		if !jobsClosed && jobsSent == len(projects) {
			close(jobs)
			jobsClosed = true
		}
	}
	closeJobsIfDone() // In case all projects were ready initially

	clearStatus := func() {
		term.ClearLine()
	}

	showStatus := func(projectName, line string) {
		elapsed := time.Since(startTime).Round(time.Second)
		termWidth := getTerminalWidth()
		prefix := fmt.Sprintf("  [%s] %s: ", elapsed, projectName)
		maxLen := termWidth - len(prefix) - 3 // Reserve space for "..."

		cleanLine := stripAnsi(line)
		if len(cleanLine) > maxLen && maxLen > 0 {
			line = line[:min(maxLen, len(line))] + "..."
		}
		term.Status("%s%s", prefix, line)
	}

	// Track last status for heartbeat
	var lastProject, lastLine string
	var lastMu sync.Mutex

	// Heartbeat ticker to show we're still alive
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Collect results
	succeeded := 0
	testSucceeded := 0
	buildSucceeded := 0
	completed := 0
	var failures []runResult
	var allResults []runResult // For --print-output mode
	directPrinted := make(map[string]bool) // Track which failures were already printed via direct mode

	for completed < len(projects) {
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

		case r := <-results:
			completed++
			allResults = append(allResults, r)
			if *flagQuiet {
				// In quiet mode, skip progress output but still mark cache
				now := time.Now()
				relevantDirs := project.GetRelevantDirs(r.project, forwardGraph)
				contentHash := computeContentHash(root, relevantDirs)
				key := cache.MakeKey(contentHash, argsHash, r.project.Path)
				db.Mark(key, now, r.success, []byte(r.output), argsForCache)
				if r.success {
					succeeded++
				} else {
					failures = append(failures, r)
				}
				// Unblock projects waiting on this one
				for path, p := range pending {
					delete(pendingDeps[path], r.project.Path)
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
			skipIndicator := term.SkipIndicator(r.skippedBuild, r.skippedRestore)

			// Pad project name and duration for alignment
			paddedName := fmt.Sprintf("%-*s", maxNameLen, r.project.Name)
			durationStr := fmt.Sprintf("%7s", r.duration.Round(time.Millisecond))

			// Build stats and suffix based on job type
			var stats, suffix string
			if r.buildOnly {
				// Build-only job - show build indicator instead of test stats
				if term.IsPlain() {
					suffix = "  (no tests)"
				} else {
					suffix = fmt.Sprintf("  %s(no tests)%s", term.ColorDim, term.ColorReset)
				}
			} else {
				// Test job - show test stats
				stats = extractTestStats(r.output)
				// Add filter info if tests were filtered
				if r.filteredTests && len(r.testClasses) > 0 {
					if term.IsPlain() {
						suffix = fmt.Sprintf("  [%s]", strings.Join(r.testClasses, ", "))
					} else {
						suffix = fmt.Sprintf("  %s[%s]%s", term.ColorCyan, strings.Join(r.testClasses, ", "), term.ColorReset)
					}
				}
			}

			if r.success {
				succeeded++
				if r.buildOnly {
					buildSucceeded++
				} else {
					testSucceeded++
				}
				term.ResultLine(true, skipIndicator, paddedName, durationStr, stats, suffix)

				// Touch project.assets.json to ensure mtime is updated for future skip detection
				// (dotnet doesn't always rewrite it if packages haven't changed)
				projectPath := filepath.Join(root, r.project.Path)
				assetsPath := filepath.Join(filepath.Dir(projectPath), "obj", "project.assets.json")
				now := time.Now()
				os.Chtimes(assetsPath, now, now)

				// Mark successful immediately (including dependencies)
				// Store output only for the project that ran
				// Use build-specific hash for build-only projects
				cacheArgsHash := argsHash
				cacheArgsForCache := argsForCache
				if r.buildOnly {
					cacheArgsHash = buildArgsHash
					cacheArgsForCache = buildArgsForCache
				}

				relevantDirs := project.GetRelevantDirs(r.project, forwardGraph)
				contentHash := computeContentHash(root, relevantDirs)
				key := cache.MakeKey(contentHash, cacheArgsHash, r.project.Path)
				db.Mark(key, now, true, []byte(r.output), cacheArgsForCache)

				// Also mark transitive dependencies (without output)
				for _, depPath := range project.GetTransitiveDependencies(r.project.Path, forwardGraph) {
					if dep, ok := projectsByPath[depPath]; ok {
						depRelevantDirs := project.GetRelevantDirs(dep, forwardGraph)
						depContentHash := computeContentHash(root, depRelevantDirs)
						depKey := cache.MakeKey(depContentHash, cacheArgsHash, dep.Path)
						db.Mark(depKey, now, true, nil, cacheArgsForCache)
					}
				}
			} else {
				failures = append(failures, r)
				// Mark failure in cache for --failed support
				// Use build-specific hash for build-only projects
				cacheArgsHash := argsHash
				cacheArgsForCache := argsForCache
				if r.buildOnly {
					cacheArgsHash = buildArgsHash
					cacheArgsForCache = buildArgsForCache
				}

				relevantDirs := project.GetRelevantDirs(r.project, forwardGraph)
				contentHash := computeContentHash(root, relevantDirs)
				key := cache.MakeKey(contentHash, cacheArgsHash, r.project.Path)
				db.Mark(key, time.Now(), false, []byte(r.output), cacheArgsForCache)
				// Check if output was already printed in direct mode
				alreadyPrinted := false
				select {
				case <-stopNewJobs:
					alreadyPrinted = true
					directPrinted[r.project.Name] = true
				default:
				}

				// Print failure inline with stats
				term.ResultLine(false, skipIndicator, paddedName, durationStr, stats, suffix)

				if !*flagKeepGoing {
					// Print output if not already printed
					if !alreadyPrinted {
						term.Printf("\n%s\n", r.output)
					}
					cancel() // Stop other goroutines
					totalDuration := time.Since(startTime).Round(time.Millisecond)
					term.Summary(succeeded, len(projects), len(cachedProjects), totalDuration, false)
					return false
				}
			}

			// Unblock projects waiting on this one
			for path, p := range pending {
				delete(pendingDeps[path], r.project.Path)
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

	// Print failure output for failures that weren't already shown via direct mode
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

	// Show summary - combined format if we have both tests and builds
	if buildSucceeded > 0 || (len(buildOnlyProjects) > 0 && len(failures) > 0) {
		testTotal := len(projects) - len(buildOnlyProjects)
		buildTotal := len(buildOnlyProjects)

		success := len(failures) == 0
		color := term.ColorGreen
		if !success {
			color = term.ColorRed
		}

		if term.IsPlain() {
			if len(cachedProjects) > 0 {
				term.Printf("\n%d/%d tests succeeded, %d/%d builds succeeded, %d cached (%s)\n",
					testSucceeded, testTotal, buildSucceeded, buildTotal, len(cachedProjects), totalDuration)
			} else {
				term.Printf("\n%d/%d tests succeeded, %d/%d builds succeeded (%s)\n",
					testSucceeded, testTotal, buildSucceeded, buildTotal, totalDuration)
			}
		} else {
			if len(cachedProjects) > 0 {
				term.Printf("\n%s%d/%d tests succeeded%s, %s%d/%d builds succeeded%s, %s%d cached%s (%s)\n",
					color, testSucceeded, testTotal, term.ColorReset,
					color, buildSucceeded, buildTotal, term.ColorReset,
					term.ColorCyan, len(cachedProjects), term.ColorReset, totalDuration)
			} else {
				term.Printf("\n%s%d/%d tests succeeded%s, %s%d/%d builds succeeded%s (%s)\n",
					color, testSucceeded, testTotal, term.ColorReset,
					color, buildSucceeded, buildTotal, term.ColorReset, totalDuration)
			}
		}
	} else {
		term.Summary(succeeded, len(projects), len(cachedProjects), totalDuration, len(failures) == 0)
	}

	// Print all outputs sorted by project name if requested
	if *flagPrintOutput {
		// Collect all outputs: ran projects + cached projects
		type outputEntry struct {
			name   string
			output string
		}
		var outputs []outputEntry

		// Add outputs from projects that ran
		for _, r := range allResults {
			if r.output != "" {
				outputs = append(outputs, outputEntry{r.project.Name, r.output})
			}
		}

		// Add outputs from cached projects (read from bbolt cache)
		for _, p := range cachedProjects {
			relevantDirs := project.GetRelevantDirs(p, forwardGraph)
			contentHash := computeContentHash(root, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)
			if result := db.Lookup(key); result != nil && len(result.Output) > 0 {
				outputs = append(outputs, outputEntry{p.Name, string(result.Output)})
			}
		}

		if len(outputs) > 0 {
			// Sort by project name for deterministic output
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

// needsRestoreRetry checks if a build failure is due to missing NuGet packages
// This happens when we optimistically skipped restore but packages aren't in cache
func needsRestoreRetry(output string) bool {
	// Common patterns indicating missing NuGet packages
	patterns := []string{
		"could not be found",                    // CS0006: Metadata file '...' could not be found
		"are you missing an assembly reference", // CS0234/CS0246
		"Run a NuGet package restore",           // NU1101 type errors
		"assets file.*doesn't have a target",    // Assets file issue
	}

	outputLower := strings.ToLower(output)
	for _, pattern := range patterns {
		if strings.Contains(outputLower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// looksLikeDotnetFlag returns true if the arg looks like a dotnet test/build flag
// that should be passed after the -- separator
func looksLikeDotnetFlag(arg string) bool {
	// Common dotnet test/build flags that users might forget to put after --
	dotnetFlags := []string{
		"--filter", "-f",
		"--configuration", "-c",
		"--framework", "-f",
		"--runtime", "-r",
		"--no-build",
		"--no-restore",
		"--collect",
		"--settings", "-s",
		"--logger", "-l",
		"--output", "-o",
		"--results-directory",
		"--blame",
		"--blame-crash",
		"--blame-hang",
		"--diag",
		"--verbosity", "-v",
		"--list-tests", "-lt",
		"--arch", "-a",
		"--os",
	}

	argLower := strings.ToLower(arg)
	for _, flag := range dotnetFlags {
		if argLower == flag || strings.HasPrefix(argLower, flag+"=") || strings.HasPrefix(argLower, flag+":") {
			return true
		}
	}

	// Also catch things like "Category!=Live" which is clearly a filter value
	if strings.Contains(arg, "!=") || strings.Contains(arg, "~") {
		return true
	}

	return false
}

// formatExtraArgs formats extra args for display in status messages
// Returns empty string if no displayable args, or " (args...)" otherwise
func formatExtraArgs(args []string) string {
	display := filterDisplayArgs(args)
	if len(display) == 0 {
		return ""
	}
	if term.IsPlain() {
		return " (" + strings.Join(display, " ") + ")"
	}
	return " " + term.ColorYellow + "(" + strings.Join(display, " ") + ")" + term.ColorReset
}

// filterDisplayArgs filters extra args for display, removing internal/verbose flags
func filterDisplayArgs(args []string) []string {
	var display []string
	skip := false
	for _, arg := range args {
		if skip {
			skip = false
			continue
		}
		// Skip internal flags that aren't useful for display
		if strings.HasPrefix(arg, "--logger:") || strings.HasPrefix(arg, "--results-directory:") {
			continue
		}
		// Skip property flags that are verbose
		if strings.HasPrefix(arg, "--property:") || strings.HasPrefix(arg, "-p:") {
			continue
		}
		// Skip the next arg if this is a flag that takes a value
		if arg == "--logger" || arg == "--results-directory" || arg == "-l" || arg == "-r" {
			skip = true
			continue
		}
		display = append(display, arg)
	}
	return display
}

// prettyPrintFilter prints a test filter in a readable format
func prettyPrintFilter(projectName, filter string) {
	// Parse filter: "FullyQualifiedName~Foo|FullyQualifiedName~Bar" -> ["Foo", "Bar"]
	parts := strings.Split(filter, "|")
	if len(parts) == 0 {
		return
	}

	var testNames []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "FullyQualifiedName~") {
			testNames = append(testNames, strings.TrimPrefix(part, "FullyQualifiedName~"))
		} else if strings.HasPrefix(part, "FullyQualifiedName=") {
			testNames = append(testNames, strings.TrimPrefix(part, "FullyQualifiedName="))
		} else if part != "" {
			testNames = append(testNames, part)
		}
	}

	if len(testNames) == 0 {
		return
	}

	// Print project name and test count
	term.Printf("  %s (%d tests):\n", projectName, len(testNames))
	for _, name := range testNames {
		term.Dim("    %s", name)
	}
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}
