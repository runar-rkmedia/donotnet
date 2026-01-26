package main

import (
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
	// When a file is NOT in coverage map, should fdefault back to heuristics
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

	// Add a changed file that's NOT in coverage
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/NewFile.cs")

	// Get filter - should use heuristics (NewFile.cs -> NewFileTests)
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true with heuristics fdefaultback, got false. Reason: %s", result.Reason)
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

	// Get filter - should use heuristics (Service.cs -> ServiceTests)
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if !result.CanFilter {
		t.Errorf("expected CanFilter=true with heuristics, got false. Reason: %s", result.Reason)
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

	// Test disabling a default heuristic
	h = ParseHeuristics("default,-DirToNamespace")
	expectedCount = len(AvailableHeuristics) - 1
	if len(h) != expectedCount {
		t.Errorf("expected %d heuristics for 'default,-DirToNamespace', got %d", expectedCount, len(h))
	}
	// Verify DirToNamespace is not in the result
	for _, heuristic := range h {
		if heuristic.Name == "DirToNamespace" {
			t.Error("DirToNamespace should have been disabled")
		}
	}

	// Test disabling multiple
	h = ParseHeuristics("default,-NameToNameTests,-DirToNamespace")
	if len(h) != 0 {
		t.Errorf("expected 0 heuristics when all defaults disabled, got %d", len(h))
	}

	// Test adding opt-in while disabling default
	h = ParseHeuristics("default,-DirToNamespace,ExtensionsToBase")
	expectedCount = len(AvailableHeuristics) - 1 + 1 // minus DirToNamespace, plus ExtensionsToBase
	if len(h) != expectedCount {
		t.Errorf("expected %d heuristics, got %d", expectedCount, len(h))
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
	tf := NewTestFilter()

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
