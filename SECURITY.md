# Security

## Reporting a vulnerability

Report privately through GitHub's [security advisories](https://github.com/thirawat27/wut/security/advisories/new),
not as a public issue. You will get an acknowledgement within a few days.

Please include what you did, what happened, and what you expected — a
reproduction matters far more than a severity rating.

## What WUT does, and what it refuses to do

The threat model is narrow, and most of it is enforced by tests rather than by
policy. If you find a way around any of the following, that is a vulnerability
whatever else it does:

| Guarantee | How it is enforced |
|---|---|
| **WUT never runs your command.** | An architecture test fails the build if `os/exec` appears anywhere except the read-only fact prober and the model supervisor. An end-to-end test feeds a command that would leave a file behind to every entry point and asserts the file never appears. |
| **The fact prober runs only allowlisted, read-only invocations.** | Every invocation is compared against the allowlist argv-for-argv, never by prefix — a longer argv cannot smuggle arguments in behind an allowed one. |
| **A destructive command never reaches your shell.** | In `--shell` mode only an accepted command reaches stdout, the picker refuses anything the policy rates Destructive or above, and with `--yes` the refusal is unconditional. |
| **A local model cannot invent a command.** | Commands come from deterministic producers; a model may only rephrase. Any generated sentence containing a flag, path, URL, or program not present in the page it came from is discarded whole, and a generation that pipes a download into an interpreter is discarded whatever the page says. |
| **Captured output is scrubbed before it is written.** | Tier T1 is off by default. When on, output is capped, matched against credential patterns, and deleted after `capture.retention`. `wut purge` removes everything immediately. |
| **Nothing leaves your machine.** | The only outbound request is the tldr archive during `wut db sync`. There is no telemetry, no account, and no cloud model. |

## Scope

In scope: anything that gets WUT to execute a command, to emit a destructive
command into a shell, to write outside its own directories, to leak captured
output, or to accept a page that changes what it tells you to run.

Out of scope: an attacker who already has code execution as your user, and the
contents of the upstream tldr corpus itself, which is a third-party dataset.

## Verifying a release

Every release is signed keylessly with cosign, so there is no private key to
leak and anyone can verify an artifact against the workflow that built it. The
command is printed in each release's notes.

The install scripts verify the SHA-256 of the archive against `checksums.txt`
before unpacking, and refuse to install on a mismatch.
