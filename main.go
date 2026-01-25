package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
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
	"syscall"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"github.com/runar-rkmedia/donotnet/coverage"
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
	flagParseCoverage = flag.String("parse-coverage", "", "Parse a Cobertura coverage XML file and print covered files as JSON")
	flagCoverage      = flag.Bool("coverage", false, "Collect code coverage during test runs (adds --collect:\"XPlat Code Coverage\")")
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
  donotnet -watch test              # Watch for changes and rerun tests
  donotnet -watch -coverage test    # Watch mode with coverage-based selection
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

// canSkipRestore checks if --no-restore can be safely used
// Returns true if obj/project.assets.json exists and is newer than the .csproj
func canSkipRestore(projectPath string) bool {
	projectDir := filepath.Dir(projectPath)
	assetsPath := filepath.Join(projectDir, "obj", "project.assets.json")

	assetsInfo, err := os.Stat(assetsPath)
	if err != nil {
		return false
	}

	projectInfo, err := os.Stat(projectPath)
	if err != nil {
		return false
	}

	return assetsInfo.ModTime().After(projectInfo.ModTime())
}

// canSkipBuild checks if --no-build can be safely used
// Returns true if output DLL exists and is newer than all source files
func canSkipBuild(projectPath string) bool {
	projectDir := filepath.Dir(projectPath)
	projectName := strings.TrimSuffix(filepath.Base(projectPath), ".csproj")

	// Find the output DLL - check common locations
	var dllInfo os.FileInfo

	// Check bin/Debug and bin/Release with various target frameworks
	binDir := filepath.Join(projectDir, "bin")
	filepath.WalkDir(binDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), projectName+".dll") {
			info, err := d.Info()
			if err == nil {
				if dllInfo == nil || info.ModTime().After(dllInfo.ModTime()) {
					dllInfo = info
				}
			}
		}
		return nil
	})

	if dllInfo == nil {
		return false
	}

	// Check if any source file is newer than the DLL
	newerSourceFound := false
	filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || newerSourceFound {
			return filepath.SkipAll
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

		if info.ModTime().After(dllInfo.ModTime()) {
			newerSourceFound = true
			return filepath.SkipAll
		}
		return nil
	})

	return !newerSourceFound
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
	LastSuccess int64  // Unix timestamp
	CreatedAt   int64  // Unix timestamp
	Output      []byte // Captured stdout from the run
}

// encodeCacheEntry encodes a cache entry to bytes
// Format: [LastSuccess:8][CreatedAt:8][OutputLen:4][Output:OutputLen]
func encodeCacheEntry(e cacheEntry) []byte {
	outputLen := len(e.Output)
	buf := make([]byte, 20+outputLen)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(e.LastSuccess))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(e.CreatedAt))
	binary.LittleEndian.PutUint32(buf[16:20], uint32(outputLen))
	if outputLen > 0 {
		copy(buf[20:], e.Output)
	}
	return buf
}

// decodeCacheEntry decodes a cache entry from bytes
func decodeCacheEntry(data []byte) cacheEntry {
	if len(data) < 16 {
		return cacheEntry{}
	}
	entry := cacheEntry{
		LastSuccess: int64(binary.LittleEndian.Uint64(data[0:8])),
		CreatedAt:   int64(binary.LittleEndian.Uint64(data[8:16])),
	}
	// Handle old format (16 bytes) and new format (20+ bytes)
	if len(data) >= 20 {
		outputLen := binary.LittleEndian.Uint32(data[16:20])
		if len(data) >= 20+int(outputLen) {
			// Must copy - bbolt's buffer is only valid during transaction
			entry.Output = make([]byte, outputLen)
			copy(entry.Output, data[20:20+outputLen])
		}
	}
	return entry
}

// cacheResult contains the result of a cache lookup
type cacheResult struct {
	Time   time.Time
	Output []byte
}

// lookupCache checks if a cache entry exists and returns the result
func lookupCache(db *bolt.DB, key string) *cacheResult {
	var result *cacheResult
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
		result = &cacheResult{
			Time:   time.Unix(entry.LastSuccess, 0),
			Output: entry.Output,
		}
		return nil
	})
	return result
}

// markCacheSuccess records a successful test/build for the given key
func markCacheSuccess(db *bolt.DB, key string, t time.Time, output []byte) error {
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
			Output:      output,
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
	for i, arg := range args {
		if arg == "--" {
			dotnetArgs = args[i+1:]
			break
		}
		if command == "" && (arg == "test" || arg == "build") {
			command = arg
		}
	}

	// Show help if no command and no action flags
	if command == "" && *flagListAffected == "" && !*flagCacheStats && *flagCacheClean < 0 {
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

	gitRoot, err := findGitRoot()
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
		db, err := openCacheDB(cachePath)
		if err != nil {
			term.Errorf("opening cache: %v", err)
			os.Exit(1)
		}
		defer db.Close()

		if *flagCacheClean >= 0 {
			maxAge := time.Duration(*flagCacheClean) * 24 * time.Hour
			deleted, err := deleteOldEntries(db, maxAge)
			if err != nil {
				term.Errorf("cleaning cache: %v", err)
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
		term.Errorf("finding projects: %v", err)
		os.Exit(1)
	}

	// Set verbose mode on terminal
	term.SetVerbose(*flagVerbose)
	term.Verbose("Found %d projects", len(projects))

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
		term.Errorf("opening cache: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Get git state
	commit := getGitCommit(gitRoot)
	dirtyFiles := getGitDirtyFiles(gitRoot)
	argsHash := hashArgs(append([]string{command}, dotnetArgs...)) // include command in hash

	term.Verbose("Git commit: %s", commit)
	if len(dirtyFiles) > 0 {
		term.Verbose("Dirty files: %d", len(dirtyFiles))
		for _, f := range dirtyFiles {
			term.Verbose("  %s", f)
		}
	} else {
		term.Verbose("Working tree clean")
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
			vcsChangedFiles, err = getGitChangedFiles(gitRoot, *flagVcsRef)
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
	changed := findChangedProjects(db, projects, gitRoot, commit, dirtyFiles, argsHash, forwardGraph, vcsChangedFiles, useVcsFilter)

	term.Verbose("Changed projects: %v", changed)

	// Find affected projects (changed + dependents)
	affected := findAffectedProjects(changed, graph, projects)

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

	// Handle commands
	if command != "" {
		// Watch mode
		if *flagWatch {
			// Run initial test/build first
			if len(targetProjects) > 0 {
				runDotnetCommand(command, targetProjects, dotnetArgs, gitRoot, db, commit, dirtyFiles, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir, nil)
			} else {
				term.Dim("No affected projects to %s (%d cached)", command, len(cachedProjects))
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

			watchCtx := &watchContext{
				command:        command,
				dotnetArgs:     dotnetArgs,
				gitRoot:        gitRoot,
				db:             db,
				projects:       projects,
				graph:          graph,
				forwardGraph:   forwardGraph,
				projectsByPath: projectsByPath,
				reportsDir:     reportsDir,
				argsHash:       argsHash,
				testFilter:     NewTestFilter(),
				coverageMap:    covMap,
			}
			runWatchMode(watchCtx)
			return
		}

		if len(targetProjects) == 0 {
			if !*flagQuiet {
				term.Dim("No affected projects to %s (%d cached)", command, len(cachedProjects))
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
					relevantDirs := getProjectRelevantDirs(p, forwardGraph)
					projectDirtyFiles := filterFilesToProject(dirtyFiles, relevantDirs)
					dirtyHash := computeDirtyHash(gitRoot, projectDirtyFiles)
					key := makeCacheKey(commit, dirtyHash, argsHash, p.Path)
					if result := lookupCache(db, key); result != nil && len(result.Output) > 0 {
						fmt.Printf("=== %s ===\n%s\n", p.Name, string(result.Output))
					}
				}
			}
			return
		}

		success := runDotnetCommand(command, targetProjects, dotnetArgs, gitRoot, db, commit, dirtyFiles, argsHash, forwardGraph, projectsByPath, cachedProjects, reportsDir, nil)
		if !success {
			os.Exit(1)
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
				term.Verbose("warning: failed to parse %s: %v", relPath, err)
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
				term.Verbose("  vcs candidate: %s (%d files)", p.Name, len(projectVcsFiles))
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
		term.Verbose("  forced: %s (key=%s)", p.Name, key)
		return true
	}

	// Check cache
	if result := lookupCache(db, key); result != nil {
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
	project        *Project
	success        bool
	output         string
	duration       time.Duration
	skippedBuild   bool
	skippedRestore bool
	filteredTests  bool     // true if only specific tests were run
	testClasses    []string // test classes that were run (if filtered)
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
	failedN, _ := strconv.Atoi(failed)
	passedN, _ := strconv.Atoi(passed)
	skippedN, _ := strconv.Atoi(skipped)
	totalN, _ := strconv.Atoi(total)

	// Failed: dim if 0, red otherwise
	var failedStr string
	if failedN == 0 {
		failedStr = fmt.Sprintf("%sFailed: %2s%s", colorDim, failed, colorReset)
	} else {
		failedStr = fmt.Sprintf("%sFailed: %2s%s", colorRed, failed, colorReset)
	}

	// Passed: green if passed+skipped=total (all accounted for)
	var passedStr string
	if passedN+skippedN == totalN {
		passedStr = fmt.Sprintf("%sPassed: %3s%s", colorGreen, passed, colorReset)
	} else {
		passedStr = fmt.Sprintf("Passed: %3s", passed)
	}

	// Skipped: dim if 0, yellow otherwise
	var skippedStr string
	if skippedN == 0 {
		skippedStr = fmt.Sprintf("%sSkipped: %2s%s", colorDim, skipped, colorReset)
	} else {
		skippedStr = fmt.Sprintf("%sSkipped: %2s%s", colorYellow, skipped, colorReset)
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
					term.Status("  %s✗%s %s\n\n", colorRed, colorReset, w.project.Name)
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

func runDotnetCommand(command string, projects []*Project, extraArgs []string, root string, db *bolt.DB, commit string, dirtyFiles []string, argsHash string, forwardGraph map[string][]string, projectsByPath map[string]*Project, cachedProjects []*Project, reportsDir string, testFilter *TestFilter) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C to kill all running processes
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
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
		if len(cachedProjects) > 0 {
			term.Printf("Running %s on %d projects (%d cached, %d workers)...\n", command, len(projects), len(cachedProjects), numWorkers)
		} else {
			term.Printf("Running %s on %d projects (%d workers)...\n", command, len(projects), numWorkers)
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
				args := []string{command, projectPath, "--property:WarningLevel=0"}

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
					// --no-build is only valid for test, not build
					if command == "test" && !hasNoBuild {
						if canSkipBuild(projectPath) {
							args = append(args, "--no-build")
							hasNoBuild = true
							skippedBuild = true
							term.Verbose("  [%s] skipping build (up-to-date)", p.Name)
						} else {
							term.Verbose("  [%s] cannot skip build: source files newer than DLL", p.Name)
						}
					}
					if !hasNoRestore && !hasNoBuild {
						if canSkipRestore(projectPath) {
							args = append(args, "--no-restore")
							skippedRestore = true
							term.Verbose("  [%s] skipping restore (up-to-date)", p.Name)
						} else if term.verbose {
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
				if command == "test" && testFilter != nil {
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

				// Add TRX logger if reports enabled
				var trxPath string
				if !*flagNoReports && command == "test" {
					os.MkdirAll(reportsDir, 0755)
					trxPath = filepath.Join(reportsDir, p.Name+".trx")
					args = append(args, "--logger", "trx;LogFileName="+trxPath)
				}

				args = append(args, extraArgs...)

				cmd := exec.CommandContext(ctx, "dotnet", args...)
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Cancel = func() error {
					// Kill the entire process group
					return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				}

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
				case results <- runResult{project: p, success: err == nil, output: output.String(), duration: duration, skippedBuild: skippedBuild, skippedRestore: skippedRestore, filteredTests: filteredTests, testClasses: testClasses}:
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
				// In quiet mode, skip progress output
				if r.success {
					succeeded++
				} else {
					failures = append(failures, r)
				}
				continue
			}
			clearStatus()
			stats := extractTestStats(r.output)
			skipIndicator := term.SkipIndicator(r.skippedBuild, r.skippedRestore)

			// Pad project name and duration for alignment
			paddedName := fmt.Sprintf("%-*s", maxNameLen, r.project.Name)
			durationStr := fmt.Sprintf("%7s", r.duration.Round(time.Millisecond))

			// Add filter info if tests were filtered
			filterInfo := ""
			if r.filteredTests && len(r.testClasses) > 0 {
				filterInfo = fmt.Sprintf("  %s[%s]%s", colorCyan, strings.Join(r.testClasses, ", "), colorReset)
			}

			if r.success {
				succeeded++
				term.ResultLine(true, skipIndicator, paddedName, durationStr, stats, filterInfo)

				// Touch project.assets.json to ensure mtime is updated for future skip detection
				// (dotnet doesn't always rewrite it if packages haven't changed)
				projectPath := filepath.Join(root, r.project.Path)
				assetsPath := filepath.Join(filepath.Dir(projectPath), "obj", "project.assets.json")
				now := time.Now()
				os.Chtimes(assetsPath, now, now)

				// Mark successful immediately (including dependencies)
				// Store output only for the project that ran
				relevantDirs := getProjectRelevantDirs(r.project, forwardGraph)
				projectDirtyFiles := filterFilesToProject(dirtyFiles, relevantDirs)
				dirtyHash := computeDirtyHash(root, projectDirtyFiles)
				key := makeCacheKey(commit, dirtyHash, argsHash, r.project.Path)
				markCacheSuccess(db, key, now, []byte(r.output))

				// Also mark transitive dependencies (without output)
				for _, depPath := range getTransitiveDependencies(r.project.Path, forwardGraph) {
					if dep, ok := projectsByPath[depPath]; ok {
						depRelevantDirs := getProjectRelevantDirs(dep, forwardGraph)
						depDirtyFiles := filterFilesToProject(dirtyFiles, depRelevantDirs)
						depDirtyHash := computeDirtyHash(root, depDirtyFiles)
						depKey := makeCacheKey(commit, depDirtyHash, argsHash, dep.Path)
						markCacheSuccess(db, depKey, now, nil)
					}
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
				term.ResultLine(false, skipIndicator, paddedName, durationStr, stats, "")

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

	term.Summary(succeeded, len(projects), len(cachedProjects), totalDuration, len(failures) == 0)

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
			relevantDirs := getProjectRelevantDirs(p, forwardGraph)
			projectDirtyFiles := filterFilesToProject(dirtyFiles, relevantDirs)
			dirtyHash := computeDirtyHash(root, projectDirtyFiles)
			key := makeCacheKey(commit, dirtyHash, argsHash, p.Path)
			if result := lookupCache(db, key); result != nil && len(result.Output) > 0 {
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
				fmt.Printf("=== %s ===\n%s\n", o.name, o.output)
			}
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

// ============================================================================
// Watch Mode
// ============================================================================

// watchContext holds all the state needed for watch mode
type watchContext struct {
	command        string
	dotnetArgs     []string
	gitRoot        string
	db             *bolt.DB
	projects       []*Project
	graph          map[string][]string // reverse dependency graph
	forwardGraph   map[string][]string
	projectsByPath map[string]*Project
	projectsByDir  map[string]*Project // maps directory to project
	reportsDir     string
	argsHash       string
	testFilter     *TestFilter    // tracks changed files for smart test filtering
	coverageMap    *coverage.Map  // maps source files to test projects that cover them
}

// relevantExtensions are file extensions we care about
var relevantExtensions = map[string]bool{
	".cs":      true,
	".csproj":  true,
	".razor":   true,
	".props":   true,
	".targets": true,
}

// buildCoverageMap builds a coverage map from all test projects
func buildCoverageMap(gitRoot string, projects []*Project) *coverage.Map {
	var testProjects []coverage.TestProject
	for _, p := range projects {
		if p.IsTest {
			testProjects = append(testProjects, coverage.TestProject{
				Path: p.Path,
				Dir:  p.Dir,
			})
		}
	}

	if len(testProjects) == 0 {
		return nil
	}

	return coverage.BuildMap(gitRoot, testProjects)
}

func runWatchMode(ctx *watchContext) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		term.Errorf("creating watcher: %v", err)
		os.Exit(1)
	}
	defer watcher.Close()

	// Add all project directories to watcher
	watchedDirs := make(map[string]bool)
	for _, p := range ctx.projects {
		projectDir := filepath.Join(ctx.gitRoot, p.Dir)
		if err := addDirRecursive(watcher, projectDir, watchedDirs); err != nil {
			term.Verbose("warning: failed to watch %s: %v", projectDir, err)
		}
	}

	term.Info("Watching %d directories for changes (Ctrl+C to stop)...", len(watchedDirs))

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Debounce timer
	var debounceTimer *time.Timer
	pendingChanges := make(map[string]bool)   // project paths with pending changes
	pendingFiles := make(map[string]struct{}) // changed file paths (relative to gitRoot)
	var pendingMu sync.Mutex

	// Initialize test filter if not already done
	if ctx.testFilter == nil {
		ctx.testFilter = NewTestFilter()
	}

	runPending := func() {
		pendingMu.Lock()
		if len(pendingChanges) == 0 && len(pendingFiles) == 0 {
			pendingMu.Unlock()
			return
		}

		// Collect changed projects and files
		changedProjects := make(map[string]bool)
		for p := range pendingChanges {
			changedProjects[p] = true
		}
		changedFiles := make([]string, 0, len(pendingFiles))
		for f := range pendingFiles {
			changedFiles = append(changedFiles, f)
		}
		pendingChanges = make(map[string]bool)
		pendingFiles = make(map[string]struct{})

		// Copy test filter and clear for next batch
		testFilter := ctx.testFilter
		ctx.testFilter = NewTestFilter()
		pendingMu.Unlock()

		// Determine target test projects using coverage map or fallback
		var targetProjects []*Project
		usedCoverage := false

		if ctx.command == "test" && ctx.coverageMap != nil && ctx.coverageMap.HasCoverage() {
			// Try coverage-based selection
			coveredTestProjects := make(map[string]bool)
			uncoveredFiles := []string{}

			for _, f := range changedFiles {
				testProjs := ctx.coverageMap.GetTestProjectsForFile(f)
				if len(testProjs) > 0 {
					for _, tp := range testProjs {
						coveredTestProjects[tp] = true
					}
				} else {
					uncoveredFiles = append(uncoveredFiles, f)
				}
			}

			if len(uncoveredFiles) == 0 && len(coveredTestProjects) > 0 {
				// All files are covered - use coverage-based selection
				usedCoverage = true
				for _, p := range ctx.projects {
					if coveredTestProjects[p.Path] {
						targetProjects = append(targetProjects, p)
					}
				}
				term.Verbose("  coverage-based: %d test projects for %d files",
					len(targetProjects), len(changedFiles))
			} else if len(uncoveredFiles) > 0 {
				// Some files not in coverage map - will fall back
				term.Verbose("  uncovered files: %v", uncoveredFiles)
			}
		}

		if !usedCoverage {
			// Fallback: use dependency-based selection
			affected := findAffectedProjects(changedProjects, ctx.graph, ctx.projects)

			for _, p := range ctx.projects {
				if ctx.command == "test" && !p.IsTest {
					continue
				}
				if affected[p.Path] {
					targetProjects = append(targetProjects, p)
				}
			}
			if ctx.coverageMap != nil {
				term.Verbose("  fallback to dependencies: %d projects", len(targetProjects))
			}
		}

		if len(targetProjects) == 0 {
			return
		}

		term.Println()

		// Get current git state for cache
		commit := getGitCommit(ctx.gitRoot)
		dirtyFiles := getGitDirtyFiles(ctx.gitRoot)

		runDotnetCommand(ctx.command, targetProjects, ctx.dotnetArgs, ctx.gitRoot, ctx.db, commit, dirtyFiles, ctx.argsHash, ctx.forwardGraph, ctx.projectsByPath, nil, ctx.reportsDir, testFilter)

		term.Info("\nWatching for changes...")
	}

	for {
		select {
		case <-sigChan:
			term.Dim("\nStopping watch mode...")
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Only care about writes and creates
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// Check if it's a relevant file
			ext := strings.ToLower(filepath.Ext(event.Name))
			if !relevantExtensions[ext] {
				continue
			}

			// Find which project this file belongs to
			relPath, err := filepath.Rel(ctx.gitRoot, event.Name)
			if err != nil {
				continue
			}

			var affectedProject *Project
			for _, p := range ctx.projects {
				if strings.HasPrefix(relPath, p.Dir+"/") || strings.HasPrefix(relPath, p.Dir+"\\") {
					affectedProject = p
					break
				}
			}

			if affectedProject == nil {
				continue
			}

			term.Verbose("  changed: %s (%s)", relPath, affectedProject.Name)

			pendingMu.Lock()
			pendingChanges[affectedProject.Path] = true
			pendingFiles[relPath] = struct{}{}
			ctx.testFilter.AddChangedFile(affectedProject.Path, relPath)
			pendingMu.Unlock()

			// Debounce - wait 100ms for more changes before running
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(100*time.Millisecond, runPending)

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			term.Errorf("watcher: %v", err)
		}
	}
}

func addDirRecursive(watcher *fsnotify.Watcher, dir string, watched map[string]bool) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		// Skip non-project directories
		if name == "bin" || name == "obj" || name == ".git" || name == "node_modules" || name == ".vs" {
			return filepath.SkipDir
		}

		if watched[path] {
			return nil
		}

		if err := watcher.Add(path); err != nil {
			return err
		}
		watched[path] = true
		return nil
	})
}
