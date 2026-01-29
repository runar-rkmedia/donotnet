package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	// Test that the root command has expected subcommands
	subcommands := rootCmd.Commands()

	expectedCommands := []string{"version", "config", "list", "cache", "coverage", "plan", "test", "build"}
	foundCommands := make(map[string]bool)

	for _, cmd := range subcommands {
		foundCommands[cmd.Name()] = true
	}

	for _, expected := range expectedCommands {
		if !foundCommands[expected] {
			t.Errorf("expected subcommand %q not found", expected)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	// Verify version command exists and has correct use string
	if versionCmd.Use != "version" {
		t.Errorf("expected Use to be 'version', got %q", versionCmd.Use)
	}
}

func TestConfigCommand(t *testing.T) {
	// Verify config command exists
	if configCmd.Use != "config" {
		t.Errorf("expected Use to be 'config', got %q", configCmd.Use)
	}

	// Check flags
	if configCmd.Flags().Lookup("format") == nil {
		t.Error("expected --format flag on config command")
	}
	if configCmd.Flags().Lookup("locations") == nil {
		t.Error("expected --locations flag on config command")
	}
}

func TestListSubcommands(t *testing.T) {
	subcommands := listCmd.Commands()
	expectedSubs := []string{"affected", "tests", "heuristics", "coverage"}

	foundSubs := make(map[string]bool)
	for _, cmd := range subcommands {
		foundSubs[cmd.Name()] = true
	}

	for _, expected := range expectedSubs {
		if !foundSubs[expected] {
			t.Errorf("expected list subcommand %q not found", expected)
		}
	}
}

func TestCacheSubcommands(t *testing.T) {
	subcommands := cacheCmd.Commands()
	expectedSubs := []string{"stats", "clean", "dump"}

	foundSubs := make(map[string]bool)
	for _, cmd := range subcommands {
		foundSubs[cmd.Name()] = true
	}

	for _, expected := range expectedSubs {
		if !foundSubs[expected] {
			t.Errorf("expected cache subcommand %q not found", expected)
		}
	}
}

func TestCoverageSubcommands(t *testing.T) {
	subcommands := coverageCmd.Commands()
	expectedSubs := []string{"parse", "build"}

	foundSubs := make(map[string]bool)
	for _, cmd := range subcommands {
		foundSubs[cmd.Name()] = true
	}

	for _, expected := range expectedSubs {
		if !foundSubs[expected] {
			t.Errorf("expected coverage subcommand %q not found", expected)
		}
	}
}

func TestTestCommandFlags(t *testing.T) {
	flags := []string{
		"coverage",
		"heuristics",
		"failed",
		"staleness-check",
		"coverage-granularity",
		"no-reports",
		"vcs-changed",
		"vcs-ref",
		"watch",
		"print-output",
		"full-build",
		"no-solution",
		"solution",
	}

	for _, flag := range flags {
		if testCmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected --%s flag on test command", flag)
		}
	}
}

func TestBuildCommandFlags(t *testing.T) {
	flags := []string{
		"no-solution",
		"solution",
		"full-build",
		"vcs-changed",
		"vcs-ref",
		"watch",
		"print-output",
	}

	for _, flag := range flags {
		if buildCmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected --%s flag on build command", flag)
		}
	}
}

func TestVersionString(t *testing.T) {
	vs := versionString()
	if !strings.Contains(vs, "donotnet") {
		t.Errorf("version string should contain 'donotnet', got: %s", vs)
	}
}

func TestRootHelp(t *testing.T) {
	// Capture help output
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"--help"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "donotnet") {
		t.Error("help output should mention 'donotnet'")
	}
	if !strings.Contains(output, "test") {
		t.Error("help output should mention 'test' command")
	}
	if !strings.Contains(output, "build") {
		t.Error("help output should mention 'build' command")
	}
}
