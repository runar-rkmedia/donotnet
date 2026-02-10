package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseXMLDocSummary(t *testing.T) {
	tests := []struct {
		name     string
		docLines []string
		want     []string
	}{
		{
			name: "simple summary",
			docLines: []string{
				"<summary>",
				"This is a test summary.",
				"</summary>",
			},
			want: []string{"This is a test summary."},
		},
		{
			name: "summary on one line",
			docLines: []string{
				"<summary>Short description</summary>",
			},
			want: []string{"Short description"},
		},
		{
			name: "multi-line summary",
			docLines: []string{
				"<summary>",
				"Reproduces the 25-hour outage: connection drops, reconnect fails once,",
				"then succeeds. Without the fix, _client stays disposed-but-not-null",
				"and IsOpenAsync() throws ObjectDisposedException forever.",
				"</summary>",
			},
			want: []string{
				"Reproduces the 25-hour outage: connection drops, reconnect fails once,",
				"then succeeds. Without the fix, _client stays disposed-but-not-null",
				"and IsOpenAsync() throws ObjectDisposedException forever.",
			},
		},
		{
			name:     "empty",
			docLines: nil,
			want:     nil,
		},
		{
			name: "no summary tags",
			docLines: []string{
				"Just some text without tags",
			},
			want: []string{"Just some text without tags"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseXMLDocSummary(tt.docLines)
			if len(got) != len(tt.want) {
				t.Errorf("parseXMLDocSummary() returned %d lines, want %d\ngot: %v\nwant: %v",
					len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d:\ngot:  %q\nwant: %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractXMLDocComments(t *testing.T) {
	lines := []string{
		"using System;",
		"",
		"namespace Test",
		"{",
		"    public class MyTests",
		"    {",
		"        /// <summary>",
		"        /// This test verifies something important.",
		"        /// </summary>",
		"        [Fact]",
		"        public void TestMethod()",
		"        {",
		"        }",
		"    }",
		"}",
	}

	// methodLine is 0-indexed, pointing to "public void TestMethod()"
	methodLine := 10
	got := extractXMLDocComments(lines, methodLine)

	if len(got) == 0 {
		t.Fatal("extractXMLDocComments() returned no lines")
	}

	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "verifies something important") {
		t.Errorf("expected doc to contain 'verifies something important', got: %v", got)
	}
}

func TestHighlightPaths(t *testing.T) {
	input := `Error in test
at MyNamespace.MyClass.TestMethod() in /home/user/project/Tests.cs:line 42
Stack trace ends`

	result := highlightPaths(input)

	// In plain mode, should return unchanged
	// In color mode, should add color codes
	if !strings.Contains(result, "/home/user/project/Tests.cs") {
		t.Error("expected path to be preserved in output")
	}
}

func TestExtractDocStringFromFile(t *testing.T) {
	// Create a temporary C# file
	dir := t.TempDir()
	csFile := filepath.Join(dir, "TestFile.cs")

	content := `using System;
using Xunit;

namespace MyTests
{
    public class SampleTests
    {
        /// <summary>
        /// Verifies that addition works correctly.
        /// </summary>
        [Fact]
        public void AdditionTest()
        {
            Assert.Equal(4, 2 + 2);
        }

        /// <summary>
        /// Tests edge case with zero.
        /// </summary>
        [Theory]
        [InlineData(0)]
        public void ZeroTest(int value)
        {
            Assert.Equal(value, 0);
        }
    }
}
`
	err := os.WriteFile(csFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tests := []struct {
		name     string
		lineNum  string
		contains string
	}{
		{"AdditionTest method line", "15", "addition"},
		{"AdditionTest body", "17", "addition"},
		{"ZeroTest method line", "25", "zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docLines := extractDocString(csFile, tt.lineNum)
			if len(docLines) == 0 {
				t.Fatalf("expected to extract docstring for line %s", tt.lineNum)
			}
			joined := strings.Join(docLines, " ")
			if !strings.Contains(strings.ToLower(joined), tt.contains) {
				t.Errorf("expected doc to mention %q, got: %v", tt.contains, docLines)
			}
		})
	}
}

func TestEnhanceFailureOutput_NoTests(t *testing.T) {
	output := "Some random output without test failures"
	result := EnhanceFailureOutput(output, "/tmp")

	// Should still return something (possibly with highlighted paths)
	if result == "" {
		t.Error("EnhanceFailureOutput should return non-empty result")
	}
}
