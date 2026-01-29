# Refactor Gaps: cli-refactor vs main

A refactoring was performed, which was meant to be mostly just wiring of the cli, and separation of concerns, but it turned out that we missed a lot of functioanlity.

While fixing these issues, attempt to create tests where suitable, but only if the tests make sense. They should be high-level, easy to read anc consise, and should test end-user functioanlity.

## High Severity

### 1. ~~Coverage build: within-project parallelism changed from serial to full GOMAXPROCS~~ FIXED

- **Old**: `testcoverage.go` passed `maxJobs=1` to `buildSingleTestCoverageMap`, intentionally serializing test runs within a single project because coverlet instruments DLLs and concurrent access causes file locking.
- **New**: `coverage/build.go:buildSingleProjectCoverage` runs at full `GOMAXPROCS` parallelism.
- **Impact**: Will cause intermittent coverlet file locking failures during coverage builds.
- **Fix**: Set worker count to 1 in `buildSingleProjectCoverage`.

### 2. ~~Non-watch test filtering by dirty files is gone~~ FIXED

- **Old**: `main.go` created a `TestFilter`, loaded coverage maps, and called `AddChangedFile()` for each dirty file in single-run (non-watch) mode. This gave per-test filtering even without `--watch`.
- **New**: `runner/runner.go` only sets `TestFilter` in watch mode. In single-run mode, dirty files are only used for project-level VCS filtering, never per-test filtering.
- **Impact**: Single-run `donotnet test` no longer narrows down to specific tests based on dirty files.
- **Fix**: Restore test filter setup in `runner.Run()` for non-watch mode.

## Medium Severity

### 3. ~~`findChangedProjects` parallelism removed~~ FIXED

- **Old**: `changed.go` used goroutines + `sync.WaitGroup` for concurrent content hash computation.
- **New**: `runner/runner.go` is sequential.
- **Impact**: Slower startup on large repos where content hash computation involves filesystem I/O.
- **Fix**: Re-add goroutine-based parallelism to `findChangedProjects`.

### 4. ~~`--list-affected` no longer auto-enables VCS-changed mode~~ FIXED

- **Old**: Auto-set `-vcs-changed=true` when `-list-affected` was used without any VCS filter, showing an info message.
- **New**: `list affected` subcommand uses cache-miss detection instead.
- **Impact**: Different semantic for listing affected projects.
- **Fix**: Auto-enable VCS-changed in `list affected` when no VCS flags are set.

### 5. ~~`--list-tests` no longer scopes to affected projects~~ FIXED

- **Old**: First tried affected-only test projects, falling back to all.
- **New**: `list tests` always scans all test projects.
- **Impact**: Slower and noisier output.
- **Fix**: Added `--affected` flag to `list tests` that scopes to VCS-changed + cache-miss projects.

### 6. ~~Test list caching removed from coverage build path~~ FIXED

- **Old**: `getTestList()` cached `dotnet test --list-tests` results via `cache.DB` with content hash keys.
- **New**: `listTests()` in `coverage/build.go` always re-runs the command.
- **Impact**: Unnecessary re-runs of `dotnet test --list-tests` during coverage builds.
- **Fix**: Added `TestListCache` interface in coverage, implemented in runner. Shared `coverage.ListTests()` used by both `cmd/list_tests.go` and coverage build. Cache backed by `cache.DB` with content hash keys.

### 7. ~~`--print-output` doesn't work for cached-only runs~~ FIXED

- **Old**: Looked up and printed cached output when all projects were cached (zero targets).
- **New**: Zero-targets path just prints summary and returns.
- **Impact**: `--print-output` is silently ignored when everything is cached.
- **Fix**: Look up cached output in the zero-targets path when `PrintOutput` is set.

### 8. ~~`loadAllTestCoverageMaps` path changed from gitRoot to cacheDir~~ NOT A BUG

- **Old**: Loaded from `gitRoot` then computed `.donotnet` subdir internally.
- **New**: Loads from `cacheDir` directly (which defaults to `gitRoot/.donotnet`).
- **Conclusion**: Equivalent behavior, and the new code correctly respects `--cache-dir`.

### 9. ~~Signal handling missing in `runSolutionCommand`~~ FIXED

- **Old**: Had its own Ctrl+C signal handler.
- **New**: `runner/solution.go` relies on context cancellation but never sets up signal catching.
- **Impact**: Ctrl+C during solution builds may not propagate correctly.
- **Fix**: Moved signal handling before the solution path branch in `runProjects`.

## Low Severity

### 10. ~~Trait info removed from grouping listings and list-tests output~~ FIXED

- **Old**: `listTestCoverageGroupings` displayed trait annotations (Category, Trait, etc.) on groups and a per-project trait summary. `list-tests` JSON had `TestDetail` with `Traits` field.
- **New**: No trait integration in `coverage/groupings.go`. List-tests outputs flat `[]string`.
- **Fix**: Added `TraitMap`, `BuildTraitMap`, `GetTraitsForTest`, `AllTraits` to `testfilter/traits.go`. Integrated into groupings (per-project trait summary + per-group annotations) and list-tests (JSON `TestDetail` with traits, plain text yellow bracket annotations).

### 11. ~~`coverage parse` output sorting missing~~ FIXED

- **Old**: Sorted `CoveredFiles` and `AllFiles` before JSON output for determinism.
- **New**: No sorting, non-deterministic output.
- **Fix**: Add `sort.Strings()` calls before JSON encoding.

### 12. ~~`--dev-plan` / `plan` shows all projects instead of affected+cached~~ FIXED

- **Old**: Used `allRelevant` combining targeted and cached projects.
- **New**: Shows all projects unconditionally.
- **Fix**: Added cache-miss detection to filter plan to affected projects only.

### 13. ~~`--list-affected` prints Name instead of Path~~ FIXED

- **Old**: Printed `p.Path`.
- **New**: Prints `p.Name`.
- **Fix**: Print `p.Path` or make it configurable.

### 14. ~~`--dump-cache` shows less diagnostic info~~ FIXED

- **Old**: Showed cache key, content hash comparison, args hash, all entries.
- **New**: Only shows basic info (status, args, last run, output).
- **Fix**: Added cache key, content hash, args hash, current hash comparison, and output size.

### 15. ~~`--list-heuristics` output format differs~~ FIXED

- **Old**: Showed `AvailableHeuristics` (default) and `OptInHeuristics` separately with detailed examples.
- **New**: Shows "(none - all heuristics are opt-in)" for defaults.
- **Fix**: Now reads from `testfilter.AvailableHeuristics` and `testfilter.OptInHeuristics` directly instead of duplicating definitions. Shows both groups dynamically.

## Cosmetic

### 16. `--version` is now only a subcommand

- **Old**: `-version` flag worked.
- **New**: Must use `donotnet version`.
- **Fix**: Do not fix.

### 17. ~~Flag suggestion output no longer colorized~~ FIXED

- **Old**: Green flag name, dim usage description, "Run 'donotnet -help'" footer.
- **New**: Plain `fmt.Errorf` message.
- **Fix**: Added green coloring for suggested flag name using `term.Color`.

### 18. ~~`ListGroupings` uses hardcoded `/` instead of `filepath.Join`~~ FIXED

- **Fix**: Use `filepath.Join` and `filepath.Dir` for portability.
