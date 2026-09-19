#!/usr/bin/env bash
# check-doc-shas.sh — check 3 of docs/plans/README.md's corpus-honesty sweep, corrected.
#
# WHAT THE PROSE VERSION MISSES, AND IT IS MOST OF THE CLASS IT EXISTS TO CATCH.
#
# The documented one-liner asks `git rev-parse --verify <sha>^{commit}`. That succeeds for a
# DANGLING object — one that still sits in this clone's object store but is reachable from no
# ref. A commit authored in a jail and rewritten by a rebase before the push is exactly that:
# the old object survives locally, so the check passes here and the SHA is unusable to every
# other reader, and to this one after a `git gc`.
#
# MEASURED 2026-09-18, right after a push: of 314 resolvable SHAs in `docs/`, **48 were not in
# HEAD's history** while the documented check reported all 314 fine. The README already says
# this class recurs — "a SHA can be correct when typed and dead by the time anyone reads it" —
# so the check was blind precisely where its own note says to look.
#
# The fix is one predicate: reachability, not existence.
#
#   ok       — in HEAD's history. A reader can `git show` it.
#   DANGLING — resolves here, reachable from no ref. The rewrite case; re-derive by subject.
#   OTHER-REF— resolves, reachable only from another ref (a backup branch). Pre-rebase history.
#   UNKNOWN  — does not resolve at all. Upstream revs, other projects, tree hashes, image IDs:
#              the allowlist cases the README enumerates. Reported, never called a defect.
#
# `--fix-map` prints `old new subject` for every DANGLING sha whose subject appears exactly
# once in HEAD's history, which is the input to a repoint sweep. It changes no files: run it
# AFTER the last push of a sprint, or the new SHAs dangle on the next one.

set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

mode="${1:-report}"
declare -i n_ok=0 n_dangling=0 n_otherref=0 n_unknown=0

shas=$(rg -o '`[0-9a-f]{7,40}`' docs/ internal/ packs/ cmd/ integration/ -g '!.claude' 2>/dev/null |
  sed 's/.*`\([0-9a-f]*\)`.*/\1/' | grep -v '^[0-9]*$' | sort -u)

for s in $shas; do
  if ! git rev-parse --verify --quiet "$s^{commit}" >/dev/null 2>&1; then
    n_unknown+=1
    [ "$mode" = report ] && echo "UNKNOWN   $s  (upstream rev, other project, tree hash or image id — allowlist)"
    continue
  fi
  if git merge-base --is-ancestor "$s" HEAD 2>/dev/null; then
    n_ok+=1
    continue
  fi
  subject=$(git log -1 --format='%s' "$s" 2>/dev/null)
  if [ -n "$(git for-each-ref --contains "$s" --format='%(refname:short)' 2>/dev/null | head -1)" ]; then
    n_otherref+=1
    [ "$mode" = report ] && echo "OTHER-REF $s  $subject"
    continue
  fi
  # THE ONE ALLOWLISTED DANGLE, and it is allowlisted for the right reason: the doc
  # citing it SAYS it never merged, and names this very predicate as the evidence
  # ("on a fork, never merged; `git merge-base --is-ancestor` confirms it"). A SHA
  # offered as history rather than as evidence is exactly the case docs/plans/README.md
  # tells this sweep to leave alone.
  case "$s" in
    4b84ea8*) n_otherref+=1; [ "$mode" = report ] && echo "ALLOWED   $s  $(git log -1 --format='%s' "$s")  (documented as never merged)"; continue ;;
  esac
  n_dangling+=1
  if [ "$mode" = --fix-map ]; then
    hits=$(git log --format='%h %s' HEAD | grep -cF -- "$subject")
    if [ "$hits" = 1 ]; then
      printf '%s %s %s\n' "$s" "$(git log --format='%h %s' HEAD | grep -F -m1 -- "$subject" | awk '{print $1}')" "$subject"
    else
      printf '%s ?? %s  (subject matches %s commits — resolve by hand)\n' "$s" "$subject" "$hits"
    fi
  else
    echo "DANGLING  $s  $subject"
  fi
done

if [ "$mode" = report ]; then
  echo
  echo "ok=$n_ok dangling=$n_dangling other-ref=$n_otherref unknown=$n_unknown"
  [ "$n_dangling" -gt 0 ] && exit 1
fi
exit 0
