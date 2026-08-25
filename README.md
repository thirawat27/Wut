<div align="center">

# wut

**The terminal answers.**

</div>

---

```
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

Three letters. One name to remember.

## What it is

WUT exists for one moment: you typed something, and it failed, or you do not
know the incantation. That question has three faces, and they are all the same
question at different times:

| | You are thinking | You type |
|---|---|---|
| **Repair** | "that failed — what did I mean?" | `wut` |
| **Recall** | "how do I do X with this thing?" | `wut compress a folder to tar.gz` |
| **Comprehend** | "what will this do to my machine?" | `wut explain "rm -rf ./build"` |

## Four things that make it different

**It never runs your command.** Not as a policy — as a property of the build.
An architecture test fails CI if `os/exec` appears anywhere except the
read-only fact prober, and that prober compares every invocation against an
allowlist argv-for-argv. Tools that re-run your failed command to read its
error message will push twice after a failed `git push`. This one cannot.

**It always tells you why.** Every suggestion carries its reasons, each with a
source you can check — a rule id, a `git rev-parse`, a line in `package.json`.
A candidate with no reasons cannot be displayed; that is enforced in the type,
not by convention.

**It knows what actually happened.** The shell records the command, its exit
code, the directory, and how long it took — using shell builtins only, no
process spawned, under a millisecond. So `wut` on its own already knows what
you are asking about.

**It stays on your machine.** No account, no telemetry, no cloud model, and no
model download either: the semantic index that makes natural-language questions
work is trained from the tldr pages themselves during `wut db sync`. An
optional wording model can use a local runtime you already have, and it is
never allowed to invent a command — it can only reorder and rephrase answers
that came from the pages, and every flag it writes is checked against the
source page before you see it.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | sh
```

The script verifies the SHA-256 of the archive against `checksums.txt` and
refuses to install if they disagree. On Windows, use `scripts/install.ps1`.
From source:

```bash
go install github.com/thirawat27/wut/cmd/wut@latest
```

Then:

```bash
wut shell install
wut db sync
```

and open a new shell.

## Commands

```
wut                          correct the last command that failed
wut <question>               ask in plain language
wut ask <question>           the same, when your question starts with a
                             word that is also a command below
wut ui [question]            search, read, and keep commands on one screen
wut fix [command]            correct a specific command
wut explain <command>        say what a command does and what it changes
wut save [command]           keep a command you want to find again
wut alias set <name> <cmd>   define a shorthand
wut history [--stats]        what your shell has told WUT
wut shell install|status     shell integration
wut shell capture <tier>     how much the shell tells WUT
wut db sync|status           the knowledge index
wut daemon start|status      the optional background helper
wut model status             which models are in use
wut risk check <command>     ask the safety policy about a command
wut rules list               the correction rules
wut config show|set|explain  read and change settings
wut doctor                   what WUT can and cannot see here
wut purge                    delete everything WUT has recorded
```

Every command takes `--output json`, conforming to a versioned schema in
[`pkg/wutjson`](pkg/wutjson/wutjson.go).

## Which shells

No shell is dropped, and none is promised more than it can deliver.

| Class | Shells | What you get |
|---|---|---|
| **Full** | zsh · fish · PowerShell 7 · Windows PowerShell · nushell · xonsh · elvish | Automatic capture. Bare `wut` just works. |
| **Full — later** | bash | Same, once coexistence with `bash-preexec` / Starship is verified. |
| **Manual** | sh · dash · ksh · cmd.exe | No hook surface exists. `wut fix "<cmd>"` and `cmd 2>&1 \| wut` work exactly as well as anywhere. |

`wut doctor` tells you which one you got. Every claim in that table is checked
by `scripts/shell-matrix.sh`, which sources the real hook into a live session
of each shell, runs a command that fails, and asserts the record.

## What WUT records

| Tier | Contents | Default |
|---|---|---|
| `off` | nothing | |
| `T0` | command, exit code, directory, duration | |
| `T0.5` | T0 plus the name of a command that was not found | **on** |
| `T1` | T0.5 plus the error text | off |

T1 is the only tier that reads output. It is capped, scrubbed for credentials
and tokens, deleted after 24 hours, and never leaves the machine.
`wut purge` deletes all of it, immediately.

## How good is it, honestly

Natural-language search is the weakest part, and it is measured rather than
asserted. `internal/eval` runs 203 questions against the real index:

| | Target | Measured |
|---|---|---|
| Top-3 page hit rate | 80% | **46%** |
| Top-3, lexical only, no semantic layer | 60% | **41%** |
| False corrections on already-correct commands | 0 | **0 of 27** |
| Invented flags surviving the grounding check | 0 | **0 of 30** |

**The retrieval target is not met.** Questions whose words appear in the
documentation work well; questions needing world knowledge — "see which process
is using a port" should reach `lsof` — often do not. That is the ceiling of
keyword search plus embeddings trained from the same corpus, and closing it
needs a real sentence encoder. The benchmark asserts against a floor so the
number cannot quietly get worse, and prints the target on every run so it
cannot quietly be forgotten.

## Development

```bash
go test ./...                    # everything
go test ./internal/arch/...      # the architecture rules
go test ./internal/eval/...      # is it any good, rather than does it work
bash scripts/shell-matrix.sh     # live shell sessions
go build -o build/wut ./cmd/wut
```

`internal/arch` is worth reading first: it is the set of rules that keep the
design from decaying, and every one of them is a defect found in the tool this
one replaces.

The design is in [`docs/architecture/`](docs/architecture/README.md).

## Licence

MIT.
