package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// TestInfo represents test information for JSON output
type TestInfo struct {
	Project string   `json:"project"`
	Tests   []string `json:"tests"`
	Count   int      `json:"count"`
}

// TestCoverageMap maps source files to the tests that cover them
type TestCoverageMap struct {
	Project        string              `json:"project"`
	FileToTests    map[string][]string `json:"file_to_tests"`    // source file -> test names
	TestToFiles    map[string][]string `json:"test_to_files"`    // test name -> source files
	GeneratedAt    time.Time           `json:"generated_at"`
	TotalTests     int                 `json:"total_tests"`
	ProcessedTests int                 `json:"processed_tests"`
}

// loadTestCoverageMap loads an existing coverage map from disk
func loadTestCoverageMap(path string) (*TestCoverageMap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var m TestCoverageMap
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// saveTestCoverageMap saves a coverage map to disk
func saveTestCoverageMap(path string, m *TestCoverageMap) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// CoverageStaleness represents the staleness state of coverage data
type CoverageStaleness int

const (
	CoverageNotFound CoverageStaleness = iota
	CoverageStale
	CoverageFresh
)

// StalenessCheckMethod specifies how to check for stale coverage
type StalenessCheckMethod int

const (
	StalenessCheckGit   StalenessCheckMethod = iota // Check git for changed files since coverage generation
	StalenessCheckMtime                             // Check file modification times
	StalenessCheckBoth                              // Check both git and mtime, report stale if either finds changes
)

// CoverageGranularity specifies how to group tests when building coverage maps
type CoverageGranularity int

const (
	CoverageGranularityMethod CoverageGranularity = iota // Run each test method individually (most precise, slowest)
	CoverageGranularityClass                             // Group tests by class name (faster, less precise)
	CoverageGranularityFile                              // Group tests by source file (fastest, file-level precision)
)

// ParseCoverageGranularity parses a coverage granularity from string
// Valid values: "method", "class", "file" (defaults to "method")
func ParseCoverageGranularity(s string) CoverageGranularity {
	switch strings.ToLower(s) {
	case "class":
		return CoverageGranularityClass
	case "file":
		return CoverageGranularityFile
	default:
		return CoverageGranularityMethod
	}
}

// CoverageStatus contains detailed information about coverage staleness
type CoverageStatus struct {
	Staleness     CoverageStaleness
	ChangedFiles  []string  // Files that changed since coverage was generated
	OldestCoverage time.Time // When the oldest coverage was generated
}

// relevantForCoverage returns true if a file path is relevant for coverage staleness
// Uses the shared filter from project package
func relevantForCoverage(path string) bool {
	return project.IsRelevantForCoverage(path)
}

// CheckCoverageStalenessGit checks staleness by looking at git changes since coverage generation
func CheckCoverageStalenessGit(gitRoot string) CoverageStatus {
	cacheDir := filepath.Join(gitRoot, ".donotnet")

	// Find oldest coverage generation time
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return CoverageStatus{Staleness: CoverageNotFound}
	}

	var oldestGenerated time.Time
	foundAny := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".testcoverage.json") {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		covMap, err := loadTestCoverageMap(path)
		if err != nil {
			continue
		}
		foundAny = true
		if oldestGenerated.IsZero() || covMap.GeneratedAt.Before(oldestGenerated) {
			oldestGenerated = covMap.GeneratedAt
		}
	}

	if !foundAny {
		return CoverageStatus{Staleness: CoverageNotFound}
	}

	// Get files changed since coverage was generated
	changedFiles := getFilesChangedSince(gitRoot, oldestGenerated)

	// Filter to relevant files only
	var relevantChanges []string
	for _, f := range changedFiles {
		if relevantForCoverage(f) {
			relevantChanges = append(relevantChanges, f)
		}
	}

	if len(relevantChanges) > 0 {
		return CoverageStatus{
			Staleness:      CoverageStale,
			ChangedFiles:   relevantChanges,
			OldestCoverage: oldestGenerated,
		}
	}

	return CoverageStatus{Staleness: CoverageFresh, OldestCoverage: oldestGenerated}
}

// CheckCoverageStalenessMtime checks staleness by comparing file modification times
func CheckCoverageStalenessMtime(gitRoot string) CoverageStatus {
	cacheDir := filepath.Join(gitRoot, ".donotnet")

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return CoverageStatus{Staleness: CoverageNotFound}
	}

	var oldestGenerated time.Time
	coveredFiles := make(map[string]bool)
	foundAny := false

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".testcoverage.json") {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		covMap, err := loadTestCoverageMap(path)
		if err != nil {
			continue
		}
		foundAny = true
		if oldestGenerated.IsZero() || covMap.GeneratedAt.Before(oldestGenerated) {
			oldestGenerated = covMap.GeneratedAt
		}
		// Collect all covered files
		for f := range covMap.FileToTests {
			coveredFiles[f] = true
		}
	}

	if !foundAny {
		return CoverageStatus{Staleness: CoverageNotFound}
	}

	// Check mtime of covered files
	var modifiedFiles []string
	for relPath := range coveredFiles {
		absPath := filepath.Join(gitRoot, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}
		if info.ModTime().After(oldestGenerated) {
			modifiedFiles = append(modifiedFiles, relPath)
		}
	}

	if len(modifiedFiles) > 0 {
		return CoverageStatus{
			Staleness:      CoverageStale,
			ChangedFiles:   modifiedFiles,
			OldestCoverage: oldestGenerated,
		}
	}

	return CoverageStatus{Staleness: CoverageFresh, OldestCoverage: oldestGenerated}
}

// getFilesChangedSince returns files changed (committed or uncommitted) since the given time
func getFilesChangedSince(gitRoot string, since time.Time) []string {
	filesMap := make(map[string]bool)

	// Get committed changes since the time
	sinceStr := since.Format("2006-01-02T15:04:05")
	cmd := exec.Command("git", "-C", gitRoot, "log", "--name-only", "--pretty=format:", "--since="+sinceStr)
	out, err := cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				filesMap[line] = true
			}
		}
	}

	// Also get uncommitted changes (dirty files)
	cmd = exec.Command("git", "-C", gitRoot, "status", "--porcelain")
	out, err = cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) < 3 {
				continue
			}
			file := strings.TrimSpace(line[3:])
			if idx := strings.Index(file, " -> "); idx >= 0 {
				file = file[idx+4:]
			}
			if file != "" {
				filesMap[file] = true
			}
		}
	}

	var files []string
	for f := range filesMap {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// ParseStalenessCheckMethod parses a staleness check method from string
// Valid values: "git", "mtime", "both" (defaults to "git")
func ParseStalenessCheckMethod(s string) StalenessCheckMethod {
	switch strings.ToLower(s) {
	case "mtime":
		return StalenessCheckMtime
	case "both":
		return StalenessCheckBoth
	default:
		return StalenessCheckGit
	}
}

// CheckCoverageStalenessWithMethod checks coverage staleness using the specified method
func CheckCoverageStalenessWithMethod(gitRoot string, method StalenessCheckMethod) CoverageStatus {
	switch method {
	case StalenessCheckMtime:
		return CheckCoverageStalenessMtime(gitRoot)
	case StalenessCheckBoth:
		// Check both methods and combine results
		gitStatus := CheckCoverageStalenessGit(gitRoot)
		mtimeStatus := CheckCoverageStalenessMtime(gitRoot)

		// If either is not found, report not found
		if gitStatus.Staleness == CoverageNotFound || mtimeStatus.Staleness == CoverageNotFound {
			return CoverageStatus{Staleness: CoverageNotFound}
		} else if gitStatus.Staleness == CoverageStale || mtimeStatus.Staleness == CoverageStale {
			// Combine changed files from both methods (deduplicated)
			filesMap := make(map[string]bool)
			for _, f := range gitStatus.ChangedFiles {
				filesMap[f] = true
			}
			for _, f := range mtimeStatus.ChangedFiles {
				filesMap[f] = true
			}
			var combined []string
			for f := range filesMap {
				combined = append(combined, f)
			}
			sort.Strings(combined)

			oldestCov := gitStatus.OldestCoverage
			if mtimeStatus.OldestCoverage.Before(oldestCov) {
				oldestCov = mtimeStatus.OldestCoverage
			}
			return CoverageStatus{
				Staleness:      CoverageStale,
				ChangedFiles:   combined,
				OldestCoverage: oldestCov,
			}
		} else {
			return CoverageStatus{Staleness: CoverageFresh, OldestCoverage: gitStatus.OldestCoverage}
		}
	default:
		return CheckCoverageStalenessGit(gitRoot)
	}
}

// GetCoverageSuggestion returns a suggestion for coverage rebuild if appropriate
// Returns nil if no suggestion is needed
func GetCoverageSuggestion(gitRoot string, method StalenessCheckMethod) *Suggestion {
	status := CheckCoverageStalenessWithMethod(gitRoot, method)

	switch status.Staleness {
	case CoverageNotFound:
		return &Suggestion{
			ID:          "coverage-not-found",
			Title:       "Enable coverage-based test filtering",
			Description: "Run with --build-test-coverage to enable coverage-based test filtering",
		}
	case CoverageStale:
		var desc string
		if len(status.ChangedFiles) <= 3 {
			desc = fmt.Sprintf("Coverage may be stale (%s changed). Run --build-test-coverage to update",
				strings.Join(status.ChangedFiles, ", "))
		} else {
			desc = fmt.Sprintf("Coverage may be stale (%d file(s) changed). Run --build-test-coverage to update",
				len(status.ChangedFiles))
		}
		return &Suggestion{
			ID:          "coverage-stale",
			Title:       "Update test coverage data",
			Description: desc,
		}
	}
	return nil
}

// loadAllTestCoverageMaps loads all .testcoverage.json files from the cache directory
// Returns a map of project name -> coverage map
func loadAllTestCoverageMaps(gitRoot string) map[string]*TestCoverageMap {
	cacheDir := filepath.Join(gitRoot, ".donotnet")
	result := make(map[string]*TestCoverageMap)

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return result // Return empty map if directory doesn't exist
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".testcoverage.json") {
			continue
		}

		path := filepath.Join(cacheDir, name)
		covMap, err := loadTestCoverageMap(path)
		if err != nil {
			term.Verbose("  failed to load %s: %v", name, err)
			continue
		}

		// Extract project name from filename (e.g., "Foo.Tests.testcoverage.json" -> "Foo.Tests")
		projectName := strings.TrimSuffix(name, ".testcoverage.json")
		result[projectName] = covMap
	}

	return result
}

// testGroup represents a group of tests to run together for coverage
type testGroup struct {
	name    string   // group identifier (method name, class name, or file path)
	tests   []string // individual test names in this group
	filter  string   // dotnet test --filter argument
}

// getTestClassName extracts the class name from a fully qualified test name
// e.g., "Namespace.SubNs.ClassName.MethodName" -> "Namespace.SubNs.ClassName"
func getTestClassName(testName string) string {
	// Strip parameters first
	if idx := strings.Index(testName, "("); idx > 0 {
		testName = testName[:idx]
	}
	// Find last dot to separate class from method
	if idx := strings.LastIndex(testName, "."); idx > 0 {
		return testName[:idx]
	}
	return testName
}

// groupTestsByClass groups tests by their class name
func groupTestsByClass(tests []string) []testGroup {
	classToTests := make(map[string][]string)
	for _, t := range tests {
		className := getTestClassName(t)
		classToTests[className] = append(classToTests[className], t)
	}

	var groups []testGroup
	for className, classTests := range classToTests {
		groups = append(groups, testGroup{
			name:   className,
			tests:  classTests,
			filter: fmt.Sprintf("FullyQualifiedName~%s", className),
		})
	}
	return groups
}

// testFileInfo contains information about a test file
type testFileInfo struct {
	path      string   // relative path to the test file
	namespace string   // namespace declared in the file
	classes   []string // class names declared in the file
}

// scanTestFiles scans .cs files in a directory and extracts namespace and class info
func scanTestFiles(projectDir string) []testFileInfo {
	var files []testFileInfo

	namespaceRegex := regexp.MustCompile(`(?m)^\s*namespace\s+([\w.]+)`)
	classRegex := regexp.MustCompile(`(?m)^\s*(?:public\s+|internal\s+|private\s+)?(?:sealed\s+|abstract\s+|partial\s+)*class\s+(\w+)`)

	filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".cs") {
			return nil
		}
		// Skip generated files
		if strings.Contains(path, "/obj/") || strings.Contains(path, "\\obj\\") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var namespace string
		if m := namespaceRegex.FindSubmatch(content); m != nil {
			namespace = string(m[1])
		}

		var classes []string
		for _, m := range classRegex.FindAllSubmatch(content, -1) {
			classes = append(classes, string(m[1]))
		}

		if len(classes) > 0 {
			relPath, _ := filepath.Rel(projectDir, path)
			files = append(files, testFileInfo{
				path:      relPath,
				namespace: namespace,
				classes:   classes,
			})
		}
		return nil
	})

	return files
}

// buildClassToFileMap builds a map from fully qualified class name to file path
func buildClassToFileMap(projectDir string) map[string]string {
	files := scanTestFiles(projectDir)
	classToFile := make(map[string]string)

	for _, f := range files {
		for _, className := range f.classes {
			// Build fully qualified name
			var fqn string
			if f.namespace != "" {
				fqn = f.namespace + "." + className
			} else {
				fqn = className
			}
			classToFile[fqn] = f.path
		}
	}
	return classToFile
}

// groupTestsByFile groups tests by their source file
func groupTestsByFile(tests []string, classToFile map[string]string) []testGroup {
	fileToTests := make(map[string][]string)
	fileToClasses := make(map[string]map[string]bool)

	for _, t := range tests {
		className := getTestClassName(t)
		filePath, ok := classToFile[className]
		if !ok {
			// Fallback: use class name as group
			filePath = className
		}
		fileToTests[filePath] = append(fileToTests[filePath], t)
		if fileToClasses[filePath] == nil {
			fileToClasses[filePath] = make(map[string]bool)
		}
		fileToClasses[filePath][className] = true
	}

	var groups []testGroup
	for filePath, fileTests := range fileToTests {
		// Build filter for all classes in this file
		var filterParts []string
		for className := range fileToClasses[filePath] {
			filterParts = append(filterParts, fmt.Sprintf("FullyQualifiedName~%s", className))
		}
		filter := strings.Join(filterParts, " | ")

		groups = append(groups, testGroup{
			name:   filePath,
			tests:  fileTests,
			filter: filter,
		})
	}
	return groups
}

// hasCoverletCollector checks if a project has the coverlet.collector package
func hasCoverletCollector(projectPath string) bool {
	content, err := os.ReadFile(projectPath)
	if err != nil {
		return false
	}
	// Check for coverlet.collector PackageReference (case-insensitive)
	lower := strings.ToLower(string(content))
	return strings.Contains(lower, "coverlet.collector")
}

// buildTestCoverageMaps builds per-test coverage maps for multiple projects
func buildTestCoverageMaps(gitRoot string, projects []*Project, maxJobs int, granularity CoverageGranularity) {
	cacheDir := filepath.Join(gitRoot, ".donotnet")
	os.MkdirAll(cacheDir, 0755)

	// Precheck: verify all projects have coverlet.collector
	var missingCoverlet []*Project
	for _, p := range projects {
		absPath := filepath.Join(gitRoot, p.Path)
		if !hasCoverletCollector(absPath) {
			missingCoverlet = append(missingCoverlet, p)
		}
	}

	if len(missingCoverlet) > 0 {
		term.Error("The following test projects are missing the coverlet.collector package:")
		term.Error("(required for per-test coverage collection)")
		term.Println()
		for _, p := range missingCoverlet {
			term.Printf("  %s\n", p.Path)
		}
		term.Println()
		term.Info("To fix, run these commands:")
		term.Println()
		for _, p := range missingCoverlet {
			absPath := filepath.Join(gitRoot, p.Path)
			term.Printf("  dotnet add %s package coverlet.collector\n", absPath)
		}
		term.Println()
		term.Dim("Or add to Directory.Build.props in your test directories:")
		term.Dim("  <PackageReference Include=\"coverlet.collector\" Version=\"6.0.0\" />")
		return
	}

	totalWorkers := maxJobs
	if totalWorkers <= 0 {
		totalWorkers = runtime.GOMAXPROCS(0)
	}

	// Run multiple projects in parallel, but tests within each project must be sequential
	// (coverlet instruments DLLs which causes file locking if multiple tests run simultaneously)
	projectWorkers := totalWorkers
	if projectWorkers > len(projects) {
		projectWorkers = len(projects)
	}

	term.Verbose("Running %d projects in parallel", projectWorkers)

	jobs := make(chan *Project, len(projects))
	var wg sync.WaitGroup

	for i := 0; i < projectWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				buildSingleTestCoverageMap(gitRoot, p, cacheDir, 1, granularity) // sequential within project
			}
		}()
	}

	for _, p := range projects {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
}

// buildSingleTestCoverageMap builds coverage map for a single test project
func buildSingleTestCoverageMap(gitRoot string, p *Project, cacheDir string, maxJobs int, granularity CoverageGranularity) {
	absProjectPath := filepath.Join(gitRoot, p.Path)
	projectDir := filepath.Dir(absProjectPath)
	mapFile := filepath.Join(cacheDir, p.Name+".testcoverage.json")

	term.Info("Building per-test coverage map for %s", p.Name)

	// Step 1: List all tests
	term.Printf("  Listing tests...\n")
	cmd := exec.Command("dotnet", "test", absProjectPath, "--list-tests", "--no-build")
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		term.Errorf("  failed to list tests: %v", err)
		return
	}

	// Parse test names
	testLineRegex := regexp.MustCompile(`^\s{4}(\S.+)$`)
	var tests []string
	inTestList := false
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "The following Tests are available:") {
			inTestList = true
			continue
		}
		if inTestList {
			if match := testLineRegex.FindStringSubmatch(line); match != nil {
				tests = append(tests, strings.TrimSpace(match[1]))
			}
		}
	}

	if len(tests) == 0 {
		term.Warn("  no tests found")
		return
	}

	// Count unique base test names (without parameters)
	uniqueTests := make(map[string]bool)
	for _, t := range tests {
		baseName := t
		if idx := strings.Index(t, "("); idx > 0 {
			baseName = t[:idx]
		}
		uniqueTests[baseName] = true
	}

	term.Printf("  Found %d tests (%d unique)\n", len(tests), len(uniqueTests))

	// Load existing map for resume support
	existingMap, _ := loadTestCoverageMap(mapFile)
	processedTests := make(map[string]bool)
	if existingMap != nil {
		for testName := range existingMap.TestToFiles {
			processedTests[testName] = true
		}
		if len(processedTests) > 0 {
			term.Printf("  Resuming: %d tests already processed\n", len(processedTests))
		}
	}

	// Initialize or reuse coverage map
	covMap := &TestCoverageMap{
		Project:     p.Path,
		FileToTests: make(map[string][]string),
		TestToFiles: make(map[string][]string),
		TotalTests:  len(uniqueTests),
	}
	if existingMap != nil {
		covMap.FileToTests = existingMap.FileToTests
		covMap.TestToFiles = existingMap.TestToFiles
		covMap.ProcessedTests = existingMap.ProcessedTests
	}

	// Find tests that still need processing
	// Strip parameters and deduplicate - parameterized tests like Test(a), Test(b) become just "Test"
	seenBase := make(map[string]bool)
	var pendingTests []string
	for _, t := range tests {
		baseName := t
		if idx := strings.Index(t, "("); idx > 0 {
			baseName = t[:idx]
		}
		// Skip if we've already processed this base test or seen it in this batch
		if processedTests[baseName] || seenBase[baseName] {
			continue
		}
		seenBase[baseName] = true
		pendingTests = append(pendingTests, baseName)
	}

	if len(pendingTests) == 0 {
		term.Success("  All %d tests already processed", len(tests))
		return
	}

	// Group tests based on granularity
	var groups []testGroup
	switch granularity {
	case CoverageGranularityClass:
		groups = groupTestsByClass(pendingTests)
		term.Printf("  Grouped into %d classes\n", len(groups))
	case CoverageGranularityFile:
		classToFile := buildClassToFileMap(projectDir)
		groups = groupTestsByFile(pendingTests, classToFile)
		term.Printf("  Grouped into %d files\n", len(groups))
	default: // CoverageGranularityMethod
		// Each test is its own group
		for _, t := range pendingTests {
			groups = append(groups, testGroup{
				name:   t,
				tests:  []string{t},
				filter: fmt.Sprintf("FullyQualifiedName=%s", t),
			})
		}
	}

	// Step 2: Run tests with coverage in parallel
	term.Printf("  Running %d groups with coverage...\n", len(groups))

	numWorkers := maxJobs
	if numWorkers <= 0 {
		numWorkers = runtime.GOMAXPROCS(0)
	}
	if numWorkers > len(groups) {
		numWorkers = len(groups)
	}

	type groupResult struct {
		group testGroup
		files []string
	}

	jobs := make(chan testGroup, len(groups))
	results := make(chan groupResult, len(groups))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker gets its own temp directory for coverage
			workerDir := filepath.Join(projectDir, fmt.Sprintf("TestResults_worker%d", workerID))

			for group := range jobs {
				// Clean up worker's TestResults
				os.RemoveAll(workerDir)

				// Run tests with coverage using the group's filter
				testCmd := exec.Command("dotnet", "test", absProjectPath,
					"--filter", group.filter,
					"--collect", "XPlat Code Coverage",
					"--results-directory", workerDir,
					"--no-build")
				testCmd.Dir = gitRoot
				var stdout, stderr bytes.Buffer
				testCmd.Stdout = &stdout
				testCmd.Stderr = &stderr
				runErr := testCmd.Run()
				if term.IsVerbose() && runErr != nil {
					term.Verbose("    [%s] test failed: %v", group.name, runErr)
				}

				// Find and parse coverage file
				coverageFile := coverage.FindCoverageFileIn(workerDir)
				var coveredFiles []string
				if coverageFile == "" {
					term.Verbose("    [%s] no coverage file in %s. stdout=%q", group.name, workerDir, stdout.String())
				} else {
					report, err := coverage.ParseFile(coverageFile)
					if err != nil {
						term.Verbose("    [%s] failed to parse coverage: %v", group.name, err)
					} else {
						coveredFiles = report.GetCoveredFilesRelativeToGitRoot(gitRoot)
						if len(coveredFiles) == 0 && len(report.CoveredFiles) > 0 {
							term.Verbose("    [%s] coverage has %d files but none resolve to gitRoot. SourceDirs: %v", group.name, len(report.CoveredFiles), report.SourceDirs)
						}
					}
				}

				results <- groupResult{group: group, files: coveredFiles}

				// Clean up
				os.RemoveAll(workerDir)
			}
		}(i)
	}

	// Send jobs
	for _, g := range groups {
		jobs <- g
	}
	close(jobs)

	// Collect results with progress
	var mu sync.Mutex
	groupsProcessed := 0
	saveInterval := 10 // Save every 10 groups

	go func() {
		for r := range results {
			mu.Lock()
			// Map coverage to all tests in the group
			for _, testName := range r.group.tests {
				if len(r.files) > 0 {
					covMap.TestToFiles[testName] = r.files
					for _, f := range r.files {
						covMap.FileToTests[f] = append(covMap.FileToTests[f], testName)
					}
				} else {
					// Mark as processed even if no coverage (empty slice)
					covMap.TestToFiles[testName] = []string{}
				}
				covMap.ProcessedTests++
			}
			groupsProcessed++

			// Show progress
			term.Status("  [%s] %d/%d groups (%d tests)", p.Name, groupsProcessed, len(groups), covMap.ProcessedTests)

			// Periodic save for resume support
			if groupsProcessed%saveInterval == 0 {
				covMap.GeneratedAt = time.Now()
				saveTestCoverageMap(mapFile, covMap)
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	close(results)

	// Final save
	term.ClearLine()
	covMap.GeneratedAt = time.Now()
	if err := saveTestCoverageMap(mapFile, covMap); err != nil {
		term.Errorf("  failed to save coverage map: %v", err)
		return
	}

	term.Success("  Processed %d/%d tests, %d files mapped → %s", covMap.ProcessedTests, len(tests), len(covMap.FileToTests), mapFile)
}

// listTestCoverageGroupings lists how tests would be grouped for each granularity level
func listTestCoverageGroupings(gitRoot string, projects []*Project) {
	for _, p := range projects {
		if !p.IsTest {
			continue
		}

		absProjectPath := filepath.Join(gitRoot, p.Path)
		projectDir := filepath.Dir(absProjectPath)

		term.Info("%s", p.Name)

		// List all tests
		cmd := exec.Command("dotnet", "test", absProjectPath, "--list-tests", "--no-build")
		cmd.Dir = gitRoot
		output, err := cmd.Output()
		if err != nil {
			term.Errorf("  failed to list tests: %v", err)
			continue
		}

		// Parse test names
		testLineRegex := regexp.MustCompile(`^\s{4}(\S.+)$`)
		var tests []string
		inTestList := false
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "The following Tests are available:") {
				inTestList = true
				continue
			}
			if inTestList {
				if match := testLineRegex.FindStringSubmatch(line); match != nil {
					tests = append(tests, strings.TrimSpace(match[1]))
				}
			}
		}

		if len(tests) == 0 {
			term.Warn("  no tests found")
			continue
		}

		// Deduplicate (strip parameters)
		seenBase := make(map[string]bool)
		var uniqueTests []string
		for _, t := range tests {
			baseName := t
			if idx := strings.Index(t, "("); idx > 0 {
				baseName = t[:idx]
			}
			if !seenBase[baseName] {
				seenBase[baseName] = true
				uniqueTests = append(uniqueTests, baseName)
			}
		}

		term.Printf("  Total: %d tests (%d unique)\n", len(tests), len(uniqueTests))
		term.Println()

		// Method granularity
		term.Printf("  %smethod%s: %d groups (1 test each)\n",
			term.Color(term.ColorYellow), term.Color(term.ColorReset), len(uniqueTests))

		// Class granularity
		classGroups := groupTestsByClass(uniqueTests)
		classReduction := float64(len(uniqueTests)) / float64(len(classGroups))
		term.Printf("  %sclass%s:  %d groups (%.1fx reduction)\n",
			term.Color(term.ColorGreen), term.Color(term.ColorReset), len(classGroups), classReduction)

		// Show class groups
		for _, g := range classGroups {
			if len(g.tests) > 1 {
				term.Printf("    %s%s%s: %d tests\n",
					term.Color(term.ColorDim), g.name, term.Color(term.ColorReset), len(g.tests))
			}
		}

		// File granularity
		classToFile := buildClassToFileMap(projectDir)
		fileGroups := groupTestsByFile(uniqueTests, classToFile)
		fileReduction := float64(len(uniqueTests)) / float64(len(fileGroups))
		term.Printf("  %sfile%s:   %d groups (%.1fx reduction)\n",
			term.Color(term.ColorCyan), term.Color(term.ColorReset), len(fileGroups), fileReduction)

		// Show file groups
		for _, g := range fileGroups {
			if len(g.tests) > 1 {
				term.Printf("    %s%s%s: %d tests\n",
					term.Color(term.ColorDim), g.name, term.Color(term.ColorReset), len(g.tests))
			}
		}

		term.Println()
	}
}

// listAllTests runs dotnet test --list-tests on each project and outputs JSON
func listAllTests(gitRoot string, projects []*Project) {
	var results []TestInfo
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Regex to detect test names (lines starting with whitespace followed by qualified name)
	testLineRegex := regexp.MustCompile(`^\s{4}(\S.+)$`)

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > len(projects) {
		numWorkers = len(projects)
	}

	jobs := make(chan *Project, len(projects))
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				projectPath := filepath.Join(gitRoot, p.Path)
				cmd := exec.Command("dotnet", "test", projectPath, "--list-tests", "--no-build")
				cmd.Dir = gitRoot
				output, err := cmd.Output()
				if err != nil {
					term.Verbose("  [%s] failed to list tests: %v", p.Name, err)
					continue
				}

				// Parse output for test names
				var tests []string
				inTestList := false
				for _, line := range strings.Split(string(output), "\n") {
					if strings.Contains(line, "The following Tests are available:") {
						inTestList = true
						continue
					}
					if inTestList {
						if match := testLineRegex.FindStringSubmatch(line); match != nil {
							tests = append(tests, strings.TrimSpace(match[1]))
						}
					}
				}

				mu.Lock()
				results = append(results, TestInfo{
					Project: p.Name,
					Tests:   tests,
					Count:   len(tests),
				})
				mu.Unlock()
			}
		}()
	}

	for _, p := range projects {
		jobs <- p
	}
	close(jobs)
	wg.Wait()

	// Sort by project name for consistent output
	sort.Slice(results, func(i, j int) bool {
		return results[i].Project < results[j].Project
	})

	// Output JSON
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(results)
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
