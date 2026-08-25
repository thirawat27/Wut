<div align="center">

# wut (What?)

### Your terminal failed. Now it can answer back.

**Repair a command. Recall an incantation. Understand the risk — without giving up control.**

[![CI](https://img.shields.io/github/actions/workflow/status/thirawat27/wut/ci.yml?branch=main&label=CI)](https://github.com/thirawat27/wut/actions/workflows/ci.yml)
[![Go 1.25](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-8A2BE2)](LICENSE)

</div>

```console
$ git psuh -u origin main
git: 'psuh' is not a git command. See 'git --help'.

$ wut
the last command failed

▸ git push -u origin main                                        ●●○ medium
    · 'psuh' is 1 edit from 'push'                     typo/subcommand
    · git has no 'psuh' subcommand
    · the shell reported exit code 1 after 240ms       T0.5

  ↑↓ choose   ⏎ run   w why   e edit   esc cancel
```

> WUT suggests; you decide. It never re-runs the command that failed.

## Start here

### 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | sh
```

On Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1 | iex
```

Both installers verify the downloaded archive against `checksums.txt` before
installing it. Prefer building from source? Run:

```bash
go install github.com/thirawat27/wut/cmd/wut@latest
```

### 2. Let your shell share useful context

```bash
wut shell install
wut db sync
```

Open a new shell. The first command enables automatic context after failures;
the second builds WUT's local command knowledge index.

### 3. Ask naturally

```console
$ wut compress a folder to tar.gz
$ wut explain "rm -rf ./build"
$ wut fix "git psuh"
```

## Three moments. One command to remember.

| When you need… | Try this | WUT helps you… |
| --- | --- | --- |
| **A repair** | `wut` | correct the last command that failed |
| **A recall** | `wut how do I squash the last 3 commits` | find a grounded command in plain language |
| **An explanation** | `wut explain "rm -rf ./build"` | see what a command can change before you run it |

If your question starts with a WUT subcommand, use `wut ask <question>` to
make it unambiguous.

## Why it feels safe to use

| | What WUT does | Why it matters |
| --- | --- | --- |
| **✦ Never executes your command** | Suggestions are displayed, not silently replayed. Architecture tests restrict process execution to a read-only, argv-allowlisted fact prober. | A failed `git push` cannot become a second push. |
| **✦ Shows its work** | Every suggestion includes checkable reasons: a rule id, a Git fact, or source-page evidence. | You can judge the answer instead of trusting a black box. |
| **✦ Remembers the failure, not your life** | Shell hooks capture the command, exit code, directory, and duration with shell builtins. | Bare `wut` knows what just happened without slowing your prompt. |
| **✦ Stays local** | No account, telemetry, cloud model, or model download is required. | Your terminal history remains on your machine. |

## Privacy, on your terms

WUT records only the amount of context you choose. The default, **T0.5**, adds
the missing-command name to basic failure data; it does not read command output.

| Tier | WUT records | Default |
| --- | --- | --- |
| `off` | Nothing | |
| `T0` | Command, exit code, directory, duration | |
| `T0.5` | T0 plus the name of a command that was not found | **Yes** |
| `T1` | T0.5 plus error text | No |

`T1` output is capped, scrubbed for credentials and tokens, retained for at
most 24 hours, and never leaves the machine. Remove captured data whenever you
want with `wut purge`.

## Shell support

No shell is dropped, and none is promised more than it can deliver.

| Support class | Shells | Experience |
| --- | --- | --- |
| **Full** | zsh · fish · PowerShell 7 · Windows PowerShell · nushell · xonsh · elvish | Automatic capture: bare `wut` works after a failure. |
| **Full — later** | bash | The same integration is being smoke-tested; promotion awaits coexistence checks with `bash-preexec` and Starship. |
| **Manual** | sh · dash · ksh · cmd.exe | Use `wut fix "<command>"` or pipe a command's output to `wut`. |

Run `wut doctor` to see the support class and setup state of your current
shell. The [live shell matrix](scripts/shell-matrix.sh) tests each Full-class
claim in a real shell session.

## Command compass

| Goal | Commands |
| --- | --- |
| Ask and repair | `wut`, `wut ask`, `wut fix`, `wut explain`, `wut ui` |
| Save what matters | `wut save`, `wut alias set`, `wut history` |
| Tune your setup | `wut shell install\|status`, `wut db sync\|status`, `wut config show\|set\|explain` |
| Inspect and control | `wut doctor`, `wut risk check`, `wut rules list`, `wut model status`, `wut daemon start\|status` |
| Remove local data | `wut purge` |

Every command supports `--output json`, using the versioned schema in
[`pkg/wutjson`](pkg/wutjson/wutjson.go).

## An honest note about search

Natural-language search is useful, but it is not where WUT wants it to be yet.
The current retrieval benchmark reaches **45.8% top-3 page hit rate** against
an **80% target**. Questions that reuse documentation wording work best;
questions needing world knowledge often need a better sentence encoder.

That gap is deliberate product information, not fine print: benchmark floors
run in CI so quality cannot quietly regress. See the
[evaluation design](docs/architecture/06-intelligence-slm.md) for the full
methodology.

## Develop WUT

```bash
go test ./...                    # test the whole project
go test ./internal/arch/...      # enforce the architecture rules
go test ./internal/eval/...      # measure answer quality
bash scripts/shell-matrix.sh     # exercise real shell sessions
go build -o build/wut ./cmd/wut
```

Start with [`internal/arch`](internal/arch): these tests protect the boundaries
that keep WUT from turning into a tool that acts without permission. The
[architecture guide](docs/architecture/README.md) explains the decisions behind
them.

## License

[MIT](LICENSE) . Thirawat27
 