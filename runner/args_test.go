package runner

import (
	"strings"
	"testing"
)

func TestCombineFilter(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		extra    string
		wantArgs []string
	}{
		{
			name:     "no existing filter",
			args:     []string{"--no-build"},
			extra:    "FullyQualifiedName~Foo",
			wantArgs: []string{"--no-build", "--filter", "FullyQualifiedName~Foo"},
		},
		{
			name:     "existing --filter separated",
			args:     []string{"--filter", "Category!=Live"},
			extra:    "FullyQualifiedName~Foo",
			wantArgs: []string{"--filter", "(Category!=Live)&(FullyQualifiedName~Foo)"},
		},
		{
			name:     "existing --filter= joined",
			args:     []string{"--filter=Category!=Live"},
			extra:    "FullyQualifiedName~Bar",
			wantArgs: []string{"--filter", "(Category!=Live)&(FullyQualifiedName~Bar)"},
		},
		{
			name:     "filter with other args around it",
			args:     []string{"--no-build", "--filter", "Category!=Live", "--verbosity", "quiet"},
			extra:    "FullyQualifiedName~Baz",
			wantArgs: []string{"--no-build", "--verbosity", "quiet", "--filter", "(Category!=Live)&(FullyQualifiedName~Baz)"},
		},
		{
			name:     "empty args",
			args:     nil,
			extra:    "Category=Fast",
			wantArgs: []string{"--filter", "Category=Fast"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := combineFilter(tt.args, tt.extra)
			gotStr := strings.Join(got, " ")
			wantStr := strings.Join(tt.wantArgs, " ")
			if gotStr != wantStr {
				t.Errorf("combineFilter(%v, %q)\n  got:  %s\n  want: %s", tt.args, tt.extra, gotStr, wantStr)
			}
		})
	}
}

func TestExtractFilter(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--no-build"}, ""},
		{[]string{"--filter", "Category!=Live"}, "Category!=Live"},
		{[]string{"--filter=Category!=Live"}, "Category!=Live"},
		{[]string{"--no-build", "--filter", "Foo", "--verbosity", "quiet"}, "Foo"},
	}
	for _, tt := range tests {
		got := extractFilter(tt.args)
		if got != tt.want {
			t.Errorf("extractFilter(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestRemoveCategoryFromFilter(t *testing.T) {
	tests := []struct {
		filter string
		want   string
	}{
		{"Category!=Live", ""},
		{"Category=Live", ""},
		{"Category!=Live&Category!=Slow", ""},
		{"FullyQualifiedName~Foo&Category!=Live", "FullyQualifiedName~Foo"},
		{"Category!=Live&FullyQualifiedName~Foo", "FullyQualifiedName~Foo"},
		{"FullyQualifiedName~Foo&Category!=Live&TestName~Bar", "FullyQualifiedName~Foo&TestName~Bar"},
		{"FullyQualifiedName~Foo", "FullyQualifiedName~Foo"},
		{"(Category!=Live)", ""},
		{"(Category!=Live)&(FullyQualifiedName~Foo)", "(FullyQualifiedName~Foo)"},
		{"", ""},
	}
	for _, tt := range tests {
		got := removeCategoryFromFilter(tt.filter)
		if got != tt.want {
			t.Errorf("removeCategoryFromFilter(%q) = %q, want %q", tt.filter, got, tt.want)
		}
	}
}

func TestParseCategoryFilters(t *testing.T) {
	tests := []struct {
		filter       string
		wantIncluded map[string]bool
		wantExcluded map[string]bool
	}{
		{"", map[string]bool{}, map[string]bool{}},
		{"Category=Live", map[string]bool{"Live": true}, map[string]bool{}},
		{"Category!=Live", map[string]bool{}, map[string]bool{"Live": true}},
		{"Category!=Live&Category!=Slow", map[string]bool{}, map[string]bool{"Live": true, "Slow": true}},
		{"FullyQualifiedName~Foo&Category!=Live", map[string]bool{}, map[string]bool{"Live": true}},
		{"(Category!=Live)&(Category=VCR)", map[string]bool{"VCR": true}, map[string]bool{"Live": true}},
	}
	for _, tt := range tests {
		inc, exc := parseCategoryFilters(tt.filter)
		if len(inc) != len(tt.wantIncluded) {
			t.Errorf("parseCategoryFilters(%q) included = %v, want %v", tt.filter, inc, tt.wantIncluded)
			continue
		}
		for k := range tt.wantIncluded {
			if !inc[k] {
				t.Errorf("parseCategoryFilters(%q) missing included %q", tt.filter, k)
			}
		}
		if len(exc) != len(tt.wantExcluded) {
			t.Errorf("parseCategoryFilters(%q) excluded = %v, want %v", tt.filter, exc, tt.wantExcluded)
			continue
		}
		for k := range tt.wantExcluded {
			if !exc[k] {
				t.Errorf("parseCategoryFilters(%q) missing excluded %q", tt.filter, k)
			}
		}
	}
}

// TestTraitOverrideReplacesCategory simulates the applyOverridesAndRun logic
// to verify that an interactive trait override replaces (not combines with)
// the user's CLI category filter.
func TestTraitOverrideReplacesCategory(t *testing.T) {
	tests := []struct {
		name      string
		cliArgs   []string // user's --filter from CLI
		traitExpr string   // interactive trait override
		want      string   // expected --filter value
	}{
		{
			name:      "include replaces exclude",
			cliArgs:   []string{"--filter", "Category!=Live"},
			traitExpr: "Category=Live",
			want:      "--filter Category=Live",
		},
		{
			name:      "exclude replaces include",
			cliArgs:   []string{"--filter", "Category=Live"},
			traitExpr: "Category!=Live",
			want:      "--filter Category!=Live",
		},
		{
			name:      "trait with no existing filter",
			cliArgs:   []string{"--no-build"},
			traitExpr: "Category=VCR",
			want:      "--no-build --filter Category=VCR",
		},
		{
			name:      "trait replaces category but keeps other clauses",
			cliArgs:   []string{"--filter", "Category!=Live&FullyQualifiedName~Foo"},
			traitExpr: "Category=VCR",
			want:      "--filter (FullyQualifiedName~Foo)&(Category=VCR)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reproduce applyOverridesAndRun logic
			savedArgs := tt.cliArgs
			extraFilter := tt.traitExpr

			baseArgs := savedArgs
			existing := extractFilter(savedArgs)
			if existing != "" {
				stripped := removeCategoryFromFilter(existing)
				baseArgs = removeFilter(savedArgs)
				if stripped != "" {
					baseArgs = append(baseArgs, "--filter", stripped)
				}
			}
			result := combineFilter(baseArgs, extraFilter)

			got := strings.Join(result, " ")
			if got != tt.want {
				t.Errorf("trait override:\n  cli:   %v\n  trait: %s\n  got:   %s\n  want:  %s", tt.cliArgs, tt.traitExpr, got, tt.want)
			}
		})
	}
}

func TestRemoveFilter(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--no-build"}, "--no-build"},
		{[]string{"--filter", "Foo"}, ""},
		{[]string{"--filter=Foo"}, ""},
		{[]string{"--no-build", "--filter", "Foo", "--verbosity", "quiet"}, "--no-build --verbosity quiet"},
		{[]string{"--no-build", "--filter=Foo", "--verbosity", "quiet"}, "--no-build --verbosity quiet"},
	}
	for _, tt := range tests {
		got := strings.Join(removeFilter(tt.args), " ")
		if got != tt.want {
			t.Errorf("removeFilter(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}
