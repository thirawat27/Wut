# Changelog

All notable changes to WUT are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

`.github/scripts/release_notes.sh` reads the `## [version]` sections below when
building GitHub release notes, so keep the heading format intact.

## [Unreleased]

### Added

- **`oops` now fixes and runs.** The shell helpers show the correction, let you
  cycle candidates with ↑/↓, and run the accepted one in your own shell on
  Enter — so `cd`, `export`, and shell functions behave as if you typed it. Esc
  does nothing. `wut fix --shell` prints only the accepted command on stdout and
  draws the picker on the terminal, which is what keeps the two separate. With
  no terminal available it declines rather than hand back a command nobody saw.
- **Corrections derived from read-only facts.** WUT answers the questions it
  used to need the failed command's output for by reading files it chooses and
  running a fixed allowlist of read-only probes it chooses:
  - `git push` with no upstream → `git push --set-upstream <remote> <branch>`,
    resolved with `git rev-parse` / `git remote` instead of pushing again
  - `npm|yarn|pnpm|bun run <typo>` → nearest script in `package.json`
  - `make <typo>` → nearest target in the `Makefile`
  - `git checkout <typo>` → nearest local branch
  - `cd <typo>` → nearest directory that actually exists
  - `rm <dir>` / `cp <dir>` → adds the recursive flag when the target is a
    directory
  - `deploy.sh` → `./deploy.sh` when a file of that name is present

  The probe allowlist is compared argv-for-argv, so nothing derived from a
  user's command can reach a process. Covered by `facts_test.go` and
  `rules_test.go`.
- Corrections now carry every candidate, not just the best one, so the picker
  can offer alternatives — for example each name in git's "most similar
  commands" list.

### Security

- **`wut fix` and `oops` no longer execute your command.** The correction
  pipeline used to run the command being analysed in order to read its error
  output, so `oops` after a failed `git push` pushed again, and `oops` after
  `rm -rf ./dist` deleted again. Only a 7-entry deny-list stood in the way.
  Correction now works from the command string, from read-only facts, and from
  output the shell already captured — never by re-running the command.
- Updated `golang.org/x/text`, closing GO-2026-5970, which was reachable from
  `wut alias list`. `govulncheck` now reports no vulnerabilities.
- `wut db sync` caps the downloaded archive (256 MiB) and each entry it extracts
  (1 MiB), so a hijacked release asset cannot exhaust disk or memory.
- Local sync directories resolve against WUT's data directory instead of the
  process working directory. Previously any folder containing `tldr-main/pages`
  silently replaced the official page source.
- TLDR page URLs escape each path segment, so a command name cannot reshape the
  request path.
- Configuration is written atomically (temp file + rename) with `0600`
  permissions, so an interrupted write no longer truncates your config.

### Fixed

- **`wut db sync` can finish on a normal connection.** The sync manager reused
  an HTTP client whose 5-second whole-exchange timeout was sized for a single
  4 KB page, which aborted the multi-megabyte archive download mid-stream. Bulk
  downloads now use their own client and a 10-minute context budget.
- Fixed a data race in log rotation: `rotatingWriter` mutated its file handle
  and byte counter without synchronisation while the sync workers, the signal
  handler, and the suggestion engine all logged concurrently.
- Fixed log rotation rotating on every write after the first rotation — the byte
  counter was never reset once the old file was renamed away.
- `wut init` no longer discards piped answers. Each prompt built its own
  `bufio.Scanner`, and the first one's read-ahead swallowed the input meant for
  the rest, so every later prompt silently took its default.
- The PowerShell `oops` targeted the wrong history entry. `Get-History -Count 2`
  returns entries oldest-first, and the helper read index `0`, so it tried to fix
  the command *before* the one that failed.
- The "not initialized" path returns an error instead of calling `os.Exit`, which
  used to skip cleanup and every pending `defer`, including database handles.
- Suggestion results truncated by a cancelled context are no longer cached and
  served for the next 30 seconds.
- Missing database buckets produce a clear error instead of a nil-pointer panic
  (28 call sites).
- Runtime failures no longer dump the flag list. Usage is still printed for
  actual usage errors, which are detected before the command runs.

### Changed

- Go 1.26.0 → **1.27.0**, and every dependency updated to its latest release
  within its current major version.
- Dropped three dependencies that nothing imported any more after the dead-code
  removal: `github.com/agnivade/levenshtein`, `github.com/panjf2000/ants/v2`,
  `golang.org/x/sync`.
- The in-memory page cache is bounded at 512 entries and indexed by command name;
  lookups by name no longer scan the whole cache under a lock.
- `Corrector.Correct` is now a thin wrapper over `CorrectWithOutput`. Existing
  callers are unaffected.
- `cmd.Execute` returns an exit code instead of terminating the process itself.
- `runInit` (327 lines) is decomposed into an `initWizard` with one method per
  step; no function in `cmd/init.go` exceeds 60 lines.

### Removed

- ~2,700 lines of unreachable code, each removal verified against
  `go build ./...`:
  - `internal/performance`: `bench.go`, `http.go`, `worker.go`, `storage.go`,
    `init.go` — 184 of 238 exported symbols had no consumer outside the package.
  - `internal/suggest` — no importers; duplicated by `internal/smart/engine.go`.
  - `internal/health` — constructed once in `cmd/root.go` and discarded.
  - `internal/metrics` — counters were never read, persisted, or displayed, and
    it carried an unauthenticated HTTP server that nothing started.
  - Unused `Storage` methods: `GetAllPages`, `GetPageSummaries`,
    `GetPagesByPlatform`, `SearchLocal`, `IsPageStale`.

### CI/CD

- `push.yml` and `pull_request.yml` (~95% identical) are replaced by a single
  `ci.yml` with least-privilege permissions, a concurrency group, job timeouts,
  and a `CI passed` status check for branch protection.
- Added gates that were lost when `ci.yml` was deleted in v0.1.0: `gofmt`,
  `go mod tidy` verification, `golangci-lint`, `go test -race` with coverage on
  Linux/macOS/Windows, `govulncheck`, `gosec`, dependency review, and CodeQL.
- Releases run through GoReleaser, producing `checksums.txt`, a CycloneDX SBOM,
  keyless cosign signatures, and SLSA build provenance. Optional publishers are
  skipped when their credentials are absent instead of failing the release.
- Restored `.github/dependabot.yml`, extended to cover GitHub Actions and the
  container base image.
- The install scripts verify the SHA-256 of what they download against the
  release `checksums.txt`, and resolve asset names that releases actually
  publish — both previously asked for names no release has ever contained.

## [1.0.1] - 2026-07-07

- Version bump across all files.
- Improved suggestions display; added tests for command corrections.
- Removed exec flags from commands; improved error handling.

## [1.0.0] - 2026-04-01

- First stable release: core CLI commands, shell integration, undo logic,
  TLDR-backed command database, and history import.

[Unreleased]: https://github.com/thirawat27/wut/compare/v1.0.1...HEAD
[1.0.1]: https://github.com/thirawat27/wut/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/thirawat27/wut/releases/tag/v1.0.0
