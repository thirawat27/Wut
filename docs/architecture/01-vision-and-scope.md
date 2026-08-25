# 01 — Vision & Scope

> Status: **Implemented**. Evidence from the prototype is labelled `Direct` with citations.

## 1. The concept that survives

WUT exists for one moment:

> You typed something. It failed, or you don't know the incantation.
> You say **"wut?"** and the terminal answers.

That is the whole product. Everything else in the prototype was accreted around it.

The concept has three faces, and WUT keeps all three:

1. **Repair** — "that failed, what did I mean?" (`wut`)
2. **Recall** — "how do I do X with this tool?" (`wut <question>`)
3. **Comprehend** — "what does this command actually do to my machine?" (`wut explain`)

## 2. Why rebuild instead of refactor

The prototype codebase is ~25.2k LOC and works. It is being replaced because four
structural properties cannot be fixed incrementally without touching nearly
every file anyway.

### 2.1 There is no seam · `Direct`

- `cmd/` constructs `db.Storage` directly in 12 files — no repository or service
  boundary exists (audit §2).
- `internal/config` is a **global viper singleton** read by 17 files
  (`internal/config/config.go:289-331`, audit §2).

Consequence: nothing below `cmd/` can be tested without a real database and a
real config file on disk. Test coverage is **5 of 18 packages**
(audit §11.5) — that is a symptom, not an accident.

### 2.2 `cmd/` is the largest package and does everything · `Direct`

Measured at commit `023ca1b`:

| Package | LOC | Role it actually plays |
|---|---:|---|
| `cmd` | 7,147 | flag parsing + orchestration + business logic + TUI |
| `internal/db` | 4,377 | storage + HTTP client + sync + a Bubble Tea TUI (`db/tui.go`) |
| `internal/corrector` | 3,085 | correction rules + fact probes + scoring |
| `internal/performance` | 2,374 | utilities, largely unconsumed (audit M1) |
| `internal/shell` | 1,746 | rc-file writing + hook text for 9 shells |
| `internal/concurrency` | 1,009 | a worker pool and pipeline for a process that exits in under a second |
| everything else | 5,485 | |

`internal/db` containing a terminal UI (`internal/db/tui.go`, 1 of 6 files in the
package) is the clearest boundary violation: the persistence package renders
views.

### 2.3 The command surface has no mental model · `Direct`

37 `Use:` declarations across `cmd/` (grep at `cmd/*.go`). Nine of them are
**a second, duplicate command tree** in `cmd/shortcuts.go` — `t`, `s`, `h`,
`x`, `a`, `c`, `d`, `f`, `?` re-declare commands that already exist as
`terminal`, `suggest`, `history`, `explain`, `alias`, `config`, `db`, `fix`,
`smart`, with their own flag wiring.

There are at least **five different ways to ask for a suggestion**: `suggest`,
`smart`, `terminal`, `s`, `?`. A user cannot form a model of the tool.

### 2.4 WUT is blind to what happened · `Direct`

The single most important input — *what your shell just did* — is not
available to WUT. The prototype's history:

- Originally it **re-executed your command** to read stderr
  (`internal/corrector/evaluator.go:170-172`, pre-fix). Audit finding C1,
  Critical.
- The fix removed execution and made output an explicit parameter:
  `--stderr <file>`, `--stderr -`, or `$WUT_LAST_STDERR` (audit §8/C1).
- Phase 2 added a read-only **fact probe** engine to recover some of the lost
  signal without executing anything (`internal/corrector/facts.go`,
  `allowedProbes` at `:78`).

That arc is correct in direction but stops short. The shell knows the exit
code, the working directory, the duration, and the stderr. WUT asks the user to
hand it over manually. **WUT makes the shell tell WUT, automatically and
safely.** This is the largest single DX gain available and it drives the
architecture (see [04-shell-protocol.md](04-shell-protocol.md)).

## 3. What is kept, what is discarded

Per owner decision **D2 (full clean break)**, no code carries over.

### Kept

| Kept | Form in WUT |
|---|---|
| The name `wut` | Unchanged |
| ~~The name `oops`~~ | **Removed.** The repair interaction is `wut` itself — see [ADR-0013](10-decision-records.md). |
| The three-face concept (repair / recall / comprehend) | Becomes the top-level command structure |
| "WUT never executes your command" as a hard invariant | Promoted from a README promise to an enforced architectural rule ([ADR-0004](10-decision-records.md)) |
| tldr as the knowledge base | Owner decision D4 |
| Read-only, allowlisted fact probing | Retained as **runtime context**, redesigned as `internal/adapter/facts`. Confirmed by owner decision D5. |
| Cross-platform, 9 shells, single binary | Commitment kept, but honestly graded: three declared **support classes** instead of one blanket claim (owner decision D6, [04](04-shell-protocol.md) §5) |

### Discarded

| Discarded | Reason |
|---|---|
| All prototype Go source | D2 |
| The prototype bbolt file layout and its buckets | D2; new schema, no migration |
| The prototype config file and its 30+ viper keys | D2; typed config, no viper ([ADR-0008](10-decision-records.md)) |
| Installed prototype shell hooks | D2; `wut shell install` writes a new managed block. Old blocks are left alone and simply stop resolving. |
| `internal/performance`, `internal/concurrency`, `internal/commandsearch`, `internal/historyml`, `internal/smart` | Replaced by one candidate pipeline ([03](03-domain-model.md)) |
| The duplicate shortcut command tree (`cmd/shortcuts.go`) | Aliases belong on the command, not in a parallel tree |
| `suggest` vs `smart` vs `terminal` as separate concepts | Collapsed into `wut <question>` and `wut ui` |
| The goml / edlib / fuzzysearch / sahilm-fuzzy stack (4 overlapping matchers) | One matcher plus one embedding model |
| Viper, and the indirect dependencies it pulls | [ADR-0008](10-decision-records.md) |

### Deferred out of WUT by owner decision D4

- `man` page and `--help` introspection as a knowledge source.
- User/team recipe files (`.wut/recipes.yaml`).

Both remain architecturally *possible* — the `KnowledgeSource` port
([02](02-architecture-overview.md) §4) is designed so either can be added later
without touching the pipeline — but neither is implemented in WUT.

## 4. What "much better DX/UX" means, concretely

Vague goals produce vague systems. These are the ones WUT is designed against.
All targets are **proposed budgets**, not measurements.

### 4.1 UX goals (the person at the terminal)

| # | Goal | How it is achieved | Measurable target |
|---|---|---|---|
| U1 | One thing to remember | Bare `wut` is context-aware; 12 subcommands instead of 37 | A user can name what `wut` does after reading 3 lines |
| U2 | Never re-type the error | Shell protocol delivers exit code, cwd, duration, and (opt-in) stderr | 0 manual `--stderr` usage on the default path |
| U3 | Always show *why* | Every candidate carries `Why` provenance and is rendered | 100% of displayed candidates have at least one `Why` entry |
| U4 | Natural language works | Embedding retrieval over the tldr index (Tier 1 model) | Top-3 hit rate at or above 80% on a 200-question benchmark set |
| U5 | Feels instant | Cold-start budget; daemon for the model path | `wut` (repair path) p50 under 30 ms cold; `wut <question>` p50 under 120 ms cold, under 25 ms warm |
| U6 | Safe by construction | Risk policy engine; `--shell` refuses risky output | 0 executions of the user's failed command, enforced by test |
| U7 | Honest when it doesn't know | Confidence is surfaced; low confidence says so instead of guessing | No candidate below threshold is presented as an answer |
| U8 | Works offline, forever | tldr index is local; models are local | Full feature parity with the network unplugged, except `db sync` |

### 4.2 DX goals (the person changing the code)

| # | Goal | How it is achieved | Measurable target |
|---|---|---|---|
| D1 | Testable without I/O | Ports and injected dependencies; `internal/core` is pure | `internal/core/**` imports nothing from `os`, `net`, or `internal/adapter` — enforced by lint |
| D2 | Add a correction rule without writing Go | Rules are declarative data with a golden-file test harness | A new rule = 1 YAML entry + 1 fixture |
| D3 | Know where code goes | One-way dependency rule, checked in CI | Import-boundary check passes |
| D4 | Real coverage | A coverage gate from the first commit | 75% or higher on `internal/core`, 60% or higher overall |
| D5 | Scriptable and composable | Versioned JSON output on every command | `wut ask --output json` conforms to a published schema |
| D6 | Diagnosable in the field | `wut doctor` explains its own state | Doctor detects all six known-bad install states |
| D7 | One build path | GoReleaser only | `Makefile` and `build.go` deleted; audit §8 "Not addressed" closed |

## 5. Non-goals

Stated so they do not creep in:

- **Not a shell replacement.** WUT does not own the prompt, does not replace
  `command_not_found`, does not intercept execution.
- **Not an agent.** It proposes; the shell executes what the human accepted.
  No autonomous multi-step execution.
- **Not a cloud service.** No telemetry, no accounts, no sync of user data.
  Network is used for exactly two things: downloading the tldr archive, and —
  on explicit request — downloading a model file.
- **Not a general chatbot.** The SLM is a component with three bounded jobs
  ([06](06-intelligence-slm.md) §2), not a conversational surface.

## 6. Assumptions

| # | Assumption | Impact if wrong |
|---|---|---|
| ~~**A1**~~ | **Resolved — no longer an assumption.** Owner decision **D5** confirms that D4 ("tldr only") scopes *knowledge sources*, not *runtime context*. The read-only project fact probe (git upstream, `package.json` scripts, `Makefile` targets, directory listing) is retained. Rationale: tldr answers "what does this command do"; facts answer "what is true in this directory right now", and no amount of tldr syncing can answer the second. It is also what let the prototype remove command re-execution (audit C1) without losing correction quality. | — |
| **A2** | "Runs on every machine" means the *default* experience must work on any machine WUT builds for, including low-end and CPU-only. An opt-in heavier tier is acceptable. | If literally every feature must run everywhere, Tier 2 generation ([06](06-intelligence-slm.md)) is out and every explanation falls back to templates permanently. That is in fact the default: Tier 2 is off unless the user installs a model. |
| **A3** | The supported platform matrix matches the prototype: Windows, macOS, Linux, FreeBSD/OpenBSD/NetBSD, on amd64 and arm64. | A wider matrix (386, riscv64) tightens the model runtime choice further ([ADR-0007](10-decision-records.md)). |
| **A4** | Prototype users are few enough that a clean break with no migration is acceptable (owner decision D2, taken with this understood). | — |
