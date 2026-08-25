# 10 — Decision Records

> Status: every ADR here is **Accepted** and implemented unless it is marked
> superseded or rejected. Owner decisions D1–D9 (recorded in
> [README.md](README.md)) are inputs to these records, not proposals.

Format per record: Context · Options · Decision · Consequences.
Append new records; never rewrite a decided one — supersede it.

---

## ADR-0001 — Ports and adapters, with a pure core

**Context.** The prototype has no seam: `cmd/` constructs `db.Storage` in 12 files and a
global viper singleton is read by 17 (audit §2). 5 of 18 packages have tests.
The two facts are causally linked.

**Options.**

| | Pros | Cons |
|---|---|---|
| A. Layered packages, no interfaces | Simple, idiomatic for small Go tools | Nothing below the CLI is testable without real I/O — the prototype's exact failure |
| B. **Ports and adapters, pure `core`** | Domain testable as values; adapters swappable; the daemon reuses the same use cases for free | More packages; indirection a small tool does not strictly need |
| C. Full clean architecture with DTO mapping at every boundary | Maximum decoupling | Ceremony far beyond a CLI's needs |

**Decision.** **B.** With one concession to pragmatism: `core` types cross the
port boundary directly. No DTO mapping layer.

**Consequences.** An import-boundary check (I4) and a no-package-state check
(I5) run in CI, or the rule decays. Exactly one construction site: `cmd/wut`.

---

## ADR-0002 — Collapse five engines into one candidate pipeline

**Context.** The prototype has `corrector`, `smart`, `commandsearch`, `historyml`, and
`performance`'s matchers, each with its own result type, scoring, and merge
logic (`internal/smart/engine.go:711`, `:730`). Two result structs exist
(`Correction` at `corrector.go:16`, `Suggestion` at `engine.go:65`).

**Decision.** One `Candidate` type, one funnel: Signal → Facts → Producers →
Merge → Rank → Risk → (optional rerank) → Validate → Present.

**Consequences.** Adding a producer is additive and touches nothing else.
Ranking is defined once, so "why is this first" has a single answer. Cost: any
producer must express itself as a `Candidate`, which is a real constraint on
future exotic sources.

---

## ADR-0003 — `Why` is a required field

**Context.** The prototype shows a score and a source label the user cannot interpret.
UX goal U3 says every answer must explain itself.

**Decision.** `Candidate.Why []Why` is non-empty for any presented candidate,
enforced by the presenter. Each entry has a stable machine code, human text, a
signed weight, and a reference.

**Consequences.** Every producer must justify what it emits, which is
deliberate design pressure. Rendering costs vertical space — mitigated by
showing the top two `Why` lines and expanding on `w`.

---

## ADR-0004 — Execution is structurally impossible, not policed

**Context.** Audit **C1, Critical**: `oops` re-ran the user's command to
harvest stderr (`evaluator.go:170-172`). A 7-prefix deny list plus 2 regexes
(`corrector.go:566-578`) could not make that safe — `git push`,
`docker system prune -af`, `terraform destroy`, `npm publish` all passed it.
The root cause was that the pipeline conflated *analyse* with *execute*.

**Decision.** No layer above `internal/adapter/facts` may import `os/exec`,
asserted by an AST check (I2). `facts` compares argv element-for-element
against a compile-time allowlist of read-only probes. Command output is an
*input* to the pipeline, supplied by the shell ([04](04-shell-protocol.md)),
never produced by it.

**Consequences.** Some corrections that only a real run could reveal are
impossible. That is the correct trade, and the fact engine plus T0.5 recovers
most of the value. The deny-list approach is explicitly rejected as a design.

---

## ADR-0005 — Zero-spawn shell recording

**Context.** WUT needs exit code, cwd, duration, and argv. The obvious design
(`wut record ...` in the prompt hook) costs a process spawn per command:
5–15 ms on Linux, worse on Windows. That is a visible prompt regression, and
users would uninstall it.

**Options.**

| | Cost per command | Notes |
|---|---|---|
| A. Spawn `wut record` | 5–15 ms + | Rejected under constraint C1 |
| B. **Append a record with shell builtins** | under 1 ms, no fork | Needs an escaping-free record format |
| C. Write to the daemon socket from the shell | n/a | bash cannot open a unix socket |

**Decision.** **B.** Unit-separator fields, record-separator terminator, raw
command last so embedded newlines need no escaping.

**Consequences.** WUT parses a bespoke format instead of NDJSON — about 60
lines of parser, versioned in field 1. The daemon and the CLI both tail the
same files, so recording works identically with the daemon on or off.

---

## ADR-0006 — Capture is tiered, and T1 is off by default

**Context.** Owner wants much better DX; stderr is the richest signal.
Transparent stderr capture requires redirection (`exec 2> >(tee …)`), which
changes `isatty(2)` for child processes and is not available in fish, nushell,
or POSIX sh. stderr also carries secrets.

**Decision.** Four tiers ([04](04-shell-protocol.md) §4). **T0 on after
install; T0.5 on where the shell offers a native `command_not_found` hook; T1
opt-in per shell, with per-shell caveats documented; T2 always available.**
The product is designed so that T0 + T0.5 + facts is sufficient — T1 is an
enhancement.

**Consequences.** Correction quality varies by shell, and `wut doctor` must
state the achieved tier honestly. No feature may be built that *requires* T1.

---

## ADR-0007 — Model runtime: pure-Go Tier 1, portable Tier 2

**Context.** Owner decision D1 requires a local SLM that runs on every machine.
The existing release matrix cross-compiles to 8+ platforms from one runner,
which depends on the binary being CGO-free.

**Options for Tier 2.**

| | Speed | Portability | Cost |
|---|---|---|---|
| A. CGO llama.cpp bindings | Native | **Breaks cross-compilation** — per-platform toolchains, cgo on every target | Rejected |
| B. wazero + llama.cpp on WASI | 2–5x slower, no SIMD on some targets | Pure Go host, runs wherever WUT runs | Viable fallback |
| C. **Downloaded native sidecar, supervised by the daemon** | Native | Per-platform artifact, supply-chain surface | Viable primary |
| D. Delegate to an Ollama the user already runs | Native | Only for users who have it | Free win, not a strategy |

**Decision.**

- **Tier 1** (embeddings, always available): **pure Go, no CGO, no SIMD
  requirement.** Static/distilled embedding table. This is what satisfies
  "runs on every machine".
- **Tier 2** (generation, opt-in): **C primary, D when detected, B as the
  portable fallback.** The WUT binary itself stays CGO-free in all three cases.

**Consequences.** `os/exec` gains a second permitted home: the daemon's model
supervisor (I2 allows exactly two). The sidecar download is held to the release
supply-chain standard ([09](09-quality-release.md) §7). If E4 latency fails for
B on a platform, Tier 2 is simply unavailable there and templates are used.

---

## ADR-0008 — Typed config loader; viper is removed

**Context.** Global viper singleton read by 17 files; reads via viper, writes
via `yaml.Marshal` (audit M7); 30+ keys.

**Decision.** A plain struct with an explicit precedence chain, injected as a
value. Atomic writes. Unknown keys rejected with a line number. Target under 15
keys.

**Consequences.** Drops `spf13/viper` and roughly seven transitive
dependencies. Loses viper's live-reload and multi-format support — neither is
used. Every key must justify itself in a user story.

---

## ADR-0009 — The daemon hosts the same use cases, not a second implementation

**Context.** Owner decision D3 adds an opt-in daemon. The standard failure mode
is that the daemon path and the direct path drift into two behaviours.

**Decision.** `internal/daemon` is a transport. It calls `internal/app` with
the same request types the CLI uses, and contains no business logic. Any
request answerable by the daemon is answerable in-process; the CLI falls back
silently on connect failure, timeout, or version mismatch.

**Consequences.** No feature may be daemon-only. Every use case test runs twice
in CI — once in-process, once over the socket — asserting identical results.
The daemon's value is latency and model residency, nothing else.

---

## ADR-0010 — Two stores: immutable packed index, mutable bbolt

**Context.** The prototype stores tldr pages and user state in one bbolt file. The two
have opposite access patterns, and vector search needs contiguous float arrays,
which a KV B-tree cannot provide.

**Decision.** Knowledge lives in an immutable, mmapped, CRC-checked packed
index file, replaced by atomic swap. User state lives in bbolt.

**Consequences.** Knowledge is derived data that can always be deleted and
rebuilt, which simplifies corruption recovery to one message. Brute-force
cosine over ~25,000 int8 vectors is under 2 ms, so **no ANN dependency is
needed**. Cost: a bespoke file format with its own writer, reader, and version
field.

---

## ADR-0011 — Rules and risk policy are data, not Go

**Context.** The prototype's danger detection is 7 prefixes and 2 regexes in one file,
plus a separate 14-entry list in another (`corrector.go:566-578`,
`evaluator.go:203-215`). Adding a correction requires writing Go.

**Decision.** Correction rules and the risk policy are embedded YAML with
stable ids, evaluated by a small engine, tested by golden files. Users may add
risk rules in `~/.config/wut/risk.d/`, but user rules can only **raise** a
level, never lower one.

**Consequences.** Contributors add a directory, not a function (DX goal D2).
Rule ids become a public surface — they appear in `Why.Ref` and `Risk.Rule` —
so renaming one is a breaking change.

---

## ADR-0012 — Clean break, no compatibility layer

**Context.** Owner decision D2, taken with the consequences understood.

**Decision.** No migration tool, no shim, no reading of the prototype's database or
config. The prototype rc blocks are left in place untouched; they simply stop resolving.
`wut doctor` detects a prototype block and tells the user to run `wut shell install`.

**Consequences.** Existing users must reinstall and lose their the prototype history and
bookmarks. This is the largest user-facing cost in the plan and it is
deliberate. It should be stated plainly in the 1.0.0.0 release notes, at the
top, not in a footnote.

---

## ADR-0013 — There is only `wut`; `oops` is removed

**Context.** The prototype installs two names: `wut` (the binary) and `oops` (a shell
function that corrects the last command). The owner asked for a shorter, easier
trigger than `oops`.

**Options.**

| | Keystrokes | Assessment |
|---|---:|---|
| A. Keep `oops` | 4 | Self-documenting, but a second name to learn and advertise |
| B. A shorter dedicated name (`uh`, `ww`, `hm`) | 2 | Shorter, but still a second name — and a 2-character global name is the most likely thing to collide with a user's own alias |
| C. A single character (`w`, `f`, `k`) | 1 | `w` is a real POSIX command (who is logged on). Any single letter is a collision waiting to happen |
| D. **Bare `wut`** | 3 | Fewer keystrokes than `oops`, **and one fewer name in the world.** The context-aware dispatch that makes it work is already in the design ([08](08-cli-ux.md) §3) |

**Decision.** **D.** `oops` is removed. Bare `wut` is the repair interaction.
`wut fix <command>` remains for correcting a command other than the last one.
`shell.alias` in the config lets a user add their own short trigger; it is off
by default and not advertised in the first-run flow.

**Consequences.**

- The managed rc block must define `wut` as a **shell function** wrapping the
  binary, so the accepted command can be `eval`'d in the current shell (this is
  what makes `cd` and `export` behave correctly). `command wut` still reaches
  the binary directly, and the function delegates for every other path.
- The tagline stops being a description and becomes literal: you say *wut?* and
  the terminal answers.
- Cost, stated plainly: `oops` was **discoverable**. Someone watching over your
  shoulder understood it instantly, and bare `wut` advertises the repair
  affordance less. The first-run flow and `wut doctor` must compensate by saying
  outright: *"after a command fails, just type `wut`."*
- Anything that shadows or wraps `wut` (a user alias, a version manager) now
  affects the repair path too. `wut doctor` checks for a shadowing definition.

---

## Superseded / rejected, recorded so they are not re-litigated

| Idea | Why not |
|---|---|
| Plugin ABI (WASM or subprocess) | Owner chose "binary + daemon". Extension happens via in-tree `KnowledgeSource` adapters. Revisit only with a concrete third-party demand. |
| Cloud LLM provider, even opt-in | Owner decision D1 is local-only. It also breaks the "privacy-focused" positioning that is in the product's identity. |
| Keeping a deny list for dangerous commands | ADR-0004. A deny list cannot be complete; the fix is to never execute. |
| An ANN library for vector search | ADR-0010. The corpus is too small to justify the dependency. |
| A TUI for configuration | 14 keys do not need 292 lines of view code ([08](08-cli-ux.md) §8). |
| Migrating the prototype history into WUT | ADR-0012. |
| A dedicated short trigger (`uh`, `ww`, `f`) as a shipped default | ADR-0013. Available as `shell.alias` for anyone who wants it, but not a second name the product teaches. |
| Splitting the work across two releases | Proposed as owner decision D7, then **superseded by D9**: one release, everything in it. The daemon and Tier 2 were still built last, so the daemon was validated against the workload it exists for. |
