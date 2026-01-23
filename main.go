package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
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

// Cache stores the state of last successful test runs
type Cache struct {
	Projects map[string]*ProjectState `json:"projects"`
}

type ProjectState struct {
	LastSuccess time.Time        `json:"last_success"`
	Files       map[string]*FileState `json:"files"`
}

type FileState struct {
	Mtime int64  `json:"mtime"`
	Hash  string `json:"hash"`
}

// Project represents a parsed .csproj
type Project struct {
	Path         string
	Dir          string
	Name         string
	References   []string // paths to referenced projects
	IsTest       bool
}

var (
	flagMark        = flag.Bool("mark", false, "Mark current state as successful (update cache)")
	flagTests       = flag.Bool("tests", false, "Only output test projects")
	flagAll         = flag.Bool("all", false, "Output all projects, not just affected")
	flagVerbose     = flag.Bool("v", false, "Verbose output")
	flagCache       = flag.String("cache", "", "Cache file path (default: .donotnet-cache in git root)")
	flagDir         = flag.String("C", "", "Change to directory before running")
	flagVersion     = flag.Bool("version", false, "Show version and build info")
	flagParallel    = flag.Int("j", 0, "Number of parallel workers (default: number of projects)")
	flagLocal       = flag.Bool("local", false, "Only scan current directory, not entire git repo")
	flagKeepGoing   = flag.Bool("k", false, "Keep going on errors (don't stop on first failure)")
	flagShowCached  = flag.Bool("show-cached", false, "Show cached projects in output")
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `donotnet - Fast affected project detection for .NET

Do not run what you don't need to. Tracks file changes to determine which
projects need rebuilding/retesting. Uses hybrid mtime+hash detection for
speed and accuracy.

%s

Usage: donotnet [flags] [command] [-- dotnet-args...]

Commands:
  test      Run 'dotnet test' on affected test projects (auto-marks on success)
  build     Run 'dotnet build' on affected projects (auto-marks on success)
  (none)    List affected projects

Flags:
`, versionString())
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  donotnet test                # Run tests on affected projects
  donotnet build               # Build affected projects
  donotnet test -- --no-build  # Run tests without building
  donotnet -tests              # List affected test projects
  donotnet -mark               # Manually mark all as successful
  donotnet -C /path/to/repo test  # Run in different directory
`)
	}
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Println(versionString())
		return
	}

	if *flagDir != "" {
		if err := os.Chdir(*flagDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot change to directory %s: %v\n", *flagDir, err)
			os.Exit(1)
		}
	}

	// Parse subcommand and extra args
	args := flag.Args()
	var command string
	var dotnetArgs []string
	for i, arg := range args {
		if arg == "--" {
			dotnetArgs = args[i+1:]
			break
		}
		if command == "" && (arg == "test" || arg == "build") {
			command = arg
		}
	}

	gitRoot, err := findGitRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Scan root: current dir if -local, otherwise git root
	scanRoot := gitRoot
	if *flagLocal {
		scanRoot, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	cachePath := *flagCache
	if cachePath == "" {
		cachePath = filepath.Join(gitRoot, ".donotnet-cache")
	}

	// Find all csproj files (paths always relative to git root for consistent cache keys)
	projects, err := findProjects(scanRoot, gitRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error finding projects: %v\n", err)
		os.Exit(1)
	}

	if *flagVerbose {
		fmt.Fprintf(os.Stderr, "Found %d projects\n", len(projects))
	}

	// Build dependency graphs
	graph := buildDependencyGraph(projects)
	forwardGraph := buildForwardDependencyGraph(projects)

	// Build project lookup map
	projectsByPath := make(map[string]*Project)
	for _, p := range projects {
		projectsByPath[p.Path] = p
	}

	// Load cache
	cache := loadCache(cachePath)

	if *flagMark {
		// Update cache with current state
		markSuccess(cache, projects, gitRoot)
		if err := saveCache(cachePath, cache); err != nil {
			fmt.Fprintf(os.Stderr, "error saving cache: %v\n", err)
			os.Exit(1)
		}
		if *flagVerbose {
			fmt.Fprintf(os.Stderr, "Cache updated\n")
		}
		return
	}

	// Find changed projects
	changed := findChangedProjects(cache, projects, gitRoot)

	if *flagVerbose {
		fmt.Fprintf(os.Stderr, "Changed projects: %v\n", changed)
	}

	// Find affected projects (changed + dependents)
	affected := findAffectedProjects(changed, graph, projects)

	// Filter to relevant projects and track cached
	var targetProjects []*Project
	var cachedProjects []*Project
	for _, p := range projects {
		if command == "test" && !p.IsTest {
			continue
		}
		if *flagTests && !p.IsTest {
			continue
		}
		// Track as cached if not affected (and not using -all)
		if !*flagAll && !affected[p.Path] {
			cachedProjects = append(cachedProjects, p)
			continue
		}
		targetProjects = append(targetProjects, p)
	}

	// Handle commands
	if command != "" {
		if len(targetProjects) == 0 {
			fmt.Fprintf(os.Stderr, "No affected projects to %s (%d cached)\n", command, len(cachedProjects))
			// Print all cached projects like a summary
			for _, p := range cachedProjects {
				fmt.Fprintf(os.Stderr, "  %s○%s %s (cached)\n", colorDim, colorReset+colorDim, p.Name)
			}
			fmt.Fprintf(os.Stderr, "%s\n%s0/0 succeeded, %d cached%s\n", colorReset, colorGreen, len(cachedProjects), colorReset)
			return
		}

		success := runDotnetCommand(command, targetProjects, dotnetArgs, gitRoot, cache, cachePath, forwardGraph, projectsByPath, cachedProjects)
		if !success {
			os.Exit(1)
		}
		return
	}

	// No command - just list projects
	for _, p := range targetProjects {
		fmt.Println(p.Path)
	}
}

func findGitRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not in a git repository")
		}
		dir = parent
	}
}

func findProjects(scanRoot, gitRoot string) ([]*Project, error) {
	var projects []*Project
	var mu sync.Mutex

	err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			name := d.Name()
			// Skip common non-project directories
			if name == ".git" || name == "node_modules" || name == "bin" || name == "obj" || name == ".vs" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".csproj") {
			// Always use paths relative to git root for consistent cache keys
			relPath, _ := filepath.Rel(gitRoot, path)
			p, err := parseProject(path, relPath)
			if err != nil {
				if *flagVerbose {
					fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", relPath, err)
				}
				return nil
			}
			mu.Lock()
			projects = append(projects, p)
			mu.Unlock()
		}
		return nil
	})

	return projects, err
}

var projectRefRegex = regexp.MustCompile(`<ProjectReference\s+Include="([^"]+)"`)

func parseProject(path, relPath string) (*Project, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	name := strings.TrimSuffix(filepath.Base(path), ".csproj")

	// Check if it's a test project
	isTest := strings.HasSuffix(name, ".Tests") ||
		strings.HasSuffix(name, ".Test") ||
		strings.HasSuffix(name, "Tests") ||
		strings.Contains(string(content), "<IsTestProject>true</IsTestProject>")

	// Find project references
	matches := projectRefRegex.FindAllStringSubmatch(string(content), -1)
	var refs []string
	for _, m := range matches {
		refPath := m[1]
		// Convert Windows path separators
		refPath = strings.ReplaceAll(refPath, "\\", "/")
		// Resolve relative path
		absRef := filepath.Clean(filepath.Join(dir, refPath))
		refs = append(refs, absRef)
	}

	return &Project{
		Path:       relPath,
		Dir:        filepath.Dir(relPath),
		Name:       name,
		References: refs,
		IsTest:     isTest,
	}, nil
}

// buildDependencyGraph returns a map of project path -> projects that depend on it (reverse graph)
func buildDependencyGraph(projects []*Project) map[string][]string {
	// Map absolute paths to relative paths
	absToRel := make(map[string]string)
	for _, p := range projects {
		abs, _ := filepath.Abs(p.Path)
		absToRel[abs] = p.Path
	}

	// Build reverse dependency graph
	graph := make(map[string][]string)
	for _, p := range projects {
		for _, ref := range p.References {
			if relRef, ok := absToRel[ref]; ok {
				graph[relRef] = append(graph[relRef], p.Path)
			}
		}
	}
	return graph
}

// buildForwardDependencyGraph returns a map of project path -> projects it depends on
func buildForwardDependencyGraph(projects []*Project) map[string][]string {
	// Map absolute paths to relative paths
	absToRel := make(map[string]string)
	for _, p := range projects {
		abs, _ := filepath.Abs(p.Path)
		absToRel[abs] = p.Path
	}

	// Build forward dependency graph
	graph := make(map[string][]string)
	for _, p := range projects {
		for _, ref := range p.References {
			if relRef, ok := absToRel[ref]; ok {
				graph[p.Path] = append(graph[p.Path], relRef)
			}
		}
	}
	return graph
}

// getTransitiveDependencies returns all transitive dependencies of a project
func getTransitiveDependencies(projectPath string, forwardGraph map[string][]string) []string {
	visited := make(map[string]bool)
	var result []string

	var visit func(path string)
	visit = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
		result = append(result, path)
		for _, dep := range forwardGraph[path] {
			visit(dep)
		}
	}

	// Start from direct dependencies (don't include the project itself)
	for _, dep := range forwardGraph[projectPath] {
		visit(dep)
	}
	return result
}

func loadCache(path string) *Cache {
	cache := &Cache{Projects: make(map[string]*ProjectState)}

	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}

	json.Unmarshal(data, cache)
	if cache.Projects == nil {
		cache.Projects = make(map[string]*ProjectState)
	}
	return cache
}

func saveCache(path string, cache *Cache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func findChangedProjects(cache *Cache, projects []*Project, root string) map[string]bool {
	changed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range projects {
		wg.Add(1)
		go func(p *Project) {
			defer wg.Done()
			if projectChanged(cache, p, root) {
				mu.Lock()
				changed[p.Path] = true
				mu.Unlock()
			}
		}(p)
	}

	wg.Wait()
	return changed
}

func projectChanged(cache *Cache, p *Project, root string) bool {
	state, ok := cache.Projects[p.Path]
	if !ok {
		// Never tested, consider changed
		return true
	}

	projectDir := filepath.Join(root, p.Dir)

	// Scan files, early exit on first change
	changed := false
	filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || changed {
			return filepath.SkipAll
		}
		if d.IsDir() {
			name := d.Name()
			if name == "bin" || name == "obj" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only check relevant files
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".cs" && ext != ".csproj" && ext != ".razor" && ext != ".props" && ext != ".targets" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		cachedFile, ok := state.Files[relPath]
		currentMtime := info.ModTime().UnixNano()

		if !ok {
			// New file
			if *flagVerbose {
				fmt.Fprintf(os.Stderr, "  new file: %s\n", relPath)
			}
			changed = true
			return filepath.SkipAll
		}

		if currentMtime == cachedFile.Mtime {
			// Mtime unchanged, skip (fast path)
			return nil
		}

		// Mtime changed, check hash
		currentHash := hashFile(path)
		if currentHash != cachedFile.Hash {
			if *flagVerbose {
				fmt.Fprintf(os.Stderr, "  changed: %s\n", relPath)
			}
			changed = true
			return filepath.SkipAll
		}

		// Hash same, mtime different (e.g., git checkout) - not actually changed
		if *flagVerbose {
			fmt.Fprintf(os.Stderr, "  mtime changed but hash same: %s\n", relPath)
		}
		return nil
	})

	return changed
}

func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:16])
}

func findAffectedProjects(changed map[string]bool, graph map[string][]string, projects []*Project) map[string]bool {
	affected := make(map[string]bool)

	// Copy changed to affected
	for p := range changed {
		affected[p] = true
	}

	// BFS to find all dependents
	queue := make([]string, 0, len(changed))
	for p := range changed {
		queue = append(queue, p)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, dep := range graph[current] {
			if !affected[dep] {
				affected[dep] = true
				queue = append(queue, dep)
			}
		}
	}

	return affected
}

type runResult struct {
	project  *Project
	success  bool
	output   string
	duration time.Duration
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

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorDim    = "\033[2m"
)

func extractTestStats(output string) string {
	match := testStatsRegex.FindStringSubmatch(output)
	if match == nil {
		return ""
	}
	return fmt.Sprintf("Failed: %s, Passed: %s, Skipped: %s, Total: %s", match[1], match[2], match[3], match[4])
}

func (w *statusLineWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	w.buffer.Write(p)

	w.mu.Lock()
	directMode := w.directMode
	w.mu.Unlock()

	// In direct mode, just print to stderr
	if directMode {
		os.Stderr.Write(p)
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
					fmt.Fprintf(os.Stderr, "\r\033[K  ✗ %s\n\n", w.project.Name)
					// Print buffered output
					os.Stderr.Write(w.buffer.Bytes())
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

func runDotnetCommand(command string, projects []*Project, extraArgs []string, root string, cache *Cache, cachePath string, forwardGraph map[string][]string, projectsByPath map[string]*Project, cachedProjects []*Project) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	numWorkers := *flagParallel
	if numWorkers <= 0 {
		numWorkers = len(projects)
	}
	if numWorkers > len(projects) {
		numWorkers = len(projects)
	}

	if len(cachedProjects) > 0 {
		fmt.Fprintf(os.Stderr, "Running %s on %d projects (%d cached, %d workers)...\n", command, len(projects), len(cachedProjects), numWorkers)
	} else {
		fmt.Fprintf(os.Stderr, "Running %s on %d projects (%d workers)...\n", command, len(projects), numWorkers)
	}

	// Print cached projects at the top if requested
	if *flagShowCached && len(cachedProjects) > 0 {
		for _, p := range cachedProjects {
			fmt.Fprintf(os.Stderr, "  %s○ %s (cached)%s\n", colorDim, p.Name, colorReset)
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
				args := []string{command, projectPath, "--property:WarningLevel=0"}
				args = append(args, extraArgs...)

				cmd := exec.Command("dotnet", args...)

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
				cmd.Env = append(os.Environ(),
					"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_COLOR_REDIRECTION=1",
					"TERM=xterm-256color",
				)

				err := cmd.Run()
				duration := time.Since(projectStart)

				select {
				case <-ctx.Done():
					return
				case results <- runResult{project: p, success: err == nil, output: output.String(), duration: duration}:
				}
			}
		}()
	}

	// Send jobs
	for _, p := range projects {
		jobs <- p
	}
	close(jobs)

	// Mutex for cache updates
	var cacheMu sync.Mutex

	clearStatus := func() {
		fmt.Fprintf(os.Stderr, "\r\033[K")
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
		fmt.Fprintf(os.Stderr, "\r\033[K%s%s", prefix, line)
	}

	// Track last status for heartbeat
	var lastProject, lastLine string
	var lastMu sync.Mutex

	// Heartbeat ticker to show we're still alive
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Collect results
	succeeded := 0
	completed := 0
	var failures []runResult
	directPrinted := make(map[string]bool) // Track which failures were already printed via direct mode

	for completed < len(projects) {
		select {
		case <-ticker.C:
			lastMu.Lock()
			if lastProject != "" {
				showStatus(lastProject, lastLine+" ...")
			} else {
				elapsed := time.Since(startTime).Round(time.Second)
				fmt.Fprintf(os.Stderr, "\r\033[K  [%s] waiting...", elapsed)
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
			clearStatus()
			stats := extractTestStats(r.output)
			if r.success {
				succeeded++
				if stats != "" {
					fmt.Fprintf(os.Stderr, "  %s✓%s %s (%s) (%s)\n", colorGreen, colorReset, r.project.Name, r.duration.Round(time.Millisecond), stats)
				} else {
					fmt.Fprintf(os.Stderr, "  %s✓%s %s (%s)\n", colorGreen, colorReset, r.project.Name, r.duration.Round(time.Millisecond))
				}

				// Mark successful immediately (including dependencies)
				cacheMu.Lock()
				projectsToMark := []*Project{r.project}
				// Also mark all transitive dependencies
				for _, depPath := range getTransitiveDependencies(r.project.Path, forwardGraph) {
					if dep, ok := projectsByPath[depPath]; ok {
						projectsToMark = append(projectsToMark, dep)
					}
				}
				markSuccessForProjects(cache, projectsToMark, root)
				saveCache(cachePath, cache)
				cacheMu.Unlock()
			} else {
				failures = append(failures, r)
				// Check if output was already printed in direct mode
				alreadyPrinted := false
				select {
				case <-stopNewJobs:
					alreadyPrinted = true
					directPrinted[r.project.Name] = true
				default:
				}

				// Print failure inline with stats
				if stats != "" {
					fmt.Fprintf(os.Stderr, "  %s✗%s %s (%s) (%s)\n", colorRed, colorReset, r.project.Name, r.duration.Round(time.Millisecond), stats)
				} else {
					fmt.Fprintf(os.Stderr, "  %s✗%s %s (%s)\n", colorRed, colorReset, r.project.Name, r.duration.Round(time.Millisecond))
				}

				if !*flagKeepGoing {
					// Print output if not already printed
					if !alreadyPrinted {
						fmt.Fprintf(os.Stderr, "\n%s\n", r.output)
					}
					cancel() // Stop other goroutines
					totalDuration := time.Since(startTime).Round(time.Millisecond)
					if len(cachedProjects) > 0 {
						fmt.Fprintf(os.Stderr, "\n%s%d/%d succeeded%s, %d cached (%s)\n", colorRed, succeeded, len(projects), colorReset, len(cachedProjects), totalDuration)
					} else {
						fmt.Fprintf(os.Stderr, "\n%s%d/%d succeeded%s (%s)\n", colorRed, succeeded, len(projects), colorReset, totalDuration)
					}
					return false
				}
			}
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
			fmt.Fprintf(os.Stderr, "\n--- Failure Output ---\n")
			for _, f := range failures {
				if !directPrinted[f.project.Name] {
					fmt.Fprintf(os.Stderr, "\n=== %s ===\n%s\n", f.project.Name, f.output)
				}
			}
		}
	}

	if len(failures) > 0 {
		if len(cachedProjects) > 0 {
			fmt.Fprintf(os.Stderr, "\n%s%d/%d succeeded%s, %d cached (%s)\n", colorRed, succeeded, len(projects), colorReset, len(cachedProjects), totalDuration)
		} else {
			fmt.Fprintf(os.Stderr, "\n%s%d/%d succeeded%s (%s)\n", colorRed, succeeded, len(projects), colorReset, totalDuration)
		}
	} else {
		if len(cachedProjects) > 0 {
			fmt.Fprintf(os.Stderr, "\n%s%d/%d succeeded%s, %d cached (%s)\n", colorGreen, succeeded, len(projects), colorReset, len(cachedProjects), totalDuration)
		} else {
			fmt.Fprintf(os.Stderr, "\n%s%d/%d succeeded%s (%s)\n", colorGreen, succeeded, len(projects), colorReset, totalDuration)
		}
	}
	return len(failures) == 0
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func getTerminalWidth() int {
	ws := &winsize{}
	ret, _, _ := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stderr),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)))

	if int(ret) == 0 && ws.Col > 0 {
		return int(ws.Col)
	}

	// Fallback to COLUMNS env
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}

	return 80 // Default
}

func markSuccessForProjects(cache *Cache, projects []*Project, root string) {
	now := time.Now()

	for _, p := range projects {
		state := &ProjectState{
			LastSuccess: now,
			Files:       make(map[string]*FileState),
		}

		projectDir := filepath.Join(root, p.Dir)
		filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == "bin" || name == "obj" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".cs" && ext != ".csproj" && ext != ".razor" && ext != ".props" && ext != ".targets" {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			relPath, _ := filepath.Rel(root, path)
			state.Files[relPath] = &FileState{
				Mtime: info.ModTime().UnixNano(),
				Hash:  hashFile(path),
			}
			return nil
		})

		cache.Projects[p.Path] = state
	}
}

func markSuccess(cache *Cache, projects []*Project, root string) {
	now := time.Now()

	for _, p := range projects {
		state := &ProjectState{
			LastSuccess: now,
			Files:       make(map[string]*FileState),
		}

		projectDir := filepath.Join(root, p.Dir)
		filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == "bin" || name == "obj" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".cs" && ext != ".csproj" && ext != ".razor" && ext != ".props" && ext != ".targets" {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			relPath, _ := filepath.Rel(root, path)
			state.Files[relPath] = &FileState{
				Mtime: info.ModTime().UnixNano(),
				Hash:  hashFile(path),
			}
			return nil
		})

		cache.Projects[p.Path] = state
	}
}

