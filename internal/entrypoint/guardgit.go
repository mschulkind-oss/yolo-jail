package entrypoint

// guardGit judges git clean's scope, not Git's dry-run output (which is human text,
// not a filename protocol). Git itself resolves the global options and worktree;
// only literal pathspecs are admitted when that worktree can reach protected state.
const guardGit = `# Query Git with exactly the original global options, replacing the subcommand.
# Rotate the prefix to the end, then discard the original subcommand and its args.
_yg_git_query() {
  _yg_q=$1 _yg_rotate=$2 _yg_drop=$(($3 - $2)); shift 3
  while [ "$_yg_rotate" -gt 0 ]; do
    _yg_first=$1; shift; set -- "$@" "$_yg_first"
    _yg_rotate=$((_yg_rotate - 1))
  done
  while [ "$_yg_drop" -gt 0 ]; do shift; _yg_drop=$((_yg_drop - 1)); done
  "$_yg_git_real" "$@" rev-parse "$_yg_q" 2>/dev/null
}
_yg_git_find() {
  _yg_gc=0 _yg_clean= _yg_complex=${GIT_ICASE_PATHSPECS:-}
  while [ "$#" -gt 0 ]; do
    case $1 in
      -C|-c|--git-dir|--work-tree|--namespace|--config-env)
        [ "$#" -ge 2 ] || return
        shift 2; _yg_gc=$((_yg_gc + 2)); continue ;;
      --icase-pathspecs) _yg_complex=1 ;;
      -C?*|-c?*|--git-dir=*|--work-tree=*|--namespace=*|--config-env=*|--exec-path=*|--super-prefix=*) ;;
      --no-pager|--paginate|-p|-P|--bare|--no-replace-objects|--literal-pathspecs|--glob-pathspecs|--noglob-pathspecs|--no-optional-locks|--no-lazy-fetch|--no-advice) ;;
      clean) _yg_clean=1; return ;;
      *) return ;;
    esac
    shift; _yg_gc=$((_yg_gc + 1))
  done
}
# Git normalizes dot components lexically before matching pathspecs. rm/mv instead
# walk the filesystem: link/../.yolo must not be judged through link's target here.
_yg_git_path() {
  _yg_tail=$1 _yg_norm=
  while [ -n "$_yg_tail" ]; do
    case $_yg_tail in
      */*) _yg_part=${_yg_tail%%/*}; _yg_tail=${_yg_tail#*/} ;;
      *) _yg_part=$_yg_tail; _yg_tail= ;;
    esac
    case $_yg_part in
      ''|.) ;;
      ..) _yg_norm=${_yg_norm%/*} ;;
      *) _yg_norm=$_yg_norm/$_yg_part ;;
    esac
  done
  _yg_source "${_yg_norm:-/}"
}
_yg_git_clean() {
  _yg_dry= _yg_end= _yg_want= _yg_paths=0 _yg_unknown=
  # First find the final dry-run setting. An exclusion's value is not an option.
  for _yg_a in "$@"; do
    if [ -n "$_yg_want" ]; then _yg_want=; continue; fi
    if [ -z "$_yg_end" ]; then
      case $_yg_a in
        --) _yg_end=1; continue ;;
        --dry-run) _yg_dry=1; continue ;;
        --no-dry-run) _yg_dry=; continue ;;
        --exclude) _yg_want=1; continue ;;
        --exclude=*|--force|--no-force|--interactive|--no-interactive|--quiet|--no-quiet) continue ;;
        --*) _yg_unknown=1; continue ;;
        -?*)
          _yg_c=${_yg_a#-}
          while [ -n "$_yg_c" ]; do
            _yg_ch=${_yg_c%"${_yg_c#?}"}; _yg_c=${_yg_c#?}
            case $_yg_ch in
              n) _yg_dry=1 ;;
              e) [ -n "$_yg_c" ] || _yg_want=1; break ;;
              d|f|i|q|x|X) ;;
              *) _yg_unknown=1 ;;
            esac
          done
          continue ;;
      esac
    fi
    _yg_paths=$((_yg_paths + 1))
  done
  # An unknown option may be Git's abbreviation of --no-dry-run, so it must
  # not inherit a previously seen -n's exemption.
  if [ -n "$_yg_dry" ] && [ -z "$_yg_unknown" ]; then return 0; fi
  # A repository wholly outside the protected path set needs no pathspec policy.
  if ! _yg_hit "$_yg_root"; then return 0; fi
  if [ -n "$_yg_unknown" ]; then
    _yg_refuse "git clean" "this guard cannot judge these clean options safely; use git clean -n to inspect, then clean an explicit ordinary path."
  fi
  if [ "$_yg_paths" -eq 0 ]; then
    _yg_source "$_yg_scope"
    return 0
  fi
  _yg_end= _yg_want=
  for _yg_a in "$@"; do
    if [ -n "$_yg_want" ]; then _yg_want=; continue; fi
    if [ -z "$_yg_end" ]; then
      case $_yg_a in
        --) _yg_end=1; continue ;;
        --exclude) _yg_want=1; continue ;;
        --*) continue ;;
        -?*)
          _yg_c=${_yg_a#-}
          while [ -n "$_yg_c" ]; do
            _yg_ch=${_yg_c%"${_yg_c#?}"}; _yg_c=${_yg_c#?}
            if [ "$_yg_ch" = e ]; then [ -n "$_yg_c" ] || _yg_want=1; break; fi
          done
          continue ;;
      esac
    fi
    case $_yg_a in
      *\**|*\?*|*\[*|*\]*|*\\*|:*)
        _yg_refuse "$_yg_a" "this guard cannot judge wildcard, escaped or magic pathspecs safely; use git clean -n, then an explicit ordinary path." ;;
    esac
    if [ -n "$_yg_complex" ] && [ "$_yg_complex" != 0 ]; then
      _yg_refuse "$_yg_a" "this guard cannot judge case-insensitive pathspecs safely; use git clean -n, then a case-sensitive explicit ordinary path."
    fi
    case $_yg_a in /*) _yg_git_path "$_yg_a" ;; *) _yg_git_path "$_yg_scope/$_yg_a" ;; esac
  done
}
_yg_git() {
  _yg_total=$#
  _yg_git_find "$@"
  [ -n "$_yg_clean" ] || return 0
  _yg_root=$(_yg_git_query --show-toplevel "$_yg_gc" "$_yg_total" "$@") || return 0
  _yg_prefix=$(_yg_git_query --show-prefix "$_yg_gc" "$_yg_total" "$@") || return 0
  _yg_root=$(_yg_dir "$_yg_root") || _yg_refuse "git clean" "cannot resolve the worktree; use git clean -n to inspect it."
  _yg_scope=$_yg_root/$_yg_prefix
  _yg_shift=$((_yg_gc + 1))
  while [ "$_yg_shift" -gt 0 ]; do shift; _yg_shift=$((_yg_shift - 1)); done
  _yg_git_clean "$@"
}
# Isolate the argument parser's working variables from the real invocation's argv.
(_yg_git "$@") || exit $?
`
