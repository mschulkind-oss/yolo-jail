# Message rewrites: Config loading, validation, editing and migration

Scope: `internal/config`, `internal/configedit`, `internal/configmigrate`, `internal/configdoc` (non-test `.go` files). Messages read: about 1,450, all approximate. I scanned about 2,300 source lines that hold prose literals, and many messages span several of those lines. A second pass (see the "Second pass" section) traced `inherit.go`, read the remaining `configmigrate` files line by line, read the whole repository approval prompt in `scopeapproval.go`, and covered the provider errors a user can hit. Entries below: 111 in Where/Before/After/Why form (91 from the first pass, 20 from the second), plus about 150 sites covered by the Patterns section. Six of them judge the message fine and propose no change.

How these messages reach the user: `ValidateConfig` (`internal/config/validate.go:75`) gathers the error and warning strings. A launch, `yolo check`, and every `yolo config set|add|unset` print them. When the config is TOML, `projectReadableValidationMessages` (`internal/config/validationprojection.go:319`) renames four key prefixes, such as `config.packages` to `config.install.nix`. It changes no other text, so the JSON-era wording below reaches TOML users unchanged.

## Patterns

### P1. "user-scope only — <the design reason>" with no command

Each of these messages explains the threat model in one or two sentences, which breaks rule 8. It ends with "Move it there, or remove it", which names no command (happy path). The phrase "user-scope only" is also project-internal (rule 6). One template can cover every site. Where `yolo config set … --user` can write the value, name it.

- **Before** (`internal/config/validate.go:749`):
  ```text
  config.host_floor: user-scope only — it decides which programs yolo installs on your machine and `yolo host` runs, so it is read from ~/.config/yolo-jail/config.toml and a workspace value has no effect. Move it there, or remove it.
  ```
- **After:**
  ```text
  config.host_floor: only your own config can set this, so yolo refuses it in a workspace config. Set it in ~/.config/yolo-jail/config.toml with `yolo config set host_floor <value> --user`, then remove it here with `yolo config unset host_floor --project`.
  ```
- **Sites:** `internal/config/validate.go:569, 597, 646, 683, 722, 749, 775, 827, 850, 880, 905, 1854`; `internal/config/programs.go:278`; `internal/config/profiles.go:361`; `internal/config/adapters.go:178`; `internal/config/validateproviders.go:618, 629` (and its callers at 476, 489, 503, 518); `internal/config/hostfiles.go:962`; `internal/config/onrequest.go:53`; `internal/config/localclones.go:199`; `internal/config/sharehostfonts.go:44`; `internal/config/macosusersharedtmp.go:48`; `internal/config/provisioners.go:138`; `internal/config/hostimportexclusions.go:188`; `internal/config/packs.go:790`; `internal/config/brokered.go:1461, 1467`; `internal/config/validate_loopholes.go:376, 382, 392`; `internal/config/mountsrequest.go:193, 230`; `internal/config/readablevalidate.go:111`; `internal/config/editvalidation_semantics.go:62`.
- Keep the specific extras some sites already carry, such as `YOLO_NO_UPDATE_CHECK=1` at validate.go:850, `yolo --timing` at :880, and `yolo pack add <name>` at packs.go:790. Drop the rationale sentences. Examples: "A workspace config travels with the repo and is agent-editable…" (programs.go, profiles.go, adapters.go, hostfiles.go, validateproviders.go) and "every prompt, every file the agent has read and every credential…" (adapters.go, validateproviders.go). A single short clause is acceptable, such as ", because an agent can edit workspace files".

### P2. "REMOVED — <history of the key>": retired-key messages tell the story

These warnings say why a key went away, which pack replaced it, and what an older value used to mean (rules 7 and 8). The user needs one fact (the key is gone) and one or two commands.

- **Before** (`internal/config/validate.go:515`):
  ```text
  config.journal: REMOVED — this top-level key was retired when the journal bridge became a pack-shipped loophole, and yolo's config schema no longer names a loophole. Delete the key. The bridge runs whenever its pack is selected (`yolo pack add journal`) and reads nothing until a grant covers the project: `yolo allow journal read` is what "journal": "user" (and the bare true) used to mean, for every project, and `yolo allow journal read_all` is the old "full". Both are your user config's to give, never a workspace file's: reading the whole host journal used to be settable from a file the agent inside the jail can rewrite.
  ```
- **After:**
  ```text
  config.journal: yolo no longer reads this key. Delete it. To let jails read the host journal, run these on the host:
      yolo pack add journal
      yolo allow journal read
  (`"journal": "full"` is now `yolo allow journal read_all`.)
  ```
- **Sites:** `internal/config/validate.go:243` (repo_path), `:283` (agents), `:515` (journal), `:942` (macos_log), `:1172` (host_processes), `:1252` (mcp_presets); `internal/config/validateprofiles.go:23` (agent_profiles), `:45` (use_profiles); `internal/config/validateproviders.go:596` (base_url); `internal/config/validate_loopholes.go:99` (retired loophole names; the `why` text at :70 is history).

### P3. The in-jail note says "host-generated config snapshot"

This suffix is added to every retired-key warning when the warning prints inside a jail. "Snapshot" and "host-generated" are internal terms (rule 6), and the note does not say where the HOST config is.

- **Before** (`internal/config/validate.go:288`):
  ```text
   (ignored here: this is the host-generated config snapshot, so remove the key from the HOST config.)
  ```
- **After:**
  ```text
   (Only a warning here, inside the jail, because this jail reads a copy of your config. Remove the key from ~/.config/yolo-jail/config.toml on the host.)
  ```
- **Sites:** `internal/config/validate.go:288, 524, 950, 1182, 1279`; `internal/config/brokered.go:883` (`inJailSuffix`); `internal/config/validateprofiles.go:28, 55`; `internal/config/setupsretired.go:182`; `internal/config/validateproviders.go:607`. A related note is `internal/config/validate_loopholes.go:39`, which reads "(warning in-jail: an error here would refuse every nested launch over the live-mounted workspace file — fix it host-side.)". Rewrite it as "(Only a warning inside the jail. Fix it on the host.)"

### P4. Advice spelled in JSON while the user's config is TOML

TOML is now the default format. Some advice still shows JSON snippets such as `"packs": ["<pack>"]` and `{"kind": "read", "repo": "*"}`. Some also names `~/.config/yolo-jail/config.jsonc` as a fixed path (see Wrong today). A TOML user cannot paste these snippets. The fix is to name the verb (`yolo pack add`, `yolo allow`, `yolo config set`), which AGENTS.md already requires ("Advice names a verb, not a file to hand-edit").

- **Before** (`internal/config/validate_loopholes.go:105`):
  ```text
  Installing is user-scope, and there are two shapes of it — this file can do neither. If the loophole SHIPS IN A PACK (every loophole yolo itself ships does), the install is the selection: add "packs": ["<pack>"] to ~/.config/yolo-jail/config.toml, which is user-scope only, and leave the "enabled" key where it is. If it ships in no pack, install it inline in that same file: "loopholes": {"<name>": {"command": ["<host daemon argv>"]}}.
  ```
- **After:**
  ```text
  A workspace config cannot install a host service. If a pack ships it, select the pack on the host with `yolo pack add <pack>`. Otherwise, declare it under loopholes.<name>.command in ~/.config/yolo-jail/config.toml.
  ```
- **Sites:** `internal/config/validate_loopholes.go:99, 105`; `internal/config/validate.go:1252`; `internal/config/brokered.go:154, 248, 327` (`BrokerGrantAdvice`, the JSON-only branch); `internal/config/workspacefile.go:263, 280, 295` ("{\"enabled\": true|false}"). Also, "expected an object" is the JSON word. A TOML user should see "expected a table". This is one substitution in the projection step and needs no per-site change.

### P5. Values are echoed in Python spelling

`pyReprValue` (`internal/config/repr.go:20`) prints `true` as `True`, `null` as `None`, and strings in single quotes. A user who wrote `kvm = "yes"` sees `(got 'yes')`, and a user who wrote `null` sees `(got None)`. Neither TOML nor JSON is spelled that way (rule 1).

- **Before** (`internal/config/validate.go:537`):
  ```text
  config.kvm: expected a boolean (got None)
  ```
- **After:**
  ```text
  config.kvm: expected true or false (got null)
  ```
- **Sites:** about 108 calls to `pyReprValue`/`reprAny` across the partition. Examples: `validate.go:537, 555, 639, 812`; `validate_loopholes.go:881, 958, 965`; `setups.go:180, 193, 200, 230`; `brokered.go:256, 679, 1305, 1476, 1499`; `workspacefile.go:295, 315`; `briefingprovenance.go:60`; `sharehostfonts.go:37`; `hostpath.go:191`; `macosusersharedtmp.go:41`. A single change in `repr.go` (spell values in the config file's own syntax) fixes all of them. "expected a boolean" should also read "expected true or false" wherever it appears.

### P6. Decision IDs and design-doc paths in messages

- **Before** (`internal/config/brokered.go:1461`):
  ```text
  …so a workspace may not name one (BB-P9). Put it in the user config, …
  ```
- **After:** delete the parenthetical.
- **Sites:** `internal/config/brokered.go:1463` (BB-P9), `:1469` (BB-D96); `internal/config/packs.go:979` (LG-D9 plus a doc path); `internal/config/provisioners.go:150` (PS-D2), `:349` (OQ-PS6); `internal/config/profiles.go:136` (OQ-RC20); `internal/configmigrate/packs.go:53` (OQ-RC15); `internal/config/mountsrequest.go:195` (doc path); `internal/config/validateprofiles.go:25` (doc path).

### P7. Internal failures in `yolo config set|add|unset` name no next step

`configedit` and the `editvalidation*` files return internal sentences such as "cannot set missing config value", "append target is %T, not an array", or "planned splice changes effective document values: expected %#v, got %#v". These show Go types, internal nouns (splice, receipt, prospective, binding, graph), and give no next step. Most of them guard states that should not happen. A few are reachable from ordinary input, and those are listed under "Editing config" below. For the rest, the CLI could add one wrapper line instead of rewriting each message:

- **After (wrapper the CLI prints above the detail):**
  ```text
  yolo could not make this edit, and no file was changed. Edit the file by hand, or report this as a yolo bug with the detail below.
  ```
- **Sites:** `internal/configedit/edit.go:82, 86, 89, 101, 120, 158, 178, 207, 211, 215, 249, 257, 264, 325, 336, 340, 360, 375, 390, 393, 421–447, 513–547, 625–710, 757, 761, 850, 1194–1497, 1554–1679, 1800–1952`; `internal/configedit/creation.go:22, 26, 96–158, 188, 192, 249, 265, 268, 289`; `internal/configedit/effective.go:59–88`; `internal/configedit/targeted.go:24`; `internal/config/editvalidation.go:113–131, 278, 285, 329–560`; `internal/config/editvalidation_prospective.go:106–189`; `internal/config/editvalidation_target.go:88, 95, 157–176, 216, 219, 250–362, 407–456`; `internal/config/editvalidation_sources.go:19–160`; `internal/config/editvalidation_profiles.go:19–74`; `internal/config/newreadableconfig.go:37, 118, 124`. Some messages already end in "reread and retry" or "preserve it and retry", for example `configedit/apply.go:74, 106` and `editvalidation_prospective.go:128`. Those are fine.

### P8. Package-name prefixes and Go types in errors

- **Before** (`internal/configdoc/document.go:345`):
  ```text
  configdoc: TOML date/time at providers.x.since is not a supported config scalar
  ```
- **After:**
  ```text
  providers.x.since: yolo config does not accept a TOML date or time. Write it as a quoted string.
  ```
- **Sites:** `internal/configdoc/document.go:67` ("document root is %T, want object"), `:89, 303, 310, 328, 345`; `internal/configdoc/encode.go:20, 36, 56, 222` ("unsupported TOML value %T"); `internal/configedit/edit.go:215` ("%T").

## Launch: approving config changes

### Config changed and the launch has no terminal
- **Where:** `internal/config/snapshot.go:229` — a launch with no terminal attached stops because the workspace config changed since the last approval.
- **Before:**
  ```text
  A changed workspace config is never accepted without a human — an auto-accept here would make the approval promise conditional on somebody happening to have a terminal attached, and a scripted launch is exactly where nobody is watching.
  ```
- **After:**
  ```text
  yolo needs a person to approve a changed workspace config, and this launch has no terminal to ask on.
  ```
- **Why:** 8 (design argument). The headline above it already gives the fact, so this sentence could also be deleted.

### Approve for this launch only
- **Where:** `internal/config/snapshot.go:245` — the last paragraph of the same refusal.
- **Before:**
  ```text
  Revert the change, or approve it for THIS LAUNCH ONLY by re-running with
    --accept-config-changes
  which records the new config as approved exactly as answering `y` would.
  ```
- **After:**
  ```text
  Undo the change, or approve it by running the launch again with:
    --accept-config-changes
  This records the new config as approved, just as answering y would.
  ```
- **Why:** "THIS LAUNCH ONLY" is false (see Wrong today). Rule 2 also applies.

### Launch without the broker
- **Where:** `internal/config/snapshot.go:265` — added when the repository list changed.
- **Before:**
  ```text
  To launch without github instead, take the pack that ships it out of `packs` in ~/.config/yolo-jail/config.toml: it runs whenever that pack is selected, in every project.
  ```
- **After:**
  ```text
  Or launch without github: run `yolo pack remove <pack>` on the host. This removes it from every project.
  ```
- **Why:** 1 and 6. The message says "the pack that ships it" but does not name that pack, so the user cannot run anything. Naming the pack needs the caller to pass it in.

### Required capability not met
- **Where:** `internal/config/capabilities.go:344` — a launch refuses because `required_capabilities` names something that nothing provides.
- **Before:**
  ```text
    A capability is satisfied by a declaration: `providers.<name>.capabilities` naming it (the agent has it natively there), an `mcp_servers.<name>` entry with "provides": "<capability>", or a selected agent whose pack declares it for the source the agent runs on: its built-in login, or the provider its profile selects.
  Declare the satisfier, drop the name from required_capabilities, or launch anyway with YOLO_ALLOW_UNMET_CAPABILITIES=1.
  ```
- **After:**
  ```text
    Three things can provide a capability: a `capabilities` list under a provider in `providers`, an MCP server whose entry says `provides = "<capability>"`, or a selected pack that declares it.
  Add one of those, remove the name from required_capabilities, or launch anyway with YOLO_ALLOW_UNMET_CAPABILITIES=1.
  ```
- **Why:** 2, 4 ("the satisfier"), 7.

### Warning that a capability check could not run
- **Where:** `internal/config/capabilities.go:335`.
- **Before:**
  ```text
  Warning: cannot tell whether anything satisfies required capability 'x': <err>. Continuing: this launch reports that problem itself further on.
  ```
- **After:**
  ```text
  Warning: yolo could not check whether anything provides the required capability 'x' (<err>). The launch continues and reports that problem below.
  ```
- **Why:** 3.

### Project requests a grant your config does not allow
- **Where:** `internal/config/loopholegrants.go:582` and `internal/config/brokeredskipped.go:76` — `yolo check` and the launch list a request the user config does not permit.
- **Before:**
  ```text
  this project requests git push main.
    Your config does not allow push on request, so this project runs without it.
    To allow it for every project you work on (in your config, not this file):
      yolo allow git on_request push
  ```
- **After:**
  ```text
  This project asks to use git push main, and your config does not let projects ask for push, so this project runs without it.
    To let any project ask for it, run on the host:
      yolo allow git on_request push
  ```
- **Why:** 3 (sentence starts lowercase), 5 ("allow push on request").

### Heading for the grant list
- **Where:** `internal/config/loopholegrantgate.go:270`, `internal/config/brokeredskipped.go:85`.
- **Before:**
  ```text
  Loophole grants for this project:
  Broker requests this project makes that your config skips:
  ```
- **After:**
  ```text
  Host access this project gets:
  Host access this project asks for that your config does not allow:
  ```
- **Why:** 5 and 6 (loophole, broker).

### Call-broker disclosure lines
- **Where:** `internal/config/callbroker.go:49, 132, 134` and `internal/config/scopeapproval.go:568` — lines printed in the approval prompt for an HTTP call broker.
- **Before:**
  ```text
  http selected pack=<pack>
  http declaration_sha256=<hash>
  joins working set: cell=<cell>
  joins working set: follows=<src> fact=<fact>
  selected call-broker declaration for <source>:
  ```
- **After:**
  ```text
  HTTP access comes from the pack <pack> (declaration <hash>).
  Anything the agent creates with <cell> joins the list of things it may act on without asking.
  Anything whose <fact> is already on <src>'s list of things the agent may act on without asking joins this list too.
  What the <source> service lets the agent call:
  ```
- **Why:** 3, 5, 6 (working set). I settled the meaning in the second pass. `docs/design/pack-brokers.md` §7.4 defines the join rules (rule 1 is "a call in a `joins_working_set` cell creates it"; rule 2 is "a lookup's fact names a resource of another source's working set… a CircleCI pipeline built from a branch the git broker's working set holds"). The rewrite above follows those rules. The rest of this disclosure block is covered under "Second pass: the approval prompt".

## Retired and renamed keys (beyond P2)

### mcp_presets
- **Where:** `internal/config/validate.go:1252` — `mcp_presets` is still in a config.
- **Before:**
  ```text
  config.mcp_presets: REMOVED — yolo no longer keeps its own list of MCP servers. Delete the key. For the browser (`chrome-devtools`), add `chrome-devtools` to `packs` in ~/.config/yolo-jail/config.jsonc (beside your agents, e.g. ["claude", "chrome-devtools"]); a workspace file cannot select it. For `sequential-thinking`, which was deleted, write the server yourself: "mcp_servers": {"sequential-thinking": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-sequential-thinking"]}}. Then run `yolo check`.
  ```
- **After:**
  ```text
  config.mcp_presets: yolo no longer reads this key. Delete it.
  For the browser server, run `yolo pack add chrome-devtools` on the host.
  For sequential-thinking, declare the server yourself under mcp_servers.sequential-thinking, with command "npx" and args ["-y", "@modelcontextprotocol/server-sequential-thinking"].
  Then run `yolo check`.
  ```
- **Why:** 7, P4. The fixed `.jsonc` path is also in Wrong today.

### base_url
- **Where:** `internal/config/validateproviders.go:596`.
- **Before:**
  ```text
  providers.x.base_url: REMOVED — say which protocol this URL speaks:
        endpoints.anthropic.base_url   the Anthropic Messages wire
        endpoints.openai.base_url      the OpenAI-client wire
      The bare field meant different protocols to different agents — pi read it as openai and claude as anthropic — so one line pointed two agents at two different services, and a local llama.cpp/ollama/vLLM endpoint (all OpenAI-speaking) sent claude to an address it could not talk to. Naming the protocol also makes the launch able to ROUTE it: …
  ```
- **After:**
  ```text
  providers.x.base_url: yolo no longer reads this key. Write the URL under the protocol it speaks:
        endpoints.anthropic.base_url   for the Anthropic Messages API
        endpoints.openai.base_url      for the OpenAI API (llama.cpp, ollama and vLLM use this one)
  ```
- **Why:** 8.

### "is not accepted in TOML"
- **Where:** `internal/config/readablevalidate.go:320, 322`.
- **Before:**
  ```text
  "agent_profiles" is not accepted in TOML; use active_provider instead. The old spelling is not an alias
  ```
- **After:**
  ```text
  "agent_profiles" is an old key name that TOML config does not accept. Use active_provider instead.
  ```
- **Why:** 2. The phrase "not an alias" is not needed.

### Loophole `enabled` switch (pack loophole)
- **Where:** `internal/config/validate_loopholes.go:486` (`SwitchesNothingDisclosure`), `:560`, `:598`, `:617`.
- **Before:**
  ```text
  config.loopholes.git.enabled in yolo-jail.jsonc switches nothing: the loophole 'git' runs whenever its pack is selected and grants nothing until a grant covers the project. Remove the key; to give this project access, run `yolo request git <kind>` here.
  ```
- **After:**
  ```text
  config.loopholes.git.enabled in yolo-jail.jsonc does nothing, because git runs whenever its pack is selected. Remove the key. To give this project access, run `yolo request git <kind>`.
  ```
- **Why:** 6 (loophole) and 7. The same changes apply to the variants at :560 and :617, which also add the broker read-grant advice (see "Brokers" below).

### Loophole `enabled` in a workspace file (off-switch loophole)
- **Where:** `internal/config/validate_loopholes.go:508`.
- **Before:**
  ```text
  config.loopholes.x.enabled: true in yolo-jail.jsonc cannot turn the loophole 'x' on: it runs whenever its pack is selected, and an off the user scope sets (`enabled: false`, or `off = true` under [allow.x]) is the user's to lift, never a workspace file's. Remove the key.
  ```
- **After:**
  ```text
  config.loopholes.x.enabled: true in yolo-jail.jsonc has no effect. x runs whenever its pack is selected, and only your own config can turn it back on after it turns x off. Remove the key.
  ```
- **Why:** 4 ("an off … is the user's to lift"), 6.

### Workspace switch disclosures
- **Where:** `internal/config/validate_loopholes.go:470, 475`.
- **Before:**
  ```text
  config.loopholes.x: enabled by yolo-jail.jsonc (workspace scope) — the installed loophole 'x' runs for jails launched from this workspace because that agent-editable file switched it on.
  ```
- **After:**
  ```text
  config.loopholes.x: yolo-jail.jsonc turns on the host service x for jails launched from this workspace. An agent can edit that file.
  ```
- **Why:** 5, 6.

### Loophole setting read from a workspace file
- **Where:** `internal/config/validate_loopholesettings.go:182`.
- **Before:**
  ```text
  config.loopholes.x.settings.k: yolo-jail.jsonc sets this user setting; it is read as your value for this launch, as yolo v0.15.1 read it, since it admits nothing
  ```
- **After:**
  ```text
  config.loopholes.x.settings.k: this setting belongs in your own config, but yolo still uses the value from yolo-jail.jsonc because it grants no access. To move it, run on the host: yolo config set --user allow.x.k <value>
  ```
- **Why:** 1 (version number), 3, 8. It also had no next step. The `--user` command already exists in `userSettingSetCommand` at :200.

## Config files: format, names, and parse problems

### TOML file with the wrong format number
- **Where:** `internal/config/newreadableconfig.go:216` — a `.toml` config has `format = 1` or another number below 2.
- **Before:**
  ```text
  <file>: format 1 is unsupported in a TOML file; TOML config files use format 2. The file being refused is <path>. First confirm a non-config backup path does not already exist and preserve it with `cp -p <path> <path>.backup`; then manually rewrite its contents as TOML format 2, checking every key against the accepted TOML schema before using it. changing or removing the format marker alone is not a conversion. If this is original legacy JSONC/JSON content, retain or restore those bytes under their original legacy JSONC/JSON filename, then move this unsupported TOML file out of active config filenames with `mv <path> <path>.disabled` after confirming that non-config destination does not already exist; never rename legacy bytes to `.toml`. No lossless or equivalence-checked migration is available
  ```
- **After:**
  ```text
  <path>: TOML config files must say `format = 2`, and this one says format 1. Changing the number does not convert the file.
  If the file holds old JSONC content, rename it back to its .jsonc name and run `yolo config migrate`.
  Otherwise, back it up (`cp -p <path> <path>.backup`) and rewrite it by hand; `yolo config-ref` lists every key.
  ```
- **Why:** 2, 3 ("changing" starts a sentence in lowercase), 7. It is the longest message in the partition. The next step (`yolo config migrate`) is the one the code supports for JSONC, and the original never named it.

### A newer format
- **Where:** `internal/config/newreadableconfig.go:211`.
- **Before:**
  ```text
  <file> is format 3; this yolo reads format 2. Upgrade with `yolo update`, or `just install` from a checkout. Nothing was started
  ```
- **After:**
  ```text
  <file> is written for a newer yolo (format 3), and this yolo reads format 2. Upgrade with `yolo update`. Nothing was started.
  ```
- **Why:** 1 (`just install` is a developer step). The sentence also lacks a final period.

### Format must be an integer
- **Where:** `internal/config/newreadableconfig.go:208`.
- **Before:** `<file>: format must be the integer 2 (got 'two')`
- **After:** `<file>: the first line must be format = 2 (got "two")`
- **Why:** P5.

### Resource groups in a workspace file
- **Where:** `internal/config/resourcegroups.go:36`, reached from `internal/config/load.go:307`.
- **Before:**
  ```text
  user-scope only; first remove the `resource_groups` table from this project file, then retry the command. After the workspace config graph is repaired, if you independently choose an owner-level mapping, set it on the host using `yolo config set resource_groups.default '"example.slice"' --user` (example only; replace it with your independently chosen unit; do not copy a project value).
  ```
- **After:**
  ```text
  only your own config can set resource_groups. Remove the table from this project file and run the command again. To set your own, run on the host: yolo config set resource_groups.default '"<unit>.slice"' --user
  ```
- **Why:** 2, 5 ("workspace config graph", "owner-level mapping"), 6.

### Workspace file names another workspace
- **Where:** `internal/config/workspacefile.go:338`.
- **Before:**
  ```text
  <path> names the workspace '/a', not /b, so nothing in it applies. Delete it: switches are retired, and /b gets access by a grant …
  ```
- **After:**
  ```text
  <path> belongs to the workspace /a, not /b, so yolo ignores it here. Delete it. To give /b access, run `yolo request <pack> <kind>` there.
  ```
- **Why:** 6 ("switches are retired" refers to project history), 8.

### Per-workspace file unreadable
- **Where:** `internal/config/workspacefile.go:233` (suffix used at 237, 243, 249, 280).
- **Before:**
  ```text
   Fix it, or delete it: switches are retired, and a project gets access by a grant (`yolo request <pack> <kind>`).
  ```
- **After:**
  ```text
   Fix it, or delete it. yolo no longer uses this file to turn host services on. To give the project access, run `yolo request <pack> <kind>`.
  ```
- **Why:** 6, 8.

### Loophole name regex
- **Where:** `internal/config/workspacefile.go:289, 438`.
- **Before:** `'x y' is not a loophole name (^[a-zA-Z][a-zA-Z0-9_-]{0,63}$). Remove it.`
- **After:** `"x y" is not a valid name. A name starts with a letter and has up to 64 letters, digits, "_" or "-". Remove it.`
- **Why:** 1 (regex), 6.

### `--user-layer` messages
- **Where:** `internal/config/userlayer.go:104, 113`.
- **Before:** `--user-layer: <msg> (an explicitly-passed layer is never skipped silently)`
- **After:** `--user-layer: <msg>. yolo stops here because you named this file.`
- **Why:** 5, 8.

### Inherited user config siblings
- **Where:** `internal/config/userlayer.go:306`.
- **Before:**
  ```text
  inherited-launch siblings a, b exist; these formats are never merged. Preserve their contents, choose exactly one file to keep active, then rerun the command
  ```
- **After:**
  ```text
  Your user config exists in more than one format (a, b), and yolo reads only one. Move aside the ones you do not want, then run the command again.
  ```
- **Why:** 5 ("inherited-launch siblings"), 6.

## Editing config: `yolo config set|add|unset`

### Unknown key
- **Where:** `internal/config/editvalidation_target.go:188` — the user names a key yolo does not know.
- **Before:** `config key foo.bar has no readable schema target`
- **After:** `yolo has no config key foo.bar. `yolo config-ref` lists the keys.`
- **Why:** 1, 6 (schema target). It needs a next step. The rename hint from `renamedKeyHint` stays as a suffix.

### Key with no editable scope
- **Where:** `internal/config/editvalidation_target.go:206`.
- **Before:** `config key foo has no editable scope`
- **After:** `yolo config set cannot edit foo. Edit the config file by hand.`
- **Why:** 6. It needs a next step.

### User-only key without `--user`
- **Where:** `internal/config/editvalidation_target.go:222, 292`.
- **Before:** `config.setup is user-scope only; select a User config file`
- **After:** `only your own config can set setup. Run the command again with --user.`
- **Why:** 6. The original does not name the flag. The flag `--user` is the one the CLI takes; see setups.go:87.

### Workspace-only key with `--user`
- **Where:** `internal/config/editvalidation_target.go:225, 295`; `internal/config/editvalidation_semantics.go:66, 68`; `internal/config/readablevalidate.go:114`.
- **Before:**
  ```text
  config.requests.git.push is workspace-only; move requests to the workspace config
  config.requests.git.push: workspace-only key; this file is user scope
  ```
- **After:**
  ```text
  requests.git.push is a project's request, so only a workspace config holds it. Run `yolo request git push <branch>` in the project instead.
  ```
- **Why:** 6. The second form gives no next step. Only `requests.*` keys are workspace-only (`internal/config/schema.go:238–246`), so naming `yolo request` is correct.

### Missing target file
- **Where:** `internal/config/editvalidation_target.go:358`.
- **Before:** `config target <path> does not exist; create that config file first, then retry (no file was written): <err>`
- **After:** `<path> does not exist, and no file was written. Create it (for example, `touch <path>`), then run the command again.`
- **Why:** 6 ("config target"). The first step needs to name a command.

### User config edit from inside a jail
- **Where:** `internal/config/editvalidation.go:445`.
- **Before:** `user-scope config edits are only allowed on the host; run the edit command for <path> on the host`
- **After:** `Only a command run on the host can change your user config (<path>). Run the same command in a terminal on the host.`
- **Why:** 5 ("user-scope config edits").

### Ancestor not a table on `add`
- **Where:** `internal/configedit/edit.go:137`.
- **Before:** `add parent does not exist and cannot be created: an ancestor of the key is not an object (or a missing step is an array index); fix that value in the file by hand, then rerun the command`
- **After:** `yolo cannot add this key, because a key above it holds a value, not a table. Fix that value in the file by hand, then run the command again.`
- **Why:** 5 ("add parent"), P4 ("object").

### TOML has no null
- **Where:** `internal/configdoc/encode.go:136` — `yolo config set key null` on a TOML file.
- **Before:** `TOML has no null value`
- **After:** `TOML has no null. To remove the key, run `yolo config unset <key>`.`
- **Why:** It needs a next step. The calling path may wrap the key name in front.

### A commented value cannot change shape
- **Where:** `internal/configedit/edit.go:1679`.
- **Before:** `cannot replace a commented config value with a different structure without relocating comments`
- **After:** `yolo will not change this value from one type to another, because that would move the comments next to it. Edit it by hand, or remove the comments first.`
- **Why:** 5. It needs a next step.

### Table kind change
- **Where:** `internal/configedit/edit.go:1547`.
- **Before:** `cannot change a TOML table/header kind at a.b; target a concrete value`
- **After:** `a.b is a whole table. Name one key inside it, for example a.b.<key>.`
- **Why:** 5, 6.

### Value must have a type before removal
- **Where:** `internal/configedit/effective.go:159`.
- **Before:** `config.a.b in selected file must be a list before removal; correct that value manually and run `yolo check``
- **After:** `config.a.b in this file must be a list before yolo can remove an entry from it. Fix the value by hand, then run `yolo check`.`
- **Why:** 2.

### Wrapped validation failure
- **Where:** `internal/configedit/apply.go:121`; `internal/config/editvalidation_preexisting.go:22`.
- **Before:** `planned effective-config validation failed: <problems>` / `validate planned effective config: <problems>`
- **After:** `This edit would leave a config that yolo refuses, so nothing was written: <problems>`
- **Why:** 5, 6.

### Restore after a failed write
- **Where:** `internal/configedit/apply.go:144`.
- **Before:** `written document has unexpected effective values (expected …; got …); original bytes restored`
- **After:** `The written file did not hold the expected values, so yolo put the original back. This is a yolo bug; please report it with this output.`
- **Why:** 5 ("effective values", "original bytes"). It needs a next step.

### Pack source address not checked in jail
- **Where:** `internal/config/editvalidation_sources.go:222`.
- **Before:** `pack x was not checked: <err> — its source is not visible in this jail and no copy was delivered; `yolo check` on the host checks it`
- **After:** `yolo could not check the pack x here, because its files are not visible in this jail. Run `yolo check` on the host.`
- **Why:** 7.

### Staged tree only
- **Where:** `internal/config/editvalidation_sources.go:302`; `internal/config/readablebrokerselection.go:64`.
- **Before:** `pack x only resolves through staged tree <path>, whose source and filters cannot be proved for this planned entry; rerun the host launch or `yolo pack install` before editing`
- **After:** `yolo cannot find the files for pack x on this machine. Run `yolo pack install` on the host, then try the edit again.`
- **Why:** 5, 6 ("staged tree", "planned entry").

### Embedded pack source missing
- **Where:** `internal/config/editvalidation_profiles.go:23–38`; `internal/config/packs.go:767`.
- **Before:** `embedded pack source is unavailable; rerun with a complete yolo build`
- **After:** `This yolo is missing the packs it ships with. Reinstall it with `yolo update`.`
- **Why:** 1, 6. "Rerun with a complete yolo build" does not say how to get one.

## Packs, programs, mounts and host files

### No packs configured
- **Where:** `internal/config/packs.go:58–61`.
- **Before:**
  ```text
  No packs are configured, so this jail has no coding agent
  An agent arrives as a pack. The packs yolo ships are selected by name — run `yolo pack add claude` on the host, or run `yolo pack --help` for what packs deliver and how to add one from elsewhere.
  ```
- **After:**
  ```text
  No packs are configured, so this jail has no coding agent.
  Each agent comes in a pack. To add one, run `yolo pack add claude` on the host. `yolo pack --help` lists the other ways.
  ```
- **Why:** 2, 7.

### Default-selected pack has no off switch
- **Where:** `internal/config/packs.go:977`.
- **Before:**
  ```text
  cgroup-delegate is selected by default (it ships with yolo and can only narrow the jail), so a launch selects it with or without a `packs` line; this yolo has no switch to turn it off, and a per-workspace `off` for it is planned (docs/reference/loophole-grants.md LG-D9)
  ```
- **After:**
  ```text
  cgroup-delegate is always on, because it can only limit the jail. This yolo has no way to turn it off.
  ```
- **Why:** 6 (decision ID), 8. The message has no next step because none exists. The text is true today, since LG-D9 is marked unbuilt.

### Pack `selected_by_default` in a non-shipped pack
- **Where:** `internal/config/packresolve.go:307`.
- **Before:** `pack x: selected_by_default is honored only for a pack shipped with yolo; delete that line from its pack.toml (your `packs` entry already selects it)`
- **After:** `pack x: only packs that ship with yolo can use selected_by_default. Delete that line from its pack.toml. Your packs list already selects it.`
- **Why:** 7.

### Unknown pack name
- **Where:** `internal/config/packs.go:943`.
- **Before:** `no pack named "x" ships with yolo (available: …); for a pack from elsewhere, give a full address like file:///path/to/pack or …`
- **After:** fine as is. For consistency, add the command: "`yolo pack ls` lists them."
- **Why:** optional.

### Programs `run` retired
- **Where:** `internal/config/programs.go:182`.
- **Before:** `config.programs.overrides.x.run: no longer read — yolo now builds the checkout at "path" with the pack's own build line and runs the pack's own file; remove "run"`
- **After:** `config.programs.overrides.x.run: yolo no longer reads this key. It builds your checkout at "path" the way the pack does. Remove "run".`
- **Why:** 2.

### Read-write mount request not allowed
- **Where:** `internal/config/mountsrequest.go:172`.
- **Before:**
  ```text
  config.mounts: <spec> asks for a writable host mount (write = true), and your user config does not let a project ask for one. To let projects request writable mounts, each still approved by you at launch, run `yolo config add allow.mounts.on_request write --user` or add to <file>:
      [allow.mounts]
      on_request = ["write"]
  Otherwise remove `write = true` from the entry and it mounts read-only
  ```
- **After:**
  ```text
  config.mounts: <spec> asks for a writable host folder (write = true), and your config does not let projects ask for one.
  To let projects ask, run on the host: yolo config add allow.mounts.on_request write --user
  You still approve each one at launch. Or remove `write = true`, and the folder mounts read-only.
  ```
- **Why:** 2 and 7. The hand-edit alternative can go, because the verb is enough (AGENTS.md: "Advice names a verb").

### Read-write mount in the old workspace format
- **Where:** `internal/config/mountsrequest.go:187`.
- **Before:** `config.mounts: <file> is still in the deprecated JSONC format, and a writable mount request (<spec>) needs the TOML format. Convert it first: …; the converted entry is then a request your user config must allow, approved by you at launch. Or move the entry to <user file>, where a read-write mount needs no request, or make it read-only.`
- **After:** `config.mounts: a project can ask for a writable folder (<spec>) only in a TOML config, and <file> is still JSONC. Convert it with `yolo config migrate`. Or move the entry to <user file>, or make it read-only.`
- **Why:** 2, 7.

### Mount allowance `always`
- **Where:** `internal/config/mountsrequest.go:70`.
- **Before:** `config.allow.mounts.always: mounts have no `always`. Every writable mount a project requests is approved by you, per project, at the config-change prompt, so a standing grant is the one thing this table cannot say. Write `on_request = ["write"]` …`
- **After:** `config.allow.mounts.always: mounts have no "always" setting, because you approve each writable folder at launch. Write on_request = ["write"] instead.`
- **Why:** 2, 5 ("standing grant").

### Mount reaches a protected host path
- **Where:** `internal/config/mountsrequestbind.go:465`.
- **Before:** `config.mounts: "<spec>" reaches <path>, <what> (<owner>). A workspace config may not mount it: the agent can edit that file, and the mount would carry it into the next launch. Remove the entry; the same entry in <user file> is allowed and disclosed`
- **After:** `config.mounts: "<spec>" includes <path>, <what>. A workspace config cannot mount it. Remove the entry, or move it to <user file> if you want the jail to have it.`
- **Why:** 8.

### host_files collides with a pack
- **Where:** `internal/config/hostfiles.go:1240`.
- **Before:** `<path>: destination <dest> is also composed by a selected pack — two writers for one file is refused rather than resolved, because there is no precedence rule that would not silently overwrite one of them. Drop the host_files entry, or deselect the pack that composes that surface`
- **After:** `<path>: a selected pack already writes <dest>. Remove the host_files entry, or remove that pack.`
- **Why:** 6 ("surface", "composed"), 8.

### host_import_exclusions with host_management own
- **Where:** `internal/config/hostimportexclusions.go:165`.
- **Before:** `config.host_import_exclusions: non-empty exclusions <list> cannot be used with host_management: own; host-owned values must use the pack contribution's existing host-versus-jail targeting, or remove the exclusions`
- **After:** `config.host_import_exclusions: you cannot list exclusions while host_management is "own". Remove the exclusions.`
- **Why:** 6 (contribution), 8. The first remedy ("use the pack contribution's existing … targeting") is pack-author language, and a user cannot act on it.

### host_import_exclusions surface errors
- **Where:** `internal/config/hostimportexclusions.go:51, 62, 67, 71`.
- **Before:** `…: this surface cannot receive an object host layer (it must declare readsHost and use an object codec)`
- **After:** `…: yolo does not import your host settings into this file, so it has nothing to exclude. Remove the entry.`
- **Why:** 6 ("surface", "host layer", "codec"). The other three lines need "surface" replaced with "config file" (for example, "expected a config file named agent/name").

### Provisioner nix warning
- **Where:** `internal/config/provisioners.go:149`.
- **Before:** `nix is ranked above the pack's own recipe for %s, but an agent yolo's host floor holds does not come from nix yet (docs/design/provisioner-sets.md PS-D2): the floor keeps its own copy, …`
- **After:** `You ranked nix first for %s, but yolo still installs it its own way, because it is in host_floor. To use nix, remove its pack from host_floor and install it yourself.`
- **Why:** 6 (floor, decision ID), 8.

### Provisioners in a jail
- **Where:** `internal/config/provisioners.go:348`.
- **Before:** `no order is read in a jail: its agents come from the pack's own launcher and capture store, never another provisioner (docs/design/provisioner-sets.md OQ-PS6), so this list would change nothing. "host" is the one environment this yolo resolves; remove it`
- **After:** `yolo uses this order only on the host, so it does nothing here. Use "host", or remove this entry.`
- **Why:** 6, 8.

### Writable home path already managed
- **Where:** `internal/config/writablehome.go:296`.
- **Before:** `first path segment '.x' is already managed read-write by yolo (the selected pack p declares it) — paths under it are writable without this key`
- **After:** `.x is already writable in the jail (the pack p makes it so). Remove this entry.`
- **Why:** 2. It needs a next step.

## Brokers, grants and requests

### Broker refuses a call with no read grant (agent-facing)
- **Where:** `internal/config/brokered.go:386` (`BrokerNoReadRefusal`) — an agent's `git fetch` or `gh` call is refused, and the agent relays this text to the user.
- **Before:**
  ```text
  this workspace holds no read grant on github.com/o/r, so nothing ran: the github broker runs whenever its pack is selected, but grants nothing until a grant in config does. To let every workspace read its own approved remotes through it, run `yolo allow github read` on the host (it adds 'read' to `allow.github.always` in your user config, ~/.config/yolo-jail/config.toml, or the file of it that already holds [allow]), then approve the workspace change it offers, or run `yolo config apply` on the host, in the workspace. This jail's running broker takes the grant at its next call.
  ```
- **After:**
  ```text
  Nothing ran, because this workspace has no read access to github.com/o/r. To let every workspace read its own remotes, run on the host:
      yolo allow github read
  Then answer y when it asks to apply the change, or run `yolo config apply` in this workspace. The next call here picks it up.
  ```
- **Why:** 3, 5, 7, 8. The `!live` branch becomes "Then restart the jail and approve the change at launch."

### Read grant advice (all variants)
- **Where:** `internal/config/brokered.go:327, 357, 362` (`BrokerGrantAdvice`, `brokerReadGrantAdvice`), used by several refusals.
- **Before (TOML branch):** see the previous entry, from "To let every workspace…".
- **After:**
  ```text
  To let every workspace read its own remotes, run `yolo allow <source> <capability>` on the host, then approve the change it offers (or run `yolo config apply` in the workspace).
  ```
  JSONC branch: `First convert your user config with `yolo config migrate` on the host. Then run `yolo allow <source> <capability>` there and approve the change.`
- **Why:** 2, 8 ("it adds 'read' to `allow.x.always` in…").

### Loophole selected but grants nothing
- **Where:** `internal/config/brokered.go:1688`.
- **Before:** `loophole github is selected and grants nothing: your user config holds no read grant for the 'github' source, so it refuses every call from this workspace, and its config-change prompt asks nothing about it (beyond showing, once, the removal of a read grant it approved earlier). <advice>`
- **After:** `github is selected, but your config gives it no read access, so it refuses every call from this workspace. <advice>`
- **Why:** 6, 7, 8.

### Carried read grant
- **Where:** `internal/config/brokered.go:1682`.
- **Before:** `loophole github is selected, and your user config holds no read grant for the 'github' source: here it runs on the read grant this workspace's approval record carries from an older yolo's switch, and it refuses every call from a workspace that holds …`
- **After:** `github has read access here only because an older yolo turned it on for this workspace. Other workspaces get no access. <advice>`
- **Why:** 6, 8. Whoever rewrites this should read the full message first, since the dump cut it off.

### Grant kind not allowed by the user config
- **Where:** `internal/config/brokered.go:1339` with `grantKindAllowAdvice` at :1383–1398.
- **Before:** `…: your user config does not let a workspace hold a 'create-branch' grant. Allow it with `yolo allow …` on the host, then run `yolo config apply` again; or remove this grant.`
- **After:** fine. It needs a capital letter only when it starts a line.
- **Why:** none; listed so reviewers know it was checked.

### Grant user-config shape
- **Where:** `internal/config/brokered.go:248`.
- **Before:** `….kind: 'push' is not a grant the user config holds: it holds only {"kind": "read", "repo": "*"}, read access for every workspace to its own approved remotes. Any other grant names its repository in the workspace config of the project that holds it, yolo-jail.jsonc, or yolo-jail.local.jsonc beside it, and the user config allows its kind in `brokered.x.grant_kinds`. Move it there, or remove it.`
- **After:** `….kind: your user config can hold only the read grant for every workspace. Ask for 'push' from the project instead: run `yolo request x push <values>` there, and allow it on the host with `yolo allow x on_request push`.`
- **Why:** 2, P4. Name the verbs, and do not describe the file layout.

### Valued kind under `always`
- **Where:** `internal/config/loopholegrants.go:132`.
- **Before:**
  ```text
  allow.git.always lists push, which carries a value only a project can give.
    Move it:  yolo config unset allow.git.always push   then  yolo allow git on_request push
  ```
- **After:**
  ```text
  allow.git.always lists push, but push needs a value (such as a branch name) that only a project can give.
    Fix it:
      yolo config unset allow.git.always push
      yolo allow git on_request push
  ```
- **Why:** 5 ("carries a value"). The fix also follows the repo rule that each command goes on its own line.

### Call-broker request outside the allowance
- **Where:** `internal/config/callbroker.go:318`.
- **Before:** `config.requests.x.working_set.y: call-broker default/request is outside the user ceiling and will not run; allow it on the host with `yolo allow x on_request working_set.y`, or remove the request`
- **After:** `config.requests.x.working_set.y: your config does not allow this request, so it will not take effect. Allow it on the host with `yolo allow x on_request working_set.y`, or remove the request.`
- **Why:** 6 (ceiling), 5.

### Call-broker tables
- **Where:** `internal/config/callbroker.go:260, 266, 272, 276, 314`; `internal/config/readableallow.go:1304, 1309, 1315, 1318, 1347`.
- **Before:** `config.requests.x.y: table/cell pair is not declared by the call-broker cell; use a declared table or remove this key`
- **After:** `config.requests.x.y: x does not accept this setting here. Use working_set or all, or remove this key.`
- **Why:** 5, 6 ("table/cell pair").

### Readable allow/requests declaration errors
- **Where:** `internal/config/readableallow.go:243, 259, 272, 295, 301, 314, 317, 347, 356, 367, 376, 417, 433, 452, 463, 470, 538, 542, 559, 565`.
- **Before:** `allow.x has ambiguous unselected broker declarations; remove the collision or declare a selected source`
- **After:** `allow.x: two packs that are not selected both define x, so yolo cannot tell which one you mean. Select one with `yolo pack add <pack>`.`
- **Why:** 5, 6. The other "ambiguously names both … and a pack setting" lines (:272, :301, :347, :356, :367, :452, :463, :538) are pack-author conflicts. Each can read: "allow.x.y means two different things, because the pack p also has a setting y. Report this to the pack's author."

### Pending declaration
- **Where:** `internal/config/readableallow.go:596, 1338`.
- **Before:** `declared semantic action; H runtime delivery remains pending and use is not activated` / `call-broker cell request is declaration metadata; runtime authority is not supplied here`
- **After:** `yolo does not act on this request yet.`
- **Why:** 1 ("H" is a work-package letter), 6. If these reasons are ever printed, as the `Reason` field suggests, they must not show plan letters.

### Workspace `requests` in user config
- **Where:** `internal/config/validaterequestsscope.go:27`.
- **Before:** `config.requests: a project's key, not the user config's — it asks you for access at launch, and your config answers with `allow`. Remove it from <file>, and in the project that needs it run `yolo request <source> <capability> [values]` …`
- **After:** `config.requests: only a project's config holds requests. Remove it from <file>, and run `yolo request <source> <capability> [values]` in the project that needs it (for example `yolo request git push main`).`
- **Why:** 2, 8.

### Brokered repos not taken
- **Where:** `internal/config/brokered.go:1426`.
- **Before:** `config.brokered.git.repos: not taken — this source's scope is every remote of the workspace, on any host, as its URL, so there is nothing to list here. Add a remote with `git remote add <name> <url>`, and the next launch's config-change gate asks about it; then remove this key.`
- **After:** `config.brokered.git.repos: git already covers every remote of this repository, so this list does nothing. Add a remote with `git remote add <name> <url>` instead; the next launch asks you to approve it. Then remove this key.`
- **Why:** 5 ("config-change gate"), 6 (scope).

### Brokered key outside the workspace
- **Where:** `internal/config/brokered.go:1561`.
- **Before:** `<file>: config.brokered: written in a file outside this workspace, reached through <how>. A `brokered` key is read only from this workspace's own config files, so an include or a link cannot point its repository scope at another project's list. Move the key …`
- **After:** `<file>: config.brokered is in a file outside this workspace (reached through <how>), so yolo ignores it. Move the key into yolo-jail.toml or yolo-jail.local.toml, or make the link point inside the workspace.`
- **Why:** 8.

### Unselected broker source
- **Where:** `internal/config/brokered.go:1638`.
- **Before:** `config.brokered.x: no loophole a selected pack ships brokers the source 'x', so this entry does nothing here (selected sources here: …). Check the spelling against a selected loophole manifest's `brokered.source` or `call_broker.source`; select its pack with `yolo pack add <name>`, or do nothing if no selected pack should provide it.`
- **After:** `config.brokered.x: no selected pack provides x, so this entry does nothing (selected here: …). Check the spelling, or select the pack that provides it with `yolo pack add <name>`.`
- **Why:** 1 (manifest field names), 6.

### Mount reaches broker state
- **Where:** `internal/config/brokerfence.go:72`.
- **Before:** `config.mounts: "<spec>" (user config) reaches <path>, <what> (<owner>); the jail can read AND write it. Remove the entry if the jail should not have this access.`
- **After:** fine as is. The `<what>` texts at `internal/config/mountsrequestbind.go:416–436` contain "brokers' store", "call broker", and "loophole". Rewrite them as, for example, "where yolo keeps the credentials it uses for git and GitHub".

## Providers and profiles

### Profile name reserved
- **Where:** `internal/config/profiles.go:136`.
- **Before:** `… means "no provider: the agent's own login" (OQ-RC20), so a profile by that name could …`
- **After:** drop "(OQ-RC20)". P6.

### `profile` with `active_provider`
- **Where:** `internal/config/validateactiveprovider.go:26`.
- **Before:** `config.active_provider.*: "*" names no agent. It was the old `profile` table's …`
- **After:** `config.active_provider.*: "*" is not an agent name. Use active_provider.default for every agent.` (Check that the rest of the original says the same.)
- **Why:** 8.

### `via` option
- **Where:** `internal/config/profiles.go:197`.
- **Before:** `…via: expected a service pack name such as "wire-bridge" — via names the pack whose service carries …`
- **After:** `…via: expected the name of a pack that runs a service, such as "wire-bridge".`
- **Why:** 8.

### `enforce_models`
- **Where:** `internal/config/profiles.go:184`.
- **Before:** `….enforce_models: expected true or false — whether the agents refuse a model outside a list a `models` contribution narrowed (on by default)`
- **After:** `….enforce_models: expected true or false. It controls whether agents refuse models that are not in the list (on by default).`
- **Why:** 6 (contribution).

### Option value null
- **Where:** `internal/config/profiles.go:208`; `internal/config/validateproviders.go:765`.
- **Before:** `…: expected a string option value (omit the key to leave the option unset; a null here is not the delete it is elsewhere in this config)`
- **After:** `…: expected a string. To leave the option unset, remove the key.`
- **Why:** 4 ("the delete").

### Active provider selection errors
- **Where:** `internal/config/providersemantic.go:262, 273, 276, 288`.
- **Before:** `active_provider.x: selected pack does not declare provider_sets, so this receiver can select only one provider`
- **After:** `active_provider.x: this agent can use only one provider at a time. Name one.`
- **Why:** 1 (`provider_sets`), 6 ("receiver"). The same applies to :262 ("provider-capable receiver" becomes "no selected pack installs an agent named x") and :276 ("native" needs to be defined: "the agent's own login cannot be listed together with other providers").

### Subagents shape
- **Where:** `internal/config/providersemantic.go:392–413`.
- **Before:** `subagents has unknown field "x"`
- **After:** `subagents: unknown key "x". It takes default and roles.`
- **Why:** next step.

### Profile CLI not installed
- **Where:** `internal/config/validateprofiles.go:270`.
- **Before:** `no pack installs a CLI named "x" (installed: …) — a `profile` key selects a profile by the binary a pack installs, not by pack or agent name ("*" is the one key that names no CLI: every agent the object does not name)`
- **After:** `no selected pack installs a program named "x" (installed: …). Name the program, such as "claude", not the pack.`
- **Why:** 2, 7.

### Offline model catalog
- **Where:** `internal/config/editvalidation_semantics.go:132, 136`.
- **Before:** `<msg>. A selected pack declares this model; to supply the fact, declare the model under [providers.p.models] in your user config with it. A launch runs without it, and a config edit is refused only when it writes such a model`
- **After:** `<msg>. Add it under [providers.p.models] in your user config. Launches still work without it.`
- **Why:** 5 ("supply the fact"), 8.

## Migration: `yolo config migrate`

### Migration validation found a bug
- **Where:** `internal/configmigrate/gate.go:26`.
- **Before:** `the converted files would be refused by the next launch, so nothing was changed: … This is a yolo bug: …`
- **After:** fine. Capitalize the first word when it begins a line.

### Pack has no TOML spelling
- **Where:** `internal/configmigrate/packs.go:53`.
- **Before:** `a pack entry's `x` has no TOML spelling (a pack is named by its source, OQ-RC15); this yolo has no TOML spelling for it yet, so leave it: this yolo keeps reading the old file`
- **After:** `this yolo cannot write a pack's "x" setting in TOML yet. Keep the old file for now; yolo still reads it.`
- **Why:** 6, 7. The message also says "no TOML spelling" twice. The same applies to `noSpellingYet` and its users at `internal/configmigrate/packs.go:104, 110` and `internal/configmigrate/brokered.go:196, 277, 331, 333`.

### Transcript: canonical JSON
- **Where:** `internal/configmigrate/transcript.go:88`.
- **Before:** `Effective config before and after: identical (checked as canonical JSON, 1234 bytes).`
- **After:** `The converted config means exactly what the old one does.`
- **Why:** 8.

### Transcript: records respelled
- **Where:** `internal/configmigrate/transcript.go:112, 116`.
- **Before:** `Records only yolo reads, respelled in place: …` / `  approval /path is left: it is the host's, outside this jail; the next launch asks once.`
- **After:** `yolo also updated its own records: …` / `  The approval record /path was not changed, because it is on the host. The next launch asks you once.`
- **Why:** 5, 6.

### Transcript: ceiling lines
- **Where:** `internal/configmigrate/transcript.go:58`.
- **Before:** `  + <line>: the converted workspace requests it, and its old switch admitted it`
- **After:** `  + <line>: added, because the old config turned this on for the project`
- **Why:** 5.

### Transcript: patterns
- **Where:** `internal/configmigrate/transcript.go:98`.
- **Before:** `A pattern converts as written, since a request accepts one just as a grant did: …. Names listed one by one stay listed one by one.`
- **After:** `Patterns stay as written: ….`
- **Why:** 8.

### Transcript: derived vendor
- **Where:** `internal/configmigrate/transcript.go:108`.
- **Before:** `  vendor "x" follows from the id of "providers.p.models.m", compared as derived; the launch now carries it.`
- **After:** `  providers.p.models.m: vendor "x" is now inferred from the model id.`
- **Why:** 5, 8.

### Activation differences
- **Where:** `internal/configmigrate/activation.go:188, 191, 194, 198`.
- **Before (188):** `the converted config runs x because its pack is selected, and the old one keeps it off; write `off = true` under [allow.p] in the TOML user config to keep it off, or remove the `"enabled": false` that keeps it off in the old file to let it run, then run `yolo config migrate` again`
- **After:** `The converted config would run x, because its pack is selected, but the old config keeps it off. To keep it off, add `off = true` under [allow.p] after converting. To let it run, remove "enabled": false from the old file. Then run `yolo config migrate` again.`
- **Why:** 7. In the original, the TOML file does not exist yet when this message prints (the plan refused). Also, :191 says "add "enabled": true … to keep it running" in a case where the old config does not run it. Change "keep it running" to "run it".

### Plan refused
- **Where:** `internal/configmigrate/plan.go:1175`.
- **Before:** `this plan did not prove the converted config equal to the old one, so it writes nothing; run `yolo config migrate` again to see why`
- **After:** `yolo could not confirm the converted config means the same as the old one, so it wrote nothing. Run `yolo config migrate --dry-run` to see the differences.`
- **Why:** The original says "again to see why", which does not explain how. I confirmed `--dry-run` works for this: `internal/cli/configmigrate.go:145–161` prints `plan.Transcript()` in both modes, and the transcript includes every difference and its "next:" line (`internal/configmigrate/plan.go:166–181`). With `--dry-run`, it then prints "Dry run; nothing was written." The error itself comes only from `Apply` at `internal/configmigrate/apply.go:61`, as a guard.

### Migration stopped part way
- **Where:** `internal/config/migrationjournal.go:56`.
- **Before:** `config: `yolo config migrate` stopped part way while it moved your converted config files into place (its list of moves is <journal>), so your config files are half old and half new. Run `yolo config migrate` to finish the moves it recorded; it only moves files forward and keeps every original as <name>.pre-migrate`
- **After:** `config: `yolo config migrate` stopped part way, so some config files are converted and some are not. Run `yolo config migrate` to finish. It keeps every original as <name>.pre-migrate.`
- **Why:** 2, 8.

### Gitignore note
- **Where:** `internal/configmigrate/gitignore.go:75`.
- **Before:** `<name> is not ignored by git as <old> was; nothing was added to any ignore file. To keep it out of commits, add the line `<name>` to .gitignore or .git/info/exclude`
- **After:** fine.

## Rare internals

- `internal/config/drift.go:81`: "a later launch of this workspace replaced the boot baseline this session started from". Rewrite: "Another launch of this workspace has started since this jail did, so yolo cannot compare against this jail's config. Restart the jail to compare again." Rule 6 (boot baseline). It also needs a next step.
- `internal/config/callbrokerpolicy.go:101`: "call broker policy is incomplete; retry the launch after checking its config". It does not say which config. Rewrite: "…; run `yolo check` and launch again", matching :106.
- `internal/config/readableallow.go:404`: "selected pack \"x\" has no allow.x settings; declaration defaults are not promoted into user authority". Rewrite: "pack x is selected, but your config sets none of its allow.x settings, so it uses its defaults." Rules 5 and 6.
- `internal/config/readableallow.go:741–971`: migration "Differences" strings ("preserve typed target/raw state and write nothing", "L must preserve…", "do not activate a dormant service"). These read as implementation notes. The second pass found one route to the user. `internal/config/workspacebrokergrants.go:82, 108` put these strings in a list of grants it could not place. That list enters the comparison as `workspace_unplaced` (`internal/configmigrate/brokered.go:99`), so the strings can show up in the before and after lines of a migration difference. Each one needs a plain sentence, such as "this grant has no new spelling yet. Keep the old file, which yolo still reads."
- `internal/config/derived.go:192, 201`: the grep and find block messages are fine.

## Second pass: inherited user config at launch

A launch inside a jail reads a copy of the user's config, called the inherited config. When a setting cannot cross into that copy, the launch prints a line shaped as "path (where): reason" (`InheritOmission.String()`, disclosed by `internal/cli/run/inheritscope.go:155`).

The census table in `internal/config/inherit.go` holds about 115 `reason` strings. They never print. Only `InheritDisposition` (`inherit.go:451`) returns them, and only tests call it. No rewrite is needed. `yolo config why`, `config-ref` and `yolo check` do not read them.

### Setting left out of the jail's copy
- **Where:** `internal/config/inherit.go:896`. The same tail appears at `internal/configmigrate/inherited.go:66`.
- **Before:** `<message>; the TOML user scope the jail reads has no spelling for it, so the jail reads its user scope without it, and the host's own launch reads it as before`
- **After:** `<message>. The jail cannot use this setting yet, so it runs without it. Launches on the host still use it.`
- **Why:** 4, 6 and 7. "User scope" and "spelling" are project words. Nothing is the user's to fix, so the last sentence works as the next step: no action is needed.

### Inline loophole left out of the jail's copy
- **Where:** `internal/configmigrate/inherited.go:104`.
- **Before:** `an inline loophole declaration has no spelling in the TOML user scope the jail reads, so a launch inside the jail does not declare it; the host's own launch runs it as before, and `yolo pack init` makes it a pack, which crosses`
- **After:** `The jail cannot use a loophole written straight into your config, so it runs without it. Launches on the host still use it. To use it in the jail too, turn it into a pack with `yolo pack init`.`
- **Why:** 4, 6 and 7. "Crosses" is a project word.

### Loophole switched off on the host only
- **Where:** `internal/configmigrate/inherited.go:109`.
- **Before:** `` `enabled: false` beside the pack's selection has no spelling in the TOML user scope the jail reads, so a launch inside the jail runs that loophole; the host's own launch keeps it off as before ``
- **After:** `The jail cannot use your "enabled: false" for this loophole, so a launch inside the jail runs it. Launches on the host keep it off. To keep it off in the jail too, remove its pack from your packs list.`
- **Why:** 6 and 7. This one turns something on, so it needs a next step. The suggested step follows from the message itself: the loophole runs because its pack is selected. I did not check whether `yolo pack remove` is the right verb for a user-scope pack, so a reviewer should confirm it.

### Fallback to the old format
- **Where:** `internal/config/inherit.go:848`.
- **Before:** `legacy user-scope values are not representable by the current TOML reader: <err>`
- **After:** `The jail got your config in the old format, because the new format cannot hold some of its values yet: <err>`
- **Why:** 4 and 6. A related fallback line at `internal/cli/run/inheritscope.go:173` is outside this partition.

## Second pass: the rest of `yolo config migrate`

I read `lower.go`, `loopholegrants.go`, `identities.go`, `models.go`, `inherited.go`, `keyedstate.go`, `defaults.go` and `emit.go` line by line. `models.go` adds only the difference path "(model order)". `defaults.go` and `emit.go` hold no prose.

### No conversion yet
- **Where:** `internal/configmigrate/lower.go:330, 332`, and the `noSpellingYet` constant at `lower.go:594`.
- **Before:** `this yolo has no lossless conversion to <new> yet; rewrite it by hand in the new spelling, or leave it: this yolo keeps reading the old file` and `this yolo has no TOML spelling for it yet, so leave it: this yolo keeps reading the old file`
- **After:** `yolo cannot convert this setting yet. Leave it, since yolo still reads the old file, or rewrite it by hand as <new>.` and `yolo cannot write this setting in TOML yet. Leave it: yolo still reads the old file.`
- **Why:** 6 ("lossless", "spelling"). These are close already. The rewrite puts the safe choice first.

### No row in the rename table
- **Where:** `internal/configmigrate/lower.go:386, 507`.
- **Before:** `the rename table has no row for it`
- **After:** `yolo does not know a new name for this setting yet. Leave it: yolo still reads the old file.`
- **Why:** 6 and 8. "Rename table" names the code. The original has no next step.

### Project cannot switch a loophole on
- **Where:** `internal/configmigrate/loopholegrants.go:619`.
- **Before:** `<name> runs whenever its pack is selected, and a project cannot lift the user's off, so the key turns nothing on`
- **After:** `<name> runs whenever its pack is selected, so this setting does nothing and is dropped. A project cannot turn back on what your user config turns off.`
- **Why:** 4 ("the user's off" uses an adverb as a noun) and 6 ("lift").

### Grant would be lost
- **Where:** `internal/configmigrate/loopholegrants.go:807–823`, the four `Next` lines.
- **Before (the third, as an example):** `the converted files would take away what the old ones hold here (<before>); write the grant's line in the old files (`"enabled": true` under loopholes.<name>, or its settings) so the projection keeps it, or keep the old files (this yolo keeps reading them), then run `yolo config migrate` again`
- **After:** `The new files would drop this grant (<before>). To keep it, add "enabled": true under loopholes.<name> in the old file and run `yolo config migrate` again. Or keep the old files: yolo still reads them.`
- **Why:** 2, 6 and 7. Each of the four is one sentence of about 60 words. "Projection" names the code. Apply the same split to the other three: give the reason in one sentence, the fix in one, and "or keep the old files" in one. The second line's command (`yolo allow <pack> on_request <kind>`) must stay.

### Background update state
- **Where:** `internal/configmigrate/keyedstate.go:214, 217`.
- **Before:** `background update state of <old> left: a background advance holds it, so it starts over as <new>`
- **After:** `Did not move the update progress for <old>, because an update is running. Updates for <new> start again from the beginning.`
- **Why:** 3 ("left" has no subject), 4 ("advance" used as a noun) and 6. Nothing is the user's to fix, and the line says so by stating the outcome. Lines :141, :146, :266, :286 and :291 use the same "state of … left" form and take the same fix.

### Pack records that could not move
- **Where:** `internal/configmigrate/identities.go:131, 234, 270, 279, 287, 290`. They print through `internal/cli/run/packrefresh.go:128` and `internal/cli/packsourcerename.go:298`.
- **Before (example):** `pack records under <old> could not all move to <new>, because <new> already has its own: both are kept, and every launch tries the move again. To keep the ones under <old>, delete each newer …`
- **After:** fine. Optionally, write "saved pack versions" instead of "pins and records" where those words appear.
- **Why:** Each one names a next step and what happens if the user does nothing.

## Second pass: the approval prompt for repositories and brokers

`internal/config/scopeapproval.go` builds the block the user approves with `y` when a workspace's repository list or broker grants change. I read the whole block.

### Header
- **Where:** `scopeapproval.go:616`.
- **Before:** `<Label> <kind> scope, read from this workspace's .git/config and <files>:`
- **After:** `<Label> repositories the agent may use, read from this workspace's .git/config and <files>:`
- **Why:** 6. "Scope" is a project word. I assumed the git kind here. Each `kind` needs its own plain noun.

### Count line
- **Where:** `scopeapproval.go:715`. The grant count at `:781` is joined to it as "; grants: …" (`:551`).
- **Before:** `<Label>: %d added, %d removed, %d source changed`
- **After:** `<Label>: %d added, %d removed, %d now read from a different place`
- **Why:** The plural is wrong when the number is not 1 ("2 source changed"). The fix also says what changed.

### Empty read
- **Where:** `scopeapproval.go:710`.
- **Before:** `  (the scope read as empty: <problem>)`
- **After:** `  (yolo could not read the repository list, so it treats it as empty: <problem>)`
- **Why:** 3 and 6. The original is a fragment.

### Standing grant
- **Where:** `scopeapproval.go:643–645`.
- **Before:** ` (now covered by the standing grant <g>)`
- **After:** ` (your user config already allows this through <g>)`
- **Why:** 6. "Standing grant" is a project word. The second half of :755, "(from your user config, for every workspace)", already speaks plainly, so match it.

### Call-broker declaration header
- **Where:** `scopeapproval.go:568`.
- **Before:** `selected call-broker declaration for <src>:`
- **After:** `What the <src> broker will be allowed to do (changed since you last approved it):`
- **Why:** 5 and 6. The header prints only when the declaration changed (`declarationChanged`), so the rewrite says so.

### Call-broker disclosure lines
- **Where:** `internal/config/callbroker.go:~83–135` (`CallBrokerDisclosure`), and `callbroker.go:49` for the "http selected pack=" and "http declaration_sha256=" lines.
- **Before:** raw key=value lines, such as `call broker: <src> front=… program=… executor=… scope roots=…`, `cell N tables=… default=working_set=true,all=ask max_for=…` and `http origin=… credential_source=… credential_key=… credential_header=…`.
- **After:** one plain line per fact, keeping the values. For example: `Runs <program> on <executor>, only under <roots>.`, `Sends requests to <origin> with your <credential_source> credential in the <credential_header> header.` and `For <tables>: acts without asking on things it created, asks for everything else, and an approval lasts at most <max_for>.` The two "joins working set" lines are covered in "Brokers, grants and requests" above.
- **Why:** 5, 6 and 8. This block is where the user decides what to trust, and they cannot read field names. The code comment says the block is "the exact selected call-broker policy", so a rewrite must keep every value, including the program's sha256. A plain sentence can sit in front of each value. If exact field names are needed for audit, `yolo config why` or a `--verbose` view could show them. The meanings of `cell`, `default` and `working_set` come from `docs/design/pack-brokers.md` §7.4. I did not check the rarer fields (`classifier`, `root_template`, `read_over_post`) against a design doc, so their plain wording needs a reviewer.

## Second pass: provider errors a user can hit

The provider files hold many pack-author errors. The ones below reach a user through their own config, or through `yolo config set|add|unset`. Edit validation runs `BuildReadableSemanticInputs` (`internal/config/editvalidation_semantics.go:106`), which checks the old provider keys.

### Old provider keys cannot be converted
- **Where:** `internal/config/providersemantic_input.go:154`.
- **Before:** `legacy provider consumers cannot be represented canonically at <path>: <reason>`
- **After:** `<path> uses the old profile settings, which yolo cannot read together with this change: <reason>. Run `yolo active-provider` to choose a provider the new way, then remove <path>.`
- **Why:** 5 and 6. The embedded `<reason>` comes from about 40 strings in `providersemantic_consumer.go` (:192–607) and `providersemantic_legacy.go` (:50–324), such as "tombstones have no canonical fact-presence representation" and "effective composed provider field has no canonical declaration mapping". These are implementation notes. A user who runs `yolo config set` on a config with an old `profile` key can see them. The best fix is to keep them out of the user's line: print the path and the next step, and send the reason to the log. I did not check that `yolo active-provider` covers every case these reasons describe.

### Both selection keys set
- **Where:** `internal/config/providersemantic_consumer.go:25` (`activeProviderWithProfileProblem`). `validateactiveprovider.go:31` also uses it.
- **Before:** `config: active_provider and legacy profile selectors cannot be composed together; preserve the legacy reader for old files or select through active_provider`
- **After:** `config: active_provider and profile are both set, and yolo can use only one. Remove profile with `yolo config unset profile`, and keep active_provider.`
- **Why:** 4, 6 and 8. "Preserve the legacy reader" names the code. The user cannot act on it.

### Provider name used twice
- **Where:** `internal/config/providersemantic_selection.go:234, 425`.
- **Before (:425):** `config.<providers>: provider namespace collision "<name>" between a pack's profile over provider <p> and <q>; every name selects one configuration, so rename one in your config's `<providers>` table …`
- **After:** `config.<providers>: two providers are named "<name>": a pack's profile for <p>, and <q>. Rename one in your config's <providers> table, or remove the pack that declares it.`
- **Why:** 5 and 6. "Namespace collision" is a project phrase. Both lines already name a next step.

### Selector shape
- **Where:** `internal/config/providersemantic_consumer.go:130, 136, 142`.
- **Before:** `<path>: expected a non-empty provider, provider/variant, or native selector`
- **After:** `<path>: give a provider name, such as "anthropic" or "anthropic/opus", or a list of them.`
- **Why:** 6 ("native selector") and the happy-path rule. An example tells the user what to write. The example values are placeholders: a reviewer should replace them with real provider names from the shipped packs.

## Wrong today

- `internal/config/validate.go:243`: the repo_path warning says yolo finds the repo "from the flake bundle its install shipped (or YOLO_REPO_ROOT)". `YOLO_REPO_ROOT` is refused now: `internal/reporoot/reporoot.go:86` errors with "YOLO_REPO_ROOT=%s no longer selects a launch source". Drop the parenthetical.
- `internal/config/validate.go:1254` (mcp_presets), `:1855` (cache_relocations), `internal/config/programs.go:279`, `internal/config/profiles.go:362`, `internal/config/hostfiles.go:963`: these tell the user to move a key to `~/.config/yolo-jail/config.jsonc`, a fixed path. TOML is the default, and the sibling messages use `paths.UserConfigDisplayPath()` (`internal/paths/paths.go:1269`), which names the file that exists, or `config.toml`. A TOML user who follows this advice creates a `.jsonc` beside `config.toml`, and yolo refuses that ("a TOML file beside a legacy one is refused", AGENTS.md). validate.go:1254 also gives JSON array syntax.
- `internal/config/snapshot.go:245`: "approve it for THIS LAUNCH ONLY by re-running with --accept-config-changes, which records the new config as approved exactly as answering `y` would." Recording it as approved means later launches do not ask again, so the change is not for this launch only. The two halves of the sentence contradict each other. The rest of the same message makes it clear that the flag records the approval permanently.
- `internal/config/validate.go:569, 646, 683, 722, 749, 775` and similar P1 sites: each says "a workspace value has no effect", but the message is added to `errs`. On the host, a validation error stops the launch and `yolo check`. I confirmed this in `internal/cli/run/preflight.go:39–55`, which prints "Invalid jail config" and returns. So the value does have an effect: the launch refuses until the user removes it. The P1 template above already says "refuses".
- `internal/config/snapshot.go:265` (`WithoutBrokerStep`): it tells the user to "take the pack that ships it out of `packs`" but does not name the pack. The `name` argument is the broker label, not the pack. The message is not false, but the user cannot act on it.


## Not covered

- `internal/config/providersemantic_derive.go`: only a pack bug reaches these errors, so a pack author sees them, not a config user. I did not rewrite them.
- The individual reason strings in `providersemantic_consumer.go` and `providersemantic_legacy.go`: about 40 of them. I covered them as one group ("Old provider keys cannot be converted") instead of one by one.
- `internal/config/readablescope.go:329`: the `gate.Differences` strings. I did not trace them to any output.
- `internal/cli/run/inheritscope.go:173` and `internal/packmigrate`: these are outside the partition.
- The rare call-broker disclosure fields (`classifier`, `root_template`, `read_over_post`): I did not find a design doc that defines them, so I gave no plain wording for them.
