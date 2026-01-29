# Changelog

## [0.2.0] - 2026-01-29

### Bug Fixes

- Skip detection in watch mode, add verbose diagnostics
- Cache key collision between test and build commands
- Per-test coverage lookup for cross-project changes
- --no-build and --no-restore to check transitive dependencies
- Cross-platform build for Windows
- Move dev-plan to separate module and fix flag not working
- Filter out test-specific args when building untested projects
- Properly cache build-only projects during test runs
- Retry with user filter when heuristic filter matches 0 tests
- Don't flag filter expressions after -- as misplaced
- Watch mode exits early when all projects are cached
- Restore runner flow gaps from refactor
- Restore missing functionality in subcommands
- Remove duplicate initial run in watch mode

### Documentation

- Update readme
- Add changelog
- Document untested project detection
- Update TODO with e2e speed follow-up
- Add refactoring gaps tracking document

### Features

- Improve output
- Invalidate cache when filters change
- Reports
- Bbolt-based git-aware caching
- Add --watch mode
- Auto skip build/restore, improved output formatting
- Smart test filtering in watch mode
- Add --print-output and -q flags, store output in bbolt cache
- Add coverage-based test impact analysis
- Add -list-tests flag to list all tests as JSON
- Add per-test coverage mapping (-build-test-coverage)
- Integrate per-test coverage into watch mode
- Add configurable test filter heuristics
- Allow disabling specific heuristics with - prefix
- Add --failed flag to rerun only failed tests
- Add --color and --no-progress flags for non-TTY environments
- Add goreleaser for automated releases
- Add dist/ to gitignore
- Improve CLI output and validate flag usage
- Add dependency-aware job scheduling and --dev-plan flag
- Add solution-level builds to avoid parallel build conflicts
- Add did-you-mean suggestions for misspelled flags
- Build untested projects during test runs
- Add suggestions system for performance tips
- Enable test filtering in non-watch mode
- Add TestFileOnly heuristic with safety checks (opt-in)
- Add coverage staleness suggestions with configurable check method
- Add coverage granularity options for faster coverage building
- Add caching for test listing and improve coverage grouping display
- Improve test exclusion by category filter
- Show traits in test listing and coverage groupings
- Add e2e CLI tests with fixture project and comparison mode
- Restore parity with main branch and add e2e coverage
- Add coverage staleness suggestions and update TODO
- Implement per-test coverage build
- Add detailed coverage grouping listing
- Add test list caching and --affected flag for list tests
- Add trait map and integrate into coverage groupings
- Add e2e integration coverage via go build -cover

### Miscellaneous

- Initial commit
- Remove dononet -mark

### Performance

- Limit projects to gomaxprox

### Refactor

- Simplify CLI flags: replace -tests/-all with -list-affected
- Extract Terminal helper for colored output
- Use content-based cache key instead of git commit hash
- Extract git, cache, and project packages from main.go
- Move solution matching functions to project package
- Extract term package and hash.go from main.go
- Extract test coverage functions to testcoverage.go
- Extract watch mode to watch.go
- Move all heuristics to opt-in, no defaults
- Extract helper functions from main.go
- Restructure CLI with cobra and modular packages

### Testing

- Add watch mode e2e test and update TODO progress

### Build

- Add git cliff

### Ci

- Cliff config

