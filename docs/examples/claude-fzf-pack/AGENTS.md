# claude-fzf

`@`-file completion in Claude Code is served by a custom finder this pack owns:
`~/.claude/bin/file-suggestion.sh` (an `fd | fzf --filter` pipeline), wired in
through the `fileSuggestion` key of `~/.claude/settings.json`.

**Do not edit either of those two paths in place.** In a jail
`~/.claude/bin/` is a read-only bind mount of the pack's own tree, so an edit
fails outright; and `fileSuggestion` is a *managed* key, so a value written into
`settings.json` by hand is re-asserted on the next boot. Both are symptoms of the
same thing: the pack is the source of truth.

To change the finder's behavior, edit the pack and re-apply:

```console
$ $EDITOR <pack dir>/bin/file-suggestion.sh
$ yolo pack lint --allow-exec <pack dir>     # then relaunch the jail, or:
$ yolo host apply --assert                   # to update your real home
```

The finder needs `fd` and `fzf` on PATH. In a jail both are baked into the image.
