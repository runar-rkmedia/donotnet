# Changelog

## [unreleased]

### Bug Fixes

- Skip detection in watch mode, add verbose diagnostics
- Cache key collision between test and build commands
- Per-test coverage lookup for cross-project changes
- --no-build and --no-restore to check transitive dependencies
- Cross-platform build for Windows

### Documentation

- Update readme

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

### Miscellaneous

- Initial commit
- Remove dononet -mark

### Performance

- Limit projects to gomaxprox

### Refactor

- Simplify CLI flags: replace -tests/-all with -list-affected
- Extract Terminal helper for colored output
- Use content-based cache key instead of git commit hash

### Build

- Add git cliff

