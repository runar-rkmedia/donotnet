package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	// Check default values
	if cfg.Verbose {
		t.Error("expected Verbose to be false")
	}
	if cfg.Parallel != 0 {
		t.Errorf("expected Parallel to be 0, got %d", cfg.Parallel)
	}
	if cfg.Color != "auto" {
		t.Errorf("expected Color to be 'auto', got %q", cfg.Color)
	}
	if cfg.Test.Heuristics != "default" {
		t.Errorf("expected Test.Heuristics to be 'default', got %q", cfg.Test.Heuristics)
	}
	if cfg.Test.CoverageGranularity != "class" {
		t.Errorf("expected Test.CoverageGranularity to be 'class', got %q", cfg.Test.CoverageGranularity)
	}
	if cfg.Build.Solution != "auto" {
		t.Errorf("expected Build.Solution to be 'auto', got %q", cfg.Build.Solution)
	}
}

func TestEffectiveParallel(t *testing.T) {
	cfg := Default()

	// When parallel is 0 (auto), should return GOMAXPROCS
	if cfg.EffectiveParallel() == 0 {
		t.Error("EffectiveParallel should not return 0 for auto mode")
	}

	// When parallel is set explicitly, should return that value
	cfg.Parallel = 4
	if got := cfg.EffectiveParallel(); got != 4 {
		t.Errorf("expected EffectiveParallel to be 4, got %d", got)
	}
}

func TestFindLocations(t *testing.T) {
	// Create a temp directory structure
	tmp := t.TempDir()
	gitRoot := filepath.Join(tmp, "repo")
	cwd := filepath.Join(gitRoot, "subdir")
	os.MkdirAll(cwd, 0755)

	locations := FindLocations(cwd, gitRoot)

	// Should find locations for user, git root, and cwd
	if len(locations) == 0 {
		t.Error("expected at least some locations")
	}

	// Check that git root location is present
	foundGitRoot := false
	for _, loc := range locations {
		if loc.Source == "git-root" {
			foundGitRoot = true
			break
		}
	}
	if !foundGitRoot {
		t.Error("expected to find git-root location")
	}
}

func TestLoadDefault(t *testing.T) {
	tmp := t.TempDir()

	result, err := Load(LoadOptions{
		CWD:     tmp,
		GitRoot: tmp,
		SkipEnv: true,
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.Config == nil {
		t.Fatal("expected non-nil config")
	}

	// Should have defaults as only source
	if len(result.Sources) == 0 {
		t.Error("expected at least defaults in sources")
	}
}

func TestLoadWithEnv(t *testing.T) {
	tmp := t.TempDir()

	// Set an environment variable
	os.Setenv("DONOTNET_VERBOSE", "true")
	defer os.Unsetenv("DONOTNET_VERBOSE")

	result, err := Load(LoadOptions{
		CWD:     tmp,
		GitRoot: tmp,
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !result.Config.Verbose {
		t.Error("expected Verbose to be true from env var")
	}
}

func TestLoadWithFile(t *testing.T) {
	tmp := t.TempDir()

	// Create config directory and file
	configDir := filepath.Join(tmp, ".donotnet")
	os.MkdirAll(configDir, 0755)
	configFile := filepath.Join(configDir, "config.toml")
	os.WriteFile(configFile, []byte(`verbose = true
parallel = 8
`), 0644)

	result, err := Load(LoadOptions{
		CWD:     tmp,
		GitRoot: tmp,
		SkipEnv: true,
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !result.Config.Verbose {
		t.Error("expected Verbose to be true from config file")
	}
	if result.Config.Parallel != 8 {
		t.Errorf("expected Parallel to be 8, got %d", result.Config.Parallel)
	}
}
