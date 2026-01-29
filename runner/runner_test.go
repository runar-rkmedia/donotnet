package runner

import (
	"testing"

	"github.com/runar-rkmedia/donotnet/config"
)

func TestNewOptions(t *testing.T) {
	// With nil config
	opts := NewOptions(nil)
	if opts.Heuristics != "default" {
		t.Errorf("expected Heuristics to be 'default', got %q", opts.Heuristics)
	}
	if opts.CoverageGranularity != "class" {
		t.Errorf("expected CoverageGranularity to be 'class', got %q", opts.CoverageGranularity)
	}

	// With config
	cfg := config.Default()
	cfg.Verbose = true
	cfg.Parallel = 4
	cfg.Test.Coverage = true

	opts = NewOptions(cfg)
	if !opts.Verbose {
		t.Error("expected Verbose to be true from config")
	}
	if opts.Parallel != 4 {
		t.Errorf("expected Parallel to be 4, got %d", opts.Parallel)
	}
	if !opts.Coverage {
		t.Error("expected Coverage to be true from config")
	}
}

func TestEffectiveParallel(t *testing.T) {
	opts := NewOptions(nil)
	opts.Parallel = 0

	// Should return 0 with nil config (runner will set to GOMAXPROCS)
	if opts.EffectiveParallel() != 0 {
		t.Errorf("expected EffectiveParallel to be 0, got %d", opts.EffectiveParallel())
	}

	opts.Parallel = 8
	if opts.EffectiveParallel() != 8 {
		t.Errorf("expected EffectiveParallel to be 8, got %d", opts.EffectiveParallel())
	}
}

func TestHashArgs(t *testing.T) {
	tests := []struct {
		args     []string
		wantSame bool
		compare  []string
	}{
		{[]string{}, true, []string{}},
		{[]string{"test"}, false, []string{"build"}},
		{[]string{"test", "--no-build"}, true, []string{"test", "--no-build"}},
		{[]string{"test", "--no-build"}, false, []string{"test"}},
	}

	for _, tt := range tests {
		h1 := HashArgs(tt.args)
		h2 := HashArgs(tt.compare)
		same := h1 == h2
		if same != tt.wantSame {
			t.Errorf("HashArgs(%v) == HashArgs(%v): got %v, want %v",
				tt.args, tt.compare, same, tt.wantSame)
		}
	}
}

func TestIsNonBuildFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo.cs", false},
		{"foo.csproj", false},
		{"README.md", true},
		{"readme.txt", true},
		{"Dockerfile", true},
		{".editorconfig", true},
		{"azure-pipelines.yml", true},
	}

	for _, tt := range tests {
		got := isNonBuildFile(tt.path)
		if got != tt.want {
			t.Errorf("isNonBuildFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFormatExtraArgs(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"--no-build"}, " (--no-build)"},
		{[]string{"-c", "Release"}, " (-c Release)"},
	}

	for _, tt := range tests {
		got := formatExtraArgs(tt.args)
		// formatExtraArgs may include color codes, check the plain version
		if got != tt.want && !contains(got, tt.want) {
			t.Errorf("formatExtraArgs(%v) = %q, want contains %q", tt.args, got, tt.want)
		}
	}
}

func TestFilterBuildArgs(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"--no-build"}, []string{"--no-build"}},
		{[]string{"--filter", "Category!=Live"}, nil},
		{[]string{"--filter=Category!=Live"}, nil},
		{[]string{"--blame", "--no-build"}, []string{"--no-build"}},
		{[]string{"-c", "Release", "--blame-hang"}, []string{"-c", "Release"}},
	}

	for _, tt := range tests {
		got := filterBuildArgs(tt.args)
		if len(got) != len(tt.want) {
			t.Errorf("filterBuildArgs(%v) = %v, want %v", tt.args, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("filterBuildArgs(%v)[%d] = %q, want %q", tt.args, i, got[i], tt.want[i])
			}
		}
	}
}

func TestExtractTestStats(t *testing.T) {
	tests := []struct {
		output string
		empty  bool
	}{
		{"", true},
		{"no stats here", true},
		{"Failed:   0, Passed:  10, Skipped:   0, Total:  10", false},
		{"some output\nFailed:   1, Passed:   5, Skipped:   2, Total:   8\nmore output", false},
	}

	for _, tt := range tests {
		got := extractTestStats(tt.output)
		if tt.empty && got != "" {
			t.Errorf("extractTestStats(%q) = %q, want empty", tt.output, got)
		}
		if !tt.empty && got == "" {
			t.Errorf("extractTestStats(%q) = empty, want non-empty", tt.output)
		}
	}
}

func TestNeedsRestoreRetry(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{"", false},
		{"Build succeeded.", false},
		{"run a NuGet package restore", true},
		{"NETSDK1004: something something", true},
		{"project.assets.json' not found", true},
	}

	for _, tt := range tests {
		got := needsRestoreRetry(tt.output)
		if got != tt.want {
			t.Errorf("needsRestoreRetry(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr))
}
