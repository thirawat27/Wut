# 05 — The Daemon

> Status: **Implemented**. Owner decision
> **D3**: single binary plus an **opt-in** background daemon.
>
> [ADR-0009](10-decision-records.md) still holds and matters more, not less, now
> that the daemon lands in the first release: it is a pure transport over
> `internal/app`, so nothing in the product may depend on it being present.

## 1. Why a daemon exists at all

Exactly two costs cannot be optimised away in a short-lived process:

| Cost | Cold, per invocation | Warm, with daemon |
|---|---:|---:|
| Loading and warming the Tier 1 embedding model | 60–150 ms (target) | 0 |
| Loading a Tier 2 generative model (0.5–1.5B, Q4) | **1.5–6 s** | 0 |
| mmap + page-fault the knowledge index | 5–25 ms | 0 |
| Ingesting session records into the event store | 3–10 ms | 0, done continuously |

The first two are the whole reason. Owner decision D1 puts a local SLM in the
product; a local SLM in a process that exits after 30 ms is a local SLM nobody
will wait for. The daemon is the thing that makes D1 usable, which is why D1
and D3 were answered together.

**The daemon adds no features.** See
[ADR-0009](10-decision-records.md).

## 2. Opt-in means opt-in

| | |
|---|---|
| Default state | **Not running.** Not installed as a service. |
| How it starts | `wut daemon start`, or `wut model install` offers it, or `daemon.autostart: true` in config lets the CLI spawn it on first use |
| How it stops | `wut daemon stop`, idle timeout (default 30 min), or the machine reboots |
| Service registration | Only on explicit `wut daemon enable` — writes a systemd user unit, a launchd agent, or a Windows Scheduled Task. Never on install. |
| Kill switch | `WUT_NO_DAEMON=1` makes every CLI invocation in-process, no socket attempt |

If the daemon is absent, unhealthy, or slow to answer (200 ms connect budget),
the CLI runs the use case in-process and moves on. It does not warn, does not
retry, and does not block. A degraded daemon must never be worse than no
daemon.

## 3. IPC

| Aspect | Choice | Reason |
|---|---|---|
| Transport | Unix domain socket at `$WUT_STATE_DIR/daemon.sock` (`0600`); named pipe `\\.\pipe\wut-<sid>` on Windows | No TCP port. Nothing listens on the network — invariant B4. |
| Framing | 4-byte length prefix, then payload | Trivial, no delimiter escaping |
| Codec | CBOR (see open question Q2) | Compact, schema-free, fast; JSON is the fallback if debuggability wins |
| Auth | Filesystem permissions plus a per-daemon token in `daemon.json` (`0600`) that the client must echo | Blocks another local user on a shared machine |
| Versioning | Handshake exchanges protocol version; a mismatch makes the client fall back to in-process rather than fail | A stale daemon after an upgrade must not break the CLI |
| Timeouts | connect 200 ms, request 5 s default, 30 s for `Generate` | Never hang the user's prompt |

```mermaid
stateDiagram-WUT
    [*] --> Absent
    Absent --> Starting: wut daemon start / autostart
    Starting --> Ready: socket bound, index mmapped
    Starting --> Absent: bind failed, port in use, error
    Ready --> Warm: Tier 1 loaded
    Warm --> Loaded: Tier 2 loaded on first Generate
    Loaded --> Warm: Tier 2 evicted after model.idle_evict (default 10 min)
    Warm --> Ready: Tier 1 evicted under memory pressure
    Ready --> Draining: idle timeout / stop / SIGTERM
    Warm --> Draining: idle timeout / stop / SIGTERM
    Loaded --> Draining: idle timeout / stop / SIGTERM
    Draining --> [*]: socket unlinked, state flushed

    note right of Ready
        Any state may answer a request.
        A client that cannot reach the daemon
        within 200 ms runs in-process instead.
    end note
```

## 4. What the daemon does

| Responsibility | Detail |
|---|---|
| Hold the knowledge index mmapped | The index file is read-only and shared; the OS page cache does most of the work, the daemon just avoids the re-mmap and header parse |
| Hold Tier 1 embeddings resident | ~30–60 MB RSS target |
| Load Tier 2 on demand, evict when idle | Bounded by `model.max_rss`; refuses to load if free memory is below a floor |
| Tail session record files | Ingests into the event store continuously, so `wut history` and history-weighted ranking never pay ingest cost |
| Precompute on cwd change | When a new cwd appears in the event stream, warm that project's `Facts` — the next repair in that directory is already primed |
| Run scheduled `db sync` | Only if `tldr.auto_sync` is on; the CLI never syncs implicitly |

### What it explicitly does **not** do

- No HTTP server. The prototype shipped an unauthenticated `/metrics` and `/health`
  listener that was never started (audit M4). It is not coming back.
- No supervision of user processes.
- No writes outside the state directory.
- No business logic of its own — it calls `internal/app` exactly as the CLI does.

## 5. Resource budget

Proposed limits, enforced in code and asserted in tests:

| Resource | Idle | Warm (Tier 1) | Loaded (Tier 2, 1.5B Q4) |
|---|---:|---:|---:|
| RSS | under 25 MB | under 90 MB | under 1.4 GB |
| CPU when idle | under 0.1% | under 0.1% | under 0.1% |
| Open FDs | under 32 | under 32 | under 32 |
| Disk writes when idle | 0 | 0 | 0 |

If `model.max_rss` would be exceeded, the daemon refuses to load Tier 2 and
reports it through `wut doctor` rather than being OOM-killed.

## 6. Failure handling

| Failure | Behaviour |
|---|---|
| Socket file exists but nothing is listening (stale) | Client unlinks it and continues in-process; the next `daemon start` rebinds |
| Daemon crashes mid-request | Client's 5 s timeout fires, it retries once in-process, and records a doctor warning |
| Two daemons race to bind | Advisory lock on `daemon.lock`; the loser exits with a clear message |
| Daemon is a version behind the CLI | Version handshake mismatch, client falls back in-process and prints one hint: `wut daemon restart` |
| Machine sleeps and wakes | Idle timer is monotonic-clock based; no spurious work on resume |

## 7. Observability

- `wut daemon status` — state, uptime, RSS, models loaded, requests served,
  p50/p95 latency per use case, last error.
- `wut daemon logs` — the last N structured log lines, off-by-default level.
- Everything is local. No metrics endpoint, no exporter.

## 8. Questions this design raised, and how they were settled

| # | Question | Settled |
|---|---|---|
| Q2 | CBOR or length-prefixed JSON for IPC? | **Length-prefixed JSON.** See [02](02-architecture-overview.md) §8. |
| Q7 | Should `daemon.autostart` become `true` once a model is installed? | **No.** It stays `false` until the user sets it. Installing a model is consent to load a model, not consent to leave a process running; the user who wants it types `wut daemon start` once, and the config key is one line away. |
| Q8 | On Windows, is a Scheduled Task the right mechanism, or a user-level service? | **Neither.** The daemon is spawned detached from the CLI itself (`DETACHED_PROCESS \| CREATE_NO_WINDOW`) and exits on its idle timeout. Registering a system-level object to keep an optional cache warm is a larger promise than the feature is worth. |
