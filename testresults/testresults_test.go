package testresults

import (
	"testing"
)

func TestParseTRX(t *testing.T) {
	// Sample TRX content with mix of passed and failed tests
	trxContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<TestRun xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010">
  <Results>
    <UnitTestResult testId="id-1" testName="TestPassing" outcome="Passed" />
    <UnitTestResult testId="id-2" testName="TestFailing" outcome="Failed" />
    <UnitTestResult testId="id-3" testName="TestAnother" outcome="Passed" />
    <UnitTestResult testId="id-4" testName="TestAlsoFailing(input: 42)" outcome="Failed" />
  </Results>
  <TestDefinitions>
    <UnitTest id="id-1" name="TestPassing">
      <TestMethod className="MyApp.Tests.SampleTests, MyApp.Tests" name="TestPassing" />
    </UnitTest>
    <UnitTest id="id-2" name="TestFailing">
      <TestMethod className="MyApp.Tests.SampleTests, MyApp.Tests" name="TestFailing" />
    </UnitTest>
    <UnitTest id="id-3" name="TestAnother">
      <TestMethod className="MyApp.Tests.SampleTests, MyApp.Tests" name="TestAnother" />
    </UnitTest>
    <UnitTest id="id-4" name="TestAlsoFailing(input: 42)">
      <TestMethod className="MyApp.Tests.ParameterizedTests, MyApp.Tests" name="TestAlsoFailing" />
    </UnitTest>
  </TestDefinitions>
</TestRun>`)

	failed, err := ParseTRX(trxContent)
	if err != nil {
		t.Fatalf("ParseTRX failed: %v", err)
	}

	if len(failed) != 2 {
		t.Errorf("expected 2 failed tests, got %d", len(failed))
	}

	// Check first failure
	if failed[0].FullyQualifiedName != "MyApp.Tests.SampleTests.TestFailing" {
		t.Errorf("expected FQN 'MyApp.Tests.SampleTests.TestFailing', got %q", failed[0].FullyQualifiedName)
	}

	// Check second failure (parameterized test)
	if failed[1].FullyQualifiedName != "MyApp.Tests.ParameterizedTests.TestAlsoFailing" {
		t.Errorf("expected FQN 'MyApp.Tests.ParameterizedTests.TestAlsoFailing', got %q", failed[1].FullyQualifiedName)
	}
}

func TestParseStdout(t *testing.T) {
	stdout := `
Starting test execution, please wait...
A total of 1 test files matched the specified pattern.

  Passed MyApp.Tests.SampleTests.TestPassing [12ms]
  Failed MyApp.Tests.SampleTests.TestFailing [45ms]
  Error Message:
   Assert.Equal() Failure
   Expected: 1
   Actual:   2
  Stack Trace:
     at MyApp.Tests.SampleTests.TestFailing() in /path/to/test.cs:line 42

  Passed MyApp.Tests.SampleTests.TestAnother [8ms]
  Failed MyApp.Tests.ParameterizedTests.TestAlsoFailing(input: 42) [23ms]

Failed!  - Failed:     2, Passed:     2, Skipped:     0, Total:     4
`

	failed := ParseStdout(stdout)

	if len(failed) != 2 {
		t.Errorf("expected 2 failed tests, got %d", len(failed))
	}

	// Check first failure
	if failed[0].FullyQualifiedName != "MyApp.Tests.SampleTests.TestFailing" {
		t.Errorf("expected FQN 'MyApp.Tests.SampleTests.TestFailing', got %q", failed[0].FullyQualifiedName)
	}

	// Check second failure (parameterized test - should strip parameters from FQN)
	if failed[1].FullyQualifiedName != "MyApp.Tests.ParameterizedTests.TestAlsoFailing" {
		t.Errorf("expected FQN 'MyApp.Tests.ParameterizedTests.TestAlsoFailing', got %q", failed[1].FullyQualifiedName)
	}
}

func TestParseStdoutXUnit(t *testing.T) {
	// xUnit format uses [xUnit.net ...] prefix and [FAIL] suffix
	stdout := `
Starting test execution, please wait...
A total of 1 test files matched the specified pattern.
[xUnit.net 00:00:00.05]   eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_InvalidCandidate(candidate: "") [FAIL]
[xUnit.net 00:00:00.13]   eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_ValidCandidate(candidate: "test@domain.com") [FAIL]
[xUnit.net 00:00:00.28]   eDF.Common.Tests.OtherTests.SimpleTest [FAIL]
  Failed:     3, Passed:     5, Skipped:     0, Total:     8
`

	failed := ParseStdout(stdout)

	if len(failed) != 3 {
		t.Errorf("expected 3 failed tests, got %d", len(failed))
		for i, f := range failed {
			t.Logf("  [%d] %s", i, f.FullyQualifiedName)
		}
	}

	// Check first failure (parameterized)
	if len(failed) > 0 && failed[0].FullyQualifiedName != "eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_InvalidCandidate" {
		t.Errorf("expected FQN 'eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_InvalidCandidate', got %q", failed[0].FullyQualifiedName)
	}

	// Check second failure (parameterized with email)
	if len(failed) > 1 && failed[1].FullyQualifiedName != "eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_ValidCandidate" {
		t.Errorf("expected FQN 'eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_ValidCandidate', got %q", failed[1].FullyQualifiedName)
	}

	// Check third failure (non-parameterized)
	if len(failed) > 2 && failed[2].FullyQualifiedName != "eDF.Common.Tests.OtherTests.SimpleTest" {
		t.Errorf("expected FQN 'eDF.Common.Tests.OtherTests.SimpleTest', got %q", failed[2].FullyQualifiedName)
	}
}

func TestParseStdoutAndTRXMatch(t *testing.T) {
	// This test verifies that both parsers extract the same test names
	// for equivalent output

	trxContent := []byte(`<?xml version="1.0" encoding="utf-8"?>
<TestRun xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010">
  <Results>
    <UnitTestResult testId="id-1" testName="TestFailing" outcome="Failed" />
    <UnitTestResult testId="id-2" testName="TestAlsoFailing(input: 42)" outcome="Failed" />
  </Results>
  <TestDefinitions>
    <UnitTest id="id-1" name="TestFailing">
      <TestMethod className="MyApp.Tests.SampleTests, MyApp.Tests" name="TestFailing" />
    </UnitTest>
    <UnitTest id="id-2" name="TestAlsoFailing(input: 42)">
      <TestMethod className="MyApp.Tests.ParameterizedTests, MyApp.Tests" name="TestAlsoFailing" />
    </UnitTest>
  </TestDefinitions>
</TestRun>`)

	stdout := `
  Failed MyApp.Tests.SampleTests.TestFailing [45ms]
  Failed MyApp.Tests.ParameterizedTests.TestAlsoFailing(input: 42) [23ms]
`

	trxFailed, err := ParseTRX(trxContent)
	if err != nil {
		t.Fatalf("ParseTRX failed: %v", err)
	}

	stdoutFailed := ParseStdout(stdout)

	if len(trxFailed) != len(stdoutFailed) {
		t.Fatalf("TRX found %d failures, stdout found %d", len(trxFailed), len(stdoutFailed))
	}

	for i := range trxFailed {
		if trxFailed[i].FullyQualifiedName != stdoutFailed[i].FullyQualifiedName {
			t.Errorf("mismatch at index %d: TRX=%q, stdout=%q",
				i, trxFailed[i].FullyQualifiedName, stdoutFailed[i].FullyQualifiedName)
		}
	}
}

func TestBuildFilterString(t *testing.T) {
	tests := []FailedTest{
		{FullyQualifiedName: "MyApp.Tests.SampleTests.TestFailing"},
		{FullyQualifiedName: "MyApp.Tests.OtherTests.AnotherFailing"},
	}

	filter := BuildFilterString(tests)
	expected := "FullyQualifiedName~MyApp.Tests.SampleTests.TestFailing|FullyQualifiedName~MyApp.Tests.OtherTests.AnotherFailing"

	if filter != expected {
		t.Errorf("expected filter %q, got %q", expected, filter)
	}
}

func TestBuildFilterStringEmpty(t *testing.T) {
	filter := BuildFilterString(nil)
	if filter != "" {
		t.Errorf("expected empty filter for nil input, got %q", filter)
	}

	filter = BuildFilterString([]FailedTest{})
	if filter != "" {
		t.Errorf("expected empty filter for empty input, got %q", filter)
	}
}
