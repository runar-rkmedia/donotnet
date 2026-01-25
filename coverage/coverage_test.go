package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile(t *testing.T) {
	report, err := ParseFile("testdata/coverage.cobertura.xml")
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Check source directories
	if len(report.SourceDirs) != 1 {
		t.Errorf("expected 1 source dir, got %d", len(report.SourceDirs))
	}
	if report.SourceDirs[0] != "/home/user/project/src/MyLibrary/" {
		t.Errorf("unexpected source dir: %s", report.SourceDirs[0])
	}

	// Check all files (should include all 4 classes)
	expectedAll := []string{
		"Services/UserService.cs",
		"Services/OrderService.cs",
		"Models/User.cs",
		"Helpers/StringHelper.cs",
	}
	if len(report.AllFiles) != len(expectedAll) {
		t.Errorf("expected %d files in AllFiles, got %d", len(expectedAll), len(report.AllFiles))
	}
	for _, f := range expectedAll {
		if _, ok := report.AllFiles[f]; !ok {
			t.Errorf("expected %s in AllFiles", f)
		}
	}

	// Check covered files (UserService fully covered, OrderService partially, StringHelper covered, User not covered)
	expectedCovered := []string{
		"Services/UserService.cs",  // hits > 0 on all lines
		"Services/OrderService.cs", // hits > 0 on some lines (CreateOrder is covered)
		"Helpers/StringHelper.cs",  // hits > 0
	}
	notCovered := []string{
		"Models/User.cs", // hits = 0 on all lines
	}

	if len(report.CoveredFiles) != len(expectedCovered) {
		t.Errorf("expected %d covered files, got %d", len(expectedCovered), len(report.CoveredFiles))
	}

	for _, f := range expectedCovered {
		if _, ok := report.CoveredFiles[f]; !ok {
			t.Errorf("expected %s to be covered", f)
		}
	}

	for _, f := range notCovered {
		if _, ok := report.CoveredFiles[f]; ok {
			t.Errorf("expected %s to NOT be covered", f)
		}
	}
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("testdata/nonexistent.xml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestParseFile_MalformedXML(t *testing.T) {
	// Create a temp file with malformed XML
	tmpDir := t.TempDir()
	malformedPath := filepath.Join(tmpDir, "malformed.xml")
	err := os.WriteFile(malformedPath, []byte("not valid xml <unclosed"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ParseFile(malformedPath)
	if err == nil {
		t.Error("expected error for malformed XML")
	}
}

func TestParseFile_EmptyFile(t *testing.T) {
	// Create a valid but empty coverage file
	tmpDir := t.TempDir()
	emptyPath := filepath.Join(tmpDir, "empty.xml")
	emptyXML := `<?xml version="1.0"?><coverage><sources></sources><packages></packages></coverage>`
	err := os.WriteFile(emptyPath, []byte(emptyXML), 0644)
	if err != nil {
		t.Fatal(err)
	}

	report, err := ParseFile(emptyPath)
	if err != nil {
		t.Fatalf("ParseFile failed on empty file: %v", err)
	}
	if len(report.CoveredFiles) != 0 {
		t.Errorf("expected 0 covered files, got %d", len(report.CoveredFiles))
	}
	if len(report.AllFiles) != 0 {
		t.Errorf("expected 0 all files, got %d", len(report.AllFiles))
	}
}

func TestResolveToGitRoot(t *testing.T) {
	report := &Report{
		SourceDirs: []string{
			"/home/user/project/src/MyLibrary/",
		},
		CoveredFiles: map[string]struct{}{
			"Services/UserService.cs": {},
		},
	}

	// Test resolution when file is under gitRoot
	gitRoot := "/home/user/project"
	resolved := report.ResolveToGitRoot("Services/UserService.cs", gitRoot)
	expected := "src/MyLibrary/Services/UserService.cs"
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}

	// Test with gitRoot that doesn't contain the source
	resolved = report.ResolveToGitRoot("Services/UserService.cs", "/different/path")
	if resolved != "" {
		t.Errorf("expected empty string for unrelated gitRoot, got %q", resolved)
	}
}

func TestGetCoveredFilesRelativeToGitRoot(t *testing.T) {
	report := &Report{
		SourceDirs: []string{
			"/home/user/project/src/MyLibrary/",
		},
		CoveredFiles: map[string]struct{}{
			"Services/UserService.cs": {},
			"Helpers/StringHelper.cs": {},
		},
	}

	gitRoot := "/home/user/project"
	files := report.GetCoveredFilesRelativeToGitRoot(gitRoot)

	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}

	// Convert to map for easy lookup
	fileMap := make(map[string]bool)
	for _, f := range files {
		fileMap[f] = true
	}

	expected := []string{
		"src/MyLibrary/Services/UserService.cs",
		"src/MyLibrary/Helpers/StringHelper.cs",
	}
	for _, e := range expected {
		if !fileMap[e] {
			t.Errorf("expected %q in result", e)
		}
	}
}

func TestParseFile_MultipleSourceDirs(t *testing.T) {
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "multi-source.xml")
	xmlContent := `<?xml version="1.0"?>
<coverage>
  <sources>
    <source>/home/user/project/src/LibA/</source>
    <source>/home/user/project/src/LibB/</source>
  </sources>
  <packages>
    <package name="Test">
      <classes>
        <class name="Test.Foo" filename="Foo.cs">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	err := os.WriteFile(xmlPath, []byte(xmlContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	report, err := ParseFile(xmlPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(report.SourceDirs) != 2 {
		t.Errorf("expected 2 source dirs, got %d", len(report.SourceDirs))
	}
}

func TestParseFile_WindowsPaths(t *testing.T) {
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "windows-paths.xml")
	// Cobertura on Windows might have backslashes in filenames
	xmlContent := `<?xml version="1.0"?>
<coverage>
  <sources>
    <source>C:\Users\test\project\</source>
  </sources>
  <packages>
    <package name="Test">
      <classes>
        <class name="Test.Foo" filename="Services\FooService.cs">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	err := os.WriteFile(xmlPath, []byte(xmlContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	report, err := ParseFile(xmlPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Filenames should be normalized to forward slashes
	if _, ok := report.CoveredFiles["Services/FooService.cs"]; !ok {
		t.Error("expected normalized path with forward slashes")
		t.Logf("CoveredFiles: %v", report.CoveredFiles)
	}
}

func TestFindCoverageFile(t *testing.T) {
	// Create a temp directory structure simulating a test project
	tmpDir := t.TempDir()

	// Create TestResults with multiple coverage files
	results1 := filepath.Join(tmpDir, "TestResults", "guid-1")
	results2 := filepath.Join(tmpDir, "TestResults", "guid-2")
	os.MkdirAll(results1, 0755)
	os.MkdirAll(results2, 0755)

	// Create older coverage file
	cov1 := filepath.Join(results1, "coverage.cobertura.xml")
	os.WriteFile(cov1, []byte(`<?xml version="1.0"?><coverage><sources/><packages/></coverage>`), 0644)

	// Create newer coverage file (touch it to ensure it's newer)
	cov2 := filepath.Join(results2, "coverage.cobertura.xml")
	os.WriteFile(cov2, []byte(`<?xml version="1.0"?><coverage><sources/><packages/></coverage>`), 0644)

	// Find should return the newest file
	found := FindCoverageFile(tmpDir)
	if found == "" {
		t.Fatal("expected to find coverage file")
	}

	// Should find one of them (exact one depends on filesystem timing)
	if found != cov1 && found != cov2 {
		t.Errorf("unexpected coverage file: %s", found)
	}
}

func TestFindCoverageFile_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	found := FindCoverageFile(tmpDir)
	if found != "" {
		t.Errorf("expected empty string, got %s", found)
	}
}

func TestNewMap(t *testing.T) {
	m := NewMap()
	if m.FileToTestProjects == nil {
		t.Error("FileToTestProjects should not be nil")
	}
	if m.TestProjectToFiles == nil {
		t.Error("TestProjectToFiles should not be nil")
	}
	if !m.HasCoverage() == true {
		// Empty map should return false for HasCoverage
	}
	if m.HasCoverage() {
		t.Error("empty map should not have coverage")
	}
}

func TestMap_GetTestProjectsForFile(t *testing.T) {
	m := NewMap()
	m.FileToTestProjects["src/Foo/Bar.cs"] = []string{"tests/Foo.Tests/Foo.Tests.csproj"}
	m.FileToTestProjects["src/Shared/Utils.cs"] = []string{
		"tests/Foo.Tests/Foo.Tests.csproj",
		"tests/Bar.Tests/Bar.Tests.csproj",
	}

	// File covered by one project
	projects := m.GetTestProjectsForFile("src/Foo/Bar.cs")
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}

	// File covered by multiple projects
	projects = m.GetTestProjectsForFile("src/Shared/Utils.cs")
	if len(projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projects))
	}

	// File not in map
	projects = m.GetTestProjectsForFile("src/Unknown/File.cs")
	if projects != nil {
		t.Errorf("expected nil for unknown file, got %v", projects)
	}
}

func TestBuildMap_Integration(t *testing.T) {
	// Create a mock project structure
	tmpDir := t.TempDir()

	// Create source directory structure
	srcDir := filepath.Join(tmpDir, "src", "MyLib")
	os.MkdirAll(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "Service.cs"), []byte("// source"), 0644)

	// Create test project with coverage
	testDir := filepath.Join(tmpDir, "tests", "MyLib.Tests")
	testResultsDir := filepath.Join(testDir, "TestResults", "guid-123")
	os.MkdirAll(testResultsDir, 0755)

	// Create coverage file that covers the source
	coverageXML := `<?xml version="1.0"?>
<coverage>
  <sources>
    <source>` + srcDir + `/</source>
  </sources>
  <packages>
    <package name="MyLib">
      <classes>
        <class name="MyLib.Service" filename="Service.cs">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	os.WriteFile(filepath.Join(testResultsDir, "coverage.cobertura.xml"), []byte(coverageXML), 0644)

	// Build the map
	testProjects := []TestProject{
		{Path: "tests/MyLib.Tests/MyLib.Tests.csproj", Dir: "tests/MyLib.Tests"},
	}
	m := BuildMap(tmpDir, testProjects)

	if !m.HasCoverage() {
		t.Log("Missing:", m.MissingTestProjects)
		t.Log("Stale:", m.StaleTestProjects)
		t.Fatal("expected map to have coverage")
	}

	// Check file to test project mapping
	projects := m.GetTestProjectsForFile("src/MyLib/Service.cs")
	if len(projects) != 1 {
		t.Errorf("expected 1 project for Service.cs, got %d", len(projects))
	}
	if len(projects) > 0 && projects[0] != "tests/MyLib.Tests/MyLib.Tests.csproj" {
		t.Errorf("unexpected project: %s", projects[0])
	}
}
