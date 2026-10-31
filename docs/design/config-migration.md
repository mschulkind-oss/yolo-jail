---
title: "Moving to the new config: the file name is the version, and one command converts everything"
date: 2026-10-25
status: accepted
stage: DECIDED
next: "The old reader's removal waits for a later release"
tags: [config, migration, versioning, release, design]
summary: "How a yolo knows which config it has, the order of releases, what `yolo config migrate` does and proves, and what every machine sees along the way, shown as transcripts. Every section is ruled. Part of the readable-config redesign."
---

# Moving to the new config: the file name is the version, and one command converts everything

**Status:** accepted 2026-10-25 (the maintainer, after reading every document: *"it all sounds
good and I want to move forward"*). Built by 2026-10-30, the folds included
([RC-D30](readable-config.md#RC-D30)); the old reader's removal and the fetched-pack refusal belong to a later
release, and the TOML release is not tagged yet (the [ledger](readable-config.md#decision-ledger)
names each). Legacy JSONC is still read for a few releases: a launch offers `yolo config migrate`,
and `"convert_later": true` in the old user file stops the offer. The transcripts are what a person would see under the
leanings.

**How to read this.** [§1](#1-what-youd-see) is the transcripts; [§2](#2-how-a-yolo-knows-which-config-it-has)
to [§5](#5-every-case) are approved. Nothing here is open; the rulings are in the
[ledger](readable-config.md#decision-ledger).

> **In short.** `config.toml` beside no `config.jsonc` is the whole version scheme for this move.
> The next release reads both formats, and keeps the old reader for a few releases; it offers the
> conversion, which refuses to write unless the config it produces is byte-equal to the one it
> read, and one key in the old user file says "later" so the offer stops; a later release drops
> the old reader.

**Reads with:** [`readable-config.md`](readable-config.md) (front door and ledger),
[`config-user.md`](config-user.md) and [`config-project.md`](config-project.md) (what the files
become), [`pack-manifests.md`](pack-manifests.md) (the pack half of the same move).

---

## 1. What you'd see

The migration, on the host:

```console
$ yolo config migrate
Found 4 files in the old format:
  ~/.config/yolo-jail/config.jsonc                        (201 lines)
  ~/.config/yolo-jail/overrides.jsonc                     (24 lines, via include_if_found)
  ~/.dotfiles/tag-work/config/yolo-jail/work.jsonc        (131 lines, via include_if_found)
  ~/code/yolo-jail/yolo-jail.jsonc                        (133 lines)
Not found here (listed in config.jsonc): ~/.dotfiles/tag-personal/config/yolo-jail/personal.jsonc.
  Run `yolo config migrate` on a machine that has it.

--- ~/.config/yolo-jail/config.jsonc
+++ ~/.config/yolo-jail/config.toml
  (170 lines → 17; 3 comments kept, in place)
--- ~/.dotfiles/tag-work/config/yolo-jail/work.jsonc
+++ ~/.dotfiles/tag-work/config/yolo-jail/work.toml
  (131 lines → 29; 6 facts folded into [providers.bedrock.every_model]; "vendor" dropped on 6 models: it follows from the id)
--- ~/code/yolo-jail/yolo-jail.jsonc
+++ ~/code/yolo-jail/yolo-jail.toml
  (133 lines → 6; 121 template comment lines dropped; "network.mode": "bridge" dropped: it is the default)

Effective config before and after: identical (checked as canonical JSON, 2,447 bytes).
Records only yolo reads, respelled in place: 3 approval records, 2 boot baselines.
Write 4 files, keeping each original as <name>.pre-migrate? [y/N] y
Converted: ~/.config/yolo-jail/config.toml, ~/.config/yolo-jail/overrides.toml, ~/.dotfiles/tag-work/config/yolo-jail/work.toml, ~/code/yolo-jail/yolo-jail.toml.
Kept the originals as ~/.config/yolo-jail/config.jsonc.pre-migrate, ~/.config/yolo-jail/overrides.jsonc.pre-migrate, ~/.dotfiles/tag-work/config/yolo-jail/work.jsonc.pre-migrate, yolo-jail.jsonc.pre-migrate; delete them once the new files work for you.
Records only yolo reads, respelled in place: 3 approval records, 2 boot baselines.
Nothing else changed. Next: `yolo check`.
```

A yolo from before the TOML release meeting a migrated config. Nothing can be shipped to it:
today's yolo has no idea a `config.toml` exists, so it finds no `config.jsonc` and starts with no
packs, saying today's line and nothing about the file beside it:

```console
$ yolo
No packs are configured, so this jail has no coding agent.
```

That is why the migration is the user's act, taken once every machine sharing the files runs the
TOML release ([§3](#3-the-order-of-releases)), and why the offer does not nag: the next release
meeting an old file offers once per launch until the person converts, or says "later" with one key
in the old user file, added by hand. Ruled 2026-10-25: *"force the user to add something to their
user level config file to acknowledge that they're using the old version so that it doesn't bug
them every time … They have to add that. It's an annoyance for them if they don't want to
convert. That's fine."*

```console
$ yolo
Deprecated config format: yolo now uses TOML, and this file is still JSONC:
  ~/.config/yolo-jail/config.jsonc  →  config.toml
The deprecated format will keep working for a few more releases.
Converting keeps the original next to it, as config.jsonc.pre-migrate.

  Preview:      yolo config migrate --dry-run
  Stop asking:  add "convert_later": true to ~/.config/yolo-jail/config.jsonc

config.jsonc is a symlink; it will be renamed to config.toml. If a dotfiles manager made it, you may need to update that too.

Convert to TOML now? [y/N] n
Keeping the JSONC file for now.
```

(`y` converts, says *"Converted. The original is next to the new file, as
config.jsonc.pre-migrate."*, and continues the launch. The symlink line shows only when a file
is reached through a symlink, and the closing report says it again once the link has moved
([RC-D41](readable-config.md#RC-D41)). A setting with no TOML spelling yet is
named with its next step; a conversion that would change what the config means is reported as a
yolo bug to file. Either way nothing is changed and the launch stops, saying to run the command
again and answer `n` to launch on the old files. With no terminal the offer is the
opening line and the two command lines, with no question. `"convert_later": true` in the old user
file silences the offer for every old file this machine reads; no command writes it. The wording
is the owner's ([RC-D35](readable-config.md#RC-D35)).)

An old key in a new file, from the same rename table the conversion uses, so nothing is
maintained for this message alone:

```console
$ yolo check
~/.config/yolo-jail/config.toml:14: `brokered` is no longer a key. Its lines are now:
  [allow.git]
  always = ["fetch"]
```

## 2. How a yolo knows which config it has

For this move the answer is settled: the format changes
([OQ-RC2](readable-config.md#decision-ledger), TOML), a format change changes the file name, and
so the name is the version. `.jsonc` is old, `.toml` is new, and nothing inside either says which
yolo wrote it. Reviewed 2026-10-25: *"We're moving to TOML, so that's the answer here. The only
question is, do we have to do something now to prepare for the eventual version 3?"*

The next shape change will be a change *inside* TOML, which keeps the name, so a yolo has to learn
from the file itself that it was written for a newer one. In this move a yolo older than the TOML
release cannot be told anything ([§1](#1-what-youd-see)); the TOML release can be built so that in
the next move it can. Three things it does for that:

1. **Its reader knows a `format` key** and refuses a value above its own, naming the upgrade. A
   file with no `format` is format 2, because format 1 is the other file name, and no migrated
   file writes the key: a line nobody acts on for years is a line every reader asks about
   (ruled 2026-10-25, [OQ-RC11](readable-config.md#decision-ledger)). The next shape change
   writes `format = 3` into the files it produces.
2. **The rename table is data, not code.** One table of old key → new lines drives the conversion,
   the refusal in [§1](#1-what-youd-see), and the next conversion, which adds rows.
3. **Each file name is one constant and one fixture helper**, where today over 400 Go files spell
   them ([§5](#5-every-case)); the next rename touches the constant.

What a format-3 file does to a format-2 yolo:

```console
$ yolo
~/.config/yolo-jail/config.toml is format 3; this yolo (0.19.0) reads format 2.
Upgrade with `yolo update`, or `just install` from a checkout. Nothing was started.
```

## 3. The order of releases

1. **The next release reads both formats, and keeps the old reader for a few releases.** Ruled
   2026-10-25: *"we're going to ship toml in the next version"* and *"answering no needs to launch
   with the old file at least for a few versions. we need to maintain both."* There is no
   release before it that only knows the names. It offers the conversion at launch; `n` launches
   on the old file as before, and `"convert_later": true` in the old user file stops the offer
   ([§1](#1-what-youd-see)). Both files in one place are still refused ([§5](#5-every-case)); the
   two readers never merge.
2. **A later release drops the old reader** and refuses an old file naming `yolo config migrate`.
3. **Every machine that shares dotfiles upgrades to the TOML release before any of them
   migrates**, because one machine's migration rewrites the file every other machine reads, and a
   yolo older than that release starts empty on a migrated file. Step 1's window is what lets
   that happen without pressure; the offer says so. A machine that fell behind anyway has the
   `<name>.pre-migrate` copy the migration leaves ([§4](#4-what-yolo-config-migrate-does)), one
   `mv` from working again. Reviewed 2026-10-25, on whether anything more was open here:
   *"Haven't we solved this from all the other rulings?"* Yes
   ([OQ-RC6](readable-config.md#decision-ledger)).

## 4. What `yolo config migrate` does

1. **Proves it lost nothing.** Effective config before (old files) and after (the files it would
   write), as canonical JSON in the new vocabulary; it refuses to write unless they are equal, and
   prints any difference key by key. A user config's `brokered` table converts to the
   `[allow.<source>]` tables of [config-user.md §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
   through the capabilities the declaring pack maps, and is proved against the grants the brokers
   resolve from each side ([RC-D42](readable-config.md#RC-D42)).
2. **Rewrites each file where it is, and never merges.** `config.jsonc` → `config.toml`, each
   include → its `.toml` twin with the include entries respelled; through a symlink, so the
   dotfiles copy changes, and the link itself is renamed to the new name
   ([RC-D41](readable-config.md#RC-D41)); a file it cannot reach is listed with the command to run where it can;
   each original kept as `<name>.pre-migrate`, which the output says may be deleted once the new
   files work. A renamed file whose old name git ignored and whose new name it does not is added
   to the clone's own `.git/info/exclude`, never to the tracked `.gitignore`, and the plan says
   so; where git cannot be asked, the plan names the line to add instead
   ([RC-D33](readable-config.md#RC-D33)). The same rule covers each `<name>.pre-migrate` backup,
   and no lock file is left beside any config file: the output names every change it made
   ([RC-D34](readable-config.md#RC-D34)).
3. **Keeps comments, in order.** A comment is never read as belonging to a key; each is written
   back in document order, before whatever key follows it in the new file, and nothing is dropped.
4. **Drops written defaults and folds repeated facts**, each as its own hunk, only while the
   equality check holds. Built: a written default is dropped when it equals what the readers answer
   for an absent key and only one file writes it. A fact every model row of a provider writes alike
   is folded into its `every_model`, and a `vendor` the id names is dropped, each proved against the
   provider table a launch resolves ([RC-D30](readable-config.md#RC-D30)).
5. **Respells the records only yolo reads**: the approval records under
   `~/.local/share/yolo-jail/approvals/` and each workspace's `.yolo/config-boot.json`. Without
   this every migrated workspace meets a full-file approval prompt once, and a launch with no
   terminal is refused until someone answers. A record it cannot read is left, and that workspace
   prompts once, which the output says.
6. **Runs once, on request.** The launch offers it; nothing runs it unasked. On the host for the
   user files; in a jail only for the project's.
7. **Proves the next launch accepts the converted files before it touches anything**
   ([RC-D44](readable-config.md#RC-D44)). It stages the converted set off to the side, reads it
   the way a launch would read the layout after migration, with all scopes together, and runs the
   launch's config checks over it. Any refusal the old files do not already meet refuses the
   migration, which then says *nothing was changed*, lists the problems and gives the next step.
8. **Converts only what the config reaches** ([RC-D46](readable-config.md#RC-D46)). The owner's
   ruling (2026-10-31): *"not included. not touched. we only convert included stuff."* A
   `--user-layer` file, and any file no include names, is left byte for byte as it is, and a JSONC
   layer keeps loading on top of a TOML user config.

### The commit steps

The owner's ruling (2026-10-31, [RC-D45](readable-config.md#RC-D45)): *"I don't want the converter
to just restore the originals, I want it to never touch them. When we're gonna touch them, we
should know it's gonna work, because it could get killed in the middle of rolling back."* So
nothing is ever restored, and no original's bytes are rewritten. Each step below names the state a
kill right after it leaves. The **journal** is `.config-migrate-journal.json` in the user config
directory, or in the workspace's `.yolo` when only the workspace converts. It lists the remaining
moves and the hash of the bytes each one carries.

| Step | What it does | A kill after it leaves |
| :--- | :--- | :--- |
| 1. Check | Takes the directory locks, refuses a file that changed since the plan, and refuses a name already taken | Nothing changed |
| 2. Stage | Writes each converted file beside its destination as `.<name>.migrate-staged`, and adds the git ignore lines | The old layout, loading exactly as before; the next run replaces the staged files |
| 3. Journal | Writes the journal | The old layout plus the journal: the launch and `yolo check` refuse, naming `yolo config migrate` |
| 4. Included files | Moves each converted file that only an include reads to its name, and makes its symlink twin | The old layout plus files nothing reads yet; refused as in step 3 |
| 5. Top-level files | Respells each TOML parent: a hard link keeps the original as `<name>.pre-migrate`, then a rename puts the staged file over the name. Places each `config.toml` and `yolo-jail.toml`, then moves its original aside or removes its old symlink | Part old, part new. Between placing a top-level file and moving its original aside, the reader meets both names, and its sibling refusal names the interrupted migration rather than asking you to choose one |
| 6. Originals | Moves each original that nothing reads any more to `<name>.pre-migrate`, and removes its old symlink | The new layout plus the journal; refused as in step 3 |
| 7. Done | Removes the journal, reads the written files back the way a launch reads them, and respells the records only yolo reads | The new layout, loading exactly as after; a record left unrespelled asks once at the next launch |

**The next run finishes forward.** `yolo config migrate` finds the journal and makes each move
again: a move already made is found made, and one cut off midway (both names present after a hard
link) is completed. It never moves a file back. If a file changed since the plan, it refuses and
names that file, and the journal stays. A step that fails in a live run (for example, a full disk)
leaves the same state as a kill, says *"the migration stopped part way … Nothing was put back"*,
and names the same command. A finished run says *"Converted: …"* and *"Kept the originals as …"*.

Approved 2026-10-25 ([OQ-RC18](readable-config.md#decision-ledger)): *"the equality check is
what makes the folds safe, and a conversion that leaves the shrinking for later leaves the
misreadings in place for as long."* An earlier item had the migration delete a leftover
`~/.config/yolo-jail/config.lua`; it was dropped in the same review round. That file fed a Lua
transform that rewrote the composed agent config after merging, removed with no deprecation
window ([OQ-LT1](../reference/pack-system.md#oq-lt1)); a leftover is never read, and a migration
that deletes a file it does not convert is code written for nobody.

## 5. Every case

- **Both `config.toml` and `config.jsonc` in one place:** refused, naming both and the command.
  Formats are never merged.
- **An old key in a new file:** refused, naming the replacement already respelled, from the
  conversion's own rename table. Reviewed 2026-10-25: *"If we have this mapping anyway for the
  conversion, we might as well use it for nice happy path errors."* No aliases; nothing is
  maintained for the message alone.
- **An old file under a yolo that reads both:** read, with the offer at each launch until
  `"convert_later": true` is in the old user file ([§3](#3-the-order-of-releases) step 1).
- **`"convert_later"` in a TOML file:** refused; it has nothing to say there.
- **A project file in a jail:** an agent may run the migration on it.
- **Shipped packs:** every shipped pack is a `pack.toml`, and `TestNoShippedPackHasAJSONManifest`
  fails a `pack.json`. The last ones held a value the grouped format first had no spelling for:
  a provider option declared with no default (a JSON null; dropping the key un-declares it and
  `""` hands a derive a value), a managed key deleted by a null, and a program whose
  `install_hints` name only some managers (the grouped reader gives an unwritten manager the bin,
  inventing `apt install pi` for a program installed as `pi-coding-agent`). Each now has one
  ([RC-D25](readable-config.md#RC-D25)), and `yolo pack migrate`'s equality proof compares install
  hints as the readers produce them, so an invented hint is a difference.
- **A local pack in the old format:** refused, naming `yolo pack migrate <path>`
  ([`pack-manifests.md`](pack-manifests.md#5-the-migration)).
- **A yolo older than the TOML release:** starts empty on a migrated file; nothing reaches it
  ([§3](#3-the-order-of-releases)).
- **Files only yolo reads** stay JSON in the new vocabulary; a drift report says `allow.git.push`,
  never `brokered.git.grants`. The jail's inherited copy of the user config becomes TOML.
- **An error in a new file** names `file:line:col` and the key.
- **The repository's own references:** over 400 Go files name the old file names, and only the
  workspace name has a constant; one fixture helper is what stops the next format change from
  touching 400 files (in the seed).
- **A fetched pack in the old format**, once the old reader is gone: refused, naming the two ways
  forward; there is one reader. Ruled 2026-10-25 ([OQ-RC8](readable-config.md#decision-ledger)):
  *"This is fine because I think I'm the only one that has any packs that could be shared, and I
  will be converting them immediately."*

  ```console
  $ yolo
  acme/tools (https://github.com/acme/tools) has a pack.json this yolo no longer reads.
    Ask its author to run `yolo pack migrate`, or fork it and run that yourself. Nothing was started.
  ```

## 6. Principles

1. **The name is the version** for a format change: `.jsonc` is old, `.toml` is new. A change
   inside TOML is told by the `format` key the first TOML reader already knows and no format-2
   file writes.
2. **A conversion proves equality or writes nothing.**
3. **No aliases, ever.** An old spelling is refused naming the new one, from the table the
   conversion uses.
4. **Nothing migrates unasked.** The launch offers; a person answers, `n` is a launch, and
   "later" is a key the person writes.
5. **The old reader stays for a few releases, and that window is for upgrading every machine.**
   Nothing reaches a yolo older than the TOML release.

## Appendix: evidence

- **No version field exists today**, and old files are recognized only by content digests
  (`internal/config/launchorigins.go`, `drift.go`). Unknown top-level keys are a hard error on the
  host; retired keys (`use_profiles`, `agents` and six others) are refused each with its
  replacement (`internal/config/validate.go`); nothing rewrites a config file.
- **The approval snapshot is canonical JSON** that must not change by one byte, or every
  workspace prompts ([`config-safety.md`](../reference/config-safety.md)); renaming keys changes
  it, which is why step 5 of [§4](#4-what-yolo-config-migrate-does) exists.
- **Prior art on versioning.** ESLint's flat config is a new file name plus a migrate command.
  Docker Compose's `version` key switched semantics between 2 and 3, confused users for years,
  and is now ignored with a warning. Ruff kept reading old keys forever with a warning. Renovate
  migrates in memory and writes back only through an opt-in PR. Cosmos SDK's `confix` is a Go
  tool that migrates TOML config between versions. The lesson each tool drew: change the name for
  a real shape change, make the rewrite explicit and reviewable, and avoid a version key that
  changes semantics.
- **Manifests at yolo ≤ 0.11.1 are decoded strictly**, so any new manifest field refuses the pack;
  a new manifest shape is a new file name however it is done, and an older yolo meeting a folder
  with only `pack.toml` treats it as manifest-less (`matt-craft` is that today).
- **A zero-byte `~/.config/yolo-jail/config.lua` exists on at least one machine.** The Lua
  transform that read it is gone and the file is never read
  ([OQ-LT1](../reference/pack-system.md#oq-lt1)); the migration leaves it alone.
- **Include paths are relative to the including file**, and one starting with `~` or `/` is
  refused (`internal/config/load.go`); the migration respells each entry in place.
