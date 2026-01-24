package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	bolt "go.etcd.io/bbolt"
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

// Project represents a parsed .csproj
type Project struct {
	Path       string
	Dir        string
	Name       string
	References []string // paths to referenced projects
	IsTest     bool
}

var (
	flagMark        = flag.Bool("mark", false, "Mark current state as successful (update cache)")
	flagTests       = flag.Bool("tests", false, "Only output test projects")
	flagAll         = flag.Bool("all", false, "Output all projects, not just affected")
	flagVerbose     = flag.Bool("v", false, "Verbose output")
	flagCacheDir    = flag.String("cache-dir", "", "Cache directory path (default: .donotnet in git root)")
	flagDir         = flag.String("C", "", "Change to directory before running")
	flagVersion     = flag.Bool("version", false, "Show version and build info")
	flagParallel    = flag.Int("j", 0, "Number of parallel workers (default: number of projects)")
	flagLocal       = flag.Bool("local", false, "Only scan current directory, not entire git repo")
	flagKeepGoing   = flag.Bool("k", false, "Keep going on errors (don't stop on first failure)")
	flagShowCached  = flag.Bool("show-cached", false, "Show cached projects in output")
	flagNoReports   = flag.Bool("no-reports", false, "Disable saving test reports (TRX and console output)")
	flagVcsChanged  = flag.Bool("vcs-changed", false, "Only test projects with uncommitted changes")
	flagVcsRef      = flag.String("vcs-ref", "", "Only test projects changed vs specified ref (e.g., 'main', 'origin/main', 'HEAD~3')")
	flagCacheStats  = flag.Bool("cache-stats", false, "Show cache statistics")
	flagCacheClean  = flag.Int("cache-clean", -1, "Remove cache entries older than N days (-1 = disabled)")
	flagForce       = flag.Bool("force", false, "Run all projects, ignoring cache (still updates cache on success)")
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
  (none)    List affected projects

Flags:
`, versionString())
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  donotnet test                     # Run tests on affected projects
  donotnet build                    # Build affected projects
  donotnet test -- --no-build       # Run tests without building
  donotnet -tests                   # List affected test projects
  donotnet -mark                    # Manually mark all as successful
  donotnet -C /path/to/repo test    # Run in different directory
  donotnet -vcs-changed test        # Test projects with uncommitted changes
  donotnet -vcs-ref=main test       # Test projects changed vs main branch
  donotnet -force test              # Run all tests, ignoring cache
  donotnet -cache-stats             # Show cache statistics
  donotnet -cache-clean=30          # Remove entries older than 30 days
`)
	}
}

// ============================================================================
// Git Helper Functions
// ============================================================================

// getGitCommit returns the current HEAD commit hash (short)
func getGitCommit(gitRoot string) string {
	cmd := exec.Command("git", "-C", gitRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getGitDirtyFiles returns a list of dirty (uncommitted) files relative to git root
func getGitDirtyFiles(gitRoot string) []string {
	cmd := exec.Command("git", "-C", gitRoot, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 3 {
			continue
		}
		// Format: XY filename or XY orig -> renamed
		file := strings.TrimSpace(line[3:])
		// Handle renames: "old -> new"
		if idx := strings.Index(file, " -> "); idx >= 0 {
			file = file[idx+4:]
		}
		if file != "" {
			files = append(files, file)
		}
	}
	return files
}

// getGitChangedFiles returns files changed compared to a ref (e.g., "main", "HEAD~3")
// Returns an error if the ref is invalid
func getGitChangedFiles(gitRoot, ref string) ([]string, error) {
	cmd := exec.Command("git", "-C", gitRoot, "diff", "--name-only", ref)
	out, err := cmd.Output()
	if err != nil {
		// Check if ref exists
		checkCmd := exec.Command("git", "-C", gitRoot, "rev-parse", "--verify", ref)
		if checkErr := checkCmd.Run(); checkErr != nil {
			return nil, fmt.Errorf("unknown git ref: %s", ref)
		}
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		file := strings.TrimSpace(line)
		if file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}

// computeDirtyHash computes a hash of the content of the given files
// Files should be relative to gitRoot
func computeDirtyHash(gitRoot string, files []string) string {
	if len(files) == 0 {
		return ""
	}

	// Sort files for deterministic ordering
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f))
		h.Write([]byte{0})

		content, err := os.ReadFile(filepath.Join(gitRoot, f))
		if err != nil {
			// File might be deleted, use empty content
			h.Write([]byte{})
		} else {
			h.Write(content)
		}
		h.Write([]byte{0})
	}

	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}

// getProjectRelevantDirs returns the directories that are relevant to a project
// (the project's own directory + directories of all transitive dependencies)
func getProjectRelevantDirs(project *Project, forwardGraph map[string][]string) []string {
	dirs := map[string]bool{project.Dir: true}

	// Add transitive dependencies
	var visit func(path string)
	visited := make(map[string]bool)
	visit = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
		for _, dep := range forwardGraph[path] {
			dirs[filepath.Dir(dep)] = true
			visit(dep)
		}
	}
	visit(project.Path)

	result := make([]string, 0, len(dirs))
	for d := range dirs {
		result = append(result, d)
	}
	return result
}

// filterFilesToProject filters files to those relevant to a project
func filterFilesToProject(files []string, relevantDirs []string) []string {
	var result []string
	for _, f := range files {
		for _, dir := range relevantDirs {
			if strings.HasPrefix(f, dir+"/") || dir == "." {
				result = append(result, f)
				break
			}
		}
	}
	return result
}

// ============================================================================
// bbolt Cache Layer
// ============================================================================

const cacheBucket = "cache"

// openCacheDB opens or creates the cache database
func openCacheDB(path string) (*bolt.DB, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}

	// Ensure bucket exists
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(cacheBucket))
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// makeCacheKey constructs the cache key from components
func makeCacheKey(commit, dirtyHash, argsHash, projectPath string) string {
	return fmt.Sprintf("%s:%s:%s:%s", commit, dirtyHash, argsHash, projectPath)
}

// parseCacheKey parses a cache key into its components
func parseCacheKey(key string) (commit, dirtyHash, argsHash, projectPath string) {
	parts := strings.SplitN(key, ":", 4)
	if len(parts) != 4 {
		return "", "", "", ""
	}
	return parts[0], parts[1], parts[2], parts[3]
}

// cacheEntry represents a cache entry value
type cacheEntry struct {
	LastSuccess int64 // Unix timestamp
	CreatedAt   int64 // Unix timestamp
}

// encodeCacheEntry encodes a cache entry to bytes
func encodeCacheEntry(e cacheEntry) []byte {
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(e.LastSuccess))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(e.CreatedAt))
	return buf
}

// decodeCacheEntry decodes a cache entry from bytes
func decodeCacheEntry(data []byte) cacheEntry {
	if len(data) < 16 {
		return cacheEntry{}
	}
	return cacheEntry{
		LastSuccess: int64(binary.LittleEndian.Uint64(data[0:8])),
		CreatedAt:   int64(binary.LittleEndian.Uint64(data[8:16])),
	}
}

// lookupCache checks if a cache entry exists and returns the last success time
func lookupCache(db *bolt.DB, key string) *time.Time {
	var result *time.Time
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucket))
		if b == nil {
			return nil
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		entry := decodeCacheEntry(data)
		t := time.Unix(entry.LastSuccess, 0)
		result = &t
		return nil
	})
	return result
}

// markCacheSuccess records a successful test/build for the given key
func markCacheSuccess(db *bolt.DB, key string, t time.Time) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucket))
		if b == nil {
			return nil
		}

		// Check if entry exists (to preserve CreatedAt)
		existing := b.Get([]byte(key))
		entry := cacheEntry{
			LastSuccess: t.Unix(),
			CreatedAt:   t.Unix(),
		}
		if existing != nil {
			old := decodeCacheEntry(existing)
			entry.CreatedAt = old.CreatedAt
		}

		return b.Put([]byte(key), encodeCacheEntry(entry))
	})
}

// getCacheStats returns cache statistics
func getCacheStats(db *bolt.DB) (totalEntries int, oldestEntry, newestEntry time.Time, dbSize int64) {
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucket))
		if b == nil {
			return nil
		}

		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			totalEntries++
			entry := decodeCacheEntry(v)
			t := time.Unix(entry.LastSuccess, 0)
			if oldestEntry.IsZero() || t.Before(oldestEntry) {
				oldestEntry = t
			}
			if newestEntry.IsZero() || t.After(newestEntry) {
				newestEntry = t
			}
		}
		return nil
	})

	// Get database file size
	if info, err := os.Stat(db.Path()); err == nil {
		dbSize = info.Size()
	}

	return
}

// deleteOldEntries removes cache entries older than maxAge
func deleteOldEntries(db *bolt.DB, maxAge time.Duration) (deleted int, err error) {
	cutoff := time.Now().Add(-maxAge).Unix()

	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucket))
		if b == nil {
			return nil
		}

		var keysToDelete [][]byte
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			entry := decodeCacheEntry(v)
			if entry.LastSuccess < cutoff {
				keysToDelete = append(keysToDelete, append([]byte{}, k...))
			}
		}

		for _, k := range keysToDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return
}

// ============================================================================
// Main Logic
// ============================================================================

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
		db, err := openCacheDB(cachePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error opening cache: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		if *flagCacheClean >= 0 {
			maxAge := time.Duration(*flagCacheClean) * 24 * time.Hour
			deleted, err := deleteOldEntries(db, maxAge)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error cleaning cache: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Deleted %d entries older than %d days\n", deleted, *flagCacheClean)
		}

		if *flagCacheStats {
			totalEntries, oldest, newest, dbSize := getCacheStats(db)
			fmt.Printf("Cache statistics:\n")
			fmt.Printf("  Database: %s\n", cachePath)
			fmt.Printf("  Size: %d bytes (%.2f KB)\n", dbSize, float64(dbSize)/1024)
			fmt.Printf("  Total entries: %d\n", totalEntries)
			if totalEntries > 0 {
				fmt.Printf("  Oldest entry: %s\n", oldest.Format(time.RFC3339))
				fmt.Printf("  Newest entry: %s\n", newest.Format(time.RFC3339))
			}
		}
		return
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

	// Open cache database
	db, err := openCacheDB(cachePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening cache: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get git state
	commit := getGitCommit(gitRoot)
	dirtyFiles := getGitDirtyFiles(gitRoot)
	argsHash := hashArgs(dotnetArgs)

	if *flagVerbose {
		fmt.Fprintf(os.Stderr, "Git commit: %s\n", commit)
		if len(dirtyFiles) > 0 {
			fmt.Fprintf(os.Stderr, "Dirty files: %d\n", len(dirtyFiles))
			for _, f := range dirtyFiles {
				fmt.Fprintf(os.Stderr, "  %s\n", f)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Working tree clean\n")
		}
	}

	if *flagMark {
		// Update cache with current state for all projects
		now := time.Now()
		for _, p := range projects {
			relevantDirs := getProjectRelevantDirs(p, forwardGraph)
			projectDirtyFiles := filterFilesToProject(dirtyFiles, relevantDirs)
			dirtyHash := computeDirtyHash(gitRoot, projectDirtyFiles)
			key := makeCacheKey(commit, dirtyHash, argsHash, p.Path)
			markCacheSuccess(db, key, now)
		}
		if *flagVerbose {
			fmt.Fprintf(os.Stderr, "Cache updated for %d projects\n", len(projects))
		}
		return
	}

	// Determine which projects are changed
	var vcsChangedFiles []string
	useVcsFilter := *flagVcsChanged || *flagVcsRef != ""

	if useVcsFilter {
		if *flagVcsRef != "" {
			// Get diff vs specified ref
			var err error
			vcsChangedFiles, err = getGitChangedFiles(gitRoot, *flagVcsRef)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			if len(vcsChangedFiles) == 0 {
				fmt.Fprintf(os.Stderr, "No changes vs %s\n", *flagVcsRef)
				return
			}
			if *flagVerbose {
				fmt.Fprintf(os.Stderr, "VCS filter: changes vs %s (%d files)\n", *flagVcsRef, len(vcsChangedFiles))
			}
		} else {
			// Use uncommitted changes (same as dirtyFiles)
			vcsChangedFiles = dirtyFiles
			if len(vcsChangedFiles) == 0 {
				fmt.Fprintf(os.Stderr, "No uncommitted changes\n")
				return
			}
			if *flagVerbose {
				fmt.Fprintf(os.Stderr, "VCS filter: uncommitted changes (%d files)\n", len(vcsChangedFiles))
			}
		}
	}

	// Find changed projects
	changed := findChangedProjects(db, projects, gitRoot, commit, dirtyFiles, argsHash, forwardGraph, vcsChangedFiles, useVcsFilter)

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
				fmt.Fprintf(os.Stderr, "  %s\u25cb%s %s (cached)\n", colorDim, colorReset+colorDim, p.Name)
			}
			fmt.Fprintf(os.Stderr, "%s\n%s0/0 succeeded, %d cached%s\n", colorReset, colorGreen, len(cachedProjects), colorReset)
			return
		}

		success := runDotnetCommand(command, targetProjects, dotnetArgs, gitRoot, db, commit, dirtyFiles, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir)
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

func findChangedProjects(db *bolt.DB, projects []*Project, root, commit string, dirtyFiles []string, argsHash string, forwardGraph map[string][]string, vcsChangedFiles []string, useVcsFilter bool) map[string]bool {
	changed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range projects {
		wg.Add(1)
		go func(p *Project) {
			defer wg.Done()

			// If using VCS filter, first check if this project has VCS changes
			if useVcsFilter {
				relevantDirs := getProjectRelevantDirs(p, forwardGraph)
				projectVcsFiles := filterFilesToProject(vcsChangedFiles, relevantDirs)
				if len(projectVcsFiles) == 0 {
					// No VCS changes for this project, skip entirely
					return
				}
				if *flagVerbose {
					fmt.Fprintf(os.Stderr, "  vcs candidate: %s (%d files)\n", p.Name, len(projectVcsFiles))
				}
				// Fall through to cache check
			}

			if projectChanged(db, p, root, commit, dirtyFiles, argsHash, forwardGraph) {
				mu.Lock()
				changed[p.Path] = true
				mu.Unlock()
			}
		}(p)
	}

	wg.Wait()
	return changed
}

func projectChanged(db *bolt.DB, p *Project, root, commit string, dirtyFiles []string, argsHash string, forwardGraph map[string][]string) bool {
	// Get relevant directories for this project
	relevantDirs := getProjectRelevantDirs(p, forwardGraph)

	// Filter dirty files to those relevant to this project
	projectDirtyFiles := filterFilesToProject(dirtyFiles, relevantDirs)

	// Compute dirty hash for this project
	dirtyHash := computeDirtyHash(root, projectDirtyFiles)

	// Build cache key
	key := makeCacheKey(commit, dirtyHash, argsHash, p.Path)

	// Skip cache check if force flag is set
	if *flagForce {
		if *flagVerbose {
			fmt.Fprintf(os.Stderr, "  forced: %s (key=%s)\n", p.Name, key)
		}
		return true
	}

	// Check cache
	if t := lookupCache(db, key); t != nil {
		if *flagVerbose {
			fmt.Fprintf(os.Stderr, "  cache hit: %s (key=%s)\n", p.Name, key)
		}
		return false // Not changed - cache hit
	}

	if *flagVerbose {
		fmt.Fprintf(os.Stderr, "  cache miss: %s (key=%s)\n", p.Name, key)
	}
	return true // Changed - no cache entry
}

func hashArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return fmt.Sprintf("%x", h[:8])
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
	colorReset = "\033[0m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorDim   = "\033[2m"
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
					fmt.Fprintf(os.Stderr, "\r\033[K  \u2717 %s\n\n", w.project.Name)
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

func runDotnetCommand(command string, projects []*Project, extraArgs []string, root string, db *bolt.DB, commit string, dirtyFiles []string, argsHash string, forwardGraph map[string][]string, projectsByPath map[string]*Project, cachedProjects []*Project, reportsDir string) bool {
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
			fmt.Fprintf(os.Stderr, "  %s\u25cb %s (cached)%s\n", colorDim, p.Name, colorReset)
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

				// Add TRX logger if reports enabled
				var trxPath string
				if !*flagNoReports && command == "test" {
					os.MkdirAll(reportsDir, 0755)
					trxPath = filepath.Join(reportsDir, p.Name+".trx")
					args = append(args, "--logger", "trx;LogFileName="+trxPath)
				}

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

				// Save console output if reports enabled
				if !*flagNoReports {
					consolePath := filepath.Join(reportsDir, p.Name+".log")
					os.WriteFile(consolePath, output.Bytes(), 0644)
				}

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
					fmt.Fprintf(os.Stderr, "  %s\u2713%s %s (%s) (%s)\n", colorGreen, colorReset, r.project.Name, r.duration.Round(time.Millisecond), stats)
				} else {
					fmt.Fprintf(os.Stderr, "  %s\u2713%s %s (%s)\n", colorGreen, colorReset, r.project.Name, r.duration.Round(time.Millisecond))
				}

				// Mark successful immediately (including dependencies)
				now := time.Now()
				projectsToMark := []*Project{r.project}
				// Also mark all transitive dependencies
				for _, depPath := range getTransitiveDependencies(r.project.Path, forwardGraph) {
					if dep, ok := projectsByPath[depPath]; ok {
						projectsToMark = append(projectsToMark, dep)
					}
				}
				for _, mp := range projectsToMark {
					relevantDirs := getProjectRelevantDirs(mp, forwardGraph)
					projectDirtyFiles := filterFilesToProject(dirtyFiles, relevantDirs)
					dirtyHash := computeDirtyHash(root, projectDirtyFiles)
					key := makeCacheKey(commit, dirtyHash, argsHash, mp.Path)
					markCacheSuccess(db, key, now)
				}
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
					fmt.Fprintf(os.Stderr, "  %s\u2717%s %s (%s) (%s)\n", colorRed, colorReset, r.project.Name, r.duration.Round(time.Millisecond), stats)
				} else {
					fmt.Fprintf(os.Stderr, "  %s\u2717%s %s (%s)\n", colorRed, colorReset, r.project.Name, r.duration.Round(time.Millisecond))
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
