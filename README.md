# donotnet

Designed for large .NET repositories with multiple solutions and projects.
Scans your git repo for `.csproj` files, runs tests/builds in parallel - skipping projects that haven't changed.

## Install

```bash
go install github.com/runar-rkmedia/donotnet@latest
```

Or build from source:

```bash
git clone <repo>
cd donotnet
go build -o donotnet .
```

## Usage

```bash
donotnet test    # Run tests on all affected test projects in parallel
donotnet build   # Build all affected projects in parallel
```

On subsequent runs, only projects with changes (or dependencies on changed projects) will run.

### Example output

```
Running test on 15 projects (8 workers)...
  ✓ ⚡ MyProject.Tests                    1.6s  Failed: 0  Passed: 21  Skipped: 4  Total: 25
  ✓ ⚡ MyOtherProject.Tests               1.8s  Failed: 0  Passed: 13  Skipped: 1  Total: 14
  ✓ ⚡ Common.Tests                       1.9s  Failed: 0  Passed: 67  Skipped: 0  Total: 67
  ...

15/15 succeeded (9.1s)
```

Indicators show when dotnet flags were auto-skipped to improve speed:

- ⚡ `--no-build` was applied (build artifacts are up-to-date)
- ↻ `--no-restore` was applied (NuGet packages are up-to-date)

These optimizations are detected automatically. To disable them and always run a full build/restore, use `--full-build`.

### How it works

1. Scans for all `.csproj` files in your git repo
2. Builds a dependency graph from `<ProjectReference>` entries
3. Computes a cache key from: git commit + dirty files + command + dotnet args
4. Detects solutions and uses solution-level builds when beneficial (avoids parallel build conflicts)
5. Runs affected projects in parallel (defaults to CPU count workers)
6. Skips projects that already passed with the same cache key
7. Auto-detects when `--no-build` or `--no-restore` can be safely skipped

### Commands

#### test

```bash
donotnet test                              # Run affected tests
donotnet test --force                      # Run all tests, ignore cache
donotnet test --watch                      # Watch mode - rerun on file changes
donotnet test -j 4                         # Use 4 parallel workers
donotnet test -k                           # Keep going on errors (don't stop at first failure)
donotnet test --vcs-changed                # Only test projects with uncommitted changes
donotnet test --vcs-ref=main               # Only test projects changed vs main branch
donotnet test --failed                     # Re-run only previously failed tests
donotnet test --coverage                   # Collect code coverage during test runs
donotnet test --solution                   # Force solution-level builds (when 2+ projects in a solution)
donotnet test --no-solution                # Disable solution detection, build individual projects
donotnet test -- --filter "Name~Foo"       # Pass args to dotnet test
```

#### build

```bash
donotnet build                             # Build affected projects
donotnet build --watch                     # Watch for changes and rebuild
donotnet build --vcs-changed               # Build projects with uncommitted changes
donotnet build --vcs-ref=main              # Build projects changed vs main branch
donotnet build -- -c Release               # Pass args to dotnet build
```

#### list

```bash
donotnet list affected                     # List all affected projects
donotnet list affected -t tests            # List affected test projects
donotnet list affected -t non-tests        # List affected non-test projects
donotnet list affected --vcs-ref=main      # Compare against main branch
donotnet list tests                        # List all tests as JSON
donotnet list tests --affected             # Only tests from affected projects
donotnet list heuristics                   # List available test filter heuristics
donotnet list coverage                     # Show coverage map
donotnet list coverage --groupings         # Show test groupings
```

#### cache

Cache is stored in `.donotnet/cache.db` at the git root.

```bash
donotnet cache stats                       # Show cache statistics
donotnet cache clean                       # Remove entries older than 30 days
donotnet cache clean --older-than=7        # Remove entries older than 7 days
donotnet cache dump <project>              # Show cached output for a project
```

#### coverage

```bash
donotnet coverage build                    # Build per-test coverage map
donotnet coverage build --granularity=method  # Fine-grained coverage
donotnet coverage parse <file>             # Parse a Cobertura coverage XML file
```

#### Other commands

```bash
donotnet plan                              # Show job scheduling plan (for debugging)
donotnet config                            # Show effective configuration
donotnet config --format=json              # Show config as JSON
donotnet config --locations                # Show config file locations
donotnet completion bash                   # Generate shell completions (bash/zsh/fish/powershell)
donotnet version                           # Show version and build info
```

### Solution detection

By default, donotnet detects `.sln` files and uses solution-level builds when **all** projects in a solution need building. This lets MSBuild handle internal dependencies and avoids parallel build conflicts.

- Default: Use solution only when all its projects need building
- `--solution`: Use solution when 2+ projects in it need building
- `--no-solution`: Always build individual projects

### Untested project detection

When running `donotnet test`, projects without test coverage are detected and **built** instead of tested. This prevents false confidence from running tests on a codebase where some projects have no tests at all.

Detection uses the dependency graph: a non-test project is considered "untested" if no test project references it (directly or transitively). These projects are built alongside tests in the same worker pool, showing `(no tests)` in the output:

```
warning: 3 project(s) have no tests, will build instead: LibA, LibB, LibC
Testing 12 projects + building 3 untested (8 workers)...
  ✓ MyProject.Tests           1.6s  Failed: 0  Passed: 21  Total: 21
  ✓ LibA                      0.4s (no tests)
  ✓ LibB                      0.3s (no tests)
  ...
```

## Global flags

| Flag              | Short | Description                                     |
| ----------------- | ----- | ----------------------------------------------- |
| `--force`         |       | Run all projects, ignoring cache                |
| `--watch`         |       | Watch for file changes and rerun                |
| `--keep-going`    | `-k`  | Keep going on errors                            |
| `--parallel`      | `-j`  | Number of parallel workers (default: CPU count) |
| `--verbose`       | `-v`  | Verbose output                                  |
| `--quiet`         | `-q`  | Quiet mode                                      |
| `--dir`           | `-C`  | Change to directory before running              |
| `--color`         |       | Color output: `auto`, `always`, `never`         |
| `--local`         |       | Only scan current directory, not entire git repo|
| `--show-cached`   |       | Show cached projects in output                  |
| `--no-progress`   |       | Disable progress output                         |
| `--no-suggestions`|       | Disable performance suggestions                 |
| `--config`        |       | Config file path (overrides auto-discovery)     |

## Configuration

Configuration can be set in TOML files, discovered in this order (later overrides earlier):

- `~/.config/donotnet/config.toml` (user config)
- Parent directory configs
- Git root `.donotnet/config.toml`
- Current directory config
- Environment variables (`DONOTNET_*`, e.g. `DONOTNET_VERBOSE=true`)
- Command-line flags

Run `donotnet config` to see the effective configuration, or `donotnet config --locations` to see which files are active.

### Example config

```toml
verbose = false
parallel = 0            # 0 = auto (number of CPUs)
color = "auto"           # auto, always, never
show_cached = false
local = false            # true = only scan current directory
keep_going = false
quiet = false
no_progress = false
no_suggestions = false

[test]
heuristics = "default"   # default, none, or comma-separated names
coverage = false
coverage_granularity = "class"  # method, class, file
staleness_check = "git"         # git, mtime, both
reports = true           # save TRX test reports
failed = false

[build]
solution = "auto"        # auto, always, never
full_build = false       # true = disable --no-build/--no-restore auto-detection

[vcs]
ref = ""                 # e.g. "main" to always compare against main
changed = false

[watch]
debounce_ms = 100
```
