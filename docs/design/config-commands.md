---
title: "Who writes the file: one command instead of 'edit your config', and the format it writes"
date: 2026-10-25
status: accepted
stage: BUILT
next: "Graduate into the readable-config system doc, with the readable-config family"
tags: [config, cli, editing, toml, jsonc, design]
summary: "About sixty places tell a person or an agent to hand-edit a config file. This doc shows the command each would print instead, what it does to the file (one hunk, every other byte untouched), the verbs, and the format underneath. Each section ends with its question. Part of the readable-config redesign."
---

# Who writes the file: one command instead of "edit your config", and the format it writes

**Status:** accepted 2026-10-25 (the maintainer, after reading every document: *"it all sounds
good and I want to move forward"*). Built by 2026-10-30, the shipped loophole manifests' TOML form included (`9839d26d3`, RC-D28), and
shipped in v0.16.0 (`a95fd217d`); the [ledger](readable-config.md#decision-ledger) names each. UNMEASURED by this re-check, which read the commits and the tree and ran nothing. Legacy JSONC is still read for a few
releases: a launch offers `yolo config migrate`, and `"convert_later": true` in the old user file
stops the offer. The sessions below are what a
person or an agent would see.

**How to read this.** [§1](#1-what-youd-see) is the transcripts; [§2](#2-the-sixty-places-this-replaces)
is what they replace; [§4](#4-the-commands) and
[§5](#5-the-format-underneath) record what was approved.

**Needs your ruling:** None. Every section is approved ([ledger](readable-config.md#decision-ledger)).

> **In short.** A command you typed changes the bytes of one value in the file you named, and
> nothing else. That is what makes editing your config not a mess, and it works in TOML or JSONC.

**Reads with:** [`readable-config.md`](readable-config.md) (front door and ledger),
[`config-user.md`](config-user.md) and [`config-project.md`](config-project.md) (the files these
commands write), [`config-migration.md`](config-migration.md) (the one command that rewrites a
whole file, once).

---

## 1. What you'd see

Adding a pack, on the host:

```console
$ yolo pack add local/matt
~/.config/yolo-jail/config.toml
@@ -5,2 +5,3 @@
   "local/matt-fzf", "local/matt-mods", "local/matt-agentwork",
+  "local/matt",
   { git = "https://github.com/mschulkind-oss/matt-craft" },
Added "local/matt" (found at ~/.config/yolo-jail/packs/matt). It is read at the next launch.
```

Removing one. Only the item's own bytes change; a comment is never read as belonging to anything,
so the one above the line stays where it is, yours to keep or delete:

```console
$ yolo pack remove journal
~/.config/yolo-jail/config.toml
@@ -3,2 +3,2 @@
   # journal: read the host's systemd journal from inside the jail
-  "pi", "local/pi-fork", "codex", "journal", "host-processes",
+  "pi", "local/pi-fork", "codex", "host-processes",
Removed "journal".
```

An agent in a jail asking for a tool. The project file is the agent's to edit, and adding a
word to a list is not a job for a command; what follows the edit is, and `yolo check` does it:

```console
$ yolo check --no-build
yolo-jail.toml: install.nix gained "just". This lands at the next `yolo config apply` (run by the
  human on the host, in this workspace) and rebuilds the image.
```

The same agent touching a user-scope key:

```console
$ git fetch
fatal: no read grant: the host has not allowed git fetch for jails.
  Ask the human to run this directly in a host terminal, not through `yolo host-run`:

    yolo allow git fetch
```

Granting a read on the host, and the approval that follows, which today is two steps:

```console
$ yolo allow git fetch
~/.config/yolo-jail/config.d/work.toml
@@ -3,2 +3,5 @@
 packs = ["bedrock", "aws-auth", "copilot", "claude"]
+
+[allow.git]
+always = ["fetch"]
Written to config.d/work.toml, the file that already holds [allow].
This takes effect when a workspace approves it. Approve now for this workspace? [y/N] y
  + git: fetch from mschulkind-oss/yolo-jail (from your user config, for every workspace)
Approved. The running jail's broker takes it at its next call.
```

Turning something off in one project:

```console
$ yolo config unset mcp_servers.tavily --local
/workspace/yolo-jail.local.toml
@@ -1,2 +1,3 @@
 # This machine only; not committed.
+mcp_servers.tavily = "off"
Written. Your config adds tavily; this project's jail will not see it.
```

Where did this value come from:

```console
$ yolo config why active_provider.claude
active_provider.claude = "native"
  set in  ~/.config/yolo-jail/config.d/overrides.toml:9
  default would be: active_provider.default, "bedrock"
```

## 2. The sixty places this replaces

Grouped by the edit asked for, most frequent first ([appendix A.1](#a1-the-inventory) has the
sites):

1. add a pack to `packs` (about 14 messages, most pack READMEs)
2. add the broker read grant `{"kind": "read", "repo": "*"}` (one message from about 11 call
   sites, plus the git and gh briefings)
3. turn a loophole on, or set its settings (about 10)
4. add `packages`, `mise_tools` or `resources` (the briefing on every launch)
5. move a user-scope-only key out of the project file (about 7)
6. add or remove a `branch` or `create-branch` grant (about 6)
7. set an `aws-auth` setting (about 6)
8. add a repo to `brokered.github.repos`
9. `programs.overrides`, `runtime`, a provider model, and nine one-off keys

Only the per-workspace loophole switch has a command today (`yolo loopholes enable`), and it
writes its own file. Nothing else in yolo writes a file a person also writes.

## 3. What a command does to the file

A **splice** (coined here) parses the file only to find the byte range of one value, rewrites that
range, and leaves every other byte as it was. It is not a round trip through a parser and a
printer, which is what reformats a file.

1. A new key goes at the end of its table; a new table at the end of the file after one blank
   line; a new list item after the last, with the list's own separator and line-breaking copied
   from its neighbors; a removed key takes its own line and its trailing comma or newline, and
   nothing else. A comment is never read as belonging to a key or an item; none is moved or
   removed by any command.
2. Indentation is copied from the nearest sibling; in an empty file, two spaces for JSONC and
   none for TOML.
3. It writes to a temporary file and renames, as the per-workspace file does. Two commands at
   once serialize on one lock; the second re-reads and re-splices.
4. After the write it re-parses the result and compares the effective config with the one it meant
   to produce. A mismatch restores the original bytes and refuses, printing both.
5. An item already present is a no-op that says so; so is a key absent on removal.
6. It never reorders, never reformats a line it did not write, never writes a key it cannot
   validate, never touches a file it did not name in its output.
7. `--dry-run` prints the hunk and writes nothing. Without it the command writes and prints the
   hunk.

Where it writes: a user-scope-only key to the user config, in the file that already holds its
section ([`config-user.md` A.3](config-user.md#a3-four-files-not-one)); a key both scopes accept
to the project config; `--user`, `--project`, `--local` and `--file` override; in a jail, a
user-scope key is refused with the `host-run` line. What happens next is what happens today: a
project edit lands at the next `yolo config apply` or launch through the same y/N gate; a
user-config edit is trusted, except a grant, which offers its approval.

**Approved 2026-10-25.** On 2026-10-01 the maintainer ruled that yolo never edits the user
config, because *"it always leaves that a mess"*; a one-hunk edit the user asked for is neither a
mess nor a side effect, and a companion file could add but never remove, since lists merge by
union. State yolo writes on its own, such as the per-workspace switch, still goes in a file yolo
owns beside yours.

## 4. The commands

A command earns its place by doing more than a text edit. An agent, or a person, can add a word
to a list in a TOML file; what they cannot do by hand is resolve a pack's source, chain a grant
into its approval, validate a provider name against the declared ones, write a removal into the
right lower layer, or edit the user config from inside a jail. Those are the commands, named for
the decision, in the style of `gh`:

```console
$ yolo pack add local/matt                 # resolves the source, refuses a duplicate, says when it is read
$ yolo pack remove journal
$ yolo allow git fetch                     # user config, then offers the approval that makes it live
$ yolo allow git on_request push
$ yolo request git push main               # project config, then the check and the apply it needs
$ yolo request github repos acme/other
$ yolo active-provider default bedrock     # validated against the providers the selected packs declare
$ yolo active-provider claude native
$ yolo config unset mcp_servers.tavily --local   # writes mcp_servers.tavily = "off" into the right layer
$ yolo config migrate                      # the one whole-file rewrite, once
$ yolo config why active_provider.claude   # which file set it
```

**Approved 2026-10-25.** Underneath the named verbs sits one generic verb for any key, which is
what a message prints when no named verb covers its key, and what a pack's own `allow.<pack>`
settings get, since a pack cannot ship a verb:

```console
$ yolo config set allow.aws-auth.profile my-sso-profile
$ yolo config add allow.git.on_request push
$ yolo config unset mcp_servers.tavily --local
```

The named verbs are sugar over it. The sixty messages of [§2](#2-the-sixty-places-this-replaces)
print the named verb where one exists and the generic one where none does, and in a jail the
`host-run` line that runs it on the host. An agent editing a project file by hand is still fine;
`yolo check` names the fix when a line is wrong.

**Approved 2026-10-25:** `yolo config` means yolo's own config. The six composition commands
(`ls`, `render`, `diff`, `reset`, `promote`, `capture`) move to `yolo surface …`, and each retired
spelling prints its replacement for one release.

## 5. The format underneath

The same excerpt in each format:

```jsonc
{ "allow": { "git": { "always": ["fetch"], "on_request": ["push"] } },
  "active_provider": { "default": "bedrock", "claude": "native" },
  "providers": { "bedrock": { "region": "us-east-1",
    "models": { "global.openai.gpt-6.1-sol": { "name": "Sol 6.1", "context": 272000 } } } } }
```

```toml
[allow.git]
always = ["fetch"]
on_request = ["push"]

[active_provider]
default = "bedrock"
claude = "native"

[providers.bedrock]
region = "us-east-1"
models."global.openai.gpt-6.1-sol" = { name = "Sol 6.1", context = 272_000 }
```

```yaml
allow: { git: { always: [fetch], on_request: [push] } }
active_provider: { default: bedrock, claude: native }
providers:
  bedrock:
    region: us-east-1
    models: { global.openai.gpt-6.1-sol: { name: Sol 6.1, context: 272000 } }
```

**Approved 2026-10-25: TOML**, for every file a person writes. What decided it: the shape
removes two-thirds of the lines whatever the format; TOML keeps its one-line tables when a tool
rewrites it and JSONC grows back to 133 lines; YAML reads `python: 3.10` as the number 3.1, which
a mise version is; and TOML is already vendored and used (`internal/tomlx`,
`internal/agentcfg/codec`). Files only yolo reads stay JSON. This reverses
[MN-D1](manifest-language.md#MN-D1) (2026-09-30), which kept the configs on JSONC until a
proposal said otherwise.

## 6. Principles

1. **An edit changes the bytes it was asked to and no others.** Comments, indentation, blank
   lines and key order survive because they were never read as anything but bytes.
2. **A command you typed may write your file; a side effect never does.** State yolo writes on
   its own, such as the per-workspace switch, goes in a file yolo owns beside yours.
3. **A command earns its place by doing more than a text edit.** A key with no verb is a line
   someone writes, and `yolo check` names the fix when it is wrong.
4. **A comment is never read as belonging to anything.** No command moves or removes one.
5. **Every stop prints the command.** Each of the sixty messages prints the one line that does
   the edit, or the `host-run` line when it must happen elsewhere.
6. **A write finishes the step.** It runs `yolo check` after itself, and a grant offers its
   approval.
7. **What a person reads is in a person's format; what only yolo reads is JSON.**

## Appendix: evidence

### A.1 The inventory

About sixty sites in `internal/` and `cmd/`, two always-on briefing sections (the git and gh
packs'), the two built-in skills, and the pack READMEs. The read-grant message is one function,
`BrokerGrantAdvice` in `internal/config/brokered.go`, reached from the broker daemons, the
loophole commands and the validators; the tool-request paragraph is `internal/jailcontent/briefing.go`;
the gh-repo message is `internal/ghbroker/scope.go`. `yolo init` refuses to touch an existing
file, and `yolo config promote` refuses a `pack.jsonc` outright because rewriting it through a
JSON encoder would drop every comment.

### A.2 What exists for a splice

- `json5.Locate` (`internal/json5/locate.go`) returns the byte range of a value by key path; its
  own comment says it exists for in-place edits; nothing outside its tests calls it.
- `internal/entrypoint/tomltrivia.go` scans TOML comments by position for the prism's
  read-modify-write and reattaches them, unless the value under a comment changed; `yamltrivia.go`
  does the same through the node tree.
- Every JSON write in yolo rebuilds the whole file.
- Config lists merge by union (`internal/config/load.go`), which is why a companion file can add
  and never remove.
- Parse errors today name a byte offset only (`internal/json5/json5.go`); the index a splice
  needs is the same index that gives `line:col`.

### A.3 Libraries, if one were wanted

No Go library edits TOML the way Cargo's `toml_edit` does. `creachadair/tomledit` keeps comments
but is pre-1.0, says it is not production-ready, and reformats spacing on its first write (Cosmos
SDK's `confix` uses it). `pelletier/go-toml/v2/unstable` reports positions with comments kept and
has no writer, which is all a splice needs. `tailscale/hujson` round-trips JSONC and applies
patches, but its `Format` is opinionated. `yaml.v3` is frozen upstream. None is required for the
splice; one position-reporting parse per format is.

### A.4 The ruling this meets

[OQ-BB12](boundary-broker.md#15-decision-ledger), 2026-10-01: *"I don't want to do anything that
edits the user config directly. We can edit a file that lives next to it of a different name or
whatever … I don't want to do anything ugly like trying to edit the user's configuration because
it always leaves that a mess."* Built as the per-workspace switch file; five sibling docs restate
it. The splice was ruled to fall outside it on 2026-10-25 ([OQ-RC9](readable-config.md#decision-ledger)).

### A.5 Format facts

`BurntSushi/toml` v1.6.0 and `gopkg.in/yaml.v3` are in `go.mod` and used by `internal/tomlx`,
`internal/agentcfg/codec` and the prism. Measured on the maintainer's config: today's shape 178
lines; the new shape 58 in hand-laid JSONC, 133 formatter-expanded, 53 in TOML.
