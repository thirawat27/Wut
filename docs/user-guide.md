# WUT User Guide / คู่มือการใช้งาน WUT

Use WUT when a terminal command fails, when you need to recall a command, or
when you want to understand a command before running it. This is the detailed
English guide; brief Thai notes appear with each workflow.

## Status

- **Current State:** WUT is a local terminal assistant for command repair,
  command discovery, and command explanation.
- **Implementation:** Implemented. Command syntax and defaults in this guide
  were checked with `wut --help` and relevant subcommand help.
- **Dependency:** Complete the [README quick start](../README.md#start-here)
  before following shell-aware workflows.

## 1. What WUT can do / WUT ทำอะไรได้บ้าง

| Need | Command | What happens |
| --- | --- | --- |
| Repair the latest failed command | `wut` | Reads the latest shell record and proposes a correction. |
| Repair a known command | `wut fix "git psuh"` | Analyses only the command you provide. |
| Find a command in plain language | `wut compress a folder to tar.gz` | Searches WUT's local tldr-pages index. |
| Explain a command | `wut explain "rm -rf ./build"` | Shows purpose, examples, and likely changes. |
| Inspect safety policy | `wut risk check "rm -rf ./build"` | Classifies a command against the local risk policy. |
| Review shell history | `wut history --failed` | Shows captured command events. |

**ไทย:** WUT ช่วยเสนอคำสั่งที่น่าจะถูกต้อง ค้นหาวิธีใช้ และอธิบายความเสี่ยง
แต่จะไม่ตัดสินใจหรือรันคำสั่งแทนคุณเองโดยอัตโนมัติ

## 2. First-time setup / เริ่มต้นครั้งแรก

Follow the [README quick start](../README.md#start-here) to install the binary,
then run:

```bash
wut shell install
wut db sync
```

`wut shell install` adds a managed block to the startup file of each detected
shell. It previews the change and asks for confirmation in an interactive
terminal. To inspect its plan without changing a file:

```bash
wut shell install --dry-run
wut shell status
```

Open a new terminal after installation. `wut db sync` downloads the tldr-pages
release once and builds a local index. Searches then work from that local copy.

**ไทย:** ใช้ `--dry-run` ก่อนเสมอหากอยากตรวจว่าจะมีไฟล์ shell ใดถูกแก้ไขบ้าง
จากนั้นเปิด terminal ใหม่เพื่อให้ hook เริ่มทำงาน

## 3. Repair a failed command / แก้คำสั่งที่รันพลาด

### Repair the last failure

With shell integration enabled, run a command that fails and then type:

```console
$ git psuh -u origin main
$ wut
```

WUT reads the recorded command, exit code, working directory, duration, and—at
the default capture tier—the name of a command the shell could not find. It
then presents candidates with reasons, such as a spelling distance or a Git
fact. Review the candidate before accepting it.

### Repair an explicit command

Use this when shell integration is not installed, when you are repairing an
older command, or when you want to be explicit:

```bash
wut fix "git psuh -u origin main"
wut fix "cd.."
wut fix --limit 1 "git psuh"
```

### Include a program's error output

Some rules can use error text when you intentionally pipe it to WUT:

```bash
npm run biuld 2>&1 | wut fix
```

This is explicit input: WUT does not read arbitrary terminal output by default.

**ไทย:** ถ้าไม่มี shell hook ให้ใช้ `wut fix "<command>"` ได้ทันที การเลือก
candidate ในหน้าจอ interactive เป็นการกระทำของผู้ใช้ ไม่ใช่การรันอัตโนมัติ

## 4. Ask for a command / ถามวิธีใช้คำสั่ง

Ask a direct question in ordinary language:

```bash
wut compress a folder to tar.gz
wut how do I squash the last 3 commits
```

If the first word is also a WUT subcommand, use `ask` so the question cannot be
mistaken for a command invocation:

```bash
wut ask check out a new git branch
wut ask explain what a symlink is
```

WUT searches its local tldr-pages index. Build or refresh that index with
`wut db sync` when the program asks you to do so.

### Use the terminal UI

```bash
wut ui
wut ui "find files larger than 100 MB"
```

The UI has three panes: **ask** for questions and results, **history** for
captured commands, and **knowledge** for the source tldr page. Press `Tab` to
move between panes. Selecting a command prints it; WUT does not execute it for
you.

## 5. Explain and assess risk / อธิบายและประเมินความเสี่ยง

Use `explain` before running a command whose effect is unclear:

```bash
wut explain "rm -rf ./build"
wut explain --verbose "tar"
wut risk check "rm -rf ./build"
wut risk list
```

For example, the bundled policy classifies `rm -rf ./build` as **DESTRUCTIVE**
because recursive forced deletion does not move files to a trash folder.

**ไทย:** `wut explain` ช่วยอธิบายผลของคำสั่ง ส่วน `wut risk check` ดูว่าคำสั่ง
เข้ากฎความเสี่ยงใด ทั้งสองคำสั่งไม่รัน command ที่กำลังตรวจ

## 6. Shell integration and capture / การเชื่อมต่อกับ shell

### Check and manage the hook

```bash
wut shell status
wut shell hook zsh          # print the block; write nothing
wut shell install --shells zsh,fish
wut shell uninstall
```

`shell install` manages only the WUT block in a shell startup file. A backup
is reported when one is made. Use `--alias uh` if you want an extra trigger
word.

### Choose the capture tier

```bash
wut shell capture           # show the current tier
wut shell capture T0         # command metadata, without error text
wut shell capture T1         # include capped, scrubbed error text
```

| Tier | Captured data | Default |
| --- | --- | --- |
| `off` | Nothing | No |
| `T0` | Command, exit code, directory, duration | No |
| `T0.5` | T0 plus a command-not-found name | **Yes** |
| `T1` | T0.5 plus error text | No |

T1 is capped, redacted, and retained for the configured period (24 hours by
default). Add custom redaction patterns with `capture.redact` if a terminal
prints sensitive text the built-in patterns do not cover.

### Support classes

| Class | Shells | Behaviour |
| --- | --- | --- |
| Full | zsh, fish, PowerShell 7, Windows PowerShell, nushell, xonsh, elvish | Automatic capture after a failure. |
| Full — later | bash | Integration is smoke-tested; coexistence with common prompt tooling is still a promotion gate. |
| Manual | sh, dash, ksh, cmd.exe | Use `wut fix "<command>"` or pipe output into `wut fix`. |

Run `wut doctor` if bare `wut` is not seeing the failure you expected.

## 7. Knowledge index / ฐานความรู้คำสั่ง

The knowledge index is local and based on tldr-pages. Network access is needed
only for a normal `db sync`; answering a question uses the local index.

```bash
wut db status
wut db sync
wut db clear
```

Useful sync options:

```bash
wut db sync --from-archive /path/to/tldr.zip
wut db sync --no-embed
```

`--from-archive` builds from a local tldr archive for offline installation.
`--no-embed` skips the semantic index, yielding a smaller index with keyword
search only. A failed sync leaves the previously working index untouched.

## 8. Save commands and aliases / เก็บคำสั่งและ alias

```bash
wut save "git log --oneline --decorate -10" --tag git --tag history --note "quick branch overview"
wut save list
wut save remove "git log --oneline --decorate -10"
wut save path
```

Saved commands live in a plain YAML file suitable for version control in your
dotfiles. `wut purge` does not remove that file.

Manage shorthand definitions separately:

```bash
wut alias set gs "git status --short"
wut alias list
wut alias shell
wut alias remove gs
```

`wut alias shell` prints definitions for review; WUT does not silently install
aliases into a shell startup file.

## 9. History, configuration, and privacy / ประวัติ การตั้งค่า และความเป็นส่วนตัว

### History

```bash
wut history
wut history --failed
wut history --stats
wut history --limit 50
```

### Configuration

```bash
wut config path
wut config show
wut config explain
wut config explain capture.tier
wut config get capture.tier
wut config set capture.tier T0
```

Configuration is plain YAML. Supported `WUT_` environment variables can
override selected settings for one process; for example, `WUT_NO_DAEMON=1`
disables the daemon for one invocation.

Important defaults:

| Setting | Default | Purpose |
| --- | --- | --- |
| `capture.tier` | `T0.5` | Controls how much the shell records. |
| `capture.retention` | `24h` | Maximum retention for captured T1 output. |
| `history.max_entries` | `20000` | Local event-log capacity. |
| `knowledge.auto_sync` | `true` | Enables scheduled index refreshes. |
| `knowledge.sync_interval` | `168h` | Default index refresh interval. |
| `daemon.autostart` | `false` | Keeps the optional daemon off by default. |
| `model.tier2` | `off` | Keeps optional generative wording disabled by default. |

### Remove captured data

```bash
wut purge
```

The command asks for confirmation. Use `wut purge --yes` only in an unattended
script where deleting recorded WUT data is intentional.

## 10. Optional daemon and local model / daemon และโมเดลในเครื่อง

WUT works without a daemon. The optional daemon keeps the index—and an
installed local model—in memory to reduce repeated question latency.

```bash
wut daemon start
wut daemon status
wut daemon stop
wut daemon run               # foreground process
```

Inspect local-model integration with:

```bash
wut model status
wut model list
```

The built-in tier-one semantic index is created by `wut db sync`; it is not a
model download. Tier two is optional wording assistance through a local runtime
such as Ollama. It cannot invent commands: command content remains grounded in
the tldr source page and mentioned flags are validated before display.

## 11. JSON and scripting / JSON และการใช้งานใน script

Every command accepts versioned JSON output:

```bash
wut doctor --output json
wut risk check "rm -rf ./build" --output json
wut history --failed --output json
```

| Flag | Use |
| --- | --- |
| `--output text` or `--output json` / `-o` | Select human-readable or machine-readable output. |
| `--cwd <path>` | Read project facts as if WUT ran from that directory. |
| `--no-color` | Disable terminal colour. |
| `--quiet` / `-q` | Hide candidate reasons in text output. |
| `--verbose` / `-v` | Show additional diagnostic detail where supported. |

Exit codes are part of the CLI contract: `0` candidate produced or command
succeeded; `1` internal error; `2` usage error; `3` no answer/no usable
terminal; `4` a destructive candidate was refused; `5` knowledge index is
missing or damaged; `130` interactive selection was cancelled.

## 12. Troubleshooting / แก้ปัญหาเบื้องต้น

| Symptom | Check | Likely next step |
| --- | --- | --- |
| `wut` has no failed command to repair | `wut shell status` | Install the hook, then open a new shell. |
| Natural-language queries have no useful answer | `wut db status` | Run `wut db sync`; use wording close to command documentation. |
| A shell does not capture automatically | `wut doctor` | Check its support class; use `wut fix "<command>"` for Manual shells. |
| You need a clean diagnostic view | `wut doctor --no-color` | Inspect paths, shell state, index state, and configuration. |
| You want to avoid the daemon temporarily | `WUT_NO_DAEMON=1 wut <question>` | Runs the request without the local daemon. |

For any command, run:

```bash
wut <command> --help
```

## 13. Current limits / ข้อจำกัดปัจจุบัน

- Natural-language retrieval is useful but below its target: the documented
  benchmark reports **45.8% top-three page hit rate** against an **80% target**.
  Prefer precise wording and verify the suggested command before use.
- Bash is currently **Full — later**, not Full. Treat `wut fix "<command>"`
  as the reliable fallback when prompt-tool coexistence matters.
- WUT only knows the local tldr-based index and its correction rules. It is not
  a general internet search engine and should not replace reviewing a command
  before it changes data.

## Related documentation

- [README quick start](../README.md)
- [Shell protocol and support classes](architecture/04-shell-protocol.md)
- [CLI behaviour and output contract](architecture/08-cli-ux.md)
- [Storage, retention, and configuration](architecture/07-storage-config.md)
- [Local intelligence and evaluation](architecture/06-intelligence-slm.md)
