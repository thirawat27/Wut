# WUT — Architecture & Code Audit

- Date: 2026-08-25
- Commit: 0eec139 (`main`, clean tree)
- Pass level: **Full Mode** — trigger: whole-repo scan across reliability, security, and
  maintainability requested; 3+ interacting modules, persistence, shell integration,
  network I/O, and release/CI surface all in scope.
- Baseline verified: `go build ./...` OK, `go vet ./...` OK, `go test ./...` OK
  (3 of 18 packages have tests), `gofmt -l` → 1 file, `govulncheck` → 1 reachable CVE.

---

## 1. What was inspected

| Area | Evidence |
|---|---|
| Entry point / CLI wiring | `main.go`, `cmd/root.go`, all 18 files in `cmd/` |
| Correction engine | `internal/corrector/{corrector,evaluator,semantic,shortflag}.go` |
| Persistence | `internal/db/{storage,history,client,sync,bookmark,tui}.go` (bbolt) |
| Suggestion engine | `internal/smart/engine.go`, `internal/commandsearch`, `internal/historyml` |
| Shell integration | `internal/shell/{installer,shells}.go`, `internal/alias/manager.go` |
| Cross-cutting | `internal/{config,logger,metrics,health,ui,context,concurrency,performance}` |
| Build & release | `Makefile`, `build.go`, `.goreleaser.yaml`, `.github/workflows/*`, `scripts/install.*` |
| History | `git log --diff-filter=D` for unintentionally deleted files |

**Inspection limitations.** No runtime/integration testing was performed against a live shell
install; findings marked `Verify first: Yes` need a manual reproduction before acting.

---

## 2. Confirmed architecture facts

- Single Go module `wut` (go 1.26.0), Cobra CLI, ~25.2k LOC across `cmd/` + `internal/`.
- Layering is `cmd/` (presentation + orchestration) → `internal/*` (domain) → bbolt + filesystem.
  There is no service/repository seam; `cmd/` constructs `db.Storage` directly in 12 files.
- Storage is a single bbolt file with two page buckets (`tldr`, `metadata`) plus history buckets.
- Configuration is a **global viper singleton** (`internal/config`), read by 17 files.
- Network access is limited to `raw.githubusercontent.com` (per page) and
  `github.com/tldr-pages/tldr/releases` (full archive).
- Four independent build paths exist: `Makefile`, `build.go` (`//go:build ignore`),
  `.goreleaser.yaml`, and three hand-written GitHub workflows. Only the workflows run in CI;
  `.goreleaser.yaml` is invoked by nothing.

---

## 3. Findings

Severity: `Critical` breaks correctness or contradicts the system's own flow ·
`High` actively harms change or run safety · `Medium` taxes future work · `Low` note only.

### C1 — `wut fix` / `oops` re-executes the user's command · Critical · Direct

**Evidence**

- `internal/corrector/corrector.go:62` — `Correct()` step 1.5 calls `c.evaluateErrorRules(command)`.
- `internal/corrector/evaluator.go:170-172` — `exec.CommandContext(ctx, fields[0], fields[1:]...)`
  then `cmd.CombinedOutput()`. The command is **run for real** to harvest its stderr.
- `cmd/fix.go:104` — `runFix` calls `c.Correct(input)`.
- `cmd/fix.go:73-83` — with no argument, `input` is taken from the **last shell history entry**.
- `internal/shell/installer.go:333-354` (bash/zsh) and the fish / PowerShell / nushell / xonsh /
  elvish equivalents define `oops` / `again`, which resolve the last history line and invoke
  `wut fix "$cmd"`.

**Guards that exist, and why they are insufficient**

- `corrector.go:279-303` `checkDangerous` matches 7 literal prefixes (`corrector.go:566-569`)
  and 2 regexes (`corrector.go:577-578`: `rm -rf /?$`, `> /dev/sd[a-z]`).
- `evaluator.go:203-215` `looksLikeInteractive` blocks 14 interactive binaries only.

**Failure scenario**

User runs `git push`, it fails, user types `oops` → WUT runs `git push` again. Same for
`docker system prune -af`, `terraform destroy -auto-approve`, `kubectl delete ns prod`,
`brew install <pkg>`, `rm -rf ./dist`, `npm publish`. None are matched by the blocklist.
The `git_push_set_upstream` rule (`evaluator.go:22-35`) *requires* re-running `git push` to read
its hint, so this is by design, not an oversight in one branch.

**Root cause**

The correction pipeline conflates *analyse* with *execute*. The error output it wants already
exists in the user's shell at the moment `oops` is typed; WUT throws it away and recreates it by
re-running the command. A deny-list can never make that safe.

**Smallest safe correction (proposed)**

1. Remove execution from the default path.
2. Split `coreRules` into output-independent rules (`cd..`, `apt search`, bare `go run`) — these
   already work from the command string alone — and output-dependent rules.
3. Feed output-dependent rules from the shell: the hook captures stderr and passes it via
   `WUT_LAST_STDERR` / `--stderr -`. This is the root-cause fix.
4. Keep re-execution only behind an explicit `--probe` opt-in, restricted to a curated read-only
   allowlist, never on the no-argument / `oops` path.

---

### C2 — `wut db sync` cannot finish on a normal connection · Critical · Direct

**Evidence**

- `internal/db/client.go:124-127` — `NewClient()` builds `&http.Client{Timeout: 5 * time.Second}`.
- `internal/db/sync.go:53-63` — `NewSyncManager` uses that same `NewClient()`.
- `internal/db/sync.go:310` — `sm.client.httpClient.Do(req)` for
  `https://github.com/tldr-pages/tldr/releases/latest/download/tldr.zip` (`sync.go:94`).
- `internal/db/sync.go:329` — `io.Copy(tmpFile, resp.Body)`.

`http.Client.Timeout` covers the entire exchange **including reading the body**, so the whole
multi-MB archive plus the GitHub redirect must complete inside 5 s.

**Failure scenario**

On anything slower than ~10 MB/s the download aborts with
`failed to download zip stream: context deadline exceeded`. Because `wut init` offers the same
sync, first-run setup fails for most users.

**Root cause**

One HTTP client tuned for 4 KB page fetches is reused for a whole-archive download. Timeout
policy belongs to the call, not to a shared client.

**Smallest safe correction (proposed)**

Give `SyncManager` its own client with no global `Timeout` and per-request
`context.WithTimeout` (page fetch 5 s, archive download 10 min), or set a
`ResponseHeaderTimeout` on the transport instead of a whole-exchange timeout.

---

### H1 — Data race in log rotation · High · Direct

`internal/logger/logger.go:223-297`. `rotatingWriter` has no mutex. `Write` (`:263`) reads and
writes `rw.size` and `rw.file`; `rotate` (`:277`) closes `rw.file` and reopens it. The logger is
shared: `cmd/root.go:113` logs from the signal goroutine, `internal/db/sync.go` logs from
`concurrency.Pool` workers (`sync.go:53`, `NumCPU()*2`), `internal/smart/engine.go:131+` runs five
concurrent sources. A concurrent `Write` during `rotate` writes to a closed handle.

**Fix:** `sync.Mutex` around `Write` / `rotate` / `Close`.

### H2 — Reachable dependency vulnerability · High · Direct

`govulncheck` result:

```
Vulnerability #1: GO-2026-5970  (infinite loop on invalid input, golang.org/x/text)
  Found in: golang.org/x/text@v0.34.0   Fixed in: golang.org/x/text@v0.39.0
  Trace: cmd/alias.go:209:70 cmd.listAliases -> cases.Caser.String -> norm.Form.Properties
```

**Fix:** `go get golang.org/x/text@v0.39.0 && go mod tidy`.

### H3 — `wut init` silently discards piped answers · High · Direct

`cmd/init.go:70-99`. `askYN` and `askChoice` each construct a **new** `bufio.Scanner` over
`os.Stdin`. `bufio.Scanner` reads ahead up to 64 KB, so the first prompt swallows every buffered
line; every later prompt hits EOF and silently returns its default (`init.go:82`, `:97`).
`printf 'y\nn\ny\n' | wut init` therefore ignores answers 2..n.

**Fix:** one package-level `*bufio.Reader` over stdin, shared by both helpers.

### H4 — Unbounded download and unbounded zip entry read · High · Direct

`internal/db/sync.go:329` `io.Copy` with no cap, and `:376` `io.ReadAll(rc)` per zip entry with no
cap. `internal/db/client.go:118` already caps page fetches at 64 KB — the archive path does not
follow the same rule. A compromised or hijacked release asset can exhaust disk (`io.Copy`) or RAM
(a zip-bomb entry).

**Fix:** `io.LimitReader` on both, with explicit `maxArchiveBytes` / `maxEntryBytes` constants.

### H5 — Sync source depends on the current working directory · High · Direct

`internal/db/sync.go:168-181`. `localSyncRoots()` returns the **relative** paths `tldr-main` and
`tldr-main/tldr-main`; `SyncAllWithOptions` (`:84-96`) silently prefers them over the official
archive whenever `./tldr-main/pages` happens to exist. `wut db sync` therefore produces different
databases depending on `cwd`, and any directory containing a planted `tldr-main/pages` tree seeds
the user's command database with attacker-controlled content — which `wut suggest` later offers
for execution.

**Fix:** require an explicit `--from-dir`, or resolve against a fixed data directory; log loudly
when a local source is used.

---

### Maintainability

| ID | Finding | Evidence | Severity |
|---|---|---|---|
| M1 | `internal/performance` is a dead-code island: 238 exported symbols, **184 unreferenced outside the package**. Only `FastMatcher`, `LRUCache`, `InvertedIndex`, `Autocomplete` are consumed. Removing `bench.go`, `http.go`, `worker.go`, `storage.go`, `init.go` (**2,138 lines**) keeps `go build ./...` green — verified in an isolated copy. | `internal/performance/*` | Medium |
| M2 | `internal/suggest` (231 lines) has **zero importers**; its logic is duplicated by `internal/smart/engine.go`. Removal verified against `go build ./...`. | package-wide | Medium |
| M3 | `internal/health` (272 lines) is instantiated once and discarded: `healthChecker := health.NewChecker(Version); healthChecker.RegisterDefaultChecks()` — the value is never used again. | `cmd/root.go:335-336` | Medium |
| M4 | `internal/metrics` (316 lines) is write-only. Counters are incremented in 4 call sites and never read, persisted, or displayed in a process that exits immediately. `StartServer` (`metrics.go:248`) exposes unauthenticated `/metrics` + `/health` and is never called. | `internal/metrics/metrics.go` | Medium |
| M5 | Duplicated Code: five near-identical goroutine blocks differing only in the source function. | `internal/smart/engine.go:131-200` | Medium |
| M6 | Degraded results are cached. On `ctx.Done()` the collector jumps to `done` with a partial set, which is then cached for 30 s and served to later calls. | `internal/smart/engine.go:225-239` | Medium |
| M7 | Config read/write asymmetry + non-atomic save: reads go through viper, writes go through `yaml.Marshal` + direct `os.WriteFile`. A crash mid-write truncates the user's config; viper-only/env keys get baked into the file. | `internal/config/config.go:156-176`, `:224-235` | Medium |
| M8 | ~15 unguarded `tx.Bucket(...)` dereferences. A DB file missing a bucket panics instead of erroring. | `internal/db/storage.go:186,275,357,376,402,436,460,488,497,521,551,572,620` | Medium |
| M9 | Dead public API on `Storage`: `GetAllPages`, `GetPageSummaries`, `GetPagesByPlatform`, `SearchLocal`, `IsPageStale` have no callers. | `internal/db/storage.go` | Medium |
| M10 | `os.Exit(1)` inside `PersistentPreRunE` skips `PersistentPostRun` → `cleanup()` and every pending `defer` (including `storage.Close()`). | `cmd/root.go:68` | Medium |
| M11 | The command name is interpolated into the TLDR URL with `fmt.Sprintf`, unescaped. | `internal/db/client.go:255`, `:260`, `:181` | Medium |
| M12 | `memoryCache map[string]*Page` is never evicted, and `GetPageAnyPlatform` linear-scans the whole map under `RLock`. | `internal/db/client.go:47`, `:346-356` | Medium |
| M13 | God Methods: `runInit` 327 lines, `runConfigUI` 292, `runStats` 263, `Model.Update` 221, `checkHistory` 171. | `cmd/init.go:104`, `cmd/config.go:145`, `cmd/stats.go:46`, `internal/db/tui.go:218` | Medium |
| M14 | `gofmt` unclean. | `internal/ui/renderer.go` (import grouping) | Low |
| M15 | Dead `initConfig()` registered via `cobra.OnInitialize` with an empty body; `run()` in `main.go` can never return non-nil. | `cmd/root.go:294-297`, `main.go:37-41` | Low |
| M16 | User-Agent advertises `github.com/anomalyco/wut`; the real repo is `thirawat27/wut` (`scripts/install.sh:13`). | `internal/db/client.go:22` | Low |

---

## 4. CI/CD assessment

### Regression found in history

`git log --diff-filter=D` shows commit `ee0e9dc` ("v0.1.0") deleted both
`.github/dependabot.yml` and `.github/workflows/ci.yml`. The deleted `ci.yml` ran
**golangci-lint, `go test -race -coverprofile`, and Codecov upload**. None of that came back.
Current CI is strictly weaker than CI at v0.1.0.

Also deleted and never replaced: `internal/config/config_test.go`, `internal/db/storage_test.go`
(commit `d476220`), `internal/performance/pool_test.go` (commit `c59110b`). Test coverage today is
3 packages out of 18.

### Gaps in the current three workflows

| Gap | Evidence |
|---|---|
| `push.yml` and `pull_request.yml` are ~95% identical (only artifact name and version expression differ) | both files, 88 lines each |
| No `permissions:` block on `push.yml` / `pull_request.yml` → default `GITHUB_TOKEN` scope | both files |
| No `concurrency:` group → superseded runs keep burning 3 runners each | all three |
| No `timeout-minutes` on any job | all three |
| Actions unpinned to digest (`@v4`, `@v5`, `softprops/action-gh-release@v2`) | all three |
| No lint (`golangci-lint`), no `gofmt` gate, no `-race`, no coverage, no `govulncheck`, no `gosec`, no CodeQL, no dependency-review | all three |
| Release publishes **no checksums, no SBOM, no signature/attestation** — while `scripts/install.sh:61-66` downloads and installs the binary with zero verification | `release.yml`, `scripts/install.sh` |
| `CHANGELOG.md` does not exist, so `release_notes.sh:52-61` emits generic boilerplate on every release | `.github/scripts/release_notes.sh` |
| Dead condition referencing `github.event.head_commit`, which is never set on `pull_request` events | `pull_request.yml:8` |
| `.goreleaser.yaml` (8.6 KB, with `checksum`, `sboms`, `signs`, `dockers` already configured) is invoked by no workflow — the hand-written release duplicates it, worse | `.goreleaser.yaml:89,179,233,305` |
| `.goreleaser.dockerfile` was deleted in `a4181ba` while `.goreleaser.yaml:238,252` still references `dockerfile: Dockerfile` — verify the docker step still resolves | `.goreleaser.yaml` · **Verify first: Yes** |

---

## 5. Open questions

1. **C1 direction** — is re-executing the user's command an intentional product decision, or an
   unnoticed side effect of harvesting stderr? The fix shape depends on the answer.
2. **`internal/performance`** — is it a deliberate future toolbox, or accumulated scaffolding?
   Removal is verified-safe but is a judgement call about intent.
3. **Release path** — should `.goreleaser.yaml` become the single release mechanism (it already
   has checksums / SBOM / signing), or should the hand-written workflow stay and gain them?

## 6. Risks

- Any change to `internal/corrector` affects the `oops` / `again` UX already installed in users'
  shell rc files. A behaviour change must be called out in release notes.
- `internal/shell/installer.go` writes to user rc files; changes there need manual verification per
  shell — the existing `installer_test.go` covers backup/restore only.
- Deleting `internal/performance` files is verified against `go build`, but Go build checks cannot
  see reflection or `go:linkname`. A repo-wide grep found no dynamic references.

## 7. Decisions taken

| # | Decision | Outcome |
|---|---|---|
| D1 | How to fix C1 | **Approved:** remove execution from the correction pipeline; output-dependent rules read the failed command's output from the shell |
| D2 | Delete verified-dead code | **Approved** — see §8 |
| D3 | `internal/health` and `internal/metrics` | **Deleted.** `health` was constructed and discarded; `metrics` recorded counters nothing ever read, and carried an unstarted unauthenticated HTTP server |
| D4 | Consolidate release onto GoReleaser | **Approved** |
| D5 | Restore `dependabot.yml` + a quality-gate workflow | **Approved** |

---

## 8. Resolution

Every item below was applied and verified against `go build ./...`,
`go vet ./...`, `gofmt -l`, `CGO_ENABLED=1 go test -race ./...`, and
`goreleaser check`.

### Critical

**C1 — `wut fix` / `oops` re-executed the user's command.** Root-caused to the
pipeline conflating *analyse* with *execute*, not to a gap in the deny-list.

- `internal/corrector/evaluator.go` — `evaluateErrorRules` is now a pure
  function over `(command, output)`. The `os/exec` import is gone from the
  package.
- Rules carry `NeedsOutput`. Rules that decide from the command string alone
  (`cd..`, `apt search`, bare `go run`) still work with no output; rules that
  need output stay dormant until a caller supplies it.
- `Corrector.CorrectWithOutput(command, output)` is the new entry point;
  `Correct(command)` delegates to it with empty output, so existing callers are
  unchanged.
- `cmd/fix.go` reads the output from `--stderr <file>`, `--stderr -` (stdin), or
  `$WUT_LAST_STDERR`, capped at 256 KiB.
- The bash/zsh and fish hooks forward piped output
  (`some-command 2>&1 | oops`).
- This also closes a documentation/behaviour contradiction: `README.md:20` and
  `README.md:45` already promised "WUT never executes commands", which the code
  violated.
- Regression tests: `internal/corrector/evaluator_test.go` writes a sentinel
  file from a probe command and asserts it is never created, and covers the
  side-effecting commands the old deny-list let through.
- Verified end to end against a built binary: `wut fix "cmd /c echo pwned >
  sentinel.txt"` creates nothing, while
  `wut fix --stderr err.txt "git push"` still returns
  `git push --set-upstream origin feat` at 100% confidence.

**C2 — `wut db sync` could not finish on a normal connection.**

- `internal/db/client.go` — added `NewArchiveHTTPClient()`, which leaves
  `http.Client.Timeout` unset and instead sets `ResponseHeaderTimeout` on the
  transport. The named constant `pageRequestTimeout` documents why the page
  client keeps its 5-second whole-exchange budget.
- `internal/db/sync.go` — `SyncManager` holds its own `archiveClient`;
  `SyncFromZip` wraps the call in a 10-minute context.

### High

| ID | Fix |
|---|---|
| H1 | `internal/logger/logger.go` — `sync.Mutex` guards `Write`/`rotate`/`Close`. A second defect surfaced while fixing it: `open()` only refreshes `size` when the file exists, so after a rotation the counter kept its old value and every following write rotated again, discarding all backups. `rotate` now resets it. Covered by `logger_test.go`, which passes under `-race`. |
| H2 | `golang.org/x/text` v0.34.0 → v0.39.0, closing GO-2026-5970. `govulncheck` is now clean. |
| H3 | `cmd/init.go` — one package-level `bufio.Reader` shared by all prompts, so piped answers are no longer swallowed by the first prompt's read-ahead. |
| H4 | `internal/db/sync.go` — `maxArchiveBytes` (256 MiB) on the download and `maxArchiveEntryBytes` (1 MiB) per zip entry, matching the 64 KiB cap the page path already had. |
| H5 | `internal/db/sync.go` — `localSyncRoots()` resolves against `config.GetDataDir()` instead of the process working directory. |

### Medium

| ID | Fix |
|---|---|
| M1, M2 | Deleted `internal/performance/{bench,http,worker,storage,init}.go` and `internal/suggest`. |
| M3, M4 | Deleted `internal/health` and `internal/metrics` plus their four call sites. |
| M5 | `internal/smart/engine.go` — the five copy-pasted goroutine blocks collapse into one loop over a `suggestionSource` slice. |
| M6 | Partial results from a cancelled context are returned via `finishPartial` and no longer cached. |
| M7 | `internal/config/config.go` — `writeFileAtomic` (temp file + `Sync` + rename, `0600`), with Windows's rename-over-existing handled. Covered by `config_test.go`. |
| M8 | `requireBucket` replaces 28 unguarded `tx.Bucket(...)` dereferences across `storage.go`, `history.go`, and `bookmark.go`. |
| M9 | Removed `GetAllPages`, `GetPageSummaries`, `GetPagesByPlatform`, `SearchLocal`, `IsPageStale`. |
| M10 | `os.Exit` removed from `PersistentPreRunE`; `cmd.Execute()` returns an exit code that `main` applies, so cleanup and every `defer` still run. `initConfig` (empty, registered via `cobra.OnInitialize`) and the unused `GoVersion` var are gone. |
| M11 | `pageURL()` escapes each path segment with `url.PathEscape`. |
| M12 | The page cache is bounded at 512 entries and indexed by command name; `GetPageAnyPlatform` no longer scans the whole map under a lock. |
| M13 | `runInit` (327 lines) is decomposed into an `initWizard` with one method per step. No function in `cmd/init.go` now exceeds 60 lines. The Ctrl+C watcher no longer leaks its goroutine or its `signal.Notify` registration. |
| M14, M16 | `gofmt` clean; the User-Agent points at the real repository. |

### Not addressed

- `runConfigUI` (292 lines), `runStats` (263), `db/tui.go Update` (221) are still
  long. They are self-contained view code, so the change/risk ratio is worse
  than for `runInit`, which sits on the first-run path.
- The global viper singleton in `internal/config` remains. Replacing it with an
  injected config would touch all 17 consumers and is a separate change.
- `Makefile` and `build.go` still duplicate build logic that `.goreleaser.yaml`
  now owns. They work; consolidating them is optional cleanup.

---

## 9. CI/CD rebuild

### New

- **`.github/workflows/ci.yml`** replaces `push.yml` + `pull_request.yml`.
  Jobs: `lint` (gofmt, `go mod tidy` verification, `go vet`, `golangci-lint`,
  `goreleaser check`), `test` (`-race -shuffle=on` with coverage on
  Linux/macOS/Windows), `security` (`govulncheck`, `gosec` → SARIF),
  `dependency-review` (PRs), `build` (matrix + binary smoke test), and a
  `CI passed` aggregate job to use as the single required status check.
  Least-privilege `permissions`, a `concurrency` group, and `timeout-minutes`
  throughout.
- **`.github/workflows/codeql.yml`** — `security-extended` queries on push, PR,
  and weekly.
- **`.github/dependabot.yml`** — restored from `ee0e9dc`, extended to GitHub
  Actions and the Docker base image.
- **`.golangci.yml`** — linters chosen for the defect classes this audit found:
  `bodyclose`, `noctx`, `contextcheck`, `errorlint`, `nilerr`, `gosec`,
  `unparam`, `wastedassign`.
- **`CHANGELOG.md`** — `release_notes.sh` had been reading a file that never
  existed, so every release shipped generic boilerplate.

### Release

`.github/workflows/release.yml` now runs GoReleaser after a `verify` gate
(gofmt, vet, `-race` tests, `govulncheck`). It produces `checksums.txt`, a
CycloneDX SBOM, keyless cosign signatures, and SLSA build provenance via
`actions/attest-build-provenance`. Release notes come from the curated
`CHANGELOG.md` section. Optional publishers are skipped when their credentials
are absent, so a missing `CHOCOLATEY_API_KEY` no longer fails the release.

`.goreleaser.yaml` did not pass `goreleaser check`. Fixed:

| Problem | Consequence |
|---|---|
| `chocolateys[].source` is not a valid field | `goreleaser check` failed outright |
| `-X main.BuildHost={{.Env.HOSTNAME}}` | `main.BuildHost` does not exist, and the template aborts the run when `HOSTNAME` is unset — which it is on GitHub runners |
| `archives[].files: completions/*` | the directory does not exist, so archiving failed |
| GPG `signs` block | required a `GPG_FINGERPRINT` secret; replaced with keyless cosign |
| `archives.format_overrides.format` | deprecated → `formats: [zip]` |
| `dockers` + `docker_manifests` | deprecated → a single `dockers_v2` entry |

`goreleaser check` now reports `1 configuration file(s) validated` with no
deprecations, and CI runs it on every PR.

### Install scripts

Both installers downloaded and ran release binaries with **no verification**,
and both looked for asset names no release has ever published:

- `scripts/install.sh` asked for `wut_v1.0.2_Linux_x86_64.tar.gz`; the old
  workflow published `wut_v1.0.2_lnx.tar.gz` and GoReleaser publishes
  `wut_1.0.2_Linux_x86_64.tar.gz` (no `v`).
- `scripts/install.ps1` asked for `wut-setup.exe`, which is built locally by
  Inno Setup and has never been a release asset.

Both now resolve the archive by trying both version spellings and then a
pattern match, report the release's actual asset names on a miss, verify the
SHA-256 against `checksums.txt` (escape hatch: `WUT_SKIP_CHECKSUM=1`), and
refuse to install on a mismatch. `install.ps1` additionally extracts the
portable archive to `%LOCALAPPDATA%\Programs\wut`, adds it to the user PATH, and
gained a matching uninstall path.

---

## 10. Validation gate

1. **Claim traceability** — every finding cites a file and line, or a tool
   output reproduced verbatim (`govulncheck`, `goreleaser check`, `go test`).
   Dead-code claims were verified by deleting the files in an isolated copy of
   the tree and rebuilding, not by grep alone.
2. **Scope alignment** — the audit stayed within the requested scope. Two
   additions are declared: the install-script asset-name mismatch (found while
   consolidating the release path, and a blocker for it), and the log-rotation
   size-reset bug (found while fixing the H1 race in the same function).
3. **Handoff readiness** — §8 "Not addressed" lists what remains, with the
   reason. Open questions from §5 are resolved in §7.

### Residual risks

- `internal/shell/installer.go` writes to user rc files. The `oops` change was
  verified by reading the generated snippets; it has not been exercised against
  a live install of each of the nine supported shells. **Verify first: Yes.**
- Users who already have the shell hooks installed keep the old snippet until
  they re-run `wut install`. The old snippet still calls `wut fix "$cmd"`, which
  is safe with the new binary — it simply cannot use the output-aware rules.
- The release workflow has not been executed. `goreleaser check` validates the
  config but not a live publish; the first tagged run should be done with
  `workflow_dispatch` + `dry_run: true`.

---

## 11. Phase 2 — usability, and the fact-driven correction model

The Phase 1 fix removed command execution from the correction pipeline, which
made `wut fix` safe but left it weaker: rules that needed the failed command's
output only fired when the user manually piped it in. Phase 2 closes that gap
without reintroducing the hazard, and brings the interaction up to the standard
set by `thefuck`.

### 11.1 The design difference

`thefuck` re-runs the failed command to read its stderr. That is where its
correction quality comes from, and also why running it after `git push`,
`docker system prune`, or a deploy repeats the action.

WUT answers the same questions from sources it controls
(`internal/corrector/facts.go`):

| Question a rule needs answered | thefuck | WUT |
|---|---|---|
| Does this branch have an upstream? | re-run `git push` | `git rev-parse --abbrev-ref @{u}` |
| Is `biuld` a real npm script? | re-run `npm run biuld` | read `package.json` |
| Is `instal` a real make target? | re-run `make instal` | read the `Makefile` |
| Does directory `intenral` exist? | re-run `cd intenral` | read the directory |
| Is `build` a directory or a file? | re-run `rm build` | `os.Stat` |

Every probe is read-only, and the allowlist (`allowedProbes`) is compared
**argv-for-argv**, so a longer argv cannot smuggle arguments in behind an
allowed prefix. Nothing derived from the user's command reaches a process.
`TestProbeAllowlistRejectsAnythingElse` asserts this against `git push`,
`git reset --hard`, `rm -rf .`, `sh -c`, `docker system prune -af`, and
`npm publish`.

Facts are lazy and memoised: a rule that never asks about git costs nothing in
a non-git directory.

### 11.2 Rules added

Fact-driven, no output required: `git_push_no_upstream`,
`git_checkout_unknown_branch`, `npm_unknown_script`, `make_unknown_target`,
`cd_unknown_directory`, `missing_recursive_flag`, `local_script_needs_prefix`.

Verified against a live fixture and a real git repository:

```
npm run biuld  → npm run build          (read package.json)
make instal    → make install           (read Makefile)
cd intenral    → cd internal            (read directory)
deploy.sh      → ./deploy.sh            (os.Stat)
git push       → git push --set-upstream origin feature/login
```

The last one is the important case: it is derived from `git rev-parse` and
`git remote`, in a repository where `git push` was never run.

### 11.3 Interaction

`Correction` now carries `Alternatives` — every candidate, best first — instead
of discarding all but one. `cmd/fix_picker.go` renders them as a picker with
↑/↓, Enter, and Esc.

The picker draws on the **controlling terminal** (`/dev/tty`, `CONIN$`), never
on stdout, because stdout carries the accepted command back to the shell
function that runs it. That separation is what lets `oops` execute the result in
the user's own shell, so `cd` and `export` behave as if they had typed it.

With no controlling terminal — a pipe, CI, an editor task runner — the picker
declines rather than emit a command nobody saw. `--no-confirm` is available for
callers that genuinely want the best candidate unattended.

`IsDangerous` corrections are never emitted in `--shell` mode at all, whatever
the user selects.

Two related fixes surfaced here:

- The PowerShell `oops` read `Get-History -Count 2` at index `0`. That list is
  oldest-first, so it targeted the command *before* the one that failed.
- A returned runtime error made Cobra print the full flag list. `SilenceUsage`
  is now set in `PersistentPreRunE`, which runs after flag parsing — so genuine
  usage errors still print usage, and runtime failures do not.

### 11.4 Toolchain and dependencies

- Go `1.26.0` → `1.27.0` (`go.mod`). CI reads `go-version-file: go.mod`, so the
  workflows follow automatically.
- All dependencies updated within their current major version. Notable:
  `bbolt` 1.4.3 → 1.5.0, `charmbracelet/log` 0.4.2 → 1.0.0, `huh` 0.8.0 → 1.0.0,
  `x/text` 0.39.0 → 0.41.0, `x/sys` 0.41.0 → 0.47.0.
- `go mod tidy` dropped three dependencies that nothing imported any more after
  the Phase 1 dead-code removal: `github.com/agnivade/levenshtein`,
  `github.com/panjf2000/ants/v2`, `golang.org/x/sync`. This completes the
  "leftover dependency" item from the original scope.
- `govulncheck ./...` → **No vulnerabilities found.**

**Not upgraded — decision required.** The Charm TUI stack has a v2 line
available: `bubbletea` v1.3.10 → v2.0.9, `lipgloss` v1.1.0 → v2.0.6,
`bubbles` v1.0.0 → v2.2.1. These are breaking API changes (message types,
renderer and colour model) and would touch every TUI file — `cmd/history.go`,
`cmd/config.go`, `cmd/suggestions_view.go`, `cmd/fix_picker.go`,
`internal/db/tui.go`, `internal/ui/*` — roughly 2,000 lines that currently have
no test coverage. It is a migration, not an update, and is left as a separate
piece of work.

### 11.5 Verification

`go build ./...`, `go vet ./...`, `gofmt -l`, `CGO_ENABLED=1 go test -race ./...`,
`govulncheck ./...`, and `goreleaser check` all pass. Test coverage grew from 3
packages to 5, with 30 tests in `internal/corrector` alone.

### 11.6 Residual risks

- The rewritten `oops` helpers for bash/zsh, fish, and PowerShell were reviewed
  as generated text and exercised through the Go side (`--shell`,
  `--no-confirm`, and the no-TTY path) against a real binary. They have not been
  sourced into a live session of each of the nine supported shells.
  **Verify first: Yes.**
- `oops` now runs the accepted command. That is an explicit, per-invocation
  confirmation of a command displayed first, and dangerous commands are refused
  outright — but it is a real behaviour change from Phase 1, where `oops` only
  printed. Users with the old snippet in their rc file keep the old
  print-only behaviour until they re-run `wut install`.
- The `local_script_needs_prefix` rule fires on any bare filename that exists in
  the working directory. In a directory containing a file named like a common
  command, it will suggest `./name`. It is the last fact-driven rule in the
  ordering, so a real correction wins first.
