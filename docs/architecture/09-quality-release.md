# 09 — Quality, Testing & Release

> Status: **Implemented**.

## 1. Where the prototype stands · `Direct`

| | The prototype |
|---|---|
| Packages with tests | 5 of 18 (audit §11.5) |
| Build paths | 4 — `Makefile`, `build.go`, `.goreleaser.yaml`, GitHub workflows (audit §2) |
| Regression in history | Commit `ee0e9dc` deleted the CI that ran golangci-lint, `-race`, and coverage (audit §4) |
| Deleted tests never replaced | `internal/config/config_test.go`, `internal/db/storage_test.go`, `internal/performance/pool_test.go` (audit §4) |

The CI/CD rebuild in audit §9 fixed the pipeline. WUT keeps that pipeline and
adds what a rewrite makes possible: architecture that is actually testable.

## 2. Test strategy by layer

| Layer | Kind | Target | Why it is possible now |
|---|---|---|---|
| `internal/core/**` | Pure unit + table-driven | **80%** | No I/O. Every input is a value. |
| `internal/core/correct` | **Golden files** — `testdata/<rule>/{facts.json,input,want}` | Every rule has one | Rules are data ([03](03-domain-model.md) §5) |
| `internal/core/risk` | Table over the policy file | Every rule id covered, both match and non-match | Policy is data |
| `internal/app/**` | Use case tests against **fake ports** | **70%** | Ports exist ([02](02-architecture-overview.md) §4) |
| `internal/adapter/**` | Integration, real filesystem in a temp dir | 50% | `WUT_*_DIR` env gives full isolation |
| `internal/adapter/shell` | Generate the rc block, assert the diff; **plus a real-shell matrix** (§4) | Every shell | |
| `internal/cli` | Golden files over `--output json` | Every command | The output contract ([08](08-cli-ux.md) §4) |
| End to end | Built binary, scripted shell session in a container | The 10 flows in §3 | |

Overall gate: **45%**, enforced in CI. It is a floor that fails the build, not
a target — the number to look at is the per-layer column above, because 45%
across a codebase whose CLI wiring is a third of it says almost nothing.

### Where it actually stands

| Package | Coverage |
|---|---|
| `pkg/wutjson` | 100% |
| `core/candidate` · `core/event` · `core/facts` · `core/cmdline` | 91–93% |
| `core/risk` · `adapter/model/embed` | 85–89% |
| `core/correct` · `adapter/store/userdata` | 83–89% |
| `adapter/configstore` · `core/config` | 77–79% |
| `adapter/store/events` · `adapter/store/index` | 73–74% |
| `adapter/facts` · `adapter/shell` | 65–66% |
| `internal/app` | 58% |
| `core/knowledge` | 50% |
| `adapter/knowledge/tldr` | 27% |
| `internal/daemon` | 22% |
| `internal/cli` | 0% |
| **Overall** | **47.5%** |

The two honest gaps: `internal/cli` has no tests at all — it is command wiring
over use cases that are tested, but "no tests at all" is not a defensible
place for a package to stay — and `internal/daemon` is tested at the protocol
and authorisation layer only, with the socket lifecycle left to the live
`wut daemon` path.

## 3. Invariant tests — the things that must never regress

Each of these is a named test that fails the build. They encode the findings
from the prototype audit so that a rewrite cannot reintroduce them.

| # | Invariant | How it is enforced | Where |
|---|---|---|---|
| I1 | WUT never executes the user's command | A sentinel command that would leave a file behind is fed to `fix`, `explain`, `ask`, `risk check`, and `fix --shell`. The file must never exist. Descendant of audit finding **C1**. | `internal/e2e` |
| I2 | Only allowlisted probes ever spawn | An AST check that `os/exec` appears in no package but the fact prober and the model supervisor, plus an argv-exact test of the allowlist against smuggled arguments, reordered arguments, and unrelated programs. | `internal/arch`, `internal/adapter/facts` |
| I3 | `--shell` never emits a risky command | Six commands the policy rates Destructive or above are run through `fix --shell --yes`; stdout must stay empty and the exit code non-zero. | `internal/e2e` |
| I4 | `internal/core` is pure | Import-graph check: no `os`, `net`, `os/exec`, `io/fs`, `path/filepath`, no adapter imports. | `internal/arch` |
| I5 | No package-level mutable state | AST check for package-level vars outside `platform` and the composition root. Kills the prototype's viper singleton class of problem at the root. | `internal/arch` |
| I6 | Writes stay inside WUT's directories | Five commands run with all three `WUT_*_DIR` under a temporary home; nothing else may appear in it. | `internal/e2e` |
| I7 | No network outside `db sync` | **Not enforced.** No egress-blocked run exists. The only HTTP client is constructed in `adapter/knowledge/tldr` and reached only through `port.Syncer`, which is a weaker guarantee than a test. | — |
| I8 | Generation cannot introduce a token | 30 adversarial generations — invented flags, invented programs, prompt injections, a poisoned page — must all be discarded, with a false-discard rate under 2% on 31 accurate ones. | `internal/eval` (E3) |
| I9 | Piped stdin is fully consumed | Three piped answers are fed to `shell install`. Descendant of audit finding **H3**. | `internal/e2e` |
| I10 | Atomic writes | No temporary file may survive a save, and the last write must be the one read back. Crash injection itself is **not** implemented. Descendant of audit finding **M7**. | `internal/adapter/configstore` |

I7 and the crash-injection half of I10 are the two that are written down and
not enforced. They are listed rather than quietly dropped: an invariant nobody
tests is a claim, and the point of this table is to keep the difference
visible.

## 4. The shell matrix — the gap the prototype never closed

Audit §11.6 records the residual risk plainly: the rewritten hooks "have not
been sourced into a live session of each of the nine supported shells.
**Verify first: Yes.**" That risk does not carry into WUT.

The matrix obligation covers the **Full class** only
([04](04-shell-protocol.md) §5, owner decision D6). Manual-class shells get a
single assertion that their documented degraded behaviour holds — they are not
asked to prove capture they do not have.

A container matrix runs a scripted session per Full-class shell:

```
for shell in zsh fish nu xonsh elvish; do
  1. wut shell install --yes
  2. open a fresh session
  3. run a command that succeeds  -> assert a T0 record exists, exit 0
  4. run a command that fails     -> assert a T0 record exists, exit != 0
  5. run `wut` non-interactively -> assert the expected candidate on stdout
  6. run a command with a typo'd program -> assert T0.5 fired where supported
  7. assert `command wut` still reaches the binary, not the function
  8. wut shell uninstall          -> assert the rc file is byte-identical to the backup
done
```

`bash` runs the same implementation smoke test in a separate **Full — later**
section. A green smoke test does not promote it to Full: coexistence with an
existing `DEBUG` trap, `PROMPT_COMMAND`, bash-preexec, and Starship remains the
promotion gate.

PowerShell 7 and Windows PowerShell run on the Windows runner. Manual-class
shells (`sh`, `dash`, `ksh`, `cmd.exe`) assert only steps 1, 7, and 8 plus
`wut fix "<cmd>"`, which is the whole of what they promise.

This matrix is a **release gate**, not a nice-to-have.

## 5. Performance gates

Benchmarks run nightly on a fixed runner; a regression over 20% fails.

| Benchmark | Budget |
|---|---|
| `wut fix` cold, no daemon, no model | p50 under 30 ms, p95 under 60 ms |
| `wut <question>` cold, Tier 1 | p50 under 120 ms |
| `wut <question>` warm, via daemon | p50 under 25 ms |
| Shell hook overhead per command | under 1 ms, **zero forks** — asserted by counting `fork` in an strace-equivalent |
| Binary size | under 25 MB, no CGO |
| Install footprint, Tier 1 only | under 120 MB |
| Daemon RSS, warm | under 90 MB |

## 6. CI pipeline

Carried forward from the audit §9 rebuild, extended:

| Job | Contents |
|---|---|
| `lint` | `gofmt`, `go mod tidy` verification, `go vet`, `golangci-lint`, **import-boundary check (I4)**, **package-state check (I5)**, `goreleaser check` |
| `test` | `-race -shuffle=on` with coverage on Linux, macOS, Windows; coverage gate |
| `invariants` | I1, I2, I3, I8, I9, I10 as a separate, always-visible job |
| `shells` | The §4 matrix (Linux container shells + Windows PowerShell) |
| `evals` | E1–E3 from [06](06-intelligence-slm.md) §6, on PRs touching `core` or `adapter/model` |
| `security` | `govulncheck`, `gosec` to SARIF, CodeQL `security-extended`, dependency review |
| `build` | Cross-compile matrix plus a binary smoke test |
| `CI passed` | Aggregate job used as the single required status check |

Least-privilege `permissions`, a `concurrency` group, `timeout-minutes`, and
digest-pinned actions throughout — all gaps the audit found in the prototype's workflows
(audit §4).

## 7. Release & supply chain

**One build path: GoReleaser.** `Makefile` and `build.go` are deleted, closing
the item the audit left open ("they work; consolidating them is optional
cleanup" — a rewrite is the moment it stops being optional).

Every release publishes:

- Archives for the platform matrix, `checksums.txt`
- CycloneDX SBOM
- Keyless cosign signatures
- SLSA build provenance via `actions/attest-build-provenance`
- Release notes from a curated `CHANGELOG.md`

Install scripts verify SHA-256 against `checksums.txt` and refuse on mismatch
(the prototype fix; keep the behaviour, keep the `WUT_SKIP_CHECKSUM=1` escape hatch).

### Model artifacts get the same treatment

`wut model install` is a download, so it is held to the release standard:

1. Prints the exact URL, size, licence, and SHA-256 **before** downloading.
2. Requires explicit confirmation. Never runs implicitly.
3. Verifies the digest; refuses on mismatch, deletes the partial file.
4. Verifies the signature where the publisher provides one.
5. Records provenance in `wut model list`, so a user can always see where a
   model on their disk came from.

The same applies to any Tier 2 **runtime** sidecar
([06](06-intelligence-slm.md) R2) — pinned version, digest in the WUT binary.

## 8. Definition of done, per milestone

A milestone is `Done` only when all of these hold. No exceptions, no
"we'll add tests next milestone" — that is exactly how the prototype reached 5-of-18.

- [ ] Feature works on Linux, macOS, and Windows
- [ ] Coverage gate met for the layers touched
- [ ] All applicable §3 invariants pass
- [ ] Performance budgets in §5 met, or a budget change is recorded as an ADR
- [ ] `wut doctor` reports on the new subsystem
- [ ] `--output json` supported for any new command
- [ ] Architecture docs in this directory updated
- [ ] `CHANGELOG.md` entry written
