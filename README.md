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

The ⚡ indicator means `--no-build` was auto-detected as safe (build artifacts are up-to-date).

### How it works

1. Scans for all `.csproj` files in your git repo
2. Builds a dependency graph from `<ProjectReference>` entries
3. Computes a cache key from: git commit + dirty files + command + dotnet args
4. Detects solutions and uses solution-level builds when beneficial (avoids parallel build conflicts)
5. Runs affected projects in parallel (defaults to CPU count workers)
6. Skips projects that already passed with the same cache key
7. Auto-detects when `--no-build` or `--no-restore` can be safely skipped

### Options

```bash
donotnet test                         # Run affected tests
donotnet -force test                  # Run all tests, ignore cache
donotnet -watch test                  # Watch mode - rerun on file changes
donotnet -j 4 test                    # Use 4 parallel workers
donotnet -k test                      # Keep going on errors (don't stop at first failure)
donotnet -vcs-changed test            # Only test projects with uncommitted changes
donotnet -vcs-ref=main test           # Only test projects changed vs main branch
donotnet -failed test                 # Re-run only previously failed tests
donotnet -solution test               # Force solution-level builds (when 2+ projects in a solution)
donotnet -no-solution test            # Disable solution detection, build individual projects
donotnet -dev-plan test               # Show job scheduling plan and exit (for debugging)
donotnet test -- --filter "Name~Foo"  # Pass args to dotnet test
```

### Solution detection

By default, donotnet detects `.sln` files and uses solution-level builds when **all** projects in a solution need building. This lets MSBuild handle internal dependencies and avoids parallel build conflicts.

- Default: Use solution only when all its projects need building
- `-solution`: Use solution when 2+ projects in it need building
- `-no-solution`: Always build individual projects

### Listing projects

```bash
donotnet -list-affected=tests      # List affected test projects
donotnet -list-affected=non-tests  # List affected non-test projects
donotnet -list-affected=all        # List all affected projects
```

### Cache management

Cache is stored in `.donotnet/cache.db` at the git root.

```bash
donotnet -cache-stats              # Show cache statistics
donotnet -cache-clean=30           # Remove entries older than 30 days
```

## Flags

| Flag                  | Description                                              |
| --------------------- | -------------------------------------------------------- |
| `-force`              | Run all projects, ignoring cache                         |
| `-watch`              | Watch for file changes and rerun                         |
| `-k`                  | Keep going on errors                                     |
| `-j N`                | Number of parallel workers (default: CPU count)          |
| `-v`                  | Verbose output                                           |
| `-q`                  | Quiet mode                                               |
| `-C dir`              | Change to directory before running                       |
| `-vcs-changed`        | Only test uncommitted changes                            |
| `-vcs-ref=REF`        | Only test changes vs ref                                 |
| `-list-affected=TYPE` | List projects (all/tests/non-tests)                      |
| `-failed`             | Re-run only previously failed tests                      |
| `-solution`           | Force solution-level builds when 2+ projects match       |
| `-no-solution`        | Disable solution detection, build individual projects    |
| `-dev-plan`           | Show job scheduling plan and exit                        |
