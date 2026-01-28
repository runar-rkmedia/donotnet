package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetFilterWithCoverage(t *testing.T) {
	// Create a coverage map
	covMap := &TestCoverageMap{
		Project: "MyLib.Tests",
		FileToTests: map[string][]string{
			"src/MyLib/Service.cs": {"MyLib.Tests.ServiceTests.TestMethod1", "MyLib.Tests.ServiceTests.TestMethod2"},
			"src/MyLib/Helper.cs":  {"MyLib.Tests.HelperTests.TestHelper"},
		},
		TestToFiles: map[string][]string{
			"MyLib.Tests.ServiceTests.TestMethod1": {"src/MyLib/Service.cs"},
			"MyLib.Tests.ServiceTests.TestMethod2": {"src/MyLib/Service.cs"},
			"MyLib.Tests.HelperTests.TestHelper":   {"src/MyLib/Helper.cs"},
		},
	}

	tf := NewTestFilter()
	tf.SetCoverageMaps(map[string]*TestCoverageMap{
		"MyLib.Tests": covMap,
	})

	// Add a changed file that's in coverage
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Service.cs")

	// Get filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true, got false. Reason: %s", result.Reason)
	}

	if result.TestFilter == "" {
		t.Error("expected non-empty TestFilter")
	}

	// Should contain both test methods that cover Service.cs
	if len(result.TestClasses) != 2 {
		t.Errorf("expected 2 test classes, got %d: %v", len(result.TestClasses), result.TestClasses)
	}
}

func TestGetFilterWithCoverage_UncoveredFile(t *testing.T) {
	// When a file is NOT in coverage map and is not a test file,
	// with default heuristics (TestFileOnly), should NOT filter (safe behavior)
	covMap := &TestCoverageMap{
		Project: "MyLib.Tests",
		FileToTests: map[string][]string{
			"src/MyLib/Service.cs": {"MyLib.Tests.ServiceTests.TestMethod1"},
		},
	}

	tf := NewTestFilter()
	tf.SetCoverageMaps(map[string]*TestCoverageMap{
		"MyLib.Tests": covMap,
	})

	// Add a changed file that's NOT in coverage (and not a test file)
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/NewFile.cs")

	// Get filter - with only TestFileOnly heuristic, non-test files can't filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if result.CanFilter {
		t.Errorf("expected CanFilter=false for uncovered non-test file, got true")
	}
}

func TestGetFilterWithCoverage_UncoveredFile_WithHeuristics(t *testing.T) {
	// When a file is NOT in coverage map but we enable guessing heuristics
	covMap := &TestCoverageMap{
		Project: "MyLib.Tests",
		FileToTests: map[string][]string{
			"src/MyLib/Service.cs": {"MyLib.Tests.ServiceTests.TestMethod1"},
		},
	}

	tf := NewTestFilter()
	tf.SetCoverageMaps(map[string]*TestCoverageMap{
		"MyLib.Tests": covMap,
	})
	// Enable NameToNameTests heuristic for guessing
	tf.SetHeuristics(ParseHeuristics("NameToNameTests"))

	// Add a changed file that's NOT in coverage
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/NewFile.cs")

	// Get filter - should use heuristics (NewFile.cs -> NewFileTests)
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true with NameToNameTests heuristic, got false. Reason: %s", result.Reason)
	}

	// Should use heuristic naming
	if !strings.Contains(result.Reason, "heuristic") {
		t.Errorf("expected heuristic-based reason, got: %s", result.Reason)
	}

	// Should include NewFileTests in the filter
	if !strings.Contains(result.TestFilter, "NewFileTests") {
		t.Errorf("expected NewFileTests in filter, got: %s", result.TestFilter)
	}
}

func TestGetFilterWithCoverage_NoCoverageMap(t *testing.T) {
	tf := NewTestFilter()
	// No coverage maps set

	// Add a changed non-test file
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Service.cs")

	// Get filter - with only TestFileOnly heuristic, non-test files can't filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if result.CanFilter {
		t.Errorf("expected CanFilter=false for non-test file with default heuristics, got true")
	}
}

func TestGetFilterWithCoverage_NoCoverageMap_WithHeuristics(t *testing.T) {
	tf := NewTestFilter()
	// No coverage maps set, but enable guessing heuristics
	tf.SetHeuristics(ParseHeuristics("NameToNameTests"))

	// Add a changed non-test file
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Service.cs")

	// Get filter - should use heuristics (Service.cs -> ServiceTests)
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true with NameToNameTests heuristic, got false. Reason: %s", result.Reason)
	}

	// Should use heuristic naming
	if !strings.Contains(result.Reason, "heuristic") {
		t.Errorf("expected heuristic-based reason, got: %s", result.Reason)
	}

	// Should include ServiceTests in the filter
	if !strings.Contains(result.TestFilter, "ServiceTests") {
		t.Errorf("expected ServiceTests in filter, got: %s", result.TestFilter)
	}
}

func TestGetFilterWithCoverage_TestFileOnly(t *testing.T) {
	// When only test files change, should use heuristic filtering (not coverage)
	tf := NewTestFilter()

	// Add a test file change
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "tests/MyLib.Tests/ServiceTests.cs")

	// Get filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true for test file change, got false. Reason: %s", result.Reason)
	}

	// Should use heuristic (class name from file)
	if len(result.TestClasses) == 0 {
		t.Error("expected test classes from heuristic")
	}
}

func TestParseHeuristics(t *testing.T) {
	// Test "default" - only default heuristics
	h := ParseHeuristics("default")
	if len(h) != len(AvailableHeuristics) {
		t.Errorf("expected %d heuristics for 'default', got %d", len(AvailableHeuristics), len(h))
	}

	// Test "none"
	h = ParseHeuristics("none")
	if len(h) != 0 {
		t.Errorf("expected 0 heuristics for 'none', got %d", len(h))
	}

	// Test specific selection
	h = ParseHeuristics("NameToNameTests")
	if len(h) != 1 {
		t.Errorf("expected 1 heuristic, got %d", len(h))
	}
	if len(h) > 0 && h[0].Name != "NameToNameTests" {
		t.Errorf("expected NameToNameTests, got %s", h[0].Name)
	}

	// Test empty string = default defaults
	h = ParseHeuristics("")
	if len(h) != len(AvailableHeuristics) {
		t.Errorf("expected %d heuristics for empty string, got %d", len(AvailableHeuristics), len(h))
	}

	// Test opt-in heuristic
	h = ParseHeuristics("ExtensionsToBase")
	if len(h) != 1 {
		t.Errorf("expected 1 heuristic for opt-in, got %d", len(h))
	}
	if len(h) > 0 && h[0].Name != "ExtensionsToBase" {
		t.Errorf("expected ExtensionsToBase, got %s", h[0].Name)
	}

	// Test default + opt-in
	h = ParseHeuristics("default,InterfaceToImpl")
	expectedCount := len(AvailableHeuristics) + 1
	if len(h) != expectedCount {
		t.Errorf("expected %d heuristics for 'default,InterfaceToImpl', got %d", expectedCount, len(h))
	}

	// Test multiple opt-ins
	h = ParseHeuristics("default,ExtensionsToBase,AlwaysCompositionRoot")
	expectedCount = len(AvailableHeuristics) + 2
	if len(h) != expectedCount {
		t.Errorf("expected %d heuristics, got %d", expectedCount, len(h))
	}

	// Test that "default" returns empty (no heuristics enabled by default)
	h = ParseHeuristics("default")
	if len(h) != 0 {
		t.Errorf("expected 0 heuristics for 'default', got %d", len(h))
	}

	// Test adding opt-in heuristic
	h = ParseHeuristics("TestFileOnly")
	if len(h) != 1 {
		t.Errorf("expected 1 heuristic for opt-in, got %d", len(h))
	}
	if len(h) > 0 && h[0].Name != "TestFileOnly" {
		t.Errorf("expected TestFileOnly, got %s", h[0].Name)
	}

	// Test combining multiple opt-in heuristics
	h = ParseHeuristics("TestFileOnly,NameToNameTests")
	if len(h) != 2 {
		t.Errorf("expected 2 heuristics, got %d", len(h))
	}
}

func TestHeuristic_ExtensionsToBase(t *testing.T) {
	tf := NewTestFilter()
	tf.SetHeuristics(ParseHeuristics("ExtensionsToBase"))

	tf.AddChangedFile("project", "src/Lib/FooExtensions.cs")

	result := tf.GetFilter("project", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true, got false. Reason: %s", result.Reason)
	}
	if !strings.Contains(result.TestFilter, "FooTests") {
		t.Errorf("expected FooTests in filter, got: %s", result.TestFilter)
	}
}

func TestHeuristic_InterfaceToImpl(t *testing.T) {
	tf := NewTestFilter()
	tf.SetHeuristics(ParseHeuristics("InterfaceToImpl"))

	tf.AddChangedFile("project", "src/Lib/IUserService.cs")

	result := tf.GetFilter("project", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true, got false. Reason: %s", result.Reason)
	}
	if !strings.Contains(result.TestFilter, "UserServiceTests") {
		t.Errorf("expected UserServiceTests in filter, got: %s", result.TestFilter)
	}
}

func TestHeuristic_AlwaysCompositionRoot(t *testing.T) {
	tf := NewTestFilter()
	tf.SetHeuristics(ParseHeuristics("AlwaysCompositionRoot"))

	tf.AddChangedFile("project", "src/Lib/AnyFile.cs")

	result := tf.GetFilter("project", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true, got false. Reason: %s", result.Reason)
	}
	if !strings.Contains(result.TestFilter, "CompositionRootTests") {
		t.Errorf("expected CompositionRootTests in filter, got: %s", result.TestFilter)
	}
}

func TestGetFilterWithHeuristics_Disabled(t *testing.T) {
	tf := NewTestFilter()
	tf.SetHeuristics(nil) // Disable default heuristics

	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Service.cs")

	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	// With heuristics disabled and no coverage, should fdefault back to "can't filter"
	if result.CanFilter {
		t.Errorf("expected CanFilter=false when heuristics disabled, got true")
	}

	// Reason should indicate why filtering isn't possible
	if result.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestGetFilterWithHeuristics_DirectoryNamespace(t *testing.T) {
	// Test that directory names are included in the heuristic filter
	// when NameToNameTests and DirToNamespace heuristics are enabled
	tf := NewTestFilter()
	tf.SetHeuristics(ParseHeuristics("NameToNameTests,DirToNamespace"))

	// Add a file in a subdirectory
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Cache/CacheManager.cs")

	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true, got false. Reason: %s", result.Reason)
	}

	// Should include direct name match: CacheManagerTests
	if !strings.Contains(result.TestFilter, "CacheManagerTests") {
		t.Errorf("expected CacheManagerTests in filter, got: %s", result.TestFilter)
	}

	// Should include directory namespace match: .Cache.CacheManager
	if !strings.Contains(result.TestFilter, ".Cache.CacheManager") {
		t.Errorf("expected .Cache.CacheManager in filter, got: %s", result.TestFilter)
	}
}

func TestGetFilterWithCoverage_MixedChanges(t *testing.T) {
	// When both test files and source files change
	covMap := &TestCoverageMap{
		Project: "MyLib.Tests",
		FileToTests: map[string][]string{
			"src/MyLib/Service.cs": {"MyLib.Tests.ServiceTests.TestMethod1"},
		},
	}

	tf := NewTestFilter()
	tf.SetCoverageMaps(map[string]*TestCoverageMap{
		"MyLib.Tests": covMap,
	})

	// Add both a source file and a test file
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Service.cs")
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "tests/MyLib.Tests/OtherTests.cs")

	// Get filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true for mixed changes with coverage, got false. Reason: %s", result.Reason)
	}

	// Should include both the coverage-based test AND the test class from the test file change
	if len(result.TestClasses) < 2 {
		t.Errorf("expected at least 2 test classes, got %d: %v", len(result.TestClasses), result.TestClasses)
	}
}

func TestIsSafeTestFile_HelperName(t *testing.T) {
	// Create a temp directory with a test helper file
	tmpDir := t.TempDir()

	// Create a file with "Helper" in the name
	helperFile := filepath.Join(tmpDir, "TestHelper.cs")
	helperContent := `
using NUnit.Framework;
public class TestHelper {
    [Test]
    public void SomeTest() { }
}
`
	os.WriteFile(helperFile, []byte(helperContent), 0644)

	result := IsSafeTestFile(helperFile, tmpDir)

	if result.IsSafe {
		t.Errorf("expected file with 'Helper' in name to be unsafe, got safe")
	}
	if !strings.Contains(result.Reason, "helper") && !strings.Contains(result.Reason, "Helper") {
		t.Errorf("expected reason to mention helper, got: %s", result.Reason)
	}
}

func TestIsSafeTestFile_NoTestMethods(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file without test methods (just a class with no tests)
	noTestFile := filepath.Join(tmpDir, "ServiceTests.cs")
	noTestContent := `
public class ServiceTests {
    protected void SetupSomething() { }
}
`
	os.WriteFile(noTestFile, []byte(noTestContent), 0644)

	result := IsSafeTestFile(noTestFile, tmpDir)

	if result.IsSafe {
		t.Errorf("expected file without test methods to be unsafe, got safe")
	}
	if !strings.Contains(result.Reason, "no test methods") {
		t.Errorf("expected reason to mention no test methods, got: %s", result.Reason)
	}
}

func TestIsSafeTestFile_WithTestMethods(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a proper test file
	testFile := filepath.Join(tmpDir, "ServiceTests.cs")
	testContent := `
using NUnit.Framework;
public class ServiceTests {
    [Test]
    public void TestSomething() { }
}
`
	os.WriteFile(testFile, []byte(testContent), 0644)

	result := IsSafeTestFile(testFile, tmpDir)

	if !result.IsSafe {
		t.Errorf("expected test file with test methods to be safe, got unsafe. Reason: %s", result.Reason)
	}
}

func TestIsSafeTestFile_ReferencedByOther(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a base test class
	baseFile := filepath.Join(tmpDir, "IntegrationTests.cs")
	baseContent := `
using NUnit.Framework;
public class IntegrationTests {
    [Test]
    public void TestIntegration() { }
}
`
	os.WriteFile(baseFile, []byte(baseContent), 0644)

	// Create another test file that references the base class
	otherFile := filepath.Join(tmpDir, "ServiceIntegrationTests.cs")
	otherContent := `
using NUnit.Framework;
public class ServiceIntegrationTests : IntegrationTests {
    [Test]
    public void TestService() { }
}
`
	os.WriteFile(otherFile, []byte(otherContent), 0644)

	result := IsSafeTestFile(baseFile, tmpDir)

	if result.IsSafe {
		t.Errorf("expected file referenced by other test file to be unsafe, got safe")
	}
	if !strings.Contains(result.Reason, "referenced") {
		t.Errorf("expected reason to mention referenced, got: %s", result.Reason)
	}
}

func TestGetTestClassName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Namespace.ClassName.MethodName", "Namespace.ClassName"},
		{"Namespace.SubNs.ClassName.MethodName", "Namespace.SubNs.ClassName"},
		{"Namespace.ClassName.MethodName(param1)", "Namespace.ClassName"},
		{"ClassName.MethodName", "ClassName"},
		{"MethodName", "MethodName"}, // edge case: no dots
	}

	for _, tc := range tests {
		result := getTestClassName(tc.input)
		if result != tc.expected {
			t.Errorf("getTestClassName(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestGroupTestsByClass(t *testing.T) {
	tests := []string{
		"Namespace.FooTests.TestA",
		"Namespace.FooTests.TestB",
		"Namespace.BarTests.TestC",
		"Namespace.FooTests.TestD(param)",
	}

	groups := groupTestsByClass(tests)

	// Should have 2 groups: FooTests and BarTests
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}

	// Find FooTests group
	var fooGroup *testGroup
	for i := range groups {
		if groups[i].name == "Namespace.FooTests" {
			fooGroup = &groups[i]
			break
		}
	}

	if fooGroup == nil {
		t.Fatal("FooTests group not found")
	}

	// FooTests should have 3 tests (TestD with param is still in FooTests)
	if len(fooGroup.tests) != 3 {
		t.Errorf("expected FooTests to have 3 tests, got %d: %v", len(fooGroup.tests), fooGroup.tests)
	}

	// Filter should use tilde for partial match
	if !strings.Contains(fooGroup.filter, "FullyQualifiedName~Namespace.FooTests") {
		t.Errorf("expected filter to contain partial match, got: %s", fooGroup.filter)
	}
}

func TestParseCoverageGranularity(t *testing.T) {
	tests := []struct {
		input    string
		expected CoverageGranularity
	}{
		{"method", CoverageGranularityMethod},
		{"METHOD", CoverageGranularityMethod},
		{"class", CoverageGranularityClass},
		{"CLASS", CoverageGranularityClass},
		{"file", CoverageGranularityFile},
		{"FILE", CoverageGranularityFile},
		{"invalid", CoverageGranularityMethod}, // default
		{"", CoverageGranularityMethod},        // default
	}

	for _, tc := range tests {
		result := ParseCoverageGranularity(tc.input)
		if result != tc.expected {
			t.Errorf("ParseCoverageGranularity(%q) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}
