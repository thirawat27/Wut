#!/usr/bin/env bash
# Exercise the shell hooks in live sessions of every shell on this machine.
#
# This is the gate that turns the per-shell capability table in
# docs/architecture/04-shell-protocol.md from a claim into a capability. Every
# row there was derived from that shell's documented hook surface; a row that
# nothing has actually run is a guess with a table cell around it.
#
# Each shell gets an isolated HOME, so this never reads or writes the rc files
# of the person running it.
#
# Usage:
#   scripts/shell-matrix.sh                # every shell found on this machine
#   scripts/shell-matrix.sh zsh fish       # only these
#   scripts/shell-matrix.sh --docker       # install the shells in a container first
#
# The exit code is the number of shells that failed, so CI can gate on it. A
# skipped shell is reported as skipped and never counted as a pass: "we did not
# run it" and "it works" are the two things this script exists to keep apart.

set -uo pipefail

ROOT=$(cd -- "$(dirname -- "$0")/.." && pwd)
WUT_BIN="${WUT_BIN:-$ROOT/build/wut}"

# Full class promises automatic capture, so it is tested for it.
FULL_CLASS="zsh fish nu xonsh elvish bash"
# Manual class promises only `wut fix "<command>"`, so that is all it is asked.
MANUAL_CLASS="sh dash ksh"

pass=0; fail=0; skip=0
failed_shells=""

if [ -t 1 ]; then
	green() { printf '\033[32m%s\033[0m' "$1"; }
	red()   { printf '\033[31m%s\033[0m' "$1"; }
	grey()  { printf '\033[90m%s\033[0m' "$1"; }
else
	green() { printf '%s' "$1"; }
	red()   { printf '%s' "$1"; }
	grey()  { printf '%s' "$1"; }
fi

ok()      { pass=$((pass+1)); printf '  %s %-9s %s\n' "$(green PASS)" "$1" "$(grey "${2:-}")"; }
bad()     { fail=$((fail+1)); failed_shells="$failed_shells $1"; printf '  %s %-9s %s\n' "$(red FAIL)" "$1" "$2"; }
skipped() { skip=$((skip+1)); printf '  %s %-9s %s\n' "$(grey SKIP)" "$1" "$(grey "$2")"; }

if [ "${1:-}" = "--docker" ]; then
	shift
	exec docker run --rm -v "$ROOT:/src" -w /src golang:1.23-bookworm bash -c "
		set -e
		apt-get update -qq >/dev/null
		DEBIAN_FRONTEND=noninteractive apt-get install -y -qq zsh fish ksh dash elvish >/dev/null
		go build -o build/wut ./cmd/wut
		scripts/shell-matrix.sh $*
	"
fi

if [ ! -x "$WUT_BIN" ]; then
	printf 'building %s\n' "$WUT_BIN"
	(cd "$ROOT" && go build -o "$WUT_BIN" ./cmd/wut) || exit 1
fi

WANTED=${*:-$FULL_CLASS $MANUAL_CLASS}

# rc_path echoes where WUT installs its block for a shell, relative to HOME.
# It mirrors the RCFiles table in internal/adapter/shell/shells.go; if the two
# disagree, this script says the hook wrote nothing, which is the failure you
# want rather than a silent pass.
rc_path() {
	case "$1" in
		zsh)    printf '%s' "$HOME/.zshrc" ;;
		bash)   printf '%s' "$HOME/.bashrc" ;;
		fish)   printf '%s' "$HOME/.config/fish/config.fish" ;;
		nu)     printf '%s' "$HOME/.config/nushell/config.nu" ;;
		xonsh)  printf '%s' "$HOME/.xonshrc" ;;
		elvish) printf '%s' "$HOME/.config/elvish/rc.elv" ;;
		ksh)    printf '%s' "$HOME/.kshrc" ;;
		*)      printf '%s' "$HOME/.profile" ;;
	esac
}

# session_script is what the shell is asked to run, one command per line:
# something that succeeds, then something that fails, then a trailing no-op.
#
# Two details here are load-bearing.
#
# The commands are fed on *stdin*, not with -c. An interactive shell started
# with -c executes the string and exits without ever drawing a prompt, and
# every hook writes its record on the next prompt. Testing with -c produces an
# empty record file and the false conclusion that the hook is broken — which is
# exactly what happened the first time this ran.
#
# The trailing no-op is the extra prompt cycle that flushes the failing
# command, for the same reason.
#
# The typo'd program in the middle is step 6: a shell that claims tier T0.5 has
# to record the name of a command that was not found.
session_script() {
	case "$1" in
		nu)     printf 'true\n^false\nwutnosuchcommand\ntrue\nexit\n' ;;
		xonsh)  printf 'True\nFalse\nwutnosuchcommand\nTrue\nexit\n' ;;
		elvish) printf 'nop\ntry { fail oops } catch e { }\nwutnosuchcommand\nnop\nexit\n' ;;
		*)      printf 'true\nfalse\nwutnosuchcommand\n:\nexit\n' ;;
	esac
}

isolate() {
	HOME=$(mktemp -d)
	export HOME
	export XDG_CONFIG_HOME="$HOME/.config"
	export XDG_DATA_HOME="$HOME/.local/share"
	export XDG_STATE_HOME="$HOME/.local/state"
	# Windows resolves the home directory from USERPROFILE, not HOME. Without
	# these, a run under Git Bash or MSYS installs hooks into the real home
	# directory of whoever is running the matrix.
	export USERPROFILE="$HOME"
	export APPDATA="$HOME/AppData/Roaming"
	export LOCALAPPDATA="$HOME/AppData/Local"
	export WUT_NO_DAEMON=1
	export WUT_SESSION="matrix"
	# The hook guards on `command -v wut`. Without the binary on PATH every
	# shell installs a block that quietly does nothing, and the matrix reports
	# a protocol failure that is really a harness failure.
	export PATH="$(dirname "$WUT_BIN"):$PATH"
	mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME"
}

# assert_isolated refuses to continue unless WUT agrees about where home is.
#
# This guard exists because the first run of this script edited the real
# .bashrc of the machine it was written on: HOME was overridden, WUT resolved
# the home directory another way, and the isolation was a comforting fiction.
# A test harness that can write outside its sandbox must fail closed.
assert_isolated() {
	target=$("$WUT_BIN" shell install --dry-run --shells "$1" 2>/dev/null |
		grep -o "[^ ]*$(basename "$(rc_path "$1")")" | head -n1)
	[ -n "$target" ] || return 0

	# Compare on the sandbox's unique directory name rather than the whole
	# path. Under Git Bash the same directory is spelled /tmp/tmp.XXXX and
	# C:\...\Temp\tmp.XXXX, and a prefix match would reject a run that is
	# perfectly isolated.
	sandbox=$(basename "$HOME")
	case "$target" in
		*"$sandbox"*) return 0 ;;
	esac

	printf '\n  %s\n' "$(red "ABORT: wut would write to $target, outside the sandbox $sandbox.")"
	printf '  %s\n\n' "Refusing to run — this would edit the rc files of whoever is running it."
	exit 99
}

release() { [ -n "${HOME:-}" ] && [ "${HOME#/tmp/}" != "$HOME" ] && rm -rf "$HOME"; }

# run_full walks the eight assertions in docs/architecture/09-quality-release.md §4.
run_full() {
	shell=$1; bin=$2
	saved_home=$HOME
	isolate
	assert_isolated "$shell"
	rcfile=$(rc_path "$shell")

	# Snapshot the rc file *before* install. Comparing against the post-install
	# copy would assert that uninstall changes nothing, which is the opposite
	# of what it is for. A file that does not exist yet snapshots as empty.
	: > "$HOME/rc.before"
	[ -f "$rcfile" ] && cp "$rcfile" "$HOME/rc.before"

	# 1. install
	if ! out=$("$WUT_BIN" shell install --yes --shells "$shell" 2>&1); then
		bad "$shell" "install failed: $(printf '%s' "$out" | head -n1)"
		release; HOME=$saved_home; return
	fi
	if [ ! -f "$rcfile" ]; then
		bad "$shell" "install reported success but wrote no $rcfile"
		release; HOME=$saved_home; return
	fi

	# 2-4. a live session that succeeds, then fails
	case "$shell" in
		bash)   session_script "$shell" | "$bin" --rcfile "$rcfile" -i >/dev/null 2>&1 ;;
		zsh)    session_script "$shell" | ZDOTDIR="$HOME" "$bin" -i >/dev/null 2>&1 ;;
		fish)   session_script "$shell" | "$bin" -i >/dev/null 2>&1 ;;
		nu)     session_script "$shell" | "$bin" --config "$rcfile" -i >/dev/null 2>&1 ;;
		xonsh)  session_script "$shell" | "$bin" -i >/dev/null 2>&1 ;;
		elvish) session_script "$shell" | "$bin" -rc "$rcfile" -i >/dev/null 2>&1 ;;
		*)      session_script "$shell" | "$bin" -i >/dev/null 2>&1 ;;
	esac

	# Search the whole sandbox rather than the XDG path: WUT resolves its state
	# directory per platform, and hard-coding one layout here would report a
	# working hook as broken on any machine that uses another.
	rec=$(find "$HOME" -name '*.rec' 2>/dev/null | head -n1)
	if [ -z "$rec" ]; then
		bad "$shell" "no record written. This shell is documented as Full class and delivered nothing."
		release; HOME=$saved_home; return
	fi

	# The format is US-separated fields with an RS terminator, so complete
	# records are exactly the number of 0x1E bytes.
	records=$(tr -cd '\036' < "$rec" | wc -c | tr -d ' ')
	if [ "${records:-0}" -lt 1 ]; then
		bad "$shell" "a record file exists but holds no complete record"
		release; HOME=$saved_home; return
	fi

	# 6. T0.5 — the name of a command that was not found.
	#
	# The tier is read out of the record the hook just wrote, not out of the
	# support table: what matters is what this shell actually claimed on this
	# machine. A shell that stamps its records T0.5 and then records nothing for
	# an unknown command is claiming a capability it does not deliver.
	# The name has to appear *twice* in one record — once in the not_found
	# field and once in the raw command. Checking for it once passes on the raw
	# command alone, which every tier records, and would have hidden the fact
	# that bash runs its not-found handler in a forked child where the variable
	# never comes back.
	if grep -q 'T0\.5\|T1' "$rec"; then
		if ! tr '\036' '\n' < "$rec" | grep -q 'wutnosuchcommand.*wutnosuchcommand'; then
			bad "$shell" "records claim tier T0.5 but a command that was not found left no name"
			release; HOME=$saved_home; return
		fi
	fi

	# 5. WUT reads what the hook wrote, and the failure is in there.
	#
	# Asserting only that the command exits zero would pass on an empty
	# history, which is the failure mode this step exists to catch.
	hist=$("$WUT_BIN" history --limit 10 --output json 2>&1)
	if [ $? -ne 0 ]; then
		bad "$shell" "wut could not read the records this shell wrote"
		release; HOME=$saved_home; return
	fi
	if ! printf '%s' "$hist" | grep -q '"exit_code":[^0]'; then
		bad "$shell" "the failing command was recorded with exit code 0"
		release; HOME=$saved_home; return
	fi

	# 7. `command wut` still reaches the binary rather than a shell function
	case "$shell" in
		bash|zsh)
			if ! "$bin" -c 'command -v wut' >/dev/null 2>&1; then
				bad "$shell" "the hook shadowed the wut binary"
				release; HOME=$saved_home; return
			fi
			;;
	esac

	# 8. uninstall restores the file byte for byte
	if ! "$WUT_BIN" shell uninstall --shells "$shell" >/dev/null 2>&1; then
		bad "$shell" "uninstall failed"
		release; HOME=$saved_home; return
	fi
	if [ -f "$rcfile" ] && ! cmp -s "$HOME/rc.before" "$rcfile"; then
		bad "$shell" "uninstall did not restore $rcfile byte for byte"
		release; HOME=$saved_home; return
	fi

	ok "$shell" "$records record(s), rc restored"
	release; HOME=$saved_home
}

run_manual() {
	shell=$1; bin=$2
	saved_home=$HOME
	isolate

	out=$("$bin" -c "'$WUT_BIN' fix 'git psuh' --output json" 2>&1)
	if printf '%s' "$out" | grep -q 'git push'; then
		ok "$shell" "manual class: wut fix works"
	else
		bad "$shell" "wut fix produced no correction: $(printf '%s' "$out" | head -n1)"
	fi
	release; HOME=$saved_home
}

printf '\nwut shell matrix — %s\n\n' "$($WUT_BIN version 2>/dev/null | head -n1)"

printf 'Full class — automatic capture is promised, so it is proved\n'
for shell in $FULL_CLASS; do
	case " $WANTED " in *" $shell "*) ;; *) continue ;; esac
	bin=$(command -v "$shell" 2>/dev/null)
	if [ -z "$bin" ]; then
		skipped "$shell" "not installed here"
		continue
	fi
	run_full "$shell" "$bin"
done

printf '\nManual class — only what they promise\n'
for shell in $MANUAL_CLASS; do
	case " $WANTED " in *" $shell "*) ;; *) continue ;; esac
	bin=$(command -v "$shell" 2>/dev/null)
	if [ -z "$bin" ]; then
		skipped "$shell" "not installed here"
		continue
	fi
	run_manual "$shell" "$bin"
done

printf '\n  %s passed, %s failed, %s skipped\n' "$pass" "$fail" "$skip"
if [ "$skip" -gt 0 ]; then
	printf '  %s\n' "$(grey "A skipped shell is not a passing shell. Use --docker to cover the rest.")"
fi
if [ -n "$failed_shells" ]; then
	printf '  failed:%s\n' "$failed_shells"
fi
printf '\n'
exit "$fail"
