package coverage

import (
	"bytes"
	"context"
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

	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testfilter"
)

// Granularity specifies how to group tests when building coverage maps.
type Granularity int

const (
	GranularityMethod Granularity = iota // Run each test individually (most precise, slowest)
	GranularityClass                     // Group tests by class name (faster, less precise)
	GranularityFile                      // Group tests by source file (fastest, file-level precision)
)

// ParseGranularity parses a coverage granularity from string.
// Valid values: "method", "class", "file" (defaults to "method").
func ParseGranularity(s string) Granularity {
	switch strings.ToLower(s) {
	case "class":
		return GranularityClass
	case "file":
		return GranularityFile
	default:
		return GranularityMethod
	}
}

// testGroup represents a group of tests to run together for coverage.
type testGroup struct {
	name   string   // group identifier
	tests  []string // individual test names
	filter string   // dotnet test --filter argument
}

// testLineRegex matches test names in dotnet test --list-tests output.
var testLineRegex = regexp.MustCompile(`^\s{4}(\S.+)$`)

// BuildOptions configures the per-test coverage build.
type BuildOptions struct {
	GitRoot     string
	Projects    []*project.Project
	MaxJobs     int
	Granularity Granularity
	Ctx         context.Context
}

// BuildPerTestCoverageMaps builds per-test coverage maps for the given test projects.
func BuildPerTestCoverageMaps(opts BuildOptions) {
	cacheDir := filepath.Join(opts.GitRoot, ".donotnet")
	os.MkdirAll(cacheDir, 0755)

	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Precheck: verify all projects have coverlet.collector
	var missingCoverlet []*project.Project
	for _, p := range opts.Projects {
		absPath := filepath.Join(opts.GitRoot, p.Path)
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
			absPath := filepath.Join(opts.GitRoot, p.Path)
			term.Printf("  dotnet add %s package coverlet.collector\n", absPath)
		}
		term.Println()
		term.Dim("Or add to Directory.Build.props in your test directories:")
		term.Dim("  <PackageReference Include=\"coverlet.collector\" Version=\"6.0.0\" />")
		return
	}

	totalWorkers := opts.MaxJobs
	if totalWorkers <= 0 {
		totalWorkers = runtime.GOMAXPROCS(0)
	}

	// Run multiple projects in parallel, but tests within each project are sequential
	// (coverlet instruments DLLs which causes file locking)
	projectWorkers := totalWorkers
	if projectWorkers > len(opts.Projects) {
		projectWorkers = len(opts.Projects)
	}

	term.Verbose("Running %d projects in parallel", projectWorkers)

	jobs := make(chan *project.Project, len(opts.Projects))
	var wg sync.WaitGroup

	for i := 0; i < projectWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				buildSingleProjectCoverage(ctx, opts.GitRoot, p, cacheDir, opts.Granularity)
			}
		}()
	}

	for _, p := range opts.Projects {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
}

// buildSingleProjectCoverage builds coverage map for a single test project.
func buildSingleProjectCoverage(ctx context.Context, gitRoot string, p *project.Project, cacheDir string, granularity Granularity) {
	absProjectPath := filepath.Join(gitRoot, p.Path)
	projectDir := filepath.Dir(absProjectPath)
	mapFile := filepath.Join(cacheDir, p.Name+".testcoverage.json")

	term.Info("Building per-test coverage map for %s", p.Name)

	// Step 1: List all tests
	term.Printf("  Listing tests...\n")
	tests, err := listTests(ctx, gitRoot, absProjectPath)
	if err != nil {
		term.Errorf("  failed to list tests: %v", err)
		return
	}

	if len(tests) == 0 {
		term.Warn("  no tests found")
		return
	}

	// Deduplicate (strip parameters)
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
	existingMap, _ := testfilter.LoadTestCoverageMap(mapFile)
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
	covMap := &testfilter.TestCoverageMap{
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
	seenBase := make(map[string]bool)
	var pendingTests []string
	for _, t := range tests {
		baseName := t
		if idx := strings.Index(t, "("); idx > 0 {
			baseName = t[:idx]
		}
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
	case GranularityClass:
		groups = groupTestsByClass(pendingTests)
		term.Printf("  Grouped into %d classes\n", len(groups))
	case GranularityFile:
		classToFile := buildClassToFileMap(projectDir)
		groups = groupTestsByFile(pendingTests, classToFile)
		term.Printf("  Grouped into %d files\n", len(groups))
	default: // GranularityMethod
		for _, t := range pendingTests {
			groups = append(groups, testGroup{
				name:   t,
				tests:  []string{t},
				filter: fmt.Sprintf("FullyQualifiedName=%s", t),
			})
		}
	}

	// Step 2: Run tests with coverage
	term.Printf("  Running %d groups with coverage...\n", len(groups))

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > len(groups) {
		numWorkers = len(groups)
	}

	type groupResult struct {
		group testGroup
		files []string
	}

	jobs := make(chan testGroup, len(groups))
	results := make(chan groupResult, len(groups))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerDir := filepath.Join(projectDir, fmt.Sprintf("TestResults_worker%d", workerID))

			for group := range jobs {
				os.RemoveAll(workerDir)

				testCmd := exec.CommandContext(ctx, "dotnet", "test", absProjectPath,
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
				coverageFile := FindCoverageFileIn(workerDir)
				var coveredFiles []string
				if coverageFile == "" {
					term.Verbose("    [%s] no coverage file in %s. stdout=%q", group.name, workerDir, stdout.String())
				} else {
					report, parseErr := ParseFile(coverageFile)
					if parseErr != nil {
						term.Verbose("    [%s] failed to parse coverage: %v", group.name, parseErr)
					} else {
						coveredFiles = report.GetCoveredFilesRelativeToGitRoot(gitRoot)
						if len(coveredFiles) == 0 && len(report.CoveredFiles) > 0 {
							term.Verbose("    [%s] coverage has %d files but none resolve to gitRoot. SourceDirs: %v", group.name, len(report.CoveredFiles), report.SourceDirs)
						}
					}
				}

				results <- groupResult{group: group, files: coveredFiles}
				os.RemoveAll(workerDir)
			}
		}(i)
	}

	for _, g := range groups {
		jobs <- g
	}
	close(jobs)

	// Collect results with progress
	var mu sync.Mutex
	groupsProcessed := 0
	saveInterval := 10

	go func() {
		for r := range results {
			mu.Lock()
			for _, testName := range r.group.tests {
				if len(r.files) > 0 {
					covMap.TestToFiles[testName] = r.files
					for _, f := range r.files {
						covMap.FileToTests[f] = append(covMap.FileToTests[f], testName)
					}
				} else {
					covMap.TestToFiles[testName] = []string{}
				}
				covMap.ProcessedTests++
			}
			groupsProcessed++

			term.Status("  [%s] %d/%d groups (%d tests)", p.Name, groupsProcessed, len(groups), covMap.ProcessedTests)

			if groupsProcessed%saveInterval == 0 {
				covMap.GeneratedAt = time.Now()
				testfilter.SaveTestCoverageMap(mapFile, covMap)
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	close(results)

	// Final save
	term.ClearLine()
	covMap.GeneratedAt = time.Now()
	if err := testfilter.SaveTestCoverageMap(mapFile, covMap); err != nil {
		term.Errorf("  failed to save coverage map: %v", err)
		return
	}

	term.Success("  Processed %d/%d tests, %d files mapped → %s", covMap.ProcessedTests, len(tests), len(covMap.FileToTests), mapFile)
}

// listTests runs dotnet test --list-tests and returns the test names.
func listTests(ctx context.Context, gitRoot, absProjectPath string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "dotnet", "test", absProjectPath, "--list-tests", "--no-build")
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTestListOutput(string(output)), nil
}

// parseTestListOutput parses the output of dotnet test --list-tests.
func parseTestListOutput(output string) []string {
	var tests []string
	inTestList := false
	for _, line := range strings.Split(output, "\n") {
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
	return tests
}

// hasCoverletCollector checks if a project has the coverlet.collector package.
func hasCoverletCollector(projectPath string) bool {
	content, err := os.ReadFile(projectPath)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(content))
	return strings.Contains(lower, "coverlet.collector")
}

// getTestClassName extracts the class name from a fully qualified test name.
func getTestClassName(testName string) string {
	if idx := strings.Index(testName, "("); idx > 0 {
		testName = testName[:idx]
	}
	if idx := strings.LastIndex(testName, "."); idx > 0 {
		return testName[:idx]
	}
	return testName
}

// groupTestsByClass groups tests by their class name.
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
	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	return groups
}

// testFileInfo contains information about a test file.
type testFileInfo struct {
	path      string
	namespace string
	classes   []string
}

// scanTestFiles scans .cs files in a directory and extracts namespace and class info.
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

// buildClassToFileMap builds a map from fully qualified class name to file path.
func buildClassToFileMap(projectDir string) map[string]string {
	files := scanTestFiles(projectDir)
	classToFile := make(map[string]string)

	for _, f := range files {
		for _, className := range f.classes {
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

// groupTestsByFile groups tests by their source file.
func groupTestsByFile(tests []string, classToFile map[string]string) []testGroup {
	fileToTests := make(map[string][]string)
	fileToClasses := make(map[string]map[string]bool)

	for _, t := range tests {
		className := getTestClassName(t)
		filePath, ok := classToFile[className]
		if !ok {
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
		var filterParts []string
		for className := range fileToClasses[filePath] {
			filterParts = append(filterParts, fmt.Sprintf("FullyQualifiedName~%s", className))
		}
		sort.Strings(filterParts)
		filter := strings.Join(filterParts, " | ")

		groups = append(groups, testGroup{
			name:   filePath,
			tests:  fileTests,
			filter: filter,
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	return groups
}
