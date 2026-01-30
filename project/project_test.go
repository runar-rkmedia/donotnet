package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	// Create a temp directory with a test .csproj file
	tmpDir, err := os.MkdirTemp("", "project-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test project
	projContent := `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <ProjectReference Include="..\Core\Core.csproj" />
    <PackageReference Include="Newtonsoft.Json" Version="13.0.0" />
  </ItemGroup>
</Project>`

	projPath := filepath.Join(tmpDir, "MyApp", "MyApp.csproj")
	os.MkdirAll(filepath.Dir(projPath), 0755)
	os.WriteFile(projPath, []byte(projContent), 0644)

	// Parse the project
	p, err := Parse(projPath, "MyApp/MyApp.csproj")
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if p.Name != "MyApp" {
		t.Errorf("Name = %q, want %q", p.Name, "MyApp")
	}
	if p.Path != "MyApp/MyApp.csproj" {
		t.Errorf("Path = %q, want %q", p.Path, "MyApp/MyApp.csproj")
	}
	if p.Dir != "MyApp" {
		t.Errorf("Dir = %q, want %q", p.Dir, "MyApp")
	}
	if p.IsTest {
		t.Error("IsTest should be false for non-test project")
	}
	if len(p.References) != 1 {
		t.Errorf("len(References) = %d, want 1", len(p.References))
	}
	if len(p.PackageReferences) != 1 {
		t.Errorf("len(PackageReferences) = %d, want 1", len(p.PackageReferences))
	}
	if len(p.PackageReferences) > 0 && p.PackageReferences[0] != "Newtonsoft.Json" {
		t.Errorf("PackageReferences[0] = %q, want %q", p.PackageReferences[0], "Newtonsoft.Json")
	}
}

func TestParseTestProject(t *testing.T) {
	tests := []struct {
		name     string
		projName string
		content  string
		wantTest bool
	}{
		{
			name:     "suffix .Tests",
			projName: "MyApp.Tests",
			content:  `<Project Sdk="Microsoft.NET.Sdk"></Project>`,
			wantTest: true,
		},
		{
			name:     "suffix .Test",
			projName: "MyApp.Test",
			content:  `<Project Sdk="Microsoft.NET.Sdk"></Project>`,
			wantTest: true,
		},
		{
			name:     "suffix Tests",
			projName: "MyAppTests",
			content:  `<Project Sdk="Microsoft.NET.Sdk"></Project>`,
			wantTest: true,
		},
		{
			name:     "IsTestProject property",
			projName: "MyProject",
			content:  `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup></Project>`,
			wantTest: true,
		},
		{
			name:     "regular project",
			projName: "MyApp",
			content:  `<Project Sdk="Microsoft.NET.Sdk"></Project>`,
			wantTest: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "test-project-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			projPath := filepath.Join(tmpDir, tt.projName+".csproj")
			os.WriteFile(projPath, []byte(tt.content), 0644)

			p, err := Parse(projPath, tt.projName+".csproj")
			if err != nil {
				t.Fatalf("Parse() failed: %v", err)
			}

			if p.IsTest != tt.wantTest {
				t.Errorf("IsTest = %v, want %v", p.IsTest, tt.wantTest)
			}
		})
	}
}

func TestBuildDependencyGraphs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "graph-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create Core project
	coreDir := filepath.Join(tmpDir, "Core")
	os.MkdirAll(coreDir, 0755)
	coreProj := filepath.Join(coreDir, "Core.csproj")
	os.WriteFile(coreProj, []byte(`<Project Sdk="Microsoft.NET.Sdk"></Project>`), 0644)

	// Create App project that references Core
	appDir := filepath.Join(tmpDir, "App")
	os.MkdirAll(appDir, 0755)
	appProj := filepath.Join(appDir, "App.csproj")
	appContent := `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="../Core/Core.csproj" />
  </ItemGroup>
</Project>`
	os.WriteFile(appProj, []byte(appContent), 0644)

	// Parse projects
	core, _ := Parse(coreProj, "Core/Core.csproj")
	app, _ := Parse(appProj, "App/App.csproj")
	projects := []*Project{core, app}

	// Test forward graph (tmpDir is the git root)
	forward := BuildForwardDependencyGraph(projects, tmpDir)
	if len(forward["App/App.csproj"]) == 0 {
		t.Error("Forward graph should show App depends on Core")
	}
	if len(forward["Core/Core.csproj"]) != 0 {
		t.Error("Core should have no forward dependencies")
	}

	// Test reverse graph
	reverse := BuildDependencyGraph(projects, tmpDir)
	if len(reverse["Core/Core.csproj"]) == 0 {
		t.Error("Reverse graph should show Core has dependents")
	}
}

func TestBuildDependencyGraphFromSubdirectory(t *testing.T) {
	// This test verifies that the dependency graph is correct even when
	// the working directory is NOT the git root. This reproduces a bug where
	// watch mode failed to find dependents because BuildDependencyGraph used
	// filepath.Abs (relative to CWD) instead of resolving relative to git root.
	tmpDir, err := os.MkdirTemp("", "graph-subdir-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create directory structure: gitRoot/Source/App and gitRoot/Source/App.Tests
	appDir := filepath.Join(tmpDir, "Source", "App")
	testDir := filepath.Join(tmpDir, "Source", "App.Tests")
	os.MkdirAll(appDir, 0755)
	os.MkdirAll(testDir, 0755)

	appProj := filepath.Join(appDir, "App.csproj")
	os.WriteFile(appProj, []byte(`<Project Sdk="Microsoft.NET.Sdk"></Project>`), 0644)

	testProj := filepath.Join(testDir, "App.Tests.csproj")
	testContent := `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <ProjectReference Include="../App/App.csproj" />
  </ItemGroup>
</Project>`
	os.WriteFile(testProj, []byte(testContent), 0644)

	// Parse projects with paths relative to git root (tmpDir)
	app, _ := Parse(appProj, "Source/App/App.csproj")
	tests, _ := Parse(testProj, "Source/App.Tests/App.Tests.csproj")
	projects := []*Project{app, tests}

	// Build the dependency graph with gitRoot=tmpDir, even though CWD may differ
	reverse := BuildDependencyGraph(projects, tmpDir)

	// App should have App.Tests as a dependent
	dependents := reverse["Source/App/App.csproj"]
	if len(dependents) == 0 {
		t.Errorf("Reverse graph should show App has dependents, but got none")
		t.Logf("Graph contents: %v", reverse)
		t.Logf("App references: %v", app.References)
		t.Logf("Tests references: %v", tests.References)
	} else if dependents[0] != "Source/App.Tests/App.Tests.csproj" {
		t.Errorf("App dependent = %q, want %q", dependents[0], "Source/App.Tests/App.Tests.csproj")
	}

	// Also verify forward graph
	forward := BuildForwardDependencyGraph(projects, tmpDir)
	deps := forward["Source/App.Tests/App.Tests.csproj"]
	if len(deps) == 0 {
		t.Errorf("Forward graph should show App.Tests depends on App, but got none")
	}
}

func TestFindAffectedProjects(t *testing.T) {
	projects := []*Project{
		{Path: "A/A.csproj", Name: "A"},
		{Path: "B/B.csproj", Name: "B"},
		{Path: "C/C.csproj", Name: "C"},
	}

	// B depends on A, C depends on B
	graph := map[string][]string{
		"A/A.csproj": {"B/B.csproj"},
		"B/B.csproj": {"C/C.csproj"},
	}

	// If A changes, B and C should be affected
	changed := map[string]bool{"A/A.csproj": true}
	affected := FindAffectedProjects(changed, graph, projects)

	if !affected["A/A.csproj"] {
		t.Error("A should be affected")
	}
	if !affected["B/B.csproj"] {
		t.Error("B should be affected (depends on A)")
	}
	if !affected["C/C.csproj"] {
		t.Error("C should be affected (depends on B which depends on A)")
	}
}

func TestGetTransitiveDependencies(t *testing.T) {
	// A depends on B, B depends on C
	graph := map[string][]string{
		"A/A.csproj": {"B/B.csproj"},
		"B/B.csproj": {"C/C.csproj"},
		"C/C.csproj": {},
	}

	deps := GetTransitiveDependencies("A/A.csproj", graph)
	if len(deps) != 2 {
		t.Errorf("GetTransitiveDependencies returned %d deps, want 2", len(deps))
	}

	// Check that both B and C are included
	depsMap := make(map[string]bool)
	for _, d := range deps {
		depsMap[d] = true
	}
	if !depsMap["B/B.csproj"] {
		t.Error("B should be in transitive deps")
	}
	if !depsMap["C/C.csproj"] {
		t.Error("C should be in transitive deps")
	}
}

func TestGetRelevantDirs(t *testing.T) {
	project := &Project{
		Path: "App/App.csproj",
		Dir:  "App",
	}

	// App depends on Core
	graph := map[string][]string{
		"App/App.csproj": {"Core/Core.csproj"},
	}

	dirs := GetRelevantDirs(project, graph)
	if len(dirs) != 2 {
		t.Errorf("GetRelevantDirs returned %d dirs, want 2", len(dirs))
	}

	// Check both App and Core dirs are included
	dirsMap := make(map[string]bool)
	for _, d := range dirs {
		dirsMap[d] = true
	}
	if !dirsMap["App"] {
		t.Error("App dir should be included")
	}
	if !dirsMap["Core"] {
		t.Error("Core dir should be included")
	}
}

func TestFilterFilesToProject(t *testing.T) {
	files := []string{
		"App/Program.cs",
		"App/Models/User.cs",
		"Core/Core.cs",
		"Other/Stuff.cs",
	}
	relevantDirs := []string{"App", "Core"}

	filtered := FilterFilesToProject(files, relevantDirs)
	if len(filtered) != 3 {
		t.Errorf("FilterFilesToProject returned %d files, want 3", len(filtered))
	}

	// Other/Stuff.cs should be excluded
	for _, f := range filtered {
		if f == "Other/Stuff.cs" {
			t.Error("Other/Stuff.cs should be filtered out")
		}
	}
}

func TestFindUntestedProjects(t *testing.T) {
	projects := []*Project{
		{Path: "Core/Core.csproj", Name: "Core", IsTest: false},
		{Path: "App/App.csproj", Name: "App", IsTest: false},
		{Path: "Unused/Unused.csproj", Name: "Unused", IsTest: false},
		{Path: "App.Tests/App.Tests.csproj", Name: "App.Tests", IsTest: true},
	}

	// Test project depends on App, App depends on Core
	// Unused is not referenced by any test
	graph := map[string][]string{
		"App.Tests/App.Tests.csproj": {"App/App.csproj"},
		"App/App.csproj":             {"Core/Core.csproj"},
	}

	untested := FindUntestedProjects(projects, graph)
	if len(untested) != 1 {
		t.Errorf("FindUntestedProjects returned %d projects, want 1", len(untested))
	}
	if len(untested) > 0 && untested[0].Name != "Unused" {
		t.Errorf("Untested project = %q, want %q", untested[0].Name, "Unused")
	}
}

func TestFindCompleteSolutionMatches(t *testing.T) {
	gitRoot := "/repo"
	projects := []*Project{
		{Path: "App/App.csproj", Name: "App"},
		{Path: "Core/Core.csproj", Name: "Core"},
		{Path: "Other/Other.csproj", Name: "Other"},
	}

	// Solution contains App and Core (both in our target list)
	sln := &Solution{
		Path:    "/repo/MySolution.sln",
		RelPath: "MySolution.sln",
		Projects: map[string]bool{
			"/repo/App/App.csproj":   true,
			"/repo/Core/Core.csproj": true,
		},
	}
	solutions := []*Solution{sln}

	matched, remaining := FindCompleteSolutionMatches(projects, solutions, gitRoot)

	if len(matched) != 1 {
		t.Errorf("FindCompleteSolutionMatches returned %d matches, want 1", len(matched))
	}
	if len(matched[sln]) != 2 {
		t.Errorf("Solution should have 2 projects, got %d", len(matched[sln]))
	}
	if len(remaining) != 1 {
		t.Errorf("Should have 1 remaining project, got %d", len(remaining))
	}
	if len(remaining) > 0 && remaining[0].Name != "Other" {
		t.Errorf("Remaining project = %q, want %q", remaining[0].Name, "Other")
	}
}

func TestFindCompleteSolutionMatchesPartial(t *testing.T) {
	gitRoot := "/repo"
	projects := []*Project{
		{Path: "App/App.csproj", Name: "App"},
	}

	// Solution contains App and Core, but only App is in target list
	sln := &Solution{
		Path:    "/repo/MySolution.sln",
		RelPath: "MySolution.sln",
		Projects: map[string]bool{
			"/repo/App/App.csproj":   true,
			"/repo/Core/Core.csproj": true,
		},
	}
	solutions := []*Solution{sln}

	matched, remaining := FindCompleteSolutionMatches(projects, solutions, gitRoot)

	// Should not match because not ALL solution projects are in target
	if len(matched) != 0 {
		t.Errorf("FindCompleteSolutionMatches should return 0 matches for partial, got %d", len(matched))
	}
	if len(remaining) != 1 {
		t.Errorf("All projects should be remaining, got %d", len(remaining))
	}
}

func TestGroupProjectsBySolution(t *testing.T) {
	gitRoot := "/repo"
	projects := []*Project{
		{Path: "App/App.csproj", Name: "App"},
		{Path: "Core/Core.csproj", Name: "Core"},
		{Path: "Other/Other.csproj", Name: "Other"},
	}

	// Solution contains App and Core
	sln := &Solution{
		Path:    "/repo/MySolution.sln",
		RelPath: "MySolution.sln",
		Projects: map[string]bool{
			"/repo/App/App.csproj":   true,
			"/repo/Core/Core.csproj": true,
		},
	}
	solutions := []*Solution{sln}

	grouped, remaining := GroupProjectsBySolution(projects, solutions, gitRoot)

	if len(grouped) != 1 {
		t.Errorf("GroupProjectsBySolution returned %d groups, want 1", len(grouped))
	}
	if len(grouped[sln]) != 2 {
		t.Errorf("Solution group should have 2 projects, got %d", len(grouped[sln]))
	}
	if len(remaining) != 1 {
		t.Errorf("Should have 1 remaining project, got %d", len(remaining))
	}
}

func TestFindCommonSolution(t *testing.T) {
	gitRoot := "/repo"
	projects := []*Project{
		{Path: "App/App.csproj", Name: "App"},
		{Path: "Core/Core.csproj", Name: "Core"},
	}

	// Solution that contains both projects
	sln := &Solution{
		Path:    "/repo/MySolution.sln",
		RelPath: "MySolution.sln",
		Projects: map[string]bool{
			"/repo/App/App.csproj":   true,
			"/repo/Core/Core.csproj": true,
			"/repo/Extra/Extra.csproj": true, // Extra project is fine
		},
	}
	solutions := []*Solution{sln}

	found := FindCommonSolution(projects, solutions, gitRoot)
	if found != sln {
		t.Error("FindCommonSolution should find the solution containing all projects")
	}
}

func TestFindCommonSolutionNone(t *testing.T) {
	gitRoot := "/repo"
	projects := []*Project{
		{Path: "App/App.csproj", Name: "App"},
		{Path: "Core/Core.csproj", Name: "Core"},
	}

	// Solution that only contains App (not Core)
	sln := &Solution{
		Path:    "/repo/MySolution.sln",
		RelPath: "MySolution.sln",
		Projects: map[string]bool{
			"/repo/App/App.csproj": true,
		},
	}
	solutions := []*Solution{sln}

	found := FindCommonSolution(projects, solutions, gitRoot)
	if found != nil {
		t.Error("FindCommonSolution should return nil when no solution contains all projects")
	}
}
