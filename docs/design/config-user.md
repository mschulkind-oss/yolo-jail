---
title: "Your config, one screen: packs, logins, and which setup each agent runs on"
date: 2026-10-25
status: accepted
stage: DECIDED
next: "Build git fetch of requested repositories, as the front door's ledger names it"
tags: [config, packs, providers, profiles, broker, grants, conventions, design]
summary: "The user config as one annotated file, today and proposed, then one section per part of it. Every section is ruled. Part of the readable-config redesign; the ledger of rulings is in the front door and the evidence is in the appendix."
---

# Your config, one screen: packs, logins, and which setup each agent runs on

**Status:** accepted 2026-10-25 (the maintainer, after reading every document: *"it all sounds
good and I want to move forward"*). Built by 2026-10-30 except git fetch of a project's requested repositories; catalog facts at
launch (C7) and a model said once reach a launch ([RC-D29](readable-config.md#RC-D29)). The
[ledger](readable-config.md#decision-ledger) names each. Legacy JSONC is still read for a few releases: a launch offers `yolo config migrate`,
and `"convert_later": true` in the old user file stops the offer. The "today" file is the maintainer's effective config as
this jail inherited it, condensed.

**How to read this.** [§1](#1-the-file-today) and [§2](#2-the-file-proposed) are the whole file;
[§4](#4-naming-a-pack-that-is-not-shipped) to [§8](#8-lines-you-never-write) take it one part at a
time. Every section is approved; the [ledger](readable-config.md#decision-ledger) says when.

> **In short.** Each section of your config names what you decided, in a word you would use, and
> says it once. The file below is the whole proposal; the prose only says why.

**Reads with:** [`readable-config.md`](readable-config.md) (the front door, the principles, and
the ledger of rulings), [`config-project.md`](config-project.md) (a project's file, which asks
within what this one allows), [`config-commands.md`](config-commands.md) (the commands that edit
these files).

---

## 1. The file today

Condensed from the maintainer's effective config. The `// read as` comments are what a cold
reader took each line to mean.

```jsonc
{
  "packs": ["pi", "~/.dotfiles/yolo-packs/pi-fork", "codex", "git", "github", "bedrock", "aws-auth",
            "~/.dotfiles/yolo-packs/matt", "git+https://github.com/mschulkind-oss/matt-craft?ref=main", "claude"],
  "loopholes": {
    "aws-auth": { "enabled": true,                       // read as: a second switch; which one is real?
                  "settings": { "profile": "my-sso-profile", "unnarrowed": true } }
  },
  "brokered": {                                          // read as: "I don't understand 'brokered'"
    "git":    { "grants": [{ "kind": "read", "repo": "*" }],   // read as: read access to every repo anywhere
                "grant_kinds": ["branch"] },                   // read as: "granting read on branches"
    "github": { "grants": [{ "kind": "read", "repo": "*" }] }
  },
  "profile":  { "*": "bedrock", "claude": null },        // read as: null = unset? default?
  "profiles": { "bedrock": { "provider": "bedrock", "model": "global.openai.gpt-6.1-sol" } },
                                                         // read as: three bedrocks; one thing or three?
  "providers": { "bedrock": { "region": "us-east-1", "models": {
    "global.anthropic.claude-opus-5-5": {
      "id": "global.anthropic.claude-opus-5-5",          // the key, again
      "vendor": "anthropic",                             // the id, again
      "input": ["text", "image"], "max_tokens": 128000, "reasoning": true,   // the same on all six models
      "context_window": 1000000, "name": "Opus 5.5",
      "cost": { "input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5 }
    }
    // … five more models, 18 lines each
  } } }
}
```

178 lines in all, to say about 20 things.

## 2. The file proposed

Approved as a shape on 2026-10-25. The line marked `?` is the one question left.

```toml
# ~/.config/yolo-jail/config.toml
packs = ["pi", "local/pi-fork", "codex", "git", "github", "bedrock", "aws-auth",
         "local/matt", { git = "https://github.com/mschulkind-oss/matt-craft" }, "claude"]
         # the only switch; local/<name> is ~/.config/yolo-jail/packs/<name>                (§4)

[allow.git]                       # what every project's jail may do with your git login  (§6)
always = ["fetch"]                # its own repositories; a project requests others       (§6)
on_request = ["push", "new_branches"]   # a project may request these; each approved at launch

[allow.github]
always = ["read"]
on_request = ["create_pr", "create_issue"]

[allow.mounts]                    # what a project's mounts may ask for beyond read-only  (RC-D24)
on_request = ["write"]            # a project may request a writable mount; each approved at launch, in red

[allow.aws-auth]                  # settings; the switch is the name in packs              (§7)
profile = "my-sso-profile"
unnarrowed = true

[active_provider]                 # the provider each agent runs on                       (§5)
default = "bedrock"
claude = "native"                 # left to itself: yolo configures nothing; claude logs in as it would alone

[providers.bedrock]               # selectable by its own name                           (§5)
region = "us-east-1"
model = "global.openai.gpt-6.1-sol"

[providers.bedrock.every_model]   # shared by each model below unless it says otherwise
accepts = ["text", "image"]
max_tokens = 128_000
reasoning = true

[providers.bedrock.models]        # the key is the id; the maker follows from it
"global.anthropic.claude-opus-5-5" = { name = "Opus 5.5", context = 1_000_000, price = { input = 4, output = 20, cache_read = 0.2, cache_write = 5 } }
"global.openai.gpt-6.1-sol"       = { name = "Sol 6.1",  context = 272_000,   price = { input = 2, output = 10, cache_read = 0.1, cache_write = 2.5 } }
# … four more, one line each
```

A project's own file requests what it wants under `[requests.git]` and `[requests.github]`, from
what `allow` here lists as `on_request` ([`config-project.md`](config-project.md)). On the maintainer's machines
this is four files, not one: `config.toml`, `config.d/overrides.toml`, and a work and a personal
file that his dotfiles put in `config.d/` on the right machines
([appendix A.3](#a3-four-files-not-one)). The whole set is 45 non-blank lines.

## 3. Principles

1. **Name the decision, not the mechanism.** `push = ["main"]`, not `{"kind": "branch", …}` under
   `brokered`.
2. **Say each fact once.** A model's key is its id; the maker follows from the id; facts every
   model shares sit in `every_model`.
3. **Write nothing that is the default.**
4. **One switch, one place.** `packs` is the switch. A heading or a declaration never stands for
   a value: a settings table selects nothing ([§7](#7-one-switch-and-the-nudge)), and a lone
   provider does not pick itself as everyone's setup.
5. **No value is a reserved string**, with one word excepted: `true`, `false`, a number, a name,
   a list of names, or `"native"` for an agent left to its own login; never a phrase yolo has to
   recognize.
6. **Two words for two scopes.** `allow` here says what every project gets and what one may
   request; `requests` in a project is the request. One outside the ceiling is not granted, and
   says so; it is never an error.
7. **Plural for a collection you fill, singular for one thing.** `packs`, `providers.<name>`,
   `mcp_servers.<name>`; `active_provider`, `allow.git`.

## 4. Naming a pack that is not shipped

Approved 2026-10-25. A pack lives in a source you named once, and a source is a directory tree of
packs, wherever it is: a folder on this machine, or a git repository, which is the same layout
fetched (what Homebrew calls a tap and Claude Code a marketplace). Only shipped packs are bare, so
two authors' `tools` cannot collide and nothing is ever renamed. There is no bare path form.

```toml
[pack_sources]                                # named once; each is a tree of <pack>/ directories
dotfiles = "~/.dotfiles/yolo-packs"
src = "~/src"
acme = { git = "https://github.com/acme/yolo-packs" }       # the default branch, locked (§8, C9)

packs = [
  "claude",                                   # shipped: bare; yolo/claude is the explicit spelling
  "local/matt",                               # ~/.config/yolo-jail/packs/matt; local is the one built-in source
  "dotfiles/matt-fzf",                        # ~/.dotfiles/yolo-packs/matt-fzf
  "src/tools",                                # ~/src/tools: a folder you named that happens to hold one pack
  "acme/tools",                               # acme/yolo-packs' tools/ directory, fetched and locked as today
  { git = "https://github.com/mschulkind-oss/matt-craft" },   # a repository that IS one pack:
]                                             #   named mschulkind-oss/matt-craft from its URL, as today
```

Two git forms, each with one reading: a repository *of* packs is a source, and a repository that
*is* a pack is listed directly. Both are fetched and locked as git packs are today
(`packs.lock.json`). `ref = "v2.1.0"` or `ref = "alice/review"` pins something other than the
default branch; with no `ref`, the default branch is read from the remote and locked.

The name is used wherever yolo names the pack back to you: the launch's pack line, `yolo pack
ls`, `explain` and `footprint`, the nudge in [§7](#7-one-switch-and-the-nudge), `pack lint`, and
a settings table for a login-serving pack, which TOML then quotes: `[allow."local/my-auth"]`. A
shipped pack's table stays bare, `[allow.aws-auth]`. How other tools name things from several
sources, and why this is Homebrew's model, is
[appendix A.5](#a5-how-other-tools-name-things-from-several-sources).

What an AGENT calls a pack's skills is a separate name, ruled in
[`pack-naming.md`](pack-naming.md#33-the-namespace--what-agents-see): every pack not shipped is
delivered under a **namespace**, its source key, its own name for `local/<pack>` and a repository
listed on its own, or a `namespace = "…"` written on that entry, so `acme/tools`' skill `review`
reaches an agent as `acme-review`. `bare = true` on a source (`dotfiles = { path = "…", bare =
true }`) or on an entry (`{ pack = "local/scratch", bare = true }`) delivers the author's own
names, and a clash is refused rather than renamed.

## 5. Which setup each agent runs on

Today three keys share one word. `providers.bedrock` says where models come from; `profiles.bedrock`
picks that provider and a starting model; `profile` maps each agent to a profile, with `"*"` for
everyone and `null` for "left to itself". Seven of the nine shipped profiles are named after
the one provider they pick, so `-p bedrock` names one of two things.

**Approved 2026-10-25:** a provider is selectable by its own name and starts on its own `model`.
A second configuration of the same provider is a *variant* of it: a different starting model, or
a different route (`via`). Provider and variant names share one namespace, and a shipped profile
that only repeats its provider's name is deleted. What a profile carried that a provider could not,
the starting model, now sits on the provider; what a variant is for is the rest.

```toml
[providers.bedrock]                            # selectable as "bedrock"
region = "us-east-1"
model = "global.openai.gpt-6.1-sol"            # where agents start

[providers.bedrock.variants.opus]              # a variant: the same provider, a different start
model = "global.anthropic.claude-opus-5-5"

[providers.bedrock.variants.bridge]            # or a different route to it
via = "wire-bridge"

[providers.openai-codex]                       # the ChatGPT login, declared by the openai-auth pack; alias lets -p codex work
alias = "codex"

[active_provider]                              # the provider each agent runs on
default = "bedrock"                            # a provider, by name; -p bedrock
claude = "bedrock/opus"                        # a variant, as provider/variant; -p bedrock/opus
pi = ["bedrock", "codex"]                      # several, switchable inside the agent, as today's list is
codex = "native"                               # left to itself: yolo configures nothing for it
```

A variant is selected as `provider/variant` and nothing shorter; the one short name a provider
offers is its `alias`, which selects the provider's own configuration and so starts on its
`model`. A converted profile that was the way you selected a provider under a short name
(`profiles.or` on openrouter) comes out as `openrouter/or`; to have `or` back, make it the
provider's alias and put the starting model on the provider
([RC-D62](readable-config.md#RC-D62)), and the conversion's report names that command when it
writes a variant for a provider with no alias.

A name is refused with the list of providers the selected packs declare, so the names are
discoverable at the point of use:

```console
$ yolo check
config.toml:19: active_provider.claude = "bedrok": no such provider. Declared: bedrock, bedrock/opus,
  bedrock/bridge, openai-codex (codex), openrouter.
```

`-p` and the selection key name the same things, as today. **Approved 2026-10-25:** the selection
is `active_provider`, and `"native"` is the one reserved word in the config: an agent set to it is
left to itself, since what claude does on its own is not one nameable thing (its subscription, an
API key, or Bedrock through its own environment). A provider may not be named `native`; declaring
one is an error.

```console
$ yolo check
error: config.toml:31: [providers.native]: "native" is reserved; it means an agent is left to its own login.
  Rename the provider.
```

## 6. What a jail may do with your logins

Today this is `brokered`: a user read grant `{"kind": "read", "repo": "*"}` and a `grant_kinds`
list in your config, and per-repository grants in a project's. The broker approves a scope per
workspace at launch, the project's own remotes plus any it names, and the grants act within it.

**Approved 2026-10-25:** the section is `allow`. It says two things, in two lists of the same
shape: what every project's jail gets (`always`), and what a project may *request* on top
(`on_request`), each request approved at launch. A project requests under `requests`.

```toml
# your config
[allow.git]
always = ["fetch"]                # every project's jail: its own repositories, as approved at launch
on_request = ["push", "new_branches"]   # a project may request these; never granted by this line

[allow.github]
always = ["read"]
on_request = ["create_pr", "create_issue"]

[allow.aws-auth]                  # the AWS login the aws-auth pack serves to the jail
profile = "my-sso-profile"
unnarrowed = true

# a project's yolo-jail.toml   (config-project.md)
[requests.git]
push = ["main"]
new_branches = true

[requests.github]
create_pr = true
repos = ["acme/shared"]           # beyond this repository's own remotes
```

**A request outside your ceiling is not an error.** Two people share a repository and one allows
pushes while the other does not; the project file is the same for both. The one who does not sees
the request reported and skipped, with the line that would allow it, and the jail runs without it:

```console
$ yolo check
yolo-jail.toml:3: this project requests push to main.
  Your config does not allow push on request, so this project runs without it.
  To allow it for every project you work on (in your config, not this file):
    yolo allow git on_request push
```

At launch the approval prompt lists each granted request as `+ git: push to main` and each
skipped one as `– git: push to main (not allowed on request by your config)`; `*` leaves the
vocabulary. The four ruled grant kinds map onto these keys ([appendix A.6](#a6-the-grant-kinds-mapped)).

**Approved 2026-10-25: `fetch` reaches the project's own repositories**, as approved at launch,
plus what the project's `requests.github.repos` names, and nothing else. An agent that wants
another repository adds it to the project file and goes through the normal route: `yolo check`,
`yolo config apply`, the prompt that names the repository, your `y`. No mechanism bypasses that
prompt, and this is not an exception to it.

```console
$ git -C /workspace/vendor/other fetch
fatal: the remote https://github.com/acme/other is not in this workspace's approved scope ...
  request it with `yolo request github repos acme/other` in the workspace, then ask the user to run
  `yolo config apply` on the host and approve it at the config-change gate
```

**A public repository passes through without asking** (ruled 2026-10-25,
[OQ-RC21](readable-config.md#decision-ledger)): the broker tries the fetch with no credential
first, and if the remote serves it, nothing of yours was lent and no prompt is owed. A private
repository outside the scope is refused as above. *"The broker exists to lend your logins; a fetch
that lends none is not its concern, and the prompt protects what it protects either way."*

```console
$ git clone https://github.com/golang/go        # public: served with no credential, nothing asked
$ git clone https://github.com/acme/private     # private: the refusal above, naming the request to add
```

## 7. One switch, and the nudge

Approved 2026-10-25. `packs` is the only switch; `loopholes.<name>.enabled` goes. A settings table
such as `[allow.aws-auth]` selects nothing. `allow.<pack>` is valid only when `<pack>` is in
`packs`; the check offers the fix first, and declined, it fails and a launch refuses.

```console
$ yolo check
~/.config/yolo-jail/config.toml:12: [allow.aws-auth] is set, but aws-auth is not in packs.
  Add it to packs? [y/N] n
error: config.toml:12: allow.aws-auth names a pack that is not selected. Add it (yolo pack add aws-auth)
  or delete the table.

$ yolo check
~/.config/yolo-jail/config.toml:12: [allow.aws-aut] names no pack. Did you mean aws-auth?
error: config.toml:12: allow.aws-aut: no such pack.
```

The reverse, a listed login pack whose table names no profile, is a warning: the pack installs,
its service serves nothing, and the check says what to set.

```console
$ yolo check
~/.config/yolo-jail/config.toml:3: packs lists aws-auth, but [allow.aws-auth] names no profile, so it serves nothing.
  Set one: yolo config set allow.aws-auth.profile <aws profile>
```

What this amends in the loophole rules, and why each one's reason survives, is
[appendix A.4](#a4-what-l4-amends).

## 8. Lines you never write

Approved 2026-10-25, all six. A convention is a default that follows from something you already
did, so the line that would say it is never written. Three rules bound every one: the explicit
key still wins when written; `yolo config show` prints what a convention supplied, as
`packs = [..., "local/matt"]   # by convention: local/ is ~/.config/yolo-jail/packs`; and no
convention widens what a jail may do with a login.

```toml
# C1  your packs folder: ~/.config/yolo-jail/packs/<name> is local/<name>
pack_sources = { dotfiles = "~/.dotfiles/yolo-packs" }       # before
packs = ["dotfiles/matt"]
packs = ["local/matt"]                                        # after, with the folder linked:
```

```console
$ ln -s ~/.dotfiles/yolo-packs ~/.config/yolo-jail/packs     # or one link per pack, or real directories
```

`~/.config/yolo-jail/packs/` is an ordinary directory; a symlink in it, or the directory itself
being one, is a path like any other. A local pack already follows symlinks wherever they point,
by rule ([OQ-NC9](../plans/notch-convergence.md#7-decision-ledger), 2026-09-28), because dotfile
managers deploy exactly this shape; a fetched pack does not. If you keep both spellings and list
both, `local/matt` and `dotfiles/matt` resolve to one directory, and yolo refuses the duplicate
naming both lines; list one.

```toml
# C2  machine files: config.d/*.toml are read after config.toml, in name order
include_if_found = ["../../.dotfiles/tag-work/config/yolo-jail/work.toml"]   # before
                                                              # after: no line; the file is in config.d/
```

```text
~/.config/yolo-jail/
  config.toml
  config.d/
    overrides.toml
    work.toml -> ~/.dotfiles/tag-work/config/yolo-jail/work.toml      # present on work machines only
```

The `tag-work/` directory is from the path of the maintainer's own work file: the layout rcm's
`rcup -t work` uses, where a `tag-<name>/` directory is linked into `~` only on machines where the
tag is on. If that is what puts `work.jsonc` on work machines today, moving it to
`tag-work/config/yolo-jail/config.d/work.toml` puts it in `config.d/` there and nowhere else, and
the include line is unnecessary. If the dotfiles link differently, C2 still holds for any file
placed in `config.d/`; only the "no line at all" part depends on the linking.

```toml
# C5  a project needs no file                                 (config-project.md §2)
# C6  a pack needs no manifest                                (pack-manifests.md §3)

# C7  a model named without facts takes the catalog's (pending the catalog check)
"global.anthropic.claude-opus-5-5" = { name = "Opus 5.5", context = 1_000_000, price = { … } }   # before
"global.anthropic.claude-opus-5-5" = { name = "Opus 5.5" }                                       # after

# C9  a git source or pack with no ref follows the repository's default branch, pinned by the lock
{ git = "https://github.com/acme/yolo-packs", ref = "main" }   # before (today: ?ref= is mandatory)
{ git = "https://github.com/acme/yolo-packs" }                 # after: read from the remote, locked at first fetch
```

C9 amends a stated rule: `internal/packsrc/addr.go` makes `?ref=` mandatory because "an unpinned
source silently changes under you." The lock file (`packs.lock.json`) records the commit, so an
omitted ref is pinned at first fetch and moves only on `yolo pack update`, as a git dependency
does in Cargo, npm and Go. The default branch is read with `git ls-remote --symref <url> HEAD`;
a remote that does not answer is refused naming the `ref` to write. C5 and C6 are shown in
[`config-project.md`](config-project.md#2-no-file-at-all) and
[`pack-manifests.md`](pack-manifests.md#3-no-manifest-at-all).

## Appendix: evidence

### A.1 Where today's config misleads

| Line today | Read cold as | What it means |
| :--- | :--- | :--- |
| `"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}` | read access to every repository anywhere | fetch from each workspace's own approved remotes |
| `"grant_kinds": ["branch"]` beside that | "granting read on branches" | the most a project may ask for: pushing to branches it names |
| `"profile": {"*": "bedrock", "claude": null}` | `null` reads as "unset" or "default" | every agent uses `bedrock` except claude, which keeps its own login |
| `"profiles": {"bedrock": {"provider": "bedrock", …}}` | three `bedrock`s: one thing or three? | a profile named after the provider it picks, plus a starting model |
| `"loopholes": {"aws-auth": {"enabled": true, …}}` with `"aws-auth"` in `packs` | two switches | the pack is installed by `packs`; its service is off until `enabled` says so |
| `"input": ["text", "image"]` beside `"cost": {"input": 10}` | two meanings of one word | what the model accepts; its price per million input tokens |

The approval prompt showed the maintainer `+ grant read * (from your user config, for every
workspace)`, which is the first row.

### A.2 Measured size

The effective user config is 170 lines of JSON plus about 8 lines of broker grants the jail's copy
omits. The models are 109 lines; `id`, `input`, `max_tokens` and `reasoning` are each written six
times. The same content: 58 lines in reshaped JSONC laid out by hand, 133 fully expanded by a
formatter, 53 in TOML. The shape removes about two-thirds of the lines whatever the format.

### A.3 Four files, not one

The jail's copy carries a `yolo-origins` line recording which host file set each key
(`internal/config/inherit.go` writes it):

| File | Sets | Reaches a machine by |
| :--- | :--- | :--- |
| `~/.config/yolo-jail/config.jsonc` | 15 packs, two mise tools | dotfiles, every machine |
| `~/.config/yolo-jail/overrides.jsonc` | `profile`, `mcp_servers` | `include_if_found` |
| `~/.dotfiles/tag-work/config/yolo-jail/work.jsonc` | `providers`, `profiles`, `loopholes`, six packs | the same, work machines only |
| `…/personal.jsonc` | the `or` profile, two OpenRouter `@200k` models, three packs | the same, personal machines; not visible from this jail |

A migration rewrites each where it is and never merges them, and the origins line is most of a
`yolo config why`.

### A.4 What L4 amends

`packs` as the only switch amends four loophole rules and keeps each one's reason
([`loophole-system.md`](../reference/loophole-system.md#principles)): R1 "presence never
activates" (a manifest on disk still activates nothing; the name in `packs` is the user saying so,
and nothing is listed by default, `run.warnIfNoPacks`); R2 "absent `default_enabled` means off"
(the manifest key goes; the list decides); R4 "host access is never on by default" (`allow` is
user scope only, and a listed login pack with no table serves nothing); R5 "enable is either
scope" (a workspace may still turn a non-login pack *off* with `off = true`, which is what the
per-workspace switch file's `enabled: false` becomes, and may still *request* under `requests`).

### A.5 How other tools name things from several sources

Checked 2026-10-25 where linked; the rest from documentation not re-read for this draft.

| Tool | A bare name means | A thing from another source | Two with one name | Model |
| :--- | :--- | :--- | :--- | :--- |
| Python imports | the first match on `sys.path` | the same bare name | silently shadowed | the failure to avoid |
| apt | one flat name across repositories | the same name | pin priorities decide, silently | the same, by policy |
| [Homebrew](https://rubydoc.brew.sh/TapFormulaAmbiguityError.html) | a core formula | `user/repo/name` from a tap | refused: "use the fully-qualified name" | **built-ins bare, the rest qualified** |
| [Claude Code plugins](https://code.claude.com/docs/en/discover-plugins) | — | `plugin@marketplace` | cannot meet | the same, with `@` |
| Docker images | `library/ubuntu` | `user/image` | cannot meet | the same |
| Nix flakes, Terraform providers | — | every input has a local name you choose, plus its source | cannot meet | all handles, no bare names |
| Cargo | a crates.io name | `key = { git = …, package = "x" }` | renamed through the key | rename on collision |
| Go imports | — | the import path is the identity | an import alias | rename on collision |
| [lazy.nvim](https://git.kjan.de/jank/lazy.nvim/src/tag/v9.8.4/doc/lazy.nvim.txt) | — | `owner/repo`, directory named by the repository | `name =` overrides | rename on collision |

Today a configured pack is named by the last segment of its source address, the manifest's `name`
is informational (`internal/packload/packload.go`, the `Pack.Name` comment), and two entries with
one name are refused with "give one an explicit `name`" (`internal/config/packs.go`).

### A.6 The grant kinds, mapped

The four ruled grant kinds ([OQ-BB19](boundary-broker.md#OQ-BB19)) become `read` → `read`,
`create-branch` → `new_branches = true`, `branch` → `push = ["main"]`, `create-pr` →
`create_pr = true` and `create-issue` → `create_issue = true`; the user-config `grant_kinds`
list becomes `on_request`, and the user read grant becomes `always = ["fetch"]`. An `on_request`
kind answers [OQ-BB23](boundary-broker.md#OQ-BB23)
with "as a ceiling, never as a grant", its leaning. A GitHub
`create_pr` or `create_issue` request is accepted by the config and refused at use until a jail's write request can reach
`yolo approve`, whose delivery is not built (`internal/ghbroker/window.go`). `always = ["fetch"]`
reaches what today's `{"kind": "read", "repo": "*"}` reaches: the project's remotes plus `repos`,
approved at launch.

### A.7 Models under the providers doc

The providers doc's leaning on [OQ-PP1](providers-and-profiles-redesign.md#OQ-PP1) was to keep
profiles as the only selectable thing, for [OQ-AP1](active-provider-sets.md#11-decision-ledger)'s
reason: a provider loses a profile's options, the starting model among them. The ruling in
[§5](#5-which-setup-each-agent-runs-on) answers that reason directly: the provider carries
`model`. What it cannot carry (`via`, a second starting model) is what a variant is for.
