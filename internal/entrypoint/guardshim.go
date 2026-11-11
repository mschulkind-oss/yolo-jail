package entrypoint

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// guardshim.go renders a GUARD (a term coined in docs/design/workspace-state-safety.md §3.3,
// declared by a blocked-tool entry's `guard`, packdecl/guard.go): a shim that runs the real
// `mv` or `rm` unless an argument names protected state, or `git` unless clean's scope
// can remove it: the workspace's `.yolo`, something under it outside
// the durable dir, or a directory containing `.yolo`, and refuses only then.
//
// HOW A PATH IS JUDGED. Each argument's PARENT is resolved with `cd -P … && pwd -P` (the
// shell's own realpath, so the shim needs no `realpath` binary, which older macOS lacks) and
// the argument's last name is appended unread, so a symbolic link named as the argument is
// judged as the link, never by its target: `rm link-to-dot-yolo` removes only the link and
// passes. The two exceptions are names that do not name an entry of their parent: a trailing
// `/`, `.` or `..`, which the program itself resolves through, and so does the shim. The
// workspace is resolved the same way at run time, so a workspace reached through a link
// (macOS's /var → /private/var) compares equal.
//
// WHERE THE PATHS COME FROM, per setup. The workspace is baked in at render time from the
// launch: /workspace in a container jail, the real workspace path under macos-user (the Env's
// Workspace), and the directory `yolo host --` was started in. The durable dir is always
// `<workspace>/.yolo/durable`; $YOLO_DURABLE_DIR holds that same path in a jail and is unset
// at the host, so the shim derives it from the workspace rather than reading the variable,
// which also means unsetting or editing the variable does not widen the exemption.
//
// THE DURABLE DIR ITSELF IS GUARDED; only what is under it is free. Removing the directory
// takes every agent's worktrees with it, and only a fresh launch makes it again.

// guardScript renders the guard shim for tool (one of packdecl.GuardBins) in front of
// realBin (already shell-quoted), protecting workspace's state dir. It refuses with exit 1,
// not a blocker's 127: the tool exists and ran its own check, it is not "not found".
func guardScript(tool, realBin, workspace string) string {
	state := paths.WorkspaceStateDir(workspace)
	dur := filepath.Join(state, durable.DirName)
	verb := "move or delete"
	if tool == "mv" {
		verb = "move"
	} else if tool == "rm" {
		verb = "delete"
	}
	why := state + " holds this workspace's agent homes, including this session's own files, " +
		"so nothing in it is safe to " + verb + ". To start without it, ask the human for " +
		"`yolo --fresh-home`. Your own files under " + dur + " are yours to " + verb +
		". (YOLO_BYPASS_SHIMS=1 bypasses this shim.)"
	if tool == "git" {
		why += " Use `git clean -n` to inspect, then clean an explicit path outside protected state."
	}
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	w("#!/bin/sh")
	w("# A guard: runs the real " + tool + " unless an argument names this workspace's .yolo.")
	w(`if [ -z "$YOLO_BYPASS_SHIMS" ]; then`)
	w("unset CDPATH")
	w("_yg_tool=" + shquote.Quote(tool))
	w("_yg_ws=" + shquote.Quote(workspace))
	w("_yg_show=" + shquote.Quote(state))
	w("_yg_why=" + shquote.Quote(why))
	b.WriteString(guardBody)
	if tool == "git" {
		w("_yg_git_real=" + realBin)
		b.WriteString(guardGit)
	} else if tool == "mv" {
		b.WriteString(guardMv)
	} else {
		b.WriteString(guardRm)
	}
	w("fi")
	w("exec " + realBin + ` "$@"`)
	return b.String()
}

// guardBody is the tool-independent half: path resolution, the rule, and the refusal.
const guardBody = `_yg_abs() {
  case $1 in /*) printf '%s\n' "$1" ;; *) printf '%s/%s\n' "$(pwd -P)" "$1" ;; esac
}
_yg_dir() {
  case $1 in /*) _yg_d=$1 ;; *) _yg_d=./$1 ;; esac
  (cd -P -- "$_yg_d" 2>/dev/null && pwd -P)
}
# _yg_resolve PATH: the absolute name PATH stands for, its parent resolved and its last name
# appended unread.
_yg_resolve() {
  _yg_p=$1 _yg_slash=
  while :; do
    case $_yg_p in ?*/) _yg_p=${_yg_p%/} _yg_slash=1 ;; *) break ;; esac
  done
  case $_yg_p in
    */*) _yg_par=${_yg_p%/*} _yg_base=${_yg_p##*/} ;;
    *) _yg_par=. _yg_base=$_yg_p ;;
  esac
  [ -n "$_yg_par" ] || _yg_par=/
  if [ -n "$_yg_slash" ] || [ "$_yg_base" = . ] || [ "$_yg_base" = .. ] || [ "$_yg_p" = / ]; then
    if _yg_r=$(_yg_dir "$_yg_p"); then printf '%s\n' "$_yg_r"; return; fi
  fi
  if _yg_r=$(_yg_dir "$_yg_par"); then
    case $_yg_r in /) printf '/%s\n' "$_yg_base" ;; *) printf '%s/%s\n' "$_yg_r" "$_yg_base" ;; esac
  else
    _yg_abs "$_yg_p"
  fi
}
_yg_wsr=$(_yg_dir "$_yg_ws") || _yg_wsr=$_yg_ws
case $_yg_wsr in /) _yg_state=/.yolo ;; *) _yg_state=$_yg_wsr/.yolo ;; esac
_yg_durable=$_yg_state/durable
# _yg_hit NAME: NAME is .yolo, under it outside the durable dir, or a directory holding it.
_yg_hit() {
  case $1 in "$_yg_durable"/*) return 1 ;; "$_yg_state"|"$_yg_state"/*) return 0 ;; esac
  [ "$1" = / ] && return 0
  case $_yg_state in "$1"/*) return 0 ;; esac
  return 1
}
_yg_refuse() {
  printf '%s\n' "$_yg_tool: refusing $1: $2" "$_yg_why" >&2
  exit 1
}
_yg_source() {
  _yg_t=$(_yg_resolve "$1")
  if _yg_hit "$_yg_t"; then
    case $_yg_t in
      "$_yg_state"|"$_yg_state"/*) _yg_refuse "$1" "it is inside $_yg_show." ;;
      *) _yg_refuse "$1" "it contains $_yg_show." ;;
    esac
  fi
}
`

// guardRm judges every operand; no rm option takes a separate value.
const guardRm = `_yg_end=
for _yg_a in "$@"; do
  if [ -z "$_yg_end" ]; then
    case $_yg_a in
      --) _yg_end=1; continue ;;
      -) ;;
      -*) continue ;;
    esac
  fi
  _yg_source "$_yg_a"
done
`

// guardMv walks mv's arguments twice: once to find the destination (-t DIR, or the last
// operand), once to judge each source and where it would land.
const guardMv = `# _yg_walk PASS ARGS...: calls _yg_op_PASS INDEX OPERAND for each operand, and sets
# _yg_tdir (-t), _yg_notdir (-T) and _yg_n (the operand count).
_yg_walk() {
  _yg_pass=$1; shift
  _yg_end= _yg_want= _yg_n=0 _yg_tdir= _yg_notdir=
  for _yg_a in "$@"; do
    if [ -n "$_yg_want" ]; then
      [ "$_yg_want" = t ] && _yg_tdir=$_yg_a
      _yg_want=; continue
    fi
    if [ -z "$_yg_end" ]; then
      case $_yg_a in
        --) _yg_end=1; continue ;;
        --t*=*) _yg_tdir=${_yg_a#*=}; continue ;;
        --t*) _yg_want=t; continue ;;
        --su*=*) continue ;;
        --su*) _yg_want=s; continue ;;
        --no-t*) _yg_notdir=1; continue ;;
        --*) continue ;;
        -) ;;
        -*)
          _yg_c=${_yg_a#-}
          while [ -n "$_yg_c" ]; do
            _yg_ch=${_yg_c%"${_yg_c#?}"}; _yg_c=${_yg_c#?}
            case $_yg_ch in
              t) if [ -n "$_yg_c" ]; then _yg_tdir=$_yg_c; else _yg_want=t; fi; break ;;
              S) [ -n "$_yg_c" ] || _yg_want=s; break ;;
              T) _yg_notdir=1 ;;
            esac
          done
          continue ;;
      esac
    fi
    _yg_n=$((_yg_n + 1))
    "_yg_op_$_yg_pass" "$_yg_n" "$_yg_a"
  done
}
_yg_op_count() { _yg_last=$2; }
_yg_walk count "$@"
if [ -n "$_yg_tdir" ]; then _yg_dest=$_yg_tdir
elif [ "$_yg_n" -ge 2 ]; then _yg_dest=$_yg_last
else _yg_dest=
fi
# _yg_land NAME ARG: refuse a move that would replace .yolo or a directory holding it, or
# overwrite something already inside .yolo outside the durable dir. A new name inside .yolo
# passes: it destroys nothing.
_yg_land() {
  case $1 in "$_yg_durable"/*) return ;; esac
  case $1 in
    "$_yg_state"/*) if [ -e "$1" ] || [ -L "$1" ]; then _yg_refuse "$2" "it would overwrite $1, inside $_yg_show."; fi ;;
    *) if _yg_hit "$1"; then _yg_refuse "$2" "it would replace $_yg_show or a directory holding it."; fi ;;
  esac
}
_yg_op_judge() {
  if [ -z "$_yg_tdir" ] && [ "$1" -eq "$_yg_n" ] && [ "$_yg_n" -ge 2 ]; then return; fi
  _yg_source "$2"
  [ -n "$_yg_dest" ] || return
  if [ -z "$_yg_notdir" ] && { [ -n "$_yg_tdir" ] || [ -d "$_yg_dest" ]; }; then
    _yg_b=$2
    while :; do case $_yg_b in ?*/) _yg_b=${_yg_b%/} ;; *) break ;; esac; done
    _yg_b=${_yg_b##*/}
    if _yg_into=$(_yg_dir "$_yg_dest"); then :; else _yg_into=$(_yg_resolve "$_yg_dest"); fi
    case $_yg_into in /) _yg_land "/$_yg_b" "$_yg_dest" ;; *) _yg_land "$_yg_into/$_yg_b" "$_yg_dest" ;; esac
  else
    _yg_land "$(_yg_resolve "$_yg_dest")" "$_yg_dest"
  fi
}
_yg_walk judge "$@"
`

// renderGuard is renderBlocker for an entry carrying `guard`. ok=false writes nothing: a path
// set or tool this build does not know (a newer pack's) is skipped with a line rather than
// rendered as a plain block, which would refuse every `mv`.
func renderGuard(name, guard string, r BlockerRender, warn func(string)) (BlockerScript, bool) {
	if !slices.Contains(packdecl.KnownGuards, guard) {
		warn("not guarding " + name + ": it names the path set " + strconv.Quote(guard) +
			", which this yolo does not know; a newer yolo reads it (`yolo --version` says which this is)")
		return BlockerScript{}, false
	}
	if !slices.Contains(packdecl.GuardBins, name) {
		warn("not guarding " + name + ": a guard reads its tool's own arguments, and this yolo knows " +
			"those of " + strings.Join(packdecl.GuardBins, " and ") + " only")
		return BlockerScript{}, false
	}
	if r.Workspace == "" {
		warn("not guarding " + name + ": this launch has no workspace for it to protect")
		return BlockerScript{}, false
	}
	// The program behind the guard: Behind where the caller resolves one (`yolo host --`, whose
	// PATH may simply hold no mv, which leaves nothing to guard and nothing to say), else
	// RealBin (a jail's, which says so itself when it finds none).
	p, ok := "", false
	if r.Behind != nil {
		p = r.Behind(name)
		ok = p != ""
	} else if r.RealBin != nil {
		p, ok = r.RealBin(name)
	}
	if !ok || p == "" {
		return BlockerScript{}, false
	}
	return BlockerScript{Name: name, Content: guardScript(name, shquote.Quote(p), r.Workspace)}, true
}
