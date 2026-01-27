package main

import "testing"

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"a", "a", 0},
		{"ab", "ab", 0},
		{"ab", "ba", 2},
		{"kitten", "sitting", 3},
		{"dev-plan", "dev-paln", 2},
		{"watch", "watc", 1},
		{"vcs-ref", "vcs-reff", 1},
	}

	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestSuggestFlag(t *testing.T) {
	defined := []string{"dev-plan", "watch", "vcs-ref", "verbose", "version", "help"}

	tests := []struct {
		unknown         string
		wantSuggestion  string
		wantFound       bool
	}{
		{"dev-paln", "dev-plan", true},
		{"dev-pln", "dev-plan", true},
		{"watc", "watch", true},
		{"wathc", "watch", true},
		{"vcs-reff", "vcs-ref", true},
		{"xyz", "", false},           // too different from anything
		{"versin", "version", true},
		{"", "", false},
	}

	for _, tt := range tests {
		gotSuggestion, gotFound := suggestFlag(tt.unknown, defined)
		if gotFound != tt.wantFound {
			t.Errorf("suggestFlag(%q, ...) found = %v, want %v", tt.unknown, gotFound, tt.wantFound)
		}
		if gotFound && gotSuggestion != tt.wantSuggestion {
			t.Errorf("suggestFlag(%q, ...) = %q, want %q", tt.unknown, gotSuggestion, tt.wantSuggestion)
		}
	}
}

func TestParseUnknownFlag(t *testing.T) {
	tests := []struct {
		arg      string
		wantName string
		wantFlag bool
	}{
		{"-v", "v", true},
		{"--verbose", "verbose", true},
		{"-dev-plan", "dev-plan", true},
		{"--vcs-ref=main", "vcs-ref", true},
		{"-C=/path", "C", true},
		{"test", "", false},
		{"build", "", false},
		{"-", "", false},
		{"--", "", false},
	}

	for _, tt := range tests {
		gotName, gotFlag := parseUnknownFlag(tt.arg)
		if gotFlag != tt.wantFlag {
			t.Errorf("parseUnknownFlag(%q) isFlag = %v, want %v", tt.arg, gotFlag, tt.wantFlag)
		}
		if gotFlag && gotName != tt.wantName {
			t.Errorf("parseUnknownFlag(%q) = %q, want %q", tt.arg, gotName, tt.wantName)
		}
	}
}
