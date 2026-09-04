## Search tools

`grep -r` and `find` are blocked in this jail. Use `rg` and `fd` instead — they are
faster, respect `.gitignore`, and produce output sized for reading rather than for
scrolling past.

Plain `grep` still works: only recursive invocations are refused, so pipe filters
(`… | grep foo`) and single-file greps pass through untouched. `find` is blocked
outright; `fd` covers its common uses and `fd --exec` covers `find -exec`.

If either replacement is missing from this jail, the block turns itself off rather
than leaving you with neither — so a refusal here always has somewhere to send you.
