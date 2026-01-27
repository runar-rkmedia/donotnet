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
func buildTestCoverageMaps(gitRoot string, projects []*Project, maxJobs int) {
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
				buildSingleTestCoverageMap(gitRoot, p, cacheDir, 1) // sequential within project
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
func buildSingleTestCoverageMap(gitRoot string, p *Project, cacheDir string, maxJobs int) {
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

	// Step 2: Run tests with coverage in parallel
	term.Printf("  Running %d tests with coverage...\n", len(pendingTests))

	numWorkers := maxJobs
	if numWorkers <= 0 {
		numWorkers = runtime.GOMAXPROCS(0)
	}
	if numWorkers > len(pendingTests) {
		numWorkers = len(pendingTests)
	}

	type testResult struct {
		testName string
		files    []string
	}

	jobs := make(chan string, len(pendingTests))
	results := make(chan testResult, len(pendingTests))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker gets its own temp directory for coverage
			workerDir := filepath.Join(projectDir, fmt.Sprintf("TestResults_worker%d", workerID))

			for testName := range jobs {
				// Clean up worker's TestResults
				os.RemoveAll(workerDir)

				// Run test with coverage (testName is already stripped of parameters)
				filterArg := fmt.Sprintf("FullyQualifiedName=%s", testName)

				testCmd := exec.Command("dotnet", "test", absProjectPath,
					"--filter", filterArg,
					"--collect", "XPlat Code Coverage",
					"--results-directory", workerDir,
					"--no-build")
				testCmd.Dir = gitRoot
				var stdout, stderr bytes.Buffer
				testCmd.Stdout = &stdout
				testCmd.Stderr = &stderr
				runErr := testCmd.Run()
				if term.IsVerbose() && runErr != nil {
					term.Verbose("    [%s] test failed: %v", testName, runErr)
				}

				// Find and parse coverage file
				coverageFile := coverage.FindCoverageFileIn(workerDir)
				var coveredFiles []string
				if coverageFile == "" {
					term.Verbose("    [%s] no coverage file in %s. stdout=%q", testName, workerDir, stdout.String())
				} else {
					report, err := coverage.ParseFile(coverageFile)
					if err != nil {
						term.Verbose("    [%s] failed to parse coverage: %v", testName, err)
					} else {
						coveredFiles = report.GetCoveredFilesRelativeToGitRoot(gitRoot)
						if len(coveredFiles) == 0 && len(report.CoveredFiles) > 0 {
							term.Verbose("    [%s] coverage has %d files but none resolve to gitRoot. SourceDirs: %v", testName, len(report.CoveredFiles), report.SourceDirs)
						}
					}
				}

				results <- testResult{testName: testName, files: coveredFiles}

				// Clean up
				os.RemoveAll(workerDir)
			}
		}(i)
	}

	// Send jobs
	for _, t := range pendingTests {
		jobs <- t
	}
	close(jobs)

	// Collect results with progress
	var mu sync.Mutex
	processed := 0
	saveInterval := 10 // Save every 10 tests

	go func() {
		for r := range results {
			mu.Lock()
			if len(r.files) > 0 {
				covMap.TestToFiles[r.testName] = r.files
				for _, f := range r.files {
					covMap.FileToTests[f] = append(covMap.FileToTests[f], r.testName)
				}
			} else {
				// Mark as processed even if no coverage (empty slice)
				covMap.TestToFiles[r.testName] = []string{}
			}
			covMap.ProcessedTests++
			processed++

			// Show progress
			progress := covMap.ProcessedTests
			total := covMap.TotalTests
			term.Status("  [%s] %d/%d processed", p.Name, progress, total)

			// Periodic save for resume support
			if processed%saveInterval == 0 {
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
