package main

import (
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
	// Create a coverage map without the changed file
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

	// Get filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if result.CanFilter {
		t.Errorf("expected CanFilter=false for uncovered file, got true")
	}

	// Reason should mention file not in coverage
	if result.Reason == "" {
		t.Error("expected non-empty Reason")
	}
}

func TestGetFilterWithCoverage_NoCoverageMap(t *testing.T) {
	tf := NewTestFilter()
	// No coverage maps set

	// Add a changed non-test file
	tf.AddChangedFile("tests/MyLib.Tests/MyLib.Tests.csproj", "src/MyLib/Service.cs")

	// Get filter
	result := tf.GetFilter("tests/MyLib.Tests/MyLib.Tests.csproj", "/tmp/gitroot")

	if result.CanFilter {
		t.Errorf("expected CanFilter=false when no coverage data, got true")
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
