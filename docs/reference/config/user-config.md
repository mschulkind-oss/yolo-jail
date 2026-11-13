---
title: "yolo's own config: the user config"
status: current
stage: CURRENT
verified: 2026-11-12
verified_commit: 8dc1bab44
covers:
  - internal/config/readablepacks.go
  - internal/config/packs.go
  - internal/config/providernamespace.go
  - internal/config/validateactiveprovider.go
  - internal/config/readableallow.go
  - internal/config/readablenamed.go
  - internal/config/brokeredskipped.go
  - internal/packload/served.go
  - internal/brokerscope/requested.go
  - internal/cli/check/packnudge.go
  - packs/git/loopholes/git-broker/loophole.toml
  - packs/github/loopholes/github-broker/loophole.toml
tags: [config, packs, providers, active_provider, allow, grants, conventions, reference]
summary: "The user config, one part at a time: packs and where a pack not shipped comes from, packs as the only switch, providers and the names they answer to, which provider each agent runs on, the allow ceilings for logins and writable mounts, a login pack's settings, and the conventions that make lines unnecessary."
---

# yolo's own config: the user config

**Status:** Verified 2026-11-12 against `8dc1bab44`.

The user config, `~/.config/yolo-jail/config.toml` and its `config.d/` files
([README](README.md#the-files)), says what every project gets: which packs are selected, which
provider each agent runs on, and what a jail may do with the user's logins. Only the user writes
it, on the host. A typical one:

```toml
packs = ["pi", "local/pi-fork", "codex", "git", "github", "bedrock", "aws-auth",
         "local/matt", { git = "https://github.com/mschulkind-oss/matt-craft" }, "claude"]

[allow.git]                       # what every project's jail may do with your git login
always = ["fetch"]                # its own repositories, as approved at launch
on_request = ["push", "new_branches"]   # a project may request these

[allow.github]
always = ["read"]
on_request = ["create_pr", "create_issue"]

[allow.mounts]
on_request = ["write"]            # a project may request a writable mount

[allow.aws-auth]                  # a login pack's settings; the switch is its name in packs
profile = "my-sso-profile"

[active_provider]
default = "bedrock"
claude = "native"                 # left to its own login

[providers.bedrock]
region = "us-east-1"
model = "global.openai.gpt-6.1-sol"

[providers.bedrock.every_model]   # facts every model below shares unless it says otherwise
accepts = ["text", "image"]
reasoning = true

[providers.bedrock.models]        # the key is the model id; the maker follows from it
"global.anthropic.claude-opus-5-5" = { name = "Opus 5.5", context = 1_000_000 }
```

## Packs, and naming a pack that is not shipped

`packs` is a list. A shipped pack is written bare (`"claude"`; `yolo/claude` is the explicit
spelling). Every other pack is named by the **source** it comes from: a directory tree of
`<pack>/` directories, on this machine or in a git repository with the same layout.

```toml
[pack_sources]
dotfiles = "~/.dotfiles/yolo-packs"                         # a folder
acme = { git = "https://github.com/acme/yolo-packs" }       # a repository of packs
quiet = { path = "~/src/packs", bare = true }

packs = [
  "local/matt",                    # ~/.config/yolo-jail/packs/matt: local is the one built-in source
  "dotfiles/matt-fzf",
  "acme/tools",                    # fetched and locked
  { git = "https://github.com/mschulkind-oss/matt-craft" },   # a repository that IS one pack
  { pack = "local/scratch", bare = true },
]
```

- **There is no bare path form.** A path, or a source the file does not declare, is refused
  (`expected source/pack`, `unknown pack source`). So two authors' `tools` cannot collide and
  nothing is renamed.
- **A git source or pack** takes an optional `ref`. With none, the remote's default branch is
  read and the commit is pinned in `packs.lock.json` at first fetch; it moves only on
  `yolo pack update`. A remote that does not answer is refused naming the `ref` to write.
- **Two entries that resolve to one directory are refused**, naming both lines.
- **The name** (`acme/tools`) is how yolo names the pack back: the launch's pack line,
  `yolo pack ls`, `explain`, `footprint` and `lint`, and a login pack's settings table, which TOML
  then quotes, `[allow."local/my-auth"]`.
- **What an agent calls a pack's skills** is a separate name, the pack's namespace: its source
  key, its own name for `local/<pack>` and a repository listed directly, or a `namespace` written
  on the entry. `bare = true` on a source or an entry delivers the author's own names, and a clash
  is refused. The rule is [the pack system's](../pack-system.md#pack-namespace).

## One switch: `packs`

`packs` is the only switch. A settings table selects nothing: `[allow.aws-auth]` is valid only
when `aws-auth` is in `packs`. `yolo check` offers to add it; declined, it fails, and a launch
refuses, naming `yolo pack add aws-auth` or deleting the table (`packnudge.go`). A table naming no
pack at all is refused with the closest name ("Did you mean aws-auth?").

The reverse, a selected login pack whose table names no profile, is not an error: the pack
installs, its service serves nothing, and its pointers are withheld naming the setting to write
(`packload.UnconfiguredLogins`).

This is how the loophole system's rules read with `packs` as the switch
([`loophole-system.md`](../loophole-system.md#principles)): a manifest on disk activates nothing,
and nothing is selected by default; host access is never on by default, because `allow` is user
scope only. A loophole that runs whenever its pack is selected can still be held off:
`off = true` under `[allow.<pack>]` holds it off everywhere, and under a project's
`[requests.<pack>]` for that project (`run.LoopholesOff` names each switch and the command that
removes it).

## Which provider each agent runs on

A **provider** is where models come from. It is selectable by its own name and starts on its own
`model`. A **variant** is a second configuration of the same provider: a different starting model,
or a different route (`via`). Provider and variant names share one namespace.

```toml
[providers.bedrock]                     # selectable as "bedrock"
region = "us-east-1"
model = "global.openai.gpt-6.1-sol"

[providers.bedrock.variants.opus]       # selectable as "bedrock/opus"
model = "global.anthropic.claude-opus-5-5"

[providers.openrouter]                  # your entry for the provider the openrouter pack declares
name = "or"                             # selected as "or", and only as "or"
model = "anthropic/claude-opus-5-5"
alias = ["router"]                      # another name for exactly these settings

[active_provider]
default = "bedrock"                     # every agent the table does not name
claude = "bedrock/opus"
pi = ["bedrock", "codex"]               # several, switchable inside the agent
helper = "or"
codex = "native"                        # left to itself: yolo configures nothing for it
```

- **Your `[providers.<id>]` table is your entry for that provider.** The selected pack's
  declaration fills in every key you do not write, and each provider id has one entry. A pack
  declares a provider the same way, `[providers.<id>]` in its `pack.toml`.
- **`name`** defaults to the id; set, it is the only name the provider's own configuration
  answers to, and its variants follow as `<name>/<variant>`. Only the user may name a provider; a
  pack names a variant, and gives its provider an `alias` instead (codex-auth's `openai-codex`
  answers to `codex`). A variant's `name` makes that its one spelling, at the top level: the
  bedrock pack's `bridge` variant is `bedrock-bridge`.
- **`alias`**, a string or a list, adds names for exactly the configuration it sits on. An alias is
  another name for the same settings; a variant is a version with different settings.
- **Every collision is fatal**, except an alias equal to its own configuration's name. No written
  name contains `/`. Selecting a name you replaced is refused naming yours:
  `openrouter is named "or" in your config (providers.openrouter.name): -p or`.
- **`native`** is the one reserved word in the config: an agent set to it is left to its own login
  (its subscription, an API key, or whatever it does alone). A provider or alias named `native`
  is refused.
- **A declaration never selects.** A lone provider does not become everyone's choice; an agent
  runs on what `active_provider` (or `-p`) names. An unknown name is refused listing the names the
  selected packs and this file declare.

`-p` at launch and `active_provider` name the same things. Agent configs, environment variables
and config paths keep the provider's id; only the selection namespace uses names. The namespace is
`config.SelectableProfiles` (`providersemantic_selection.go`), and its collision rules are
`providernamespace.go`.

**Models are said once.** A provider's `models` table is keyed by the model id, and the maker
follows from the id. `every_model` holds the facts every row shares (`accepts`, `max_tokens`,
`reasoning`, `context`, `price`) unless a row says otherwise. A row that names no facts takes
the model catalog's. The rows reach every agent through one composition
(`packload.ComposeProviders`). Shared facts several providers reference are `model_facts`
([`model-facts-migration.md`](../../design/model-facts-migration.md)).

## What a jail may do with your logins

`allow.<source>` is the ceiling for one broker source, in two lists of the same shape:

- **`always`**: what every project's jail gets, within that project's approved scope.
- **`on_request`**: what a project may request in its own `requests.<source>`, each request
  approved at launch. A line here grants nothing by itself.

| Source | `always` token | `on_request` tokens | Grant kinds they stand for |
| :--- | :--- | :--- | :--- |
| `git` | `fetch` | `push`, `new_branches`, `push_tags` | `branch`, `create-branch`, `tag` |
| `github` | `read` | `create_pr`, `create_issue`, `runs`, `approve_runs` | `create-pr`, `create-issue`, `runs`, `approve-run` |

The tokens are each broker's own declaration (`read_capability`, `kind_capabilities` in its
`loophole.toml`), so a loophole pack with `[[grants]]` adds its own: `yolo allow journal read`,
`yolo allow host-processes on_request view`. A source's permission sets are `allow.<source>.sets`.

**A request outside the ceiling is not an error.** Two people share a repository and one allows
pushes while the other does not; the project file is the same for both. The one who does not sees
the request reported and skipped with the `yolo allow <source> on_request <token>` line that would
allow it (`brokeredSkippedRequests`), and the jail runs without it. At launch the approval prompt
lists each granted request and each skipped one.

**`fetch` reaches the project's own repositories**, as approved at launch, plus each repository a
`[repos.<name>]` table names, and nothing else ([project-config.md](project-config.md#named-repositories)).
A requested github.com repository joins git's read scope only, as two exact URLs, the HTTPS one
and the `git@github.com:` one (`brokerscope.RequestedRepoURLs`). An agent that wants another
repository adds it to the project file and goes through the config-change prompt; no mechanism
bypasses it. A **public** repository outside the scope is served anyway: the git broker tries an
HTTPS fetch with no credential first, and a fetch that lends nothing owes no prompt. A private
one is refused naming `yolo request repos <alias> github.com/OWNER/REPO`.

The user config may name a repository too, `yolo allow repos infra github.com/acme/infra`, and
give it a ceiling of its own, `[repos.infra.allow.github]`, which adds to the broad
`[allow.github]` for that repository only.

**Writable mounts.** `[allow.mounts] on_request = ["write"]` lets a project request a read-write
mount; without it such a request is refused naming this line
([`context-mounts.md`](../context-mounts.md#a-project-may-request-one-once-your-config-allows-it)).

**A login pack's settings** sit in its own table, `[allow.<pack>]`, such as
`[allow.aws-auth] profile = "my-sso-profile"`. A pack cannot ship a verb, so these are written with
`yolo config set allow.<pack>.<setting> <value>`.

## Lines you never write

A **convention** is a default that follows from something already done, so the line that would
say it is never written. An explicitly written key still wins, and no convention widens what a
jail may do with a login.

| | Convention | Instead of |
| :--- | :--- | :--- |
| C1 | `~/.config/yolo-jail/packs/<name>` is `local/<name>`. That directory, or a link in it, is a path like any other; a local pack follows symlinks wherever they point, a fetched one does not | a `[pack_sources]` entry for the folder |
| C2 | `config.d/*.toml` is read after `config.toml`, in name order | an `include_if_found` line per machine file |
| C5 | a project needs no file ([project-config.md](project-config.md#no-file-at-all)) | a template `yolo-jail.toml` |
| C6 | a pack needs no manifest when its `home/` and overlay folders say everything ([`pack-manifests.md`](../../design/pack-manifests.md#3-no-manifest-at-all)) | a `pack.toml` |
| C7 | a model named without facts takes the catalog's | repeating context, price and limits |
| C9 | a git source or pack with no `ref` follows the default branch, pinned by the lock | `ref = "main"` |

## What the user config is not

- **Not a project's file.** It holds what every project may get; a project asks within it. A key
  for one project goes in that project's file.
- **Not edited by yolo on its own.** A command the user typed may write it, one hunk at a time
  ([editing.md](editing.md)); state yolo keeps for itself goes in the per-workspace file.
- **Not reachable from a jail.** Inside a jail, a command that would write it is refused with the
  line to run on the host.

## Where the code lives

- Pack entries and sources: `internal/config/readablepacks.go` (`readableLocalPackRoot`),
  `packs.go`, `packsrc/addr.go`.
- Provider names, aliases, variants and `native`: `internal/config/providernamespace.go`,
  `providersemantic_selection.go`, `validateactiveprovider.go`, `validateproviders.go`; model rows
  reach agents through `internal/packload/providers.go` (`ComposeProviders`).
- Ceilings, requests and skips: `internal/config/readableallow.go`, `brokeredskipped.go`;
  named repositories: `readablenamed.go`; the git read-scope URLs: `internal/brokerscope/requested.go`.
- The settings-table nudge: `internal/cli/check/packnudge.go`; unconfigured login packs:
  `internal/packload/served.go`.
