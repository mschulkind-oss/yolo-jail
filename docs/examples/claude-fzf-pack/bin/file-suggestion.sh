#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# REPLACE THIS BODY WITH YOUR REAL SCRIPT.
#
# This is a working REFERENCE IMPLEMENTATION, not the maintainer's finder. The
# real one lives at ~/.dotfiles/claude/file-suggestion.sh on the host, which is
# not visible from inside a jail (the credential boundary — see AGENTS.md
# "Limitations"), so it could not be copied in. Everything about the pack is
# structured so swapping this file is a ONE-FILE EDIT: nothing else in the pack
# references the script's internals, only its path.
#
# To adopt your own: overwrite this file, keep it executable (chmod +x), keep the
# filename (pack.json's `fileSuggestion.command` names it), then re-run
# `yolo pack lint --allow-exec <pack dir>`.
#
# ⚠ BUT READ THE CONTRACT BELOW FIRST. It was read out of the Claude Code binary
# (v2.1.220), and it is probably NOT what an existing hand-rolled script assumes:
# the query arrives as JSON ON STDIN, not as "$1".
# ---------------------------------------------------------------------------
#
# Claude Code's custom file finder, wired in via settings.json:
#
#   "fileSuggestion": { "type": "command",
#                       "command": "~/.claude/bin/file-suggestion.sh" }
#
# CONTRACT — verified against the v2.1.220 binary, not guessed. The finder runs
# through Claude's ordinary HOOK executor, so it inherits hook semantics:
#
#   stdin    ONE LINE OF JSON. The base hook input plus a `query` field:
#              {"session_id":…,"transcript_path":…,"cwd":…,"prompt_id":…,
#               "permission_mode":…,"agent_id":…,"agent_type":…,"query":"partial"}
#            `query` is what the user typed after `@`. NO POSITIONAL ARGUMENT is
#            passed — a script reading "$1" silently sees an empty query and
#            returns an unranked dump of the tree.
#   argv     none.
#   cwd      the project dir (hooks run there), so emitting RELATIVE paths is
#            right. `CLAUDE_PROJECT_DIR` is also exported.
#   stdout   one candidate path per line. Claude splits on newlines, trims, drops
#            blanks, and KEEPS ONLY THE FIRST 15 — so ranking matters far more
#            than volume, and a big limit here is wasted work.
#   exit     must be 0. A non-zero exit (or an abort) discards ALL output, so a
#            no-match `fzf --filter` exit of 1 must be swallowed, not propagated.
#   timeout  5s default, then the results are dropped. Keep it fast.
#   shell    the command string is run through bash (`spawn(cmd, {shell:true})`),
#            so `~` IS expanded by the shell and a pipeline in `command` works.
#   trust    skipped entirely until the workspace trust dialog is accepted
#            ("Skipping FileSuggestion command execution - workspace trust not
#            accepted"). yolo's claude pack pre-accepts trust for ${workspace}.
#
# WHY fd | fzf --filter: this is a non-interactive, one-shot query. `--filter`
# runs fzf's fuzzy matcher as a pure stdin filter and prints the ranked matches
# to stdout with no TTY — exactly the shape this protocol wants.

set -uo pipefail
# Deliberately NOT `set -e`: a no-match `fzf --filter` exits 1, and under -e that
# would abort the script before the "no matches is a normal answer" exit 0 below.

# Claude sends 15 to the UI; ask for a few more than that so a later `sort`/dedupe
# tweak has headroom, but not so many that fzf ranks a whole monorepo for nothing.
limit="${CLAUDE_FILE_SUGGESTION_LIMIT:-40}"

# THE QUERY: read the JSON line from stdin and pull out `query`. jq is used when
# present (correct for any escaping); the fallback is a bounded sed for the common
# case, so a host without jq degrades to "works for plain queries" rather than
# "returns nothing". `read -r -t 2` so a caller that sends no stdin at all cannot
# hang the picker.
payload=""
IFS= read -r -t 2 payload || true

query=""
if [[ -n "$payload" ]]; then
  if command -v jq >/dev/null 2>&1; then
    query="$(printf '%s' "$payload" | jq -r '.query // ""' 2>/dev/null || true)"
  else
    query="$(printf '%s' "$payload" |
      sed -n 's/.*"query"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  fi
fi

# yolo's default blocked tools are `grep` and `find` — NOT `fd`, which is the
# suggested replacement and stays unshimmed (verified: ~/.yolo-shims/ holds
# find/grep, no fd). So this script needs no bypass today. It is set anyway
# because it costs nothing and a user who later adds `fd` to
# `security.blocked_tools` would otherwise get an empty picker with no clue why:
# this script is not an agent typing a recursive search, it is the file picker,
# whose entire job is a recursive listing. Harmless outside a jail.
export YOLO_BYPASS_SHIMS=1

# fd, not find/ls: it honors .gitignore, skips .git, and stays fast on a large
# tree. --type f so a directory never appears as a completion; --hidden to keep
# dotfiles reachable, with .git excluded as the one hidden tree nobody completes
# into.
list_candidates() {
  fd --type f --hidden --follow --exclude .git . 2>/dev/null
}

if [[ -z "$query" ]]; then
  # Empty query: nothing to rank, so skip fzf entirely and just cap the list.
  list_candidates | head -n "$limit"
  exit 0
fi

# `fzf --filter` prints matches ranked best-first (it sorts by score unless
# --no-sort is given). Exit 1 means no match, which is a legitimate empty answer,
# so the explicit `exit 0` below swallows it.
list_candidates | fzf --filter "$query" | head -n "$limit"
exit 0
