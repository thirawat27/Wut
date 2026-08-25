# Changelog

## 1.0.0 (2026-08-25)

The first release. WUT was built from scratch; only the concept and the name
carry over from the prototype that preceded it.

### What it does

- **WUT knows what just failed.** The shell records each command's exit code,
  directory, and duration using shell builtins only — no process is started,
  so the prompt does not get slower. Bare `wut` reads that record and corrects
  the command.
- **Every answer says why.** Each suggestion carries its reasons with sources
  you can check: a rule id, a `git rev-parse`, a line in `package.json`. A
  candidate with no reasons cannot be displayed — that is enforced in the type,
  not by convention.
- **Natural-language questions.** `wut compress a folder to tar.gz` searches a
  local index built from tldr pages, using a semantic index trained from those
  same pages during `wut db sync`. Nothing is downloaded but the pages.
- **`wut ui`** — one screen for searching, reading, and keeping commands,
  without leaving the terminal.
- **`--output json` on every command**, conforming to a versioned schema in
  `pkg/wutjson`.
- **A declarative safety policy.** 26 rules with stable ids, inspectable with
  `wut risk list` and `wut risk check <command>`, extendable by the user — and
  a user rule can only raise a verdict, never lower one.
- **An optional background daemon** that keeps the index warm. Nothing requires
  it; `WUT_NO_DAEMON=1` turns it off entirely.
- **`wut doctor`** reports what WUT can and cannot see on this machine,
  including which support class each of your shells falls into.
- **`wut purge`** deletes everything WUT has recorded, in one command.

### What it will not do

- **It never runs your command.** Not as a policy — as a property of the build.
  An architecture test fails CI if `os/exec` appears anywhere except the
  read-only fact prober and the model supervisor, and the prober compares every
  invocation against an allowlist argv-for-argv.
- **It never invents a command.** A generated sentence whose flags do not
  appear in the page it came from is discarded whole, not filtered.
- **It never leaves the machine.** No account, no telemetry, no cloud model.

### Coming from the prototype

There is no migration, by design. Nothing of the old database or configuration
is read, and your old history and bookmarks stay exactly where they are.

`oops` is gone. Type `wut` instead — the same three letters as the program, and
one fewer name to remember. Run `wut shell install` to write the new block into
your shell startup file; the old block is left alone and simply stops
resolving, and `wut shell status` tells you it is there.
