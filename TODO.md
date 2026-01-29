# CLI Refactor — Parity Tracker

Everything from `main` must work identically in `cli-refactor`.
Each item is checked off only when implemented **and** tested.

The core execution logic in `runner/runner.go` is a faithful port — it already
**consumes** `FailedTestFilters`, `BuildOnlyProjects`, `PrintOutput`, `Watch`,
etc. The gaps are all in the **wiring/setup layer** (`cmd/runner.go` or early
in `Runner.Run()`) that the old monolithic `main()` handled before calling
the run loop. Most fixes are adding the same setup lines that existed before.

---

## 5. `--print-output` Cache Miss Forcing — TRIVIAL (2 lines)

Runner already has `r.opts.PrintOutput`. Just missing a conditional in `projectChanged()`.

- [ ] In `projectChanged()` (`runner.go`): if `PrintOutput` is set, project is test, and cached entry has empty output → return changed
- [ ] E2E test

**Reference:** `git show main:changed.go` (lines 63-68)

---

## 7. Auto-enable `--print-output` for Dotnet `--list-tests` — TRIVIAL (1 line)

Auto-quiet already works. Just missing `r.opts.PrintOutput = true` next to the existing `term.SetQuiet(true)`.

- [ ] Add `r.opts.PrintOutput = true` in `Runner.Run()` alongside auto-quiet
- [ ] Unit test

**Reference:** `git show main:main.go` (line ~314)

---

## 3. Untested Project Detection and Build — SMALL (~30 lines of wiring)

`BuildOnlyProjects` map is defined and the runner already handles build-only
execution (`runSingleProject` checks it). Nobody calls `FindUntestedProjects()`
to populate the map.

- [ ] Call `project.FindUntestedProjects()` in `Runner.Run()` after project scanning
- [ ] Populate `BuildOnlyProjects` map
- [ ] Warn message: `N project(s) have no tests, will build instead: ...`
- [ ] Summary shows both test and build counts (runner already supports this)
- [ ] E2E test: fixture with unreferenced non-test project, verify it gets built

**Reference:** `git show main:main.go` (search `untestedProjects`, lines ~693-723)

---

## 2. `--failed` Flag (Re-run Failed Tests) — SMALL (~50 lines of wiring)

Flag is plumbed to `runner.Options.Failed` and `runSingleProject()` already
applies `FailedTestFilters` if set. Nobody queries the cache to populate the
filter map or trim the target list.

- [ ] In `Runner.Run()`: if `Failed`, call `db.GetFailed(argsHash)` to get failed entries
- [ ] Port `getFailedTestFilter()` from old main.go (builds per-project filter strings)
- [ ] Populate `FailedTestFilters` map and trim `targetProjects` to only failed ones
- [ ] E2E test: run with failing test, fix, run `--failed`, verify correct behavior

**Reference:** `git show main:main.go` (search `flagFailed` / `failedFilter`, lines ~582-631)

---

## 6. Misplaced Dotnet Flag Detection — SMALL (~10 lines in cobra cmd)

Old pre-parse check detected `--filter`, `--configuration`, etc. before `--`.
Cobra handles `--` natively, so the user gets an "unknown flag" error, but
without the helpful hint about using `--`.

- [ ] In `cmd/test.go` and `cmd/build.go` RunE: check `args` for known dotnet flag patterns
- [ ] Error message: `flag "X" looks like a dotnet flag. Use: donotnet test -- --filter "..."`
- [ ] Detect filter expressions (e.g. `Category!=Live`)
- [ ] Unit test

**Reference:** `git show main:main.go` (search `looks like a dotnet flag`, lines ~269-277)

---

## 4. Suggestions System — SMALL-MEDIUM (port ~100 lines + 3 lines of calls)

Old `suggestions.go` was deleted. `--no-suggestions` flag is wired but no-op.
The suggestion logic is self-contained — port it to a package, add calls in `Runner.Run()`.

- [ ] Create `suggestions/` package (or add to runner) with `Suggestion` struct
- [ ] Per-session dedup (each suggestion shown once)
- [ ] `parallel-test-framework`: detect xUnit projects missing `Meziantou.Xunit.ParallelTestFramework`
- [ ] `coverage-not-found`: no `.testcoverage.json` → suggest `--build-test-coverage`
- [ ] `coverage-stale`: source changed since coverage generated → suggest updating
- [ ] Formatted output with title, description, affected projects, link
- [ ] Respect `--no-suggestions`
- [ ] Unit test for detection logic

**Reference:** `git show main:suggestions.go`

---

## 8. Did-You-Mean for Misspelled Flags — SMALL (verify + possibly ~10 lines)

Cobra has built-in subcommand suggestions. Need to verify it also covers flags.

- [ ] Verify: `donotnet test --watc` → does cobra suggest `--watch`?
- [ ] If not, add custom suggestion logic on command
- [ ] E2E test: misspelled flag gets suggestion

**Reference:** `git show main:flags.go`

---

## 1. Watch Mode (`--watch`) — MEDIUM (port ~261 lines)

Stub exists, call site is wired. Old `watch.go` logic needs porting into
`runner.runWatch()`. The `Runner` struct already has the fields the old
`watchContext` needed.

- [ ] fsnotify-based recursive directory watching
- [ ] Skip `bin`, `obj`, `.git`, `node_modules`, `.vs` directories
- [ ] React only to Write/Create events on `.cs`, `.csproj`, `.razor`, `.props`, `.targets`
- [ ] 100ms debounce timer to aggregate rapid changes
- [ ] Map changed files to owning project via directory prefix
- [ ] Coverage-based test selection (if coverage map covers all changed files)
- [ ] Dependency-based fallback when files aren't covered
- [ ] Maintain TestFilter with coverage maps and heuristics across iterations
- [ ] Graceful Ctrl+C / SIGINT/SIGTERM to stop watcher
- [ ] Initial test/build run before entering watch loop
- [ ] E2E test for watch mode

**Reference:** `git show main:watch.go`

---

## Progress

Ordered by effort (trivial → medium). Check off as completed.

| # | Feature | Nature of gap | Implemented | Tested |
|---|---------|--------------|:-----------:|:------:|
| 7 | Auto `--print-output` for `--list-tests` | 1 line omission | ✅ | ✅ `TestPrintOutput` |
| 5 | `--print-output` cache miss | 2 line omission | ✅ | ✅ `TestPrintOutput` |
| 3 | Untested project detection | ~30 lines wiring | ✅ | ✅ `TestUntestedProjectWarning` |
| 2 | `--failed` flag | ~50 lines wiring + port helper | ✅ | ✅ `TestFailedFlag` |
| 6 | Misplaced dotnet flag hint | ~10 lines in cobra cmd | ✅ | ✅ `TestMisplacedFilterHint` |
| 4 | Suggestions system | Port ~100 lines + 3 call sites | ✅ | ✅ `TestSuggestionsShown`, `TestSuggestionsSuppressed` |
| 8 | Did-you-mean for flags | Verify cobra, possibly ~10 lines | ✅ | ✅ `TestFlagSuggestion`, `TestFlagSuggestionBuild` |
| 1 | Watch mode | Port ~261 lines into runner | ✅ | — (interactive, not e2e testable) |
