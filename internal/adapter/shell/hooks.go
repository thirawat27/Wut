package shell

import (
	"fmt"
	"strings"
)

// The record format, written by shell builtins only.
//
// Field separator is 0x1F and the terminator is 0x1E, both written as octal
// escapes because every printf implementation understands \0NNN while \xNN is
// not universal. The raw command is last, so a command containing a newline
// needs no escaping — which is the property that makes a fork-free hook
// possible at all.
//
//	1 <US> seq <US> start_ms <US> dur_ms <US> exit <US> shell <US> cwd
//	  <US> tier <US> not_found <US> raw <RS>
const recordPrintf = `1\037%s\037%s\037%s\037%s\037%s\037%s\037%s\037%s\037%s\036`

// posixRecordFn is the shared writer for POSIX-family shells.
func posixRecordFn(shellName string) string {
	return fmt.Sprintf(`  __wut_write() {
    printf '%s' "$__wut_seq" "$__wut_t0" "$__wut_dur" "$__wut_code" \
      '%s' "$PWD" "$__wut_tier" "$__wut_notfound" "$__wut_cmd" \
      >>"$__wut_rec" 2>/dev/null
  }`, recordPrintf, shellName)
}

// renderBash builds the bash block.
//
// Everything here avoids a subshell. $(( )) is arithmetic expansion and >> is
// a redirection; neither forks. A single $( ) would undo the whole design.
func renderBash(p Params) string {
	var b strings.Builder
	b.WriteString(`if command -v wut >/dev/null 2>&1; then
  : "${WUT_SESSION:=b$$}"
  export WUT_SESSION
  __wut_dir=` + shQuote(p.SessionsDir) + `
  __wut_rec="$__wut_dir/$WUT_SESSION.rec"
  # bash runs command_not_found_handle in a forked child, so a variable it
  # sets never comes back. The handler therefore leaves the name in a file and
  # the prompt hook picks it up — one redirection in the rare case, and still
  # no process started on the common path.
  __wut_nf="$__wut_dir/$WUT_SESSION.nf"
  __wut_seq=0
  __wut_cmd=''
  __wut_notfound=''
  __wut_t0=0
  __wut_dur=0
  __wut_code=0
  __wut_tier='T0.5'
  __wut_ms=0

  # EPOCHREALTIME is bash 5+. Without it the duration is reported as zero
  # rather than paying a fork for date(1) on every prompt.
  __wut_setms() {
    if [ -n "${EPOCHREALTIME:-}" ]; then
      # The separator is locale-dependent: LC_NUMERIC=de_DE gives a comma,
      # and removing only "." left a comma inside an arithmetic expansion,
      # which is a syntax error on every prompt.
      local __e=${EPOCHREALTIME/[.,]/}
      __wut_ms=$(( 10#${__e:0:16} / 1000 ))
    else
      __wut_ms=0
    fi
  }

`)
	b.WriteString(posixRecordFn("bash"))
	b.WriteString(`

  __wut_preexec() {
    [ -n "${COMP_LINE:-}" ] && return
    case "$BASH_COMMAND" in
      __wut_*|*__wut_precmd*) return ;;
    esac
    __wut_cmd=$BASH_COMMAND
    __wut_setms
    __wut_t0=$__wut_ms
  }

  __wut_precmd() {
    __wut_code=$?
    if [ -n "$__wut_cmd" ]; then
      __wut_setms
      __wut_dur=$(( __wut_ms - __wut_t0 ))
      [ "$__wut_dur" -lt 0 ] && __wut_dur=0
      # Collect what the not-found handler left behind, then blank the file.
      # read and : are builtins and the redirection costs no process, so this
      # stays fork-free even on the path that uses it.
      if [ -s "$__wut_nf" ]; then
        read -r __wut_notfound < "$__wut_nf"
        : > "$__wut_nf"
      fi
      __wut_seq=$(( __wut_seq + 1 ))
      __wut_write
      __wut_cmd=''
      __wut_notfound=''
    fi
    return $__wut_code
  }
  case "${PROMPT_COMMAND:-}" in
    *__wut_precmd*) ;;
    *) PROMPT_COMMAND="__wut_precmd${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
  esac

  # Only defined when nothing else owns it. If another tool already handles
  # unknown commands, WUT stays out of the way and stamps T0 rather than
  # claiming a T0.5 value it cannot observe.
  if declare -F command_not_found_handle >/dev/null 2>&1; then
    __wut_tier='T0'
  else
    command_not_found_handle() {
      printf '%s' "$1" >"$__wut_nf" 2>/dev/null
      printf 'bash: %s: command not found\n' "$1" >&2
      return 127
    }
  fi

  # The DEBUG trap goes on last, so nothing above is recorded as something the
  # user typed. Installing it earlier put the hook's own capability probe into
  # the first record of every session — invisible until somebody reads a
  # record file, and then obviously wrong.
  trap '__wut_preexec' DEBUG

`)
	b.WriteString(posixWutFunction("bash"))
	b.WriteString(posixAlias(p.Alias))
	b.WriteString("fi\n")
	return b.String()
}

// posixWutFunction defines `wut` as a shell function.
//
// The function exists for exactly one reason: an accepted command has to run
// in *this* shell, so `cd` and `export` take effect. A binary cannot do that
// for its parent. `command wut` still reaches the executable directly, and
// every path except repair delegates straight to it.
func posixWutFunction(shellName string) string {
	histLine := `      history -s -- "$__wut_out" 2>/dev/null`
	if shellName == "zsh" {
		histLine = `      print -s -- "$__wut_out" 2>/dev/null`
	}
	return `  wut() {
    case "${1-}" in
      ''|fix|f)
        case "${1-}" in fix|f) shift ;; esac
        local __wut_out __wut_rc
        __wut_out=$(command wut fix --shell --session "$WUT_SESSION" "$@")
        __wut_rc=$?
        if [ "$__wut_rc" -eq 0 ] && [ -n "$__wut_out" ]; then
` + histLine + `
          eval "$__wut_out"
          return $?
        fi
        return $__wut_rc
        ;;
      *)
        command wut "$@"
        ;;
    esac
  }

`
}

func posixAlias(alias string) string {
	if alias == "" {
		return ""
	}
	return "  " + alias + `() { wut "$@"; }

`
}

// renderZsh builds the zsh block. zsh has the cleanest surface of the nine:
// named hook arrays mean nothing has to be overwritten.
func renderZsh(p Params) string {
	var b strings.Builder
	b.WriteString(`if (( $+commands[wut] )); then
  zmodload zsh/datetime 2>/dev/null
  zmodload zsh/mathfunc 2>/dev/null
  : ${WUT_SESSION:=z$$}
  export WUT_SESSION
  __wut_dir=` + shQuote(p.SessionsDir) + `
  __wut_rec="$__wut_dir/$WUT_SESSION.rec"
  __wut_nf="$__wut_dir/$WUT_SESSION.nf"
  __wut_seq=0
  __wut_cmd=''
  __wut_notfound=''
  __wut_t0=0
  __wut_dur=0
  __wut_code=0
  __wut_tier='T0.5'

`)
	b.WriteString(posixRecordFn("zsh"))
	b.WriteString(`

  __wut_preexec() {
    __wut_cmd=$1
    __wut_t0=$(( int(EPOCHREALTIME * 1000) ))
  }

  __wut_precmd() {
    __wut_code=$?
    if [[ -n $__wut_cmd ]]; then
      __wut_dur=$(( int(EPOCHREALTIME * 1000) - __wut_t0 ))
      (( __wut_dur < 0 )) && __wut_dur=0
      if [[ -s $__wut_nf ]]; then
        IFS= read -r __wut_notfound < "$__wut_nf"
        : > "$__wut_nf"
      fi
      __wut_seq=$(( __wut_seq + 1 ))
      __wut_write
      __wut_cmd=''
      __wut_notfound=''
    fi
  }

  autoload -Uz add-zsh-hook 2>/dev/null
  if (( $+functions[add-zsh-hook] )); then
    add-zsh-hook preexec __wut_preexec
    add-zsh-hook precmd __wut_precmd
  fi

  if (( $+functions[command_not_found_handler] )); then
    __wut_tier='T0'
  else
    command_not_found_handler() {
      printf '%s' "$1" >"$__wut_nf" 2>/dev/null
      print -u2 "zsh: command not found: $1"
      return 127
    }
  fi

`)
	b.WriteString(posixWutFunction("zsh"))
	b.WriteString(posixAlias(p.Alias))
	b.WriteString("fi\n")
	return b.String()
}

// renderFish uses native events, so no global is touched and no other plugin
// can be disturbed.
func renderFish(p Params) string {
	alias := ""
	if p.Alias != "" {
		alias = "\n  function " + p.Alias + "\n    wut $argv\n  end\n"
	}
	return `if command -q wut
  if not set -q WUT_SESSION
    set -gx WUT_SESSION f$fish_pid
  end
  set -g __wut_rec ` + shQuote(p.SessionsDir) + `/$WUT_SESSION.rec
  set -g __wut_seq 0
  set -g __wut_notfound ""

  function __wut_postexec --on-event fish_postexec
    set -l code $status
    set -l dur $CMD_DURATION
    set -g __wut_seq (math $__wut_seq + 1)
    printf '` + recordPrintf + `' \
      $__wut_seq 0 $dur $code fish "$PWD" T0.5 "$__wut_notfound" "$argv[1]" \
      >>$__wut_rec 2>/dev/null
    set -g __wut_notfound ""
  end

  function fish_command_not_found
    set -g __wut_notfound $argv[1]
    __fish_default_command_not_found_handler $argv
  end

  function wut
    if test (count $argv) -eq 0; or test "$argv[1]" = fix; or test "$argv[1]" = f
      set -l rest $argv
      if test (count $argv) -gt 0; and contains -- "$argv[1]" fix f
        set rest $argv[2..-1]
      end
      set -l out (command wut fix --shell --session $WUT_SESSION $rest)
      set -l rc $status
      if test $rc -eq 0; and test -n "$out"
        eval $out
        return $status
      end
      return $rc
    end
    command wut $argv
  end
` + alias + `end
`
}

// renderPowerShell wraps the prompt function and reads the newest history
// entry.
//
// the prototype read `Get-History -Count 2` at index 0. That list is
// oldest-first, so it targeted the command *before* the one that failed.
// Reading -Count 1 is the whole fix, and it is why the record now carries a
// sequence number that WUT checks rather than trusting position.
func renderPowerShell(p Params) string {
	alias := ""
	if p.Alias != "" {
		alias = "\n  Set-Alias -Name " + p.Alias + " -Value wut -Scope Global\n"
	}
	return `if (Get-Command wut -ErrorAction SilentlyContinue) {
  if (-not $env:WUT_SESSION) { $env:WUT_SESSION = "p$PID" }
  $global:WutRecFile = Join-Path ` + psQuote(p.SessionsDir) + ` "$($env:WUT_SESSION).rec"
  $global:WutSeq = 0
  $global:WutLastId = -1
  $global:WutNotFound = ''
  # Capture the *original* prompt once. Without the guard, dot-sourcing the
  # profile again (". $PROFILE", or a second install) wraps WUT's own wrapper,
  # and the next prompt recurses until PowerShell runs out of stack.
  if (-not $global:WutInnerPrompt) { $global:WutInnerPrompt = $function:prompt }

  # T0.5 — the name of a command that was not found.
  #
  # Only installed when nothing else owns the hook. If another module already
  # handles unknown commands, WUT loses T0.5 rather than breaking that module,
  # which is the same rule the POSIX shells follow.
  #
  # Unlike bash, PowerShell runs this in the current session, so the variable
  # is still there when the prompt reads it.
  if (-not $ExecutionContext.InvokeCommand.CommandNotFoundAction) {
    $ExecutionContext.InvokeCommand.CommandNotFoundAction = {
      param($CommandName, $EventArgs)
      $global:WutNotFound = $CommandName
    }
  }

  function global:prompt {
    $ok = $?
    $code = if ($ok) { 0 } elseif ($LASTEXITCODE) { $LASTEXITCODE } else { 1 }
    try {
      $h = Get-History -Count 1 -ErrorAction SilentlyContinue
      if ($h -and $h.Id -ne $global:WutLastId) {
        $global:WutLastId = $h.Id
        $global:WutSeq++
        $dur = [int](($h.EndExecutionTime - $h.StartExecutionTime).TotalMilliseconds)
        $start = [int64]([DateTimeOffset]$h.StartExecutionTime).ToUnixTimeMilliseconds()
        $us = [string][char]0x1F
        $rs = [string][char]0x1E
        $nf = $global:WutNotFound
        $global:WutNotFound = ''
        $rec = "1$us$($global:WutSeq)$us$start$us$dur$us$code${us}powershell$us$($PWD.Path)${us}T0.5$us$nf$us$($h.CommandLine)$rs"
        [System.IO.File]::AppendAllText($global:WutRecFile, $rec)
      }
    } catch { }
    & $global:WutInnerPrompt
  }

  function global:wut {
    $exe = (Get-Command wut -CommandType Application -ErrorAction SilentlyContinue).Source
    if (-not $exe) { Write-Error 'wut is not on PATH'; return }
    $isRepair = ($args.Count -eq 0) -or ($args[0] -eq 'fix') -or ($args[0] -eq 'f')
    if ($isRepair) {
      $rest = @()
      if ($args.Count -gt 1 -and (($args[0] -eq 'fix') -or ($args[0] -eq 'f'))) {
        $rest = $args[1..($args.Count - 1)]
      }
      $out = & $exe fix --shell --session $env:WUT_SESSION @rest
      if ($LASTEXITCODE -eq 0 -and $out) {
        Invoke-Expression ($out -join "` + "`" + `n")
      }
      return
    }
    & $exe @args
  }
` + alias + `}
`
}

// renderNushell appends to the hook lists rather than replacing them.
func renderNushell(p Params) string {
	return `if (which wut | is-not-empty) {
  $env.WUT_SESSION = ($env.WUT_SESSION? | default $"n(random chars -l 8)")
  $env.WUT_REC = ` + nuQuote(p.SessionsDir) + ` + $"/($env.WUT_SESSION).rec"
  $env.WUT_NF = ` + nuQuote(p.SessionsDir) + ` + $"/($env.WUT_SESSION).nf"
  $env.WUT_SEQ = 0
  $env.WUT_CMD = ""
  $env.WUT_TIER = "T0.5"

  let current_config = ($env.config? | default {})
  let current_hooks = ($current_config.hooks? | default {})
  let pre_execution = (($current_hooks.pre_execution? | default []) | append {||
    $env.WUT_CMD = (commandline)
  })
  let pre_prompt = (($current_hooks.pre_prompt? | default []) | append {||
      let code = ($env.LAST_EXIT_CODE? | default 0)
      let cmd = $env.WUT_CMD
      if ($cmd | is-not-empty) {
        let not_found = if ($env.WUT_NF | path exists) { open --raw $env.WUT_NF } else { "" }
        $env.WUT_SEQ = $env.WUT_SEQ + 1
        let us = (char --integer 31)
        let rs = (char --integer 30)
        let rec = $"1($us)($env.WUT_SEQ)($us)0($us)0($us)($code)($us)nu($us)($env.PWD)($us)($env.WUT_TIER)($us)($not_found)($us)($cmd)($rs)"
        $rec | save --append --raw $env.WUT_REC
        "" | save --force --raw $env.WUT_NF
        $env.WUT_CMD = ""
      }
    })
  mut hooks = ($current_hooks | upsert pre_execution $pre_execution | upsert pre_prompt $pre_prompt)
  if (($current_hooks.command_not_found? | describe) == "nothing") {
    $hooks.command_not_found = {|name|
      $name | save --force --raw $env.WUT_NF
      null
    }
  } else {
    $env.WUT_TIER = "T0"
  }
  $env.config = ($current_config | upsert hooks $hooks)

  def --wrapped wut [...args] {
    if ($args | is-empty) or ($args.0 in ["fix" "f"]) {
      let rest = (if ($args | is-empty) { [] } else { $args | skip 1 })
      let out = (^wut fix --shell --session $env.WUT_SESSION ...$rest | complete)
      if $out.exit_code == 0 and ($out.stdout | str trim | is-not-empty) {
        # Nushell cannot eval a string into the current scope, so the command
        # is printed for the user to run. This is a real limitation of the
        # shell, and doctor reports it rather than pretending otherwise.
        print $"(ansi green)($out.stdout | str trim)(ansi reset)"
      }
    } else {
      ^wut ...$args
    }
  }
}
`
}

// renderXonsh is the one shell where captured output is free: on_postcommand
// receives it, with no redirection and no isatty side effects.
func renderXonsh(p Params) string {
	return `try:
    import os as _wut_os, time as _wut_time, subprocess as _wut_sp
    _wut_dir = ` + pyQuote(p.SessionsDir) + `
    if "WUT_SESSION" not in ${...}:
        $WUT_SESSION = "x%d" % _wut_os.getpid()
    _wut_state = {"seq": 0, "notfound": ""}

    @events.on_command_not_found
    def _wut_not_found(cmd=None, **kw):
        try:
            _wut_state["notfound"] = str((cmd or [""])[0])
        except Exception:
            _wut_state["notfound"] = ""

    @events.on_postcommand
    def _wut_post(cmd=None, rtn=None, out=None, ts=None, **kw):
        try:
            _wut_state["seq"] += 1
            start = int((ts[0] if ts else _wut_time.time()) * 1000)
            dur = int(((ts[1] - ts[0]) * 1000) if ts else 0)
            path = _wut_os.path.join(_wut_dir, "%s.rec" % $WUT_SESSION)
            notfound = _wut_state["notfound"]
            _wut_state["notfound"] = ""
            fields = ["1", str(_wut_state["seq"]), str(start), str(dur),
                      str(rtn if rtn is not None else 0), "xonsh",
                      _wut_os.getcwd(), "T1", notfound, (cmd or "").strip()]
            with open(path, "a", encoding="utf-8") as fh:
                fh.write("\x1f".join(fields) + "\x1e")
        except Exception:
            pass

    def _wut_alias(args):
        exe = $(which wut).strip() or "wut"
        if not args or args[0] in ("fix", "f"):
            rest = args[1:] if args and args[0] in ("fix", "f") else []
            proc = _wut_sp.run([exe, "fix", "--shell", "--session", $WUT_SESSION] + rest,
                               capture_output=True, text=True)
            if proc.returncode == 0 and proc.stdout.strip():
                execx(proc.stdout.strip())
            elif proc.stderr:
                print(proc.stderr, end="")
            return proc.returncode
        return _wut_sp.call([exe] + list(args))

    aliases["wut"] = _wut_alias
except Exception:
    pass
`
}

// renderElvish reports duration and whether the command errored, but not the
// error text, so T0 is the honest ceiling here.
func renderElvish(p Params) string {
	return `if (has-external wut) {
  if (not (has-env WUT_SESSION)) {
    set-env WUT_SESSION "e"(randint 100000 999999)
  }
  var wut-rec = ` + elvQuote(p.SessionsDir) + `/$E:WUT_SESSION".rec"
  var wut-seq = 0

  set edit:after-command = [$@edit:after-command {|m|
    var cmd = $m[src][code]
    if (not-eq $cmd "") {
      set wut-seq = (+ $wut-seq 1)
      var code = 0
      if (not-eq $m[error] $nil) { set code = 1 }
      var dur = (printf '%.0f' (* $m[duration] 1000))
      printf "1\u001f%s\u001f0\u001f%s\u001f%s\u001felvish\u001f%s\u001fT0\u001f\u001f%s\u001e" ^
        $wut-seq $dur $code $pwd $cmd >> $wut-rec
    }
  }]

  fn wut {|@a|
    if (or (eq (count $a) 0) (eq $a[0] fix)) {
      var rest = []
      if (and (> (count $a) 1) (eq $a[0] fix)) { set rest = $a[1..] }
      var out = (external wut) fix --shell --session $E:WUT_SESSION $@rest | slurp
      if (not-eq (str:trim-space $out) "") {
        eval $out
      }
    } else {
      (external wut) $@a
    }
  }
}
`
}

// renderManualPosix is the Manual class: no capture, and it says so.
//
// It costs almost nothing to ship, and dropping these shells would lose users
// for no gain. What would be wrong is letting someone here believe bare `wut`
// knows what just failed.
func renderManualPosix(p Params) string {
	return `# This shell has no usable hook surface, so WUT cannot see what just failed.
# Everything else works: give it the command, or pipe the error in.
#
#   wut fix "git psuh"
#   npm run biuld 2>&1 | wut fix
#   wut how do I squash the last 3 commits
if command -v wut >/dev/null 2>&1; then
  : # nothing to install; wut is used directly
fi
`
}

func renderManualCmd(p Params) string {
	return `:: cmd.exe has no hook surface. Use wut directly:
::   wut fix "git psuh"
::   wut how do I squash the last 3 commits
`
}

// shQuote single-quotes a value for POSIX shells.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote single-quotes for PowerShell, where a literal quote doubles.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func nuQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// elvQuote single-quotes for Elvish, where a literal quote is doubled.
//
// Elvish is not a POSIX shell. It has no backslash escape inside single
// quotes, so the POSIX '\” idiom does not produce a quote there: it ends the
// string and leaves a stray backslash, which is a parse error at shell start.
func elvQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pyQuote double-quotes for Python, which is what xonsh embeds.
//
// Not a raw string: a raw string keeps the backslash of an escaped quote, and
// one that ends in a backslash - every Windows directory path - cannot be
// written as a raw string at all.
func pyQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
