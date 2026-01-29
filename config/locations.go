package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// ConfigFileName is the base config file name (without extension).
const ConfigFileName = "config"

// ConfigDirName is the directory name for donotnet config.
const ConfigDirName = ".donotnet"

// SupportedExtensions are the config file extensions we support, in priority order.
var SupportedExtensions = []string{".toml", ".yaml", ".yml", ".json"}

// Location represents a config file location with its path and source description.
type Location struct {
	Path   string // Full path to config file
	Source string // Human-readable source (e.g., "user", "git-root", "parent")
	Exists bool   // Whether the file exists
}

// FindLocations returns all potential config file locations in merge order.
// Later locations override earlier ones.
// Order: user config → parent directories → git root → current directory
func FindLocations(cwd, gitRoot string) []Location {
	var locations []Location

	// 1. User config directory
	if userDir := userConfigDir(); userDir != "" {
		for _, ext := range SupportedExtensions {
			path := filepath.Join(userDir, ConfigDirName, ConfigFileName+ext)
			locations = append(locations, Location{
				Path:   path,
				Source: "user",
				Exists: fileExists(path),
			})
		}
	}

	// 2. Parent directories (from git root up to cwd, if different)
	if gitRoot != "" && gitRoot != cwd {
		parents := parentDirs(cwd, gitRoot)
		for _, parent := range parents {
			for _, ext := range SupportedExtensions {
				path := filepath.Join(parent, ConfigDirName, ConfigFileName+ext)
				locations = append(locations, Location{
					Path:   path,
					Source: "parent:" + parent,
					Exists: fileExists(path),
				})
			}
		}
	}

	// 3. Git root directory
	if gitRoot != "" {
		for _, ext := range SupportedExtensions {
			path := filepath.Join(gitRoot, ConfigDirName, ConfigFileName+ext)
			locations = append(locations, Location{
				Path:   path,
				Source: "git-root",
				Exists: fileExists(path),
			})
		}
	}

	// 4. Current directory (if not git root)
	if cwd != gitRoot {
		for _, ext := range SupportedExtensions {
			path := filepath.Join(cwd, ConfigDirName, ConfigFileName+ext)
			locations = append(locations, Location{
				Path:   path,
				Source: "cwd",
				Exists: fileExists(path),
			})
		}
	}

	return locations
}

// ExistingLocations filters locations to only those that exist.
func ExistingLocations(locations []Location) []Location {
	var existing []Location
	for _, loc := range locations {
		if loc.Exists {
			existing = append(existing, loc)
		}
	}
	return existing
}

// userConfigDir returns the user's config directory.
// On Linux/macOS: ~/.config
// On Windows: %APPDATA%
func userConfigDir() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("APPDATA")
	}

	// XDG Base Directory specification
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

// parentDirs returns directories between child and parent (exclusive of both).
// Returns them in order from parent to child for proper override semantics.
func parentDirs(child, parent string) []string {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)

	if child == parent {
		return nil
	}

	var dirs []string
	current := filepath.Dir(child)

	for current != parent && current != "/" && current != "." {
		dirs = append([]string{current}, dirs...) // prepend for correct order
		current = filepath.Dir(current)
	}

	return dirs
}

// fileExists checks if a file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
