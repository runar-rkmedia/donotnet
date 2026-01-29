package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// LoadOptions controls config loading behavior.
type LoadOptions struct {
	// CWD is the current working directory.
	CWD string

	// GitRoot is the git repository root (empty if not in a git repo).
	GitRoot string

	// ConfigFile overrides config file discovery (--config flag).
	ConfigFile string

	// SkipEnv disables environment variable loading.
	SkipEnv bool

	// Verbose enables debug output during loading.
	Verbose bool
}

// LoadResult contains the loaded config and metadata about sources.
type LoadResult struct {
	Config  *Config
	Sources []string // List of sources that contributed to the config
}

// Load loads configuration from all sources and returns the merged result.
// Order (later overrides earlier): defaults → files → env vars
// Flags are applied by the caller after Load returns.
func Load(opts LoadOptions) (*LoadResult, error) {
	k := koanf.New(".")
	result := &LoadResult{
		Sources: []string{"defaults"},
	}

	// 1. Load defaults
	if err := k.Load(structs.Provider(Default(), "koanf"), nil); err != nil {
		return nil, err
	}

	// 2. Load config files
	if opts.ConfigFile != "" {
		// Explicit config file specified
		if err := loadFile(k, opts.ConfigFile); err != nil {
			return nil, err
		}
		result.Sources = append(result.Sources, opts.ConfigFile)
	} else {
		// Auto-discover config files
		locations := FindLocations(opts.CWD, opts.GitRoot)
		for _, loc := range ExistingLocations(locations) {
			if err := loadFile(k, loc.Path); err != nil {
				// Log but continue - don't fail on config parse errors
				if opts.Verbose {
					// Can't use term here to avoid import cycles
					os.Stderr.WriteString("config: error loading " + loc.Path + ": " + err.Error() + "\n")
				}
				continue
			}
			result.Sources = append(result.Sources, loc.Source+":"+loc.Path)
		}
	}

	// 3. Load environment variables
	if !opts.SkipEnv {
		envProvider := env.Provider("DONOTNET_", ".", func(s string) string {
			// DONOTNET_TEST_COVERAGE -> test.coverage
			s = strings.TrimPrefix(s, "DONOTNET_")
			s = strings.ToLower(s)
			s = strings.ReplaceAll(s, "_", ".")
			return s
		})

		if err := k.Load(envProvider, nil); err != nil {
			return nil, err
		}
		result.Sources = append(result.Sources, "env")
	}

	// 4. Unmarshal into Config struct
	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, err
	}

	result.Config = &cfg
	return result, nil
}

// loadFile loads a single config file based on its extension.
func loadFile(k *koanf.Koanf, path string) error {
	ext := strings.ToLower(filepath.Ext(path))

	var parser koanf.Parser
	switch ext {
	case ".toml":
		parser = toml.Parser()
	case ".yaml", ".yml":
		parser = yaml.Parser()
	case ".json":
		parser = json.Parser()
	default:
		// Try TOML by default
		parser = toml.Parser()
	}

	return k.Load(file.Provider(path), parser)
}

// MustLoad is like Load but panics on error.
func MustLoad(opts LoadOptions) *LoadResult {
	result, err := Load(opts)
	if err != nil {
		panic("config: " + err.Error())
	}
	return result
}
