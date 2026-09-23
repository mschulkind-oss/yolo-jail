---
title: "RUNBOOK — what a Claude plugin install actually writes, probed from the host"
status: accepted
date: 2026-09-19
tags: [runbook, host, claude, plugins, skills, synced, transition]
summary: "Three questions a jail structurally cannot answer about Claude Code's plugin machinery — what a populated installed_plugins.json holds, which sync root a real install materializes into, and what `claude plugin uninstall` removes — each with the exact command, what a useful answer looks like, and why the jail cannot get there. Carries the data-loss warning that must be read before any `yolo host apply --assert` on a host with a synced skills tree."
---

# RUNBOOK — what a Claude plugin install actually writes, probed from the host

**Status:** CURRENT — a procedure, and its three questions (Q1–Q3) are still unanswered: no real
plugin install has been probed from a host yet.

**Audience:** an agent (or the maintainer) on the **host**, not in a jail.
**Time:** ~15 minutes. **Needs:** a real Claude Code login on that host. **Writes:** nothing
outside a throwaway plugin install you undo at the end.

> [!CAUTION]
> **Check the host's `yolo` is at `113d6731` (2026-09-22) or later before any
> `yolo host apply --assert`.** That commit is the fence: `packs/claude` reserves `synced`, so
> `hostskills.Adoptions` never adopts `~/.claude/skills/synced/`, and reports it as
> `reserved (another tool's)` when it is non-empty. An older `yolo` offers it as one adoption named `synced` — a non-dot directory
> in no ownership record, whose manifests sit two levels down where `pluginpack.ManifestPath`
> does not look — and an assert then MOVES the sync root into
> `~/.config/yolo-jail/local/skills/synced/` and composes a byte-identical copy back. Nothing
> looks wrong until the NEXT apply after an upstream sync, which deletes a new skill and reverts
> an edited one. See [`synced-skill-trees.md`](../../design/synced-skill-trees.md).
>
> **`yolo host apply` WITHOUT `--assert` is a dry run** and is safe. Use it to check exposure:
> if `synced` appears as an adoption at all, this host's `yolo` predates the fence.

## Why a jail cannot answer these

`/home/agent/.claude/skills` inside a jail is a **`:ro` bind of yolo's own staging directory**
(`~/.local/share/yolo-jail/agents/<container>/skills-claude`, per `/proc/self/mountinfo`). No
vendor process can write `synced` there whatever it intends to do — so a jail observing that
`skills/synced` is absent has measured yolo's mount, not Claude Code's behaviour. That mistake
was made once already and is corrected in the design doc.

The jail's `~/.claude/plugins` is writable and does hold a bucket, which is how the identity
half was established. What it cannot hold is a **real plugin install against a real login**.

## What is already known — do not re-derive it

Measured in-jail, and reproduced across two machines sharing one identity:

- `~/.claude/plugins/synced/<org-uuid>_<account-uuid>/` exists with **the same bucket name on
  two different machines** with the same Claude identity and no shared home. The bucket is
  minted from the identity, not from an install.
- Beside it, `.bucket-<same uuids>` is a **zero-byte file** — excluded from adoption twice
  over, being both dot-prefixed and not a directory.
- On a machine with nothing installed, the bucket is **empty** and
  `installed_plugins.json` is `{"version": 2, "plugins": {}}`, while `known_marketplaces.json`
  already carries `claude-plugins-official` with an `installLocation` and a `lastUpdated`.
- **An empty bucket is still adopted** — measured against a throwaway home: it produces
  `⚠ 1 skill … would move into your local pack: synced`.

So the open questions are only about what a **real install** does.

## Q1 — what does a populated registration look like?

```console
$ cat ~/.claude/plugins/installed_plugins.json
$ cat ~/.claude/plugins/known_marketplaces.json
$ tree -a -L 3 ~/.claude/plugins ~/.claude/skills
```

**A useful answer** names, for one installed plugin: the key it is filed under, whether the
record carries a version or a commit, whether it names a marketplace, and whether anything in
it points at the materialized content. **Redact nothing structural**; if a field holds a UUID
that is an org or account identifier, say which field rather than removing it.

⚠ `tree -a` matters: the `.bucket-` markers and `.trash`/`.staging` siblings are dot-prefixed
and a plain `tree` hides exactly the entries whose dot-ness is load-bearing.

## Q2 — which sync root does a real install materialize into?

This is **the question the whole transition design turns on**. The maintainer's report is a
`skills/synced/` bucket; the jail only ever sees a `plugins/synced/` one. If an install lands
in the skills root, it meets `Adoptions` and the data-loss path above; if it lands in the
plugins root, it does not, because no pack composes `.claude/plugins`.

Take a **before** snapshot, install a throwaway plugin, take an **after** snapshot, and diff:

```console
$ find ~/.claude/plugins ~/.claude/skills -maxdepth 4 | sort > /tmp/before.txt
$ claude plugin install <a small plugin from claude-plugins-official>
$ find ~/.claude/plugins ~/.claude/skills -maxdepth 4 | sort > /tmp/after.txt
$ diff /tmp/before.txt /tmp/after.txt
```

**A useful answer** is the diff itself, plus whether the content is a copy, a symlink or a
checkout, and whether a `SKILL.md` or a plugin manifest sits at the top of what appeared.
Say which plugin you used — the answer may differ for a plugin that ships skills versus one
that ships only commands or MCP servers, and if so **that difference is the finding**.

## Q3 — what does `claude plugin uninstall` remove?

The transition the maintainer wants is not a copy: it is *identify the plugin, find its
config, remove it, and re-home it in yolo*. Whether that can lean on the vendor's own verb, or
must edit those JSON files directly, is the difference between a small feature and a
commitment to track someone else's file format.

```console
$ claude plugin uninstall <the same plugin>
$ find ~/.claude/plugins ~/.claude/skills -maxdepth 4 | sort > /tmp/after-uninstall.txt
$ diff /tmp/after.txt /tmp/after-uninstall.txt
$ cat ~/.claude/plugins/installed_plugins.json
```

**A useful answer** says which of the three it removed — the registration, the materialized
content, the marketplace entry — and whether the bucket survived. Then the part that decides
the design:

- **Is it idempotent against already-gone content?** Re-run the uninstall, or delete the
  content directory by hand first and then uninstall. If it errors on absent content, a
  transition that moved the files first cannot call it afterwards.
- **Does anything re-materialize it?** Leave the registration in place, delete the content
  directory, and start `claude` again. The maintainer's report is that it comes back; confirm
  it, and say what triggered it — launch, login, or a background sync.

## Safety

- Use a plugin you are happy to remove. Do not probe with one you rely on.
- Nothing here needs `sudo`, and nothing should touch `~/.config/yolo-jail/`.
- If `yolo host apply` (dry run) already shows `synced` as an adoption **before** you start,
  say so — it means the host's `yolo` predates the fence and was exposed independently of this
  probe.

## Where the answers go

Append them to
[`synced-skill-trees-plan.md`](../../design/synced-skill-trees-plan.md) under a dated
**Host probe** heading, verbatim — commands and output, not a summary. The design doc's [two-sync-roots section](../../design/synced-skill-trees.md#24-two-sync-roots-one-bucket-name-and-only-one-is-exposed)
then gets rewritten from measurement rather than from inference, and the three unverified
notes it currently carries can be closed or corrected by name.

If the answers contradict anything in *What is already known* above, **the host wins** and
this runbook is what was wrong.
