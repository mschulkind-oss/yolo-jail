## Search tools

`grep -r` and `find` are blocked in this jail. Use `rg` and `fd` instead — they are
faster, respect `.gitignore`, and produce output sized for reading rather than for
scrolling past.

Plain `grep` still works: only recursive invocations are refused, so pipe filters
(`… | grep foo`) and single-file greps pass through untouched. `find` is blocked
outright; `fd` covers its common uses and `fd --exec` covers `find -exec`.

If either replacement is missing from this jail, the block turns itself off rather
than leaving you with neither — so a refusal here always has somewhere to send you.

Note the asymmetry: `grep` is blocked only for recursive invocations, `find` for all
of them. That is because `grep` has one flag that makes it recursive while `find` is
recursive by nature, so there is no equivalent flag to key on. Whether `find` should
instead be blocked only when it carries no depth limit is an open question —
OQ-GR-1 in `docs/plans/roadmap.md`.
