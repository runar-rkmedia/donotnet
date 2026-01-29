package testfilter

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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true for test file change, got false. Reason: %s", result.Reason)
	}

	// Should use heuristic (class name from file)
	if len(result.TestClasses) == 0 {
		t.Error("expected test classes from heuristic")
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
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true for mixed changes with coverage, got false. Reason: %s", result.Reason)
	}

	// Should include both the coverage-based test AND the test class from the test file change
	if len(result.TestClasses) < 2 {
		t.Errorf("expected at least 2 test classes, got %d: %v", len(result.TestClasses), result.TestClasses)
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

	result := tf.GetFilter("project", "/tmp/gitroot", "")

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

	result := tf.GetFilter("project", "/tmp/gitroot", "")

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

	result := tf.GetFilter("project", "/tmp/gitroot", "")

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

	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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

	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot", "")

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

func TestExtractCategoryTraitsFromContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name: "NUnit Category",
			content: `
[Category("Live")]
public class LiveTests {
    [Test]
    public void TestSomething() { }
}`,
			expected: []string{"Live"},
		},
		{
			name: "Commented out category - should not match",
			content: `
// [Category("Live")]
public class RegularTests {
    [Test]
    public void TestSomething() { }
}`,
			expected: nil,
		},
		{
			name: "Commented trait on same line - should not match",
			content: `
public class RegularTests {
    // [Trait("Category", "Live")]
    [Fact]
    public void TestSomething() { }
}`,
			expected: nil,
		},
		{
			name: "Mixed commented and uncommented traits",
			content: `
[Category("Active")]
// [Category("Live")]
public class MixedTests {
    [Test]
    public void TestSomething() { }
}`,
			expected: []string{"Active"},
		},
		{
			name: "xUnit Trait",
			content: `
[Trait("Category", "Integration")]
public class IntegrationTests {
    [Fact]
    public void TestSomething() { }
}`,
			expected: []string{"Integration"},
		},
		{
			name: "MSTest TestCategory",
			content: `
[TestCategory("Slow")]
public class SlowTests {
    [TestMethod]
    public void TestSomething() { }
}`,
			expected: []string{"Slow"},
		},
		{
			name: "Multiple categories",
			content: `
[Category("Live")]
[Category("Integration")]
public class LiveIntegrationTests {
    [Test]
    public void TestSomething() { }
}`,
			expected: []string{"Live", "Integration"},
		},
		{
			name:     "No categories",
			content:  `public class RegularTests { }`,
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ExtractCategoryTraitsFromContent(tc.content)
			if len(result) != len(tc.expected) {
				t.Errorf("expected %d traits, got %d: %v", len(tc.expected), len(result), result)
				return
			}
			// Check all expected traits are present
			for _, exp := range tc.expected {
				found := false
				for _, got := range result {
					if got == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected trait %q not found in %v", exp, result)
				}
			}
		})
	}
}

func TestParseFilterExclusions(t *testing.T) {
	tests := []struct {
		filter   string
		expected []string
	}{
		{"Category!=Live", []string{"Live"}},
		{"Category != Live", []string{"Live"}},
		{"Category!=Live&Category!=Slow", []string{"Live", "Slow"}},
		{"FullyQualifiedName~Foo&Category!=Integration", []string{"Integration"}},
		{"FullyQualifiedName~Foo", nil}, // no exclusions
		{"", nil},
	}

	for _, tc := range tests {
		t.Run(tc.filter, func(t *testing.T) {
			result := ParseFilterExclusions(tc.filter)
			if len(result) != len(tc.expected) {
				t.Errorf("ParseFilterExclusions(%q) = %v, expected %v", tc.filter, result, tc.expected)
			}
		})
	}
}

func TestAreAllTraitsExcluded(t *testing.T) {
	tests := []struct {
		name       string
		traits     []string
		excluded   []string
		shouldSkip bool
	}{
		{"all excluded", []string{"Live"}, []string{"Live"}, true},
		{"all excluded multiple", []string{"Live", "Slow"}, []string{"Live", "Slow", "Integration"}, true},
		{"some not excluded", []string{"Live", "Fast"}, []string{"Live"}, false},
		{"no traits", []string{}, []string{"Live"}, false},
		{"no exclusions", []string{"Live"}, []string{}, false},
		{"case insensitive", []string{"live"}, []string{"Live"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := AreAllTraitsExcluded(tc.traits, tc.excluded)
			if result != tc.shouldSkip {
				t.Errorf("AreAllTraitsExcluded(%v, %v) = %v, expected %v", tc.traits, tc.excluded, result, tc.shouldSkip)
			}
		})
	}
}

func TestAreAllTestsExcludedInFile(t *testing.T) {
	tests := []struct {
		name               string
		content            string
		excludedCategories []string
		expectExcluded     bool
		expectTestCount    int
	}{
		{
			name: "class-level trait excludes all methods",
			content: `
[Category("Live")]
public class LiveTests {
    [Test]
    public void TestA() { }

    [Test]
    public void TestB() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     true,
			expectTestCount:    2,
		},
		{
			name: "method-level trait only on some methods - not all excluded",
			content: `
public class MixedTests {
    [Category("Live")]
    [Test]
    public void LiveTest() { }

    [Test]
    public void RegularTest() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     false,
			expectTestCount:    2,
		},
		{
			name: "all methods have excluded trait at method level",
			content: `
public class AllLiveTests {
    [Category("Live")]
    [Test]
    public void TestA() { }

    [Category("Live")]
    [Test]
    public void TestB() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     true,
			expectTestCount:    2,
		},
		{
			name: "class trait combined with method trait - one method has extra non-excluded trait",
			content: `
[Category("Live")]
public class LiveTests {
    [Test]
    public void TestA() { }

    [Category("Fast")]
    [Test]
    public void TestB() { }
}`,
			excludedCategories: []string{"Live"},
			// Both methods inherit "Live" from class, so both are excluded
			expectExcluded:  true,
			expectTestCount: 2,
		},
		{
			name: "multiple classes - one has excluded trait, one doesn't",
			content: `
[Category("Live")]
public class LiveTests {
    [Test]
    public void TestLive() { }
}

public class RegularTests {
    [Test]
    public void TestRegular() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     false,
			expectTestCount:    2,
		},
		{
			name: "multiple classes - all have excluded trait",
			content: `
[Category("Live")]
public class LiveTests1 {
    [Test]
    public void Test1() { }
}

[Category("Live")]
public class LiveTests2 {
    [Test]
    public void Test2() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     true,
			expectTestCount:    2,
		},
		{
			name: "no test methods",
			content: `
[Category("Live")]
public class LiveHelpers {
    public void Setup() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     false,
			expectTestCount:    0,
		},
		{
			name: "xUnit style trait",
			content: `
[Trait("Category", "Integration")]
public class IntegrationTests {
    [Fact]
    public void TestA() { }
}`,
			excludedCategories: []string{"Integration"},
			expectExcluded:     true,
			expectTestCount:    1,
		},
		{
			name: "MSTest style category",
			content: `
[TestCategory("Slow")]
public class SlowTests {
    [TestMethod]
    public void TestA() { }
}`,
			excludedCategories: []string{"Slow"},
			expectExcluded:     true,
			expectTestCount:    1,
		},
		{
			name: "no category attributes at all",
			content: `
public class RegularTests {
    [Test]
    public void TestA() { }
}`,
			excludedCategories: []string{"Live"},
			expectExcluded:     false,
			expectTestCount:    1,
		},
		{
			name: "method without trait in class with trait",
			content: `
[Category("Live")]
public class LiveTests {
    [Test]
    public void TestInheritsLive() { }
}`,
			excludedCategories: []string{"Live"},
			// The test method inherits Live from the class
			expectExcluded:  true,
			expectTestCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			excluded, traits, testCount := AreAllTestsExcludedInFile(tc.content, tc.excludedCategories)

			if excluded != tc.expectExcluded {
				t.Errorf("expected excluded=%v, got %v (traits=%v, testCount=%d)",
					tc.expectExcluded, excluded, traits, testCount)
			}

			if testCount != tc.expectTestCount {
				t.Errorf("expected testCount=%d, got %d", tc.expectTestCount, testCount)
			}
		})
	}
}

func TestIsSafeTestFile_HelperName(t *testing.T) {
	tmpDir := t.TempDir()

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

	baseFile := filepath.Join(tmpDir, "IntegrationTests.cs")
	baseContent := `
using NUnit.Framework;
public class IntegrationTests {
    [Test]
    public void TestIntegration() { }
}
`
	os.WriteFile(baseFile, []byte(baseContent), 0644)

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
