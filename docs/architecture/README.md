# WUT — Architecture Documentation

> **Status: implemented.** This directory describes the system as it is built,
> not as it was once proposed. Where a document still argues against a design
> that was rejected, that argument is kept: the reason a boundary exists is
> the only thing that stops it from being removed later by someone reasonable.

## What this is

WUT was built from scratch. Only the **product concept** and the **name**
(`wut`) carry forward from the prototype that preceded it; the code does not.
See [01-vision-and-scope.md](01-vision-and-scope.md) §3 for the exact list of
what survives and what does not.

## Reading order

| # | Document | Answers |
|---|---|---|
| 01 | [Vision & scope](01-vision-and-scope.md) | Why rebuild? What is kept, what is thrown away, what does "better DX/UX" mean concretely? |
| 02 | [Architecture overview](02-architecture-overview.md) | System boundary, layers, module map, dependency rules |
| 03 | [Domain model](03-domain-model.md) | The core types every subsystem shares: Event, Facts, Candidate, Why, Risk |
| 04 | [Shell protocol](04-shell-protocol.md) | How WUT learns what actually happened in your shell, safely, and what each of the 9 shells can honestly deliver |
| 05 | [Daemon](05-daemon.md) | The opt-in background process: what it does, IPC, lifecycle, degradation |
| 06 | [Intelligence & SLM](06-intelligence-slm.md) | The local language model: tiering, runtime, grounding, hallucination control |
| 07 | [Storage & config](07-storage-config.md) | Packed knowledge index, mutable state store, typed config without viper |
| 08 | [CLI & UX](08-cli-ux.md) | The command surface, output contract, TUI, first-run |
| 09 | [Quality & release](09-quality-release.md) | Test strategy, CI gates, supply chain, performance budgets |
| 10 | [Decision records](10-decision-records.md) | ADR-0001..0013 — the reasoning behind every hard call |

The invariants in [09](09-quality-release.md) §3 are not documentation. Each
one is a test in `internal/arch` that fails the build, because a design rule
that is only written down lasts until the first hurried afternoon.

## Evidence and label conventions

Claims about the prototype carry one of:

- **`Direct`** — verified against a file in that repository, cited by path and line.
- **`Inferred`** — derived from evidence, not directly confirmed.
- **`Assumed`** — an explicit working premise. Listed in §Assumptions of the
  document that relies on it.

They are sourced from direct inspection and from the architecture audit
produced against commit `023ca1b`, the last commit of the prototype.

## Decisions taken by the owner

These constrained everything downstream, and are recorded here because a
constraint whose origin is forgotten gets re-litigated every six months.

| # | Question | Decision |
|---|---|---|
| D1 | AI/LLM posture | **Local model only.** Small, light, must run on every machine. No cloud provider. |
| D2 | Backward compatibility | **Full clean break.** No migration tool, no compatibility shim. |
| D3 | Runtime shape | **Single binary + opt-in background daemon.** |
| D4 | Knowledge sources | **tldr pages only.** `man`/`--help` scraping and team recipe files are out of scope. |
| D5 | Does the read-only fact probe survive D4? | **Yes.** Facts are *runtime context*, not a knowledge source. D4 scopes only where command knowledge comes from. |
| D6 | Shells that cannot support automatic capture | **Keep them, in a declared support class.** Three classes: Full, Full-later, Manual. Never promise what a shell cannot deliver. |
| ~~D7~~ | ~~Scope vs timeline~~ | ~~Split the scope across two releases.~~ **Superseded by D9.** |
| D8 | The `oops` command | **Removed. There is only `wut`.** One name, zero to remember. See [ADR-0013](10-decision-records.md). |
| **D9** | Scope vs timeline, revisited | **One release. No split.** Everything — daemon and generative inference included — ships together. Supersedes D7. |

D1 and D3 interact: see [ADR-0006](10-decision-records.md) and
[ADR-0007](10-decision-records.md).
