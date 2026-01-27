package devplan

import (
	"bytes"
	"strings"
	"testing"
)

func TestComputePlan_NoDependencies(t *testing.T) {
	projects := []*Project{
		{Path: "a/a.csproj", Name: "A"},
		{Path: "b/b.csproj", Name: "B"},
		{Path: "c/c.csproj", Name: "C"},
	}
	forwardGraph := map[string][]string{}

	plan := ComputePlan(projects, forwardGraph)

	if plan.TotalProjects != 3 {
		t.Errorf("TotalProjects = %d, want 3", plan.TotalProjects)
	}
	if plan.HasCycleError {
		t.Error("HasCycleError = true, want false")
	}
	if len(plan.Waves) != 1 {
		t.Errorf("len(Waves) = %d, want 1 (all projects can run in parallel)", len(plan.Waves))
	}
	if len(plan.Waves[0].Projects) != 3 {
		t.Errorf("Wave 1 has %d projects, want 3", len(plan.Waves[0].Projects))
	}
}

func TestComputePlan_LinearDependencies(t *testing.T) {
	// C depends on B, B depends on A
	projects := []*Project{
		{Path: "a/a.csproj", Name: "A"},
		{Path: "b/b.csproj", Name: "B"},
		{Path: "c/c.csproj", Name: "C"},
	}
	forwardGraph := map[string][]string{
		"b/b.csproj": {"a/a.csproj"},
		"c/c.csproj": {"b/b.csproj"},
	}

	plan := ComputePlan(projects, forwardGraph)

	if plan.HasCycleError {
		t.Error("HasCycleError = true, want false")
	}
	if len(plan.Waves) != 3 {
		t.Errorf("len(Waves) = %d, want 3 (sequential execution)", len(plan.Waves))
	}

	// Wave 1: A (no deps)
	if len(plan.Waves[0].Projects) != 1 || plan.Waves[0].Projects[0].Name != "A" {
		t.Errorf("Wave 1 should have only A, got %v", plan.Waves[0].Projects)
	}

	// Wave 2: B (after A)
	if len(plan.Waves[1].Projects) != 1 || plan.Waves[1].Projects[0].Name != "B" {
		t.Errorf("Wave 2 should have only B, got %v", plan.Waves[1].Projects)
	}
	if len(plan.Waves[1].Projects[0].Dependencies) != 1 || plan.Waves[1].Projects[0].Dependencies[0] != "A" {
		t.Errorf("B should depend on A, got %v", plan.Waves[1].Projects[0].Dependencies)
	}

	// Wave 3: C (after B)
	if len(plan.Waves[2].Projects) != 1 || plan.Waves[2].Projects[0].Name != "C" {
		t.Errorf("Wave 3 should have only C, got %v", plan.Waves[2].Projects)
	}
}

func TestComputePlan_DiamondDependencies(t *testing.T) {
	// D depends on B and C, both B and C depend on A
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	projects := []*Project{
		{Path: "a/a.csproj", Name: "A"},
		{Path: "b/b.csproj", Name: "B"},
		{Path: "c/c.csproj", Name: "C"},
		{Path: "d/d.csproj", Name: "D"},
	}
	forwardGraph := map[string][]string{
		"b/b.csproj": {"a/a.csproj"},
		"c/c.csproj": {"a/a.csproj"},
		"d/d.csproj": {"b/b.csproj", "c/c.csproj"},
	}

	plan := ComputePlan(projects, forwardGraph)

	if plan.HasCycleError {
		t.Error("HasCycleError = true, want false")
	}
	if len(plan.Waves) != 3 {
		t.Errorf("len(Waves) = %d, want 3", len(plan.Waves))
	}

	// Wave 1: A
	if len(plan.Waves[0].Projects) != 1 {
		t.Errorf("Wave 1 should have 1 project, got %d", len(plan.Waves[0].Projects))
	}

	// Wave 2: B and C (parallel)
	if len(plan.Waves[1].Projects) != 2 {
		t.Errorf("Wave 2 should have 2 projects (B and C), got %d", len(plan.Waves[1].Projects))
	}

	// Wave 3: D
	if len(plan.Waves[2].Projects) != 1 || plan.Waves[2].Projects[0].Name != "D" {
		t.Errorf("Wave 3 should have only D, got %v", plan.Waves[2].Projects)
	}
}

func TestComputePlan_CircularDependency(t *testing.T) {
	// A depends on B, B depends on A
	projects := []*Project{
		{Path: "a/a.csproj", Name: "A"},
		{Path: "b/b.csproj", Name: "B"},
	}
	forwardGraph := map[string][]string{
		"a/a.csproj": {"b/b.csproj"},
		"b/b.csproj": {"a/a.csproj"},
	}

	plan := ComputePlan(projects, forwardGraph)

	if !plan.HasCycleError {
		t.Error("HasCycleError = false, want true")
	}
	if len(plan.StuckProjects) != 2 {
		t.Errorf("len(StuckProjects) = %d, want 2", len(plan.StuckProjects))
	}
}

func TestComputePlan_ExternalDependenciesIgnored(t *testing.T) {
	// B depends on A and X, but X is not in the target set
	projects := []*Project{
		{Path: "a/a.csproj", Name: "A"},
		{Path: "b/b.csproj", Name: "B"},
	}
	forwardGraph := map[string][]string{
		"b/b.csproj": {"a/a.csproj", "x/x.csproj"}, // x is external
	}

	plan := ComputePlan(projects, forwardGraph)

	if plan.HasCycleError {
		t.Error("HasCycleError = true, want false")
	}
	if len(plan.Waves) != 2 {
		t.Errorf("len(Waves) = %d, want 2", len(plan.Waves))
	}

	// B should only show A as dependency, not X
	if len(plan.Waves[1].Projects[0].Dependencies) != 1 {
		t.Errorf("B should have 1 dependency (A), got %v", plan.Waves[1].Projects[0].Dependencies)
	}
}

func TestPlan_Print(t *testing.T) {
	plan := &Plan{
		TotalProjects: 3,
		Waves: []Wave{
			{
				Number: 1,
				Projects: []WaveProject{
					{Name: "A", Dependencies: nil},
				},
			},
			{
				Number: 2,
				Projects: []WaveProject{
					{Name: "B", Dependencies: []string{"A"}},
					{Name: "C", Dependencies: []string{"A"}},
				},
			},
		},
	}

	var buf bytes.Buffer
	plan.Print(&buf, PlainColors())
	output := buf.String()

	// Check key elements are present
	if !strings.Contains(output, "Job Scheduling Plan") {
		t.Error("Output should contain 'Job Scheduling Plan'")
	}
	if !strings.Contains(output, "3 projects") {
		t.Error("Output should contain '3 projects'")
	}
	if !strings.Contains(output, "Wave 1") {
		t.Error("Output should contain 'Wave 1'")
	}
	if !strings.Contains(output, "Wave 2") {
		t.Error("Output should contain 'Wave 2'")
	}
	if !strings.Contains(output, "A") {
		t.Error("Output should contain project 'A'")
	}
	if !strings.Contains(output, "after: A") {
		t.Error("Output should show B/C depend on A")
	}
}

func TestPlan_PrintCycleError(t *testing.T) {
	plan := &Plan{
		TotalProjects: 2,
		HasCycleError: true,
		StuckProjects: []StuckProject{
			{Name: "A", WaitingOn: []string{"B"}},
			{Name: "B", WaitingOn: []string{"A"}},
		},
	}

	var buf bytes.Buffer
	plan.Print(&buf, PlainColors())
	output := buf.String()

	if !strings.Contains(output, "Circular dependency detected") {
		t.Error("Output should mention circular dependency")
	}
	if !strings.Contains(output, "Stuck:") {
		t.Error("Output should show stuck projects")
	}
}

func TestComputePlan_EmptyInput(t *testing.T) {
	plan := ComputePlan(nil, nil)

	if plan.TotalProjects != 0 {
		t.Errorf("TotalProjects = %d, want 0", plan.TotalProjects)
	}
	if plan.HasCycleError {
		t.Error("HasCycleError = true, want false")
	}
	if len(plan.Waves) != 0 {
		t.Errorf("len(Waves) = %d, want 0", len(plan.Waves))
	}
}
