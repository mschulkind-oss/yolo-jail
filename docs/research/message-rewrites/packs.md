# Message rewrites: Packs, programs, skills and host rendering

Scope: non-test `.go` files in `internal/packload`, `internal/packdecl`, `internal/packsrc`, `internal/agentcfg`, `internal/render`, `internal/hostskills`, `internal/loopholes`, `internal/loopholedecl` and `internal/hostfloor`. Messages read: about 1,200. That count is approximate. It covers every `fmt.Errorf`, `errors.New`, `Fprint*` and long `Sprintf` site, plus the literal-string launch lines in `packload`, every `yolo pack footprint` detail string, and the `internal/cli` lines that print `packsrc` git errors. Rewrites proposed: 86 single entries, plus 9 patterns that cover about 120 more sites.

Who reads these messages:

- **Everyone** meets the launch lines in `packload`: provider credentials, regions, profiles, and lines that start "Not set at this notch". Everyone also meets the progress lines that `hostfloor` prints during `yolo host`, and the output of `yolo loopholes`.
- **Pack authors** meet most of `packdecl`, `loopholedecl` and `agentcfg`. A pack author is still a user, so the rules apply. Most of these messages are already specific and name the field to fix. The patterns below are the main problems.

## Patterns

### P1. Decision IDs and design-doc paths inside messages (rule 6, rule 8)

Before (`internal/packload/needs.go:93`):
```text
pack %s needs pack %q, which is not an embedded official pack — needs may name only packs yolo ships, so a manifest cannot pull unreviewed code into a launch (WB-D9); if you want %q in the launch, select it explicitly in your config's packs list
```
After:
```text
pack %s needs pack %q, but yolo does not ship a pack by that name. A pack can only pull in packs that yolo ships. To use %q, add it yourself: yolo pack add %s
```
(The final `%s` is the pack name again. The code would need one more argument for it.)

Sites with the same problem, each followed by the id or section it prints. Drop the ID or path, and keep the sentence that tells the reader what to do:

```text
internal/packload/via.go:57                               WB-D9
internal/packload/served.go:266                           HS-D15, HS-D22
internal/packload/served.go:621                           notch-convergence.md §2.4
internal/packload/served.go:872                           HS-D30
internal/packsrc/naming.go:178                            PN-D11
internal/packdecl/packdecl.go:757-758                     providers.md §5.2
internal/packdecl/packdecl.go:770,776                     OQ-PT8
internal/packdecl/sessionhome.go:104                      SP-D10
internal/packdecl/fork.go:166                             FP-D6
internal/packdecl/fork.go:172                             §12
internal/packdecl/fork.go:246                             FP-D18
internal/packdecl/contributes.go:4838                     providers.md OQ-CN7
internal/packdecl/contributes.go:5041,5086                wire-bridge.md §2.1
internal/loopholedecl/enums.go:108                        HD-R1
internal/loopholedecl/enums.go:161
internal/loopholedecl/loopholedecl.go:624-625             HD-R1
internal/loopholedecl/packshipped.go:141                  OQ-LP5
internal/loopholedecl/packshipped.go:231                  OQ-LP14
internal/loopholedecl/packshipped.go:351,398,453,498-499
internal/loopholedecl/settings.go:254                     OQ-K1
internal/agentcfg/selection.go:98,128
internal/render/modes.go:117                              pack-config-collaboration.md §8
internal/render/modes.go:171                              OQ-HC2
internal/render/fieldset.go:144                           BB-D17
internal/loopholes/guestrun.go:125                        HS-D15
```

A link to a user-guide page is fine. A design-doc ID is not.

### P2. "notch", "doorway", "floor", "surface", "contribution" and "pack mirror" in user-facing text (rule 6)

Before (`internal/packload/served.go:620`):
```text
Not set at this notch, because nothing here serves it (docs/plans/notch-convergence.md §2.4):
```
After:
```text
yolo did not set these in this launch, because nothing running here can answer them:
```
Use these replacements:

| Internal word | Replacement |
|---|---|
| "notch" | "this launch", "a jail", or "`yolo host`" |
| "yolo's floor" / "the floor" | "yolo's own copy of the agent programs" (hostfloor) |
| "surface" | "config file" |
| "contribution" | "entry in pack.toml" |
| "pack mirror" | "yolo's local copy of the pack's repository" |
| "doorway" | "connection" |

Sites: `internal/packload/profiledisclosure.go:434-435,445-446`, `internal/packload/served.go:264` ("doorway"), `internal/render/fieldset.go:34-36` ("guest notch"), `internal/render/fieldset.go:130` ("doorway"), `internal/render/modes.go:89-90` ("the host notch composes nothing"), `internal/hostfloor/node.go:183,198`, `internal/hostfloor/ensure.go:110,146,175,352,501,864`, `internal/hostfloor/preparation.go:166,209,227,229`, `internal/hostfloor/ensure.go:406,711`, `internal/hostfloor/built.go:267` ("the floor's Node"), `internal/packsrc/store.go:760,767,776,779`, `internal/packsrc/refresh.go:703,712`, `internal/packsrc/patchbranch.go:387,444`, `internal/agentcfg/compose.go:388,399,406,514,562,581,639`, `internal/agentcfg/listcontrib.go:581,588,595`, `internal/agentcfg/staterender.go:282,584,590`.

### P3. Go package names, Go symbols and `%T` types in messages (rule 1)

Before (`internal/agentcfg/compose.go:406`):
```text
agentcfg: surface %s/%s: host config is not %s (got %T)
```
After:
```text
%s: yolo cannot use this file, because it holds a %s where yolo expects a %s. Fix the file, then run the command again.
```
(The first `%s` is the host file's path. Replace `%T` with a plain word, such as "list", "text" or "table".)

These sites have the same problem. The prefixes `agentcfg:`, `luahook:`, `codec:`, `packdecl:`, `manifest:` and `packs:` are Go package names, and `%T` prints things like `[]interface {}`:

- `internal/agentcfg/compose.go:388,399,406,514,562,581,639`
- `internal/agentcfg/listcontrib.go:581,588,595`
- `internal/agentcfg/staterender.go:282,584,590`
- `internal/agentcfg/promote.go:117,121,125,140`
- `internal/agentcfg/manifest/manifest.go:450,454,465-477` ("empty Agent", "unknown Codec" use Go field capitalization)
- `internal/agentcfg/luahook/derive.go:545,1153,1202,1220,1230`
- `internal/agentcfg/luahook/mcpauth.go:80,93,123,128,136,149`
- `internal/agentcfg/luahook/marshal.go:101,134`
- `internal/agentcfg/hostimportexclusions.go:68`
- `internal/packload/deriveenv.go:286` ("set %s to a %T")
- `internal/packload/agentfiles.go:93`
- `internal/packload/packtreerecord.go:98,107`
- `internal/loopholes/runtime.go:398-400` (names `loopholes.NewHostSet / Set.%s`)

Code that panics on an impossible state can keep its prefix. These are returned errors, and some of them reach `yolo check` and a launch.

### P4. Manifest messages use the old JSON spelling in a TOML pack (rule 1, "keep it true")

Before (`internal/packdecl/contributes.go:4887`):
```text
pack.toml:12:1 (mcp_auth[0]): kind "mcp-auth" does not take "bin" — its store declaration is pack-wide, not bound to one program
```
After:
```text
pack.toml:12:1 (mcp_auth[0]): [[mcp_auth]] does not take "bin". Remove it: one [[mcp_auth]] covers the whole pack.
```
`locateTOMLProblems` (`internal/packdecl/toml.go:1817`) rewrites only the leading `contributes[N]:`. The rest of the message keeps the JSON wording:

- `kind "x"` in place of `[[x]]`. The two spellings differ for `mcp_auth` and `blocked_tool`.
- Any second `contributes[M]` reference.
- JSON snippets such as `{"kind": "env", "vars": …}`.

Sites: `internal/packdecl/contributes.go:3371-3374,3401` ("first at contributes[%d]"), `internal/packdecl/packdecl.go:1648-1650` ("already declared at contributes[%d]"), `internal/packdecl/patchedext.go:390-394`, `internal/packdecl/skew.go:126-129`, every `label+": kind \"…\""` site in `internal/packdecl/contributes.go` (4303, 4479, 4887, 4940, 4958, 4965, 4973, 5005, 5039, 5085, 5101 and others), `internal/packdecl/packdecl.go:758` (`Write {"kind": "profile", …}`), `internal/loopholedecl/packshipped.go:136-140` (`{"kind": "env", "vars": {…}}`). The fix is to name the TOML table, or to resolve the second index to a `file:line` the same way the first one is resolved.

### P5. The message explains the design instead of the fix (rule 8)

Before (`internal/packdecl/supersedes.go:150`):
```text
contains a control character (U+%04X) — this text is printed in the pack footprint, which formats the line before it parses style tags, so a newline could forge a claim line and an ESC could erase the one above it; remove it
```
After:
```text
contains a control character (U+%04X). Remove it.
```
Sites: `internal/packdecl/supersedes.go:82-84`, `internal/packdecl/nodefloor.go:114-116`, `internal/packdecl/installhints.go:141-145`, `internal/packdecl/envoverride.go:286-288`, `internal/packdecl/probeargs.go:108-110`, `internal/packdecl/projectdirs.go:88-89`, `internal/packdecl/filesregister.go:110-112`, `internal/packdecl/packdecl.go:749-751`, `internal/loopholedecl/settings.go:248-254`, `internal/loopholedecl/packshipped.go:226-231`, `internal/packload/packload.go:276-279,505-509`, `internal/packload/footprint.go:1578-1580`, `internal/packload/mcpcompose.go:94-98`, `internal/packsrc/addr.go:223-225`, `internal/packsrc/relative.go:120-124`. In each one, keep what is wrong and the step. Drop the clause after "because" or "—" that only says why yolo works this way.

### P6. Run-on messages joined with "—" and ";" (rules 3, 7)

Before (`internal/packsrc/lockeddefault.go:17`):
```text
pack %s: could not read its default-branch lock: %w — run `yolo pack install` or write an explicit ?ref=
```
After:
```text
pack %s: yolo could not read the pin it saved for the default branch (%w). Run `yolo pack install`, or add ?ref=<branch> to the pack's address.
```
This shape appears in hundreds of `packdecl`, `packsrc` and `loopholedecl` messages. Only split it where the reader needs both halves. The worst cases are `internal/packload/profiledisclosure.go:264-268,303-307,355-359,445-446`, `internal/packload/forks.go:524-527`, `internal/packload/providers.go:272-276`, `internal/packsrc/identity.go:99-101`, `internal/packsrc/naming.go:186-189`, `internal/packsrc/addr.go:141-145` and `internal/loopholes/loopholescmd.go:682-689`.

### P7. Version-skew notes do not name the next step (happy path)

Before (`internal/packdecl/skew.go:209`):
```text
contributes[%d]: skipping unknown kind %q — this build does not know it, so the contribution is not rendered (version skew; a build that knows the kind will render it)
```
After:
```text
contributes[%d]: this yolo does not know the kind %q, so it skipped this entry. Update yolo to use it.
```
Sites: `internal/packdecl/skew.go:209-211`, `internal/packdecl/packdecl.go:961-963` (unknown via), `internal/packdecl/packdecl.go:999-1003` (unknown wire_api). The neighboring notes at `internal/packdecl/skew.go:126-129,168-174` already say "update yolo". These three should say it too.

### P8. The advice names a file to edit instead of a `yolo` command (AGENTS.md: advice names a verb)

Before (`internal/packload/served.go:202-205`):
```text
set %s's %s to "" in the config layer that should not use it (`"loopholes": {"%s": {"settings": {"%s": ""}}}` in a JSONC file, `%s = "" under [allow.%s]` in a TOML one), so its service serves nothing and its pointer is withheld
```
After:
```text
To turn it off for one config file, set it to empty there: yolo config set allow.%s.%s ""
```
The first `%s` must be the loophole's name (`l.Loophole`), not the pack's. Both config formats key this setting by the loophole's name (see "Wrong today").

Sites: `internal/packload/served.go:201-205`, `internal/packload/profiles.go:551-555` (see "Wrong today"), `internal/loopholes/loopholescmd.go:347-350` (names `loopholes:` block and `yolo-jail.jsonc`).

### P9. hostfloor progress lines expose internal mechanics (rules 1, 6, 8)

Before (`internal/hostfloor/patched.go:490`):
```text
materialized %s from fork build %s (%s) by %s (%d files)
```
After:
```text
installed %s from fork build %s (%s)
```
Sites: `internal/hostfloor/patched.go:490`, `internal/hostfloor/built.go:232` ("materialized … by %s (%d files)"), `internal/hostfloor/ensure.go:390` ("capture no longer matches this runtime; reinstalling … with the bounded host installer"), `internal/hostfloor/built.go:245-248` (buildHowPer: "sealed under Seatbelt in a throwaway home"). Under P2, "into yolo's floor" also appears in every install, update and wait line.

## Launch: providers, credentials and profiles

### Credential pre-flight refusal
- **Where:** `internal/packload/providers.go:1515` — a launch stops because a selected pack's provider has no credential.
- **Before:**
  ```text
  Refusing to launch: a selected pack needs a provider this launch cannot deliver.
  ...
    Put the variable in one of the consulted channels, or launch anyway with YOLO_ALLOW_MISSING_PROVIDERS=1.
  ```
- **After:**
  ```text
  yolo did not start the agent: a selected pack needs a provider credential that this launch does not have.
  ...
    Set the variable in one of the places listed above, then launch again. To launch without it, set YOLO_ALLOW_MISSING_PROVIDERS=1.
  ```
- **Why:** 1 ("deliver", "consulted channels"), 2.

### Credential hatch notice
- **Where:** `internal/packload/providers.go:1509-1511` — the user set the missing-provider hatch.
- **Before:**
  ```text
  Warning: YOLO_ALLOW_MISSING_PROVIDERS is set — CONTINUING, with a selected pack's provider credential still missing. Nothing was repaired: the agent's first request against that provider will still fail.
  ```
- **After:**
  ```text
  Warning: YOLO_ALLOW_MISSING_PROVIDERS is set, so yolo is launching without a provider credential that a selected pack needs. The agent's first request to that provider will fail.
  ```
- **Why:** 7, 2.

### Region pre-flight refusal
- **Where:** `internal/packload/regionpreflight.go:347` — a selected provider needs a region and none is set.
- **Before:**
  ```text
  Refusing to launch: a selected provider is reached through a region, and this launch names none.
  ```
- **After:**
  ```text
  yolo did not start the agent: a selected provider needs a region, and none is set.
  ```
- **Why:** 1, 5.

### Credential-scope header
- **Where:** `internal/packload/credentialscope.go:1164-1174` — this is the heading of the launch's credential disclosure.
- **Before:**
  ```text
  Credential scope: a provider's credential reaches only the agents whose profile selects it.
  ```
- **After:**
  ```text
  Credentials: each provider's key goes only to the agents whose profile uses that provider.
  ```
- **Why:** 5 ("Credential scope"), 1.

### Withheld-pointer header
- **Where:** `internal/packload/served.go:620-621` — a launch lists the variables it left unset.
- **Before:**
  ```text
  Not set at this notch, because nothing here serves it (docs/plans/notch-convergence.md §2.4):
  ```
- **After:**
  ```text
  yolo did not set these in this launch, because nothing running here can answer them:
  ```
- **Why:** 6 (notch), 1 (doc path).

### Via line under the withheld header
- **Where:** `internal/packload/served.go:614-615` — a profile routes through a pack service that this launch does not run.
- **Before:**
  ```text
  profile "%s"'s via — its service does not run here, so its agents keep their own clients rather than routing through it
  ```
- **After:**
  ```text
  profile "%s": its pack service does not run here, so its agents connect directly instead of through that service
  ```
- **Why:** 3, 6 ("via").

### Why a daemon is not served at the host
- **Where:** `internal/packload/served.go:263-266` — `yolo host --` explains a withheld variable.
- **Before:**
  ```text
  which `yolo host --` opens for no selection: at the host a jail daemon runs only as a doorway opened for the one agent a launch runs, and only when every pointer to it is gated on that agent's selected profile or provider, which this one's is not (docs/design/host-notch-services.md HS-D15, HS-D22)
  ```
- **After:**
  ```text
  which `yolo host --` does not start, because no selected profile or provider asks for it
  ```
- **Why:** 6 (doorway, pointer, gated), 8, 1.

### Via route that has no effect
- **Where:** `internal/packload/served.go:870-872` — a launch or `yolo check` reports that a profile's route changes nothing for an agent.
- **Before:**
  ```text
  %s's config does not point it at the %q service, so the %s has no effect on %s (its config is what it would be without it) and the service is not started for it (docs/design/host-notch-services.md HS-D30)
  ```
- **After:**
  ```text
  %s's config does not use the %q service, so the %s changes nothing for %s, and yolo does not start the service for it.
  ```
- **Why:** 1, 8.

### Credential warning that names a jail daemon
- **Where:** `internal/packload/profiledisclosure.go:434-436,445-447` — a profile's credential reaches no agent at this launch.
- **Before:**
  ```text
  Warning: profile %q delivers %s no credential for provider %q at this notch: none of %s reaches it, so %s starts on a credential it finds itself or on none. The %q jail daemon's pointer would carry %s, and this notch does not set it (the "Not set at this notch" line says why); or deliver one through `env_sources`
  ```
- **After:**
  ```text
  Warning: profile %q gives %s no key for provider %q in this launch, because none of %s reaches it. %s will use a key it finds itself, or none. The %q service would supply %s, but this launch does not start it (the list above says why). You can also supply the key with an env_sources entry.
  ```
- **Why:** 6 (notch, pointer), 7, 4 ("deliver").

### Profile that reaches no client
- **Where:** `internal/packload/profiledisclosure.go:303-307` — a profile's provider names only a platform, and no selected pack gives the agent a client for it.
- **Before:**
  ```text
  provider %q names no endpoint, only platform %q, and no selected pack gives %s a client of that platform (a pack binds a platform by shipping a provider of it, needing a pack that does, or declaring its program's switch or region for it), so nothing this profile configures reaches %s's own client. Select a profile whose provider %s reaches (`-p %s=<name>`), or …
  ```
- **After:**
  ```text
  provider %q has no address, only the platform %q, and no selected pack can connect %s to that platform. This profile changes nothing for %s. Choose a profile that %s can use (`-p %s=<name>`), or …
  ```
- **Why:** 8 (the parenthetical), 7.

### Literal key that no longer reaches the agent
- **Where:** `internal/packload/profiledisclosure.go:355-359` — the user's provider has a literal key, which yolo no longer writes for a built-in provider.
- **Before:**
  ```text
  Warning: profile %s gives %s no key: provider %q's key is a literal `%s`, which reached %s only through the model entry yolo no longer writes over a provider an agent has built in, so it reaches no client and %s's own %q client starts with none. Put the key in a variable and name it under `providers.%s.api_key_env_name` (%s reads %s)
  ```
- **After:**
  ```text
  Warning: profile %s gives %s no key. Provider %q has its key written into the config as `%s`, and yolo no longer passes a written-in key to %s's built-in %q client. Put the key in an environment variable, then name it: yolo config set providers.%s.api_key_env_name <VARIABLE> (%s reads %s)
  ```
- **Why:** 7, 8, plus the verb rule.

### Undeclared profile: the agent has the name built in
- **Where:** `internal/packload/profiles.go:551-555` — the user selects a profile name that no pack declares, but an agent has a provider by that name built in.
- **Before:**
  ```text
  no profile named %q is declared — %s %s %s built in, so declare it in your user config (%s):
    "providers": {%q: {"platform": %q}},
    "profiles": {%q: {"provider": %q}}
  (declared: %s)
  ```
- **After:**
  ```text
  No profile named %q exists. %s %s %s built in, so you can create the profile with two commands:
    yolo config set providers.%s.platform %s --user
    yolo config set profiles.%s.provider %s --user
  (profiles that exist: %s)
  ```
- **Why:** "keep it true" (see "Wrong today": the snippet is JSON, and the file is usually config.toml), plus the verb rule.

### Undeclared profile: general case
- **Where:** `internal/packload/profiles.go:485-487` — the user selects a profile name that nothing declares.
- **Before:**
  ```text
  no profile named %q is declared — a profile name must be declared by a selected pack's manifest or by your config's `profiles` key (declared: %s)
  ```
- **After:**
  ```text
  No profile named %q exists. A selected pack or the `profiles` table in your config must define it. Profiles that exist: %s
  ```
- **Why:** 7, 1 ("manifest").

### Protocol mismatch between provider and agent
- **Where:** `internal/packload/protocolresolution.go:716-727` — the launch cannot connect an agent to a provider.
- **Before:**
  ```text
  provider %q speaks %s; agent %q speaks %s — this launch cannot point %s at it.
    The provider's protocols are the keys of its `endpoints`; the agent's are the `protocols` list on the pack that installs %s.
    Nothing this launch can see declares an adapter between them, so there is no address to give: choose a provider this agent speaks to, or select a pack that adapts one of the provider's protocols into one of the agent's.
  ```
- **After:**
  ```text
  %s cannot use provider %q: the provider accepts %s, and %s sends %s.
    No selected pack translates between them. Choose a provider that %s can use, or add a pack that translates one of these protocols into the other.
  ```
- **Why:** 8 (the second line is about how pack.toml declares protocols, and only a pack author needs it), 7.

### Subscription provider address refused
- **Where:** `internal/packload/providers.go:272-276` — the user set `endpoints` or `base_url` on a subscription provider.
- **Before:**
  ```text
  providers.%s.endpoints: refused — %q is a subscription provider and its address is pinned by the %s pack, because the agent sends the subscription's bearer token to whatever address it is given. Drop `endpoints` and `base_url` from your providers.%s entry; to reach another host, declare a provider under another name with its own api_key_env_name
  ```
- **After:**
  ```text
  providers.%s.endpoints: %q is a subscription provider, and the %s pack fixes its address so that your subscription token goes only to that service. Remove the address: yolo config unset providers.%s.endpoints (and providers.%s.base_url). To use another server, add a provider with a new name and its own api_key_env_name.
  ```
- **Why:** 7, plus the verb rule. The code needs one more argument for the second `providers.%s`.

### Providers table conflict
- **Where:** `internal/packload/providers.go:1202-1209` — the user's `base_url` lands on a provider that a pack ships with `endpoints`.
- **Before:**
  ```text
  composing the providers table produced an entry the config validator refuses:
    providers."%s": <conflict message>
    The endpoints are pack X's; the base_url shorthand came from your config's providers.%s.base_url.
    To re-point one protocol, write the URL under it: providers.%s.endpoints.<protocol>.base_url
  ```
- **After:**
  ```text
  Your config's providers.%s.base_url conflicts with the addresses that pack X sets for this provider.
    To change one protocol's address, set it under that protocol instead:
    yolo config set providers.%s.endpoints.<protocol>.base_url <url>
  ```
- **Why:** 1 ("composing", "config validator"), 5.

### Unconfigured login loophole
- **Where:** `internal/packload/served.go:166-167` — a launch withholds a credential service until it has a profile.
- **Before:**
  ```text
  serves nothing until its `%s` setting names a profile (`yolo config set allow.%s.%s <profile>`)
  ```
- **After:**
  ```text
  does nothing until you name a profile for it: yolo config set allow.%s.%s <profile>
  ```
- **Why:** 2. The `allow.%s` argument should be `l.Loophole`, not `l.Pack` (see "Wrong today").

### Content skipped because no pack provides the agent
- **Where:** `internal/packload/agentaudience.go:43-44` — `yolo check`, a launch and `yolo host apply` report a briefing, skill or file for an agent that is not installed.
- **Before:**
  ```text
  pack %s: %s for %s not delivered — no selected pack provides %s
  ```
- **After:**
  ```text
  pack %s: yolo skipped its %s for %s, because no selected pack installs %s
  ```
- **Why:** 3, 4.

## Launch: pack selection, names and forks

### A fork's base is not selected
- **Where:** `internal/packload/forks.go`, in `ApplyForks` — a fork pack names a base pack that is not selected.
- **Before** (the next-step clause was added 2026-11-09, `e4e96b4b3`; the sentence is still long and still says "this selection"):
  ```text
  pack %s forks pack %s's %q (its `fork_of` names %s), and %s is not in this selection — a pack yolo ships joins by itself; any other base must be in your config's packs list as a pack named %s: add it with `yolo pack add <source>/%s` (such as local/%s), or drop the fork with `yolo pack remove %s`
  ```
- **After:**
  ```text
  pack %s replaces %q from pack %s, but %s is not selected. Add it, for example: yolo pack add local/%s
  ```
- **Why:** 7, 8, plus the verb rule.

### Needs cycle
- **Where:** `internal/packload/needs.go:133-136` — pack `needs` entries form a loop.
- **Before:**
  ```text
  pack needs form a cycle: %s — packs that need each other would pull each other in forever; fix the manifests so every needs chain ends
  ```
- **After:**
  ```text
  These packs need each other in a loop: %s. Remove one `needs` entry from one of their pack.toml files.
  ```
- **Why:** 4 ("needs" as a noun), 8.

### Via names an unshipped pack
- **Where:** `internal/packload/via.go:55-58` — a profile's `via` names a pack that yolo does not ship.
- **Before:**
  ```text
  profile %q (active for %s) names via %q, which is not an embedded official pack — a via may name only packs yolo ships, so a profile cannot pull unreviewed code into a launch (the needs rule, WB-D9)
  ```
- **After:**
  ```text
  profile %q (used by %s) routes through %q, but yolo does not ship a pack by that name. A profile can only route through a pack that yolo ships. Choose another route or another profile.
  ```
- **Why:** 1 ("embedded"), 6, 1 (ID), happy path (no next step).

### Pack names moved into a namespace
- **Where:** `internal/packload/defaultnames.go:77-80` — a launch or `yolo check` warns that a converted config gave a pack a namespace.
- **Before:**
  ```text
  pack %s is now named in the namespace %q, which its config entry and source did not choose (a config converted before pack naming): %s. Keep the old names with %s; or keep the new ones and silence this with %s. Either way, then run `yolo check`
  ```
- **After:**
  ```text
  pack %s now delivers its skills and files under the prefix %q: %s. To keep the old names, use %s. To keep the new names and stop this warning, use %s. Then run `yolo check`.
  ```
- **Why:** 6 ("namespace" is never defined), 7.

### Two packs land in the same place
- **Where:** `internal/packload/namecollisions.go:43-45` — two packs would put files in the same directory.
- **Before:**
  ```text
  packs %s and %s would both land their files under %q (and mount host files under /ctx/host-%s): set `namespace = "…"` on either entry listed on its own, or drop `bare` from one
  ```
- **After:**
  ```text
  packs %s and %s would both put their files under %q. Give one of them its own prefix: set namespace = "<prefix>" on its entry in packs, or remove bare = true from one entry.
  ```
- **Why:** 4 ("land" as jargon), 7.

### MCP server shipped twice
- **Where:** `internal/packload/mcpcompose.go:94-98` — two packs ship an MCP server with the same name.
- **Before:**
  ```text
  MCP server %q is shipped by %s — a server name is sole-owned, the key its entry lands under in mcp_servers, so one would silently replace the other. Remove one of those packs from `packs` (`yolo pack remove <pack>`); to change the server you keep, write mcp_servers.%s in %s, which merges over it (`yolo config set mcp_servers.%s.<field> <value> --user` sets one field)
  ```
- **After:**
  ```text
  MCP server %q is shipped by %s, and only one can have that name. Remove one of those packs: yolo pack remove <pack>. To change the server you keep, set one of its fields: yolo config set mcp_servers.%s.<field> <value> --user
  ```
- **Why:** 6 ("sole-owned"), 8, 7.

## Pack sources and pack addresses (`yolo pack install`, `yolo pack update`, a launch)

### Namespace refusal carries an ID
- **Where:** `internal/packsrc/naming.go:177-179` — a pack entry from a named source sets `namespace`.
- **Before:**
  ```text
  %s.namespace: a pack of source %q is in that source's namespace, its key (PN-D11); to change it, rename the key under [pack_sources] and in every handle that uses it, then run `yolo check`
  ```
- **After:**
  ```text
  %s.namespace: a pack from source %q always uses the source's name as its prefix. Remove namespace. To change the prefix, rename the source under [pack_sources] and in every entry that uses it, then run `yolo check`.
  ```
- **Why:** 6 (PN-D11, "handle"), and the step to remove the key was missing.

### Missing ?ref=
- **Where:** `internal/packsrc/addr.go:223-225` — a git pack address has no `?ref=`.
- **Before:**
  ```text
  pack address %q: missing required ?ref= — a git pack must be pinned (an unpinned source silently changes under you; write ?ref=main if you really want to follow a branch)
  ```
- **After:**
  ```text
  pack address %q has no ?ref=. Add a tag or commit (?ref=v1.2.0), or a branch to follow (?ref=main).
  ```
- **Why:** 8, 2.

### Ref missing from the local copy
- **Where:** `internal/packsrc/store.go:779-781` — a launch inside a jail cannot find a pack's ref, which the host has not fetched.
- **Before:**
  ```text
  ref %q not found in the pack mirror of %s — the next host launch fetches it, or run `yolo pack install` to fetch it now
  ```
- **After:**
  ```text
  yolo has not downloaded ref %q of %s yet. The next launch from the host downloads it, or run `yolo pack install` now.
  ```
- **Why:** 6 ("pack mirror").

### Omitted ?ref= on a non-readable entry
- **Where:** `internal/packsrc/refresh.go:230` — a pack entry omits `?ref=` where that is not allowed.
- **Before:**
  ```text
  pack %s: omitted ?ref= is only supported for readable source entries
  ```
- **After:**
  ```text
  pack %s needs a ?ref= in its address. Add a tag, commit or branch, such as ?ref=main.
  ```
- **Why:** 6 ("readable source entries" is undefined), happy path.

### No lockfile for the default branch
- **Where:** `internal/packsrc/refresh.go:234` — a pack omits `?ref=` and there is no lockfile.
- **Before:**
  ```text
  pack %s omits ?ref= but has no lockfile to pin the remote default branch — provide a lockfile or write an explicit ref
  ```
- **After:**
  ```text
  pack %s has no ?ref=, and yolo has not saved which commit of the default branch to use. Run `yolo pack install` to save one, or add ?ref=<branch> to the address.
  ```
- **Why:** 1 (the user does not "provide a lockfile"; `yolo pack install` writes it).

### Internal precondition
- **Where:** `internal/packsrc/lockeddefault.go:10` — a caller passed an address that has a ref.
- **Before:**
  ```text
  pack %s: default-ref resolution requires an omitted-ref git address
  ```
- **After:**
  ```text
  pack %s: yolo could not resolve this pack's default branch. This is a yolo bug; please report it with this line.
  ```
- **Why:** 1, 5. A user can only hit this through a yolo bug, so the message should say that.

### Corrupt check-start file
- **Where:** `internal/packsrc/checkstart.go:68` — `--update` or a pack check finds its sequence file unreadable.
- **Before:**
  ```text
  check-start authority corrupt; inspect %s and retry with repaired preparation state
  ```
- **After:**
  ```text
  yolo cannot read %s. Delete it, then run the command again. yolo then runs a new check of the pack.
  ```
- **Why:** 5, 6, happy path (the user cannot "repair preparation state"). Settled from the code: `withCheckStart` (`checkstart.go:31-63`) writes a new file with a new random epoch when the file is missing. `Qualifies` (`checkstart.go:27`) accepts a recorded check only when its epoch matches. So once the file is deleted, no earlier check counts, and yolo runs a new one. The same holds for "Rolled-back" and "Sequence exhausted" below.

### Rolled-back check-start file
- **Where:** `internal/packsrc/checkstart.go:72` — a saved check is newer than the sequence file.
- **Before:**
  ```text
  check-start sequence rolled back; repair %s before retrying --update
  ```
- **After:**
  ```text
  %s is older than the last pack check yolo recorded. Delete it, then run the command again with --update. yolo then runs a new check of the pack.
  ```
- **Why:** 5, 6.

### Check authority changed
- **Where:** `internal/packsrc/checkstart.go:119,145` — another check ran while this one waited.
- **Before:**
  ```text
  fresh check authority changed; retry --update
  ```
- **After:**
  ```text
  Another pack check finished while this one was waiting. Run the command again with --update.
  ```
- **Why:** 5, 6.

### Sequence exhausted
- **Where:** `internal/packsrc/checkstart.go:122` — the sequence counter is at its maximum.
- **Before:**
  ```text
  check-start sequence exhausted; repair preparation state and retry
  ```
- **After:**
  ```text
  yolo cannot record another check for this pack. Delete %s, then run the command again. yolo then runs a new check of the pack.
  ```
- **Why:** 5, happy path. The code needs the path as an argument. The new file starts counting from zero.

### Patch branch not mapped in the jail
- **Where:** `internal/packsrc/patchcheck.go:180` — a check in a jail cannot see a patch branch repository.
- **Before:**
  ```text
  the patch branch repository is not mapped into this workspace — run `yolo pack series check` on the host or start a host launch
  ```
- **After:**
  ```text
  This jail cannot see the patch branch's repository. Run `yolo pack series check` on the host, or start the jail again from the host.
  ```
- **Why:** 6 ("mapped", "host launch").

### Refresh fell back to the cached pack
- **Where:** `internal/packsrc/refresh.go:177-178` — a launch could not update a pack and used the cached copy.
- **Before:**
  ```text
  pack %s: could not refresh %s (%s) — using the cached %s
  ```
- **After:**
  ```text
  pack %s: yolo could not update %s, so it is using the copy it already has (%s).
    Git said: %s
    Check your network and your access to the repository, then run `yolo pack install` to try again.
  ```
- **Why:** 3, 7, happy path. The original has no next step. The middle `%s` is the whole git error folded onto one line (see "Git errors that reach the terminal" below), so it reads better on its own line. The same text prints at launch (`internal/cli/run/packrefresh.go:85`) and under `yolo pack install` (`internal/cli/pack.go:1947`). Under `yolo pack install`, the last sentence should say "Run `yolo pack install` again once the repository is reachable."

### Git errors that reach the terminal

I traced the `packsrc` git wrappers to the terminal. Two wrappers build the raw text: `git %s: %w\n%s` (`internal/packsrc/store.go:491`, `internal/packsrc/replay.go:670`) and `git %s timed out after %s: %w` (`store.go:487`). The first carries Go's `exit status N` and then git's own stderr. Callers add one verb phrase: `cloning %s: %w` (`store.go:576`) or `fetching %s: %w` (`store.go:596`). `oneLine` (`internal/packsrc/refresh.go:976-985`) folds the result onto one line and drops `Cloning into` lines. These are the places where the text reaches the user:

| Printed by | Text the user sees |
|---|---|
| `internal/cli/run/packrefresh.go:85` (launch), `internal/cli/pack.go:1947` (`yolo pack install`) | `Outcome.Warning`: "could not refresh … (<folded git error>) — using the cached …". Covered by "Refresh fell back to the cached pack" above. |
| `internal/cli/pack.go:1940` (`yolo pack install`) | `Outcome.Err`. For a pack never downloaded, this is `neverFetched` (`store.go:1285-1292`) with the folded git error inside it. Otherwise it is `resolveCommit` or `materialize` errors, for example "reading %s in the pack mirror: %w" (`refresh.go:703,712`, already in P2). |
| `internal/cli/patchedfork.go:116`, `internal/cli/patchedseriescheck.go:445` | `res.Err` from the patch check, including replay errors that keep git's stderr on several lines. |
| `internal/cli/patchedseriescheck.go:443-444` | A replay error inside one yellow warning line. |

The wrappers inside `replay.go` that use `oneLine` (`:458`, `:687`, `:809`, `:839`, `:849`, `:871`, `:908`, `:914`, `:943`, `:1143`) read well enough. Each one names a step and a commit. They are left alone.

#### Pack never downloaded
- **Where:** `internal/packsrc/store.go:1286-1289`. It is printed by `yolo pack install` (`internal/cli/pack.go:1940`) and by a launch that resolves the pack.
- **Before (as printed by `yolo pack install`):**
  ```text
  yolo pack install: foo: pack https://github.com/o/r has never been fetched, and fetching it failed: cloning https://github.com/o/r: git clone: exit status 128 fatal: could not read Username for 'https://github.com': terminal prompts disabled — the next host launch retries, or run `yolo pack install` to fetch it now
  ```
- **After:**
  ```text
  yolo pack install: foo: yolo could not download pack https://github.com/o/r.
    Git said: fatal: could not read Username for 'https://github.com': terminal prompts disabled
    Check your network and your access to the repository, then run `yolo pack install` again.
  ```
- **Why:** 3, 7, happy path. The original is one sentence holding four ideas. It also repeats the repository, and under `yolo pack install` it tells the user to run the command that just failed "now". The rewrite drops Go's `exit status 128` and the `cloning …` prefix. It keeps git's own words, because they usually name the real cause (a login, a missing repository, no network). At launch, the last line should read "Check your network and your access to the repository. The next launch tries again, or run `yolo pack install`."

#### Git timed out
- **Where:** `internal/packsrc/store.go:487`, wrapped by `store.go:576,596`.
- **Before:**
  ```text
  fetching https://github.com/o/r: git fetch timed out after 30s: context deadline exceeded
  ```
- **After:**
  ```text
  git fetch did not finish within 30s, so yolo stopped it.
  ```
- **Why:** 1 (`context deadline exceeded` is Go's text). The caller's next step still applies (see the entries above).

#### Git failed (the shared wrapper)
- **Where:** `internal/packsrc/store.go:491`, `internal/packsrc/replay.go:670`.
- **Before:**
  ```text
  git fetch: exit status 128
  fatal: repository 'https://github.com/o/r/' not found
  ```
- **After:**
  ```text
  git fetch failed. Git said: fatal: repository 'https://github.com/o/r/' not found
  ```
- **Why:** 1 (`exit status 128` is Go's `exec.ExitError` text). Put git's words after "Git said:" so `oneLine` can fold them without merging them into yolo's sentence. The callers that print `%v` directly (`patchedfork.go:116`, `patchedseriescheck.go:445`) then show one readable line instead of a ragged block.

#### Replay failed inside a warning line
- **Where:** `internal/cli/patchedseriescheck.go:443-444`. `r.Err` comes from `internal/packsrc/replay.go:836` (`applying %s onto %s: %w`) or `:905` (`merging %s into %s: %w`), and both keep git's stderr on several lines.
- **Before:**
  ```text
  ⚠ foo: could not replay the series onto v1.2.0: applying 0003-fix.patch onto 1a2b3c4d: git am: exit status 128
  error: patch failed: src/main.c:40
  error: src/main.c: patch does not apply — `yolo pack update` retries
  ```
- **After:**
  ```text
  ⚠ foo: patch 0003-fix.patch does not apply to v1.2.0 (1a2b3c4d).
    Git said: error: patch failed: src/main.c:40
    Update the patch so it applies, then run `yolo pack series check` again.
  ```
- **Why:** 3, 7, happy path. The original puts git's stderr in the middle of a sentence, and its next step lands after the last stderr line. "`yolo pack update` retries" is the wrong step when a patch does not apply, because a retry fails the same way. Keep the "retries" step only for a timeout ("the series' replay ran out of its %s", `replay.go:245,661`).

#### No local copy to replay in
- **Where:** `internal/packsrc/replay.go:279,283`. It is printed by `internal/cli/patchedfork.go:116` and `internal/cli/patchedseriescheck.go:445`.
- **Before:**
  ```text
  this machine holds no copy of %s to replay the series in — a check fetches it
  ```
- **After:**
  ```text
  yolo has not downloaded %s yet. Run `yolo pack update`, or start the jail, to download it.
  ```
- **Why:** 6 ("replay the series"), happy path ("a check" names no command). `yolo pack --help` says a missing check "names yolo pack update or a launch to record one" (`internal/cli/pack.go:258-260`), so the rewrite names those two steps.

## `yolo loopholes`

### Empty list
- **Where:** `internal/loopholes/loopholescmd.go:339-350` — `yolo loopholes list` finds no loopholes.
- **Before:**
  ```text
  No loopholes installed.
    • pack: a `loophole` contribution from a selected pack; ~/.config/yolo-jail/local is selected implicitly when it exists
    • config: loopholes: block in <user config> (install-shaped keys are user-scope only; a workspace yolo-jail.jsonc may set enabled/jail_env)
  ```
- **After:**
  ```text
  No loopholes installed. A loophole comes from one of two places:
    • a selected pack that declares one. yolo always selects ~/.config/yolo-jail/local when it exists.
    • the [loopholes] table in <user config>. A project's yolo-jail.toml can only turn an entry on or off, or set its jail_env.
  ```
- **Why:** 6 ("contribution", "install-shaped"), "keep it true" (the file is yolo-jail.toml now), 7.

### Unknown loophole name
- **Where:** `internal/loopholes/loopholescmd.go:607-609` — `yolo loopholes enable|disable <name>` names nothing installed.
- **Before:**
  ```text
  yolo loopholes %s: no loophole named %q is installed on this machine. `yolo loopholes list` shows the ones that are; a pack you select in %s installs its own.
  ```
- **After:**
  ```text
  yolo loopholes %s: no loophole named %q is installed. Run `yolo loopholes list` to see the installed ones. A pack you add with `yolo pack add` can install more.
  ```
- **Why:** verb rule, 7.

### Retired verb header
- **Where:** `internal/loopholes/loopholescmd.go:645-648` — `yolo loopholes enable` or `disable` is refused.
- **Before:**
  ```text
  yolo loopholes %s is retired: a loophole runs whenever its pack is selected, and a project gets access by a
    grant, not a switch.
  ```
- **After:**
  ```text
  yolo loopholes %s no longer exists. A loophole now runs whenever its pack is selected, and a project gets access when you grant it:
  ```
- **Why:** the hard line break falls mid-phrase. "grant" is defined by the commands listed under it.

### Grants could not be read
- **Where:** `internal/loopholes/standings.go:92-93` — `yolo loopholes list|status` cannot read the project's grants.
- **Before:**
  ```text
  yolo loopholes: this project's grants could not be read, so none is shown: %v
    `yolo check` names where.
  ```
- **After:**
  ```text
  yolo loopholes: yolo could not read what this project is allowed (%v), so the list leaves it out.
    Run `yolo check` to find the file with the problem.
  ```
- **Why:** 3 (passive voice with no actor), and the next step was terse.

### Origin gate warning names Go symbols
- **Where:** `internal/loopholes/runtime.go:398-400` — a caller asks a loophole for host access without the safety check.
- **Before:**
  ```text
  loophole %s: %s withheld — this is a PACK-shipped loophole and the caller evaluated no origin gate, so its host access cannot be honored (use loopholes.NewHostSet / Set.%s)
  ```
- **After:**
  ```text
  loophole %s: yolo skipped its %s, because an internal safety check did not run. This is a yolo bug; please report it with this line.
  ```
- **Why:** 1 (Go symbols), happy path.

### Module outside the pack tree
- **Where:** `internal/loopholes/discover.go:231` — a launch finds a loophole directory outside its pack.
- **Before:**
  ```text
  loophole module dir %s is outside selected pack %s staged tree, so that loophole is NOT active; repair the selected pack tree
  ```
- **After:**
  ```text
  the loophole directory %s is missing or links outside pack %s, so yolo did not turn it on. The pack's author must fix the pack. Until then, you can remove it with `yolo pack remove %s`.
  ```
- **Why:** 5, 6, happy path ("repair the tree" gives the user no command). Settled from the code: `yolo pack install` does not fix this. The warning fires when `filepath.EvalSymlinks` or `filepath.Abs` fails on the directory, or when the resolved directory is outside the pack (`discover.go:220-232`). The directory is the `[[loophole]]` entry's `from`, read from the pack's own files (`internal/cli/run/packs.go:768,816`). Every launch copies the pack from the same source, so a new download brings the same files back. A local pack is never downloaded at all (`internal/packsrc/refresh.go:251`). See also "Wrong today".

## `yolo host`: installing agent programs (hostfloor)

### Install progress
- **Where:** `internal/hostfloor/ensure.go:146` and `internal/hostfloor/preparation.go:209` — `yolo host` installs an agent program.
- **Before:**
  ```text
  installing %s into yolo's floor (%s): %s
  ```
- **After:**
  ```text
  installing %s for yolo host (%s): %s
  ```
- **Why:** 6 ("floor"). The same fix applies to every site under P2.

### Recapture notice
- **Where:** `internal/hostfloor/ensure.go:501-502` — a native agent's captured installer is old.
- **Before:**
  ```text
  %s %s in yolo's floor is from a capture made %s ago; running `yolo capture %s` to look for a newer release (%s)
  ```
- **After:**
  ```text
  yolo's copy of %s %s was downloaded %s ago. yolo is running `yolo capture %s` to look for a newer release (%s).
  ```
- **Why:** 6, 7.

### Stale capture
- **Where:** `internal/hostfloor/ensure.go:864` — a launch recaptures an unusable capture.
- **Before:**
  ```text
  the capture of %s on this machine %s; recapturing it so the floor can use it
  ```
- **After:**
  ```text
  yolo's saved copy of %s %s, so yolo is downloading it again.
  ```
- **Why:** 6.

### Build left the store during the copy
- **Where:** `internal/hostfloor/patched.go:481-483` — a concurrent launch replaced a fork build mid-copy.
- **Before:**
  ```text
  the build %s left the capture store while it was copied (another launch moved fork pack %s's good build); the next `yolo host -- %s` installs the good build it moved to
  ```
- **After:**
  ```text
  Another launch replaced the build of %s from fork pack %s while yolo was copying it. Run `yolo host -- %s` again to install the new build.
  ```
- **Why:** 6 ("capture store"), 7, 8.

### Changed after preparation
- **Where:** `internal/hostfloor/preparation.go:154,193,294,303,307,313` — something changed between the pack check and the install.
- **Before:**
  ```text
  %s: program declaration changed after patched-Floor preparation
  ```
- **After:**
  ```text
  %s: the pack changed while yolo was preparing it. Run the command again.
  ```
- **Why:** 6 ("patched-Floor preparation"), happy path. The variants say "host platform", "patch recipe", "patch-failure authority" and "compatible installed copy". All of them get the same rewrite with their own subject. "Patch-failure authority" becomes "the record of failed patches".

### Different Floor
- **Where:** `internal/hostfloor/preparation.go:287` — preparation from one floor is used with another floor.
- **Before:**
  ```text
  patched-Floor preparation belongs to a different Floor
  ```
- **After:**
  ```text
  yolo prepared this program for another install location. This is a yolo bug; please report it with this line.
  ```
- **Why:** 6, happy path.

### Selected capture entry moved
- **Where:** `internal/hostfloor/preparation.go:446,454` — the selected capture moved on disk.
- **Before:**
  ```text
  the selected capture entry is not rooted in its recorded store path
  ```
- **After:**
  ```text
  yolo's saved copy of this program moved on disk. Run the command again so yolo can find it.
  ```
- **Why:** 6, 5.

### Node loader mismatch
- **Where:** `internal/hostfloor/node.go:231-232` — the downloaded Node asks for a loader yolo does not expect.
- **Before:**
  ```text
  %s's node asks for the dynamic loader %q, not %s as this yolo expects of …
  ```
- **After:** keep the facts, and end with a step. For example: "… Report this with your OS name, because yolo cannot run this Node build here."
- **Why:** happy path. Today the message only reports the mismatch.

## Skills (`yolo host apply`, launch)

### Skill name collisions
- **Where:** `internal/hostskills/compose.go:1052-1053` — two or more skills would get the same name.
- **Before:**
  ```text
  skills: refusing to compose — %d name collision(s):
    <collision lines>
  ```
- **After:**
  ```text
  yolo did not install the skills: %d skill names are used by more than one pack:
    <collision lines>
  ```
- **Why:** 1 ("compose"), 5 ("name collision(s)").

### Prune with unknown pack set
- **Where:** `internal/hostskills/compose.go:1256` — host skill pruning cannot tell which packs are active.
- **Before:**
  ```text
  host skills prune: refusing to prune with an unknown active pack set
  ```
- **After:**
  ```text
  yolo did not remove old skills, because it could not tell which packs are selected. Run `yolo check`, fix what it reports, then run the command again.
  ```
- **Why:** 5, happy path.

## Host rendering (`yolo apply`, `yolo host apply`, `yolo config why`)

### Guest launch not built
- **Where:** `internal/render/fieldset.go:33-36` — `yolo apply` or a config verb is asked for the Linux guest setting.
- **Before:**
  ```text
  %s at the guest notch is not built yet: the guest notch launches only on macOS, as the macos-user backend (env-manager plan Phase 7.1), and its Linux backend, bwrap + Landlock (Phase 7.2), is not written.
  ```
- **After:**
  ```text
  %s for a guest launch does not work on Linux yet. Guest launches work only on macOS, where they run as a separate macOS user.
  ```
- **Why:** 6 (notch, macos-user backend), 1 (plan phases), 8. Callers print their own next steps after this line (`internal/cli/configtarget.go:269`), so the line keeps that step.

### Reasons a file keeps no record
- **Where:** `internal/render/modes.go:113-123,169-171` — `yolo config why` explains why a file has no record of who set a key.
- **Before:**
  ```text
  in a jail `rmw` is one mechanism among four and `stateful` — the mode that captures edits to a file yolo regenerates every boot — is the one that records, so an absent record here means "this surface's mode keeps no provenance sidecar", not "this notch records nothing" (pack-config-collaboration.md §8). …
  ```
- **After:**
  ```text
  in a jail, yolo records who set each key only for files that keep your edits across restarts. This file is not one of them, so it has no record.
  ```
- **Why:** 6 (mode names, "sidecar", "notch"), 8, 1.

### Host field-set reasons
- **Where:** `internal/render/fieldset.go:130-133` — `yolo host apply` explains why it does not apply a credential loophole.
- **Before:**
  ```text
  a credential loophole's doorway (`jail_daemon.host_cmd`, in a pack yolo ships or a local one) opens at `yolo host -- <program>` for the agent whose selection asks for it, once the loophole is enabled, and closes when that agent exits; `yolo host apply` runs no process for it to live beside
  ```
- **After:**
  ```text
  a credential loophole runs only while `yolo host -- <program>` runs an agent that asks for it. `yolo host apply` starts no agent, so it has nothing to apply.
  ```
- **Why:** 6 (doorway), 7, 1 (manifest field).

## Pack manifests (pack authors: `yolo pack lint`, launch)

### Retired `requires_provider`
- **Where:** `internal/packdecl/packdecl.go:754-759` — a legacy `pack.json` uses `requires_provider`.
- **Before:**
  ```text
  contributes[%d]: "requires_provider" is no longer a contribution field — a kind "profile" is a SELECTION over a provider, so the field is now spelled "provider" and MANDATORY (docs/reference/providers.md §5.2). Write {"kind": "profile", "name": %q, "provider": %q}
  ```
- **After:**
  ```text
  contributes[%d]: "requires_provider" was renamed. Write "provider": %q instead. A profile must name its provider. Run `yolo pack migrate <path>` on the pack's directory to convert it to pack.toml.
  ```
- **Why:** 1, 6, 8. `yolo pack migrate` is the verb, and `PackMigrateStep` exists for it (`internal/packdecl/legacyspellings.go:45`).

### Retired `launch` and `env`
- **Where:** `internal/packdecl/packdecl.go:766-777` — a legacy profile carries `launch` or `env`.
- **Before:**
  ```text
  contributes[%d]: "launch" is no longer a contribution field — a kind "profile"'s launch flags moved to kind "launch" contributions with "profile" set to the profile's name; no kind takes a top-level "launch" any more (OQ-PT8)
  ```
- **After:**
  ```text
  contributes[%d]: a profile no longer takes "launch". Move the flags to a separate launch entry, and set its "profile" to this profile's name.
  ```
- **Why:** 1 (the design id), 7. Apply the same fix to the `env` twin at line 774.

### Retired `tier`
- **Where:** `internal/packdecl/packdecl.go:748-751` — a skills entry carries `tier`.
- **Before:**
  ```text
  contributes[%d]: "tier" is no longer a contribution field — a tier decides what a skill is CALLED, which is one fact about the whole pack rather than one per destination. Move it to the manifest's top-level "skills_tier": %q
  ```
- **After:**
  ```text
  contributes[%d]: "tier" moved to the top of the manifest. Remove it here, and write "skills_tier": %q at the top level.
  ```
- **Why:** 8.

### Session-home lock name
- **Where:** `internal/packdecl/sessionhome.go:103-104` — a session home tries to share a lock name.
- **Before:**
  ```text
  %q is a lock name: a lock a program takes by mkdir reads as held forever through a link, so a session home never links one (SP-D10)
  ```
- **After:**
  ```text
  %q is a lock file, and a session home cannot share it. Remove it from the list.
  ```
- **Why:** 1, 8.

### `follow` without `patches`
- **Where:** `internal/packdecl/fork.go:244-247` — a fork sets `follow` without a patch series.
- **Before:**
  ```text
  %s: "follow" needs "patches" — following an upstream is a PATCHED fork's opt-in, and a plain fork's pin moves only by `yolo pack update` (FP-D18); add a "patches" directory holding a `git format-patch --base` series (or a "patch_branch" branch), or drop "follow"
  ```
- **After:**
  ```text
  %s: "follow" works only with "patches" or "patch_branch". Add a "patches" directory with a `git format-patch --base` series, or a "patch_branch", or remove "follow". To move a fork without patches, run `yolo pack update`.
  ```
- **Why:** 1, 7.

### Retired loophole scope
- **Where:** `internal/loopholedecl/enums.go:107-111` — a loophole manifest has `"scope": "host"`.
- **Before:**
  ```text
   — 'host' was retired (docs/design/host-daemon-ownership.md, HD-R1): every jail now starts its own host daemon and ends it with the jail, and the state the daemons share (a credentials file and the flock beside it) is what serializes them. Delete 'scope'. A daemon whose work in flight must finish when its jail ends declares 'detach': true.
  ```
- **After:**
  ```text
   — 'host' is no longer accepted. Delete 'scope'. If the daemon must finish its work after its jail ends, add 'detach': true.
  ```
- **Why:** 1, 8.

### Pack-shipped jail_env
- **Where:** `internal/loopholedecl/packshipped.go:135-142` — a pack loophole declares `jail_env`.
- **Before:**
  ```text
  %s: 'jail_env' is not available to a pack-shipped loophole — declare the variables with the pack's `env` contribution kind instead: {"kind": "env", "vars": {%s}}, which the pack footprint already reports and collides on. Note the difference you are accepting: `env` is UNCONDITIONAL, while `jail_env` only applied when the loophole was active (OQ-LP5, still open in docs/design/loophole-packaging.md)
  ```
- **After:**
  ```text
  %s: a loophole cannot declare 'jail_env'. Move the variables to an [[env]] table in pack.toml: vars = {%s}. Note that [[env]] sets them in every launch, not only when the loophole is on.
  ```
- **Why:** 1, 6 ("contribution kind", "footprint"), "keep it true" (the JSON spelling, P4).

### Unknown settings key
- **Where:** `internal/loopholedecl/settings.go:248-254` — a settings declaration has an unknown key.
- **Before:**
  ```text
  %s: '%s.%s' is not part of the settings declaration schema, and an unrecognized declaration key is REFUSED rather than ignored: yolo validates config values against this declaration and then writes them into a file a host daemon reads, so honoring the half it understood would admit a value the other half was meant to reject (docs/reference/pack-system.md OQ-K1). Known here: %s
  ```
- **After:**
  ```text
  %s: '%s.%s' is not a settings key. Remove it or fix its spelling. Known keys: %s
  ```
- **Why:** 8, 1.

### Duplicate MCP server in one pack
- **Where:** `internal/packdecl/packdecl.go:1648-1650` — one pack declares an MCP server name twice.
- **Before:**
  ```text
  contributes[%d]: MCP server %q is already declared at contributes[%d] — a server name is the key its entry lands under, so the second declaration would silently replace the first
  ```
- **After:**
  ```text
  contributes[%d]: this pack already declares an MCP server named %q (at %s). Rename one of them or remove one.
  ```
- **Why:** P4 (the second index is never resolved to a `file:line`), 8.

## `yolo pack footprint` and `yolo pack lint` output

I read every detail string in `internal/packload/footprint.go`, plus the helpers it calls: `forks.go:61-79`, `patchedtrees.go:292-325`, `mcpcompose.go:273-298`, `modellists.go:337-347`, `providers.go:1786-1836`, `programneeds.go:175-187` and `loopholesource.go:306-470`. That is about 70 strings. The printers are `printClaimLines` and `reportFootprint` in `internal/cli/pack.go:1025-1043,1744-1781`. Each claim prints as `<kind> <target> <detail>`. The launch banner prints `Claim.DisclosureSentence` instead (`footprint.go:127-219`).

Most detail strings are short and fine: the `state`, `mount`, `env`, `models`, `provider`, `adapter`, `executables`, `supersedes`, plugin and installer lines, and the loophole lines (`RUNS … on your machine`, `INTERCEPTS`, `TRUSTS`, `CONNECTS`, `DOWNLOADS`, `PASSES THROUGH`). The entries below are the ones that break the rules. One cross-cutting point: the kind column prints the internal kind names (`config-overlay`, `blocked-tool`, `mcp-auth`, `needs-program`, `overridden-by`), not the `pack.toml` table names. That is P4 again. A TOML author looks for `[[overlay]]`, not `config-overlay`.

### Same thing claimed by two packs
- **Where:** `internal/packload/footprint.go:1085`, printed after `<kind> <target> — packs a, b:` (`internal/cli/pack.go:1769-1770`).
- **Before:**
  ```text
  %s is sole-owned; two packs claim it — one would shadow the other
  ```
- **After:**
  ```text
  only one pack can provide %s, so one would hide the other. Remove one of these packs with `yolo pack remove <name>`.
  ```
- **Why:** 3, 7, happy path (no next step). The same next step fits the state, plugin and loophole collisions below.

### State kept at two scopes
- **Where:** `internal/packload/footprint.go:1174`.
- **Before:**
  ```text
  state claimed at two scopes (workspace and machine-wide) — ambiguous which backing store wins
  ```
- **After:**
  ```text
  one pack keeps this directory per workspace and the other keeps it for the whole machine, so yolo cannot tell which copy to use. Remove one of these packs with `yolo pack remove <name>`.
  ```
- **Why:** 3, 5 ("backing store"), happy path.

### Two loopholes with one name
- **Where:** `internal/packload/footprint.go:1258-1261`.
- **Before:**
  ```text
  two packs ship a loophole named %s; a loophole's name is its identity everywhere — its state dir, its endpoint, its `enabled` toggle and the host-access claim you approved — so one would run under the other's approval. Rename one module directory
  ```
- **After:**
  ```text
  both packs ship a loophole named %s, so one would run with the access you approved for the other. Remove one of these packs with `yolo pack remove <name>`. If you wrote one of them, rename its loophole directory.
  ```
- **Why:** 5, 6 ("module", "endpoint"), 7. The risk is the one fact the user needs. The list of what a name keys is design detail. The plugin collision (`footprint.go:1205-1207`) should take the same two-step ending.

### Two packs claim one agent name
- **Where:** `internal/packload/footprint.go:1443-1448`.
- **Before:**
  ```text
  an agent name has exactly ONE owning pack, and pack a (program) and pack b (skills.agent) all claim claude. The owner provides everything that goes with that name — where its briefing lands, where its skills land, its config surfaces, its launch flags — and `-p claude=<profile>` and an `agents: ["claude"]` selector both resolve THROUGH it, so two owners make every one of those ambiguous. Deselect one of these packs: they cannot both be the claude pack
  ```
- **After:**
  ```text
  packs a and b both set up the agent claude, and only one pack can. Remove one of them with `yolo pack remove <name>`.
  ```
- **Why:** 5, 6 ("surfaces", "owner"), 7, 8 (JSON selector spelling in a TOML world). "Deselect" names no command. The `(program)` and `(skills.agent)` labels are manifest internals. A pack author can run `yolo pack footprint` on each pack to see which entries claim the name.

### Two providers for one program
- **Where:** `internal/packload/programneeds.go:181-187`.
- **Before:**
  ```text
  a program has ONE provider in a launch, and pack a and pack b each carry a recipe for rg (needed by pack c). You choose the provider by choosing the pack: drop all but one of a, b from `packs`
  ```
- **After:**
  ```text
  packs a and b both install rg (pack c needs it), and only one may. Keep one: remove the others with `yolo pack remove <name>`.
  ```
- **Why:** 7, P8 (names the `packs` key instead of a verb).

### Two packs own one config file
- **Where:** `internal/packload/footprint.go:1571-1591`.
- **Before (abridged):**
  ```text
  a config surface has exactly ONE owner. packs a and b each declare it, so one silently changes how the other's file is maintained — the later declaration REPLACES the earlier one whole, taking its mode, path, codec and defaults with it, with nothing reported, and these already disagree: …
      To contribute keys to a surface another pack owns, declare `config-overlay` instead of a second `config`:
        { "kind": "config-overlay", "surface": "claude/settings",
          "config": { "managed": { …your keys… } } }
      That leaves the owner's mode/path/codec alone, folds your keys in BELOW its managed layer …
  ```
- **After:**
  ```text
  packs a and b both define the config file claude/settings, and only one pack may. The later one would silently replace the earlier one. These fields already differ: …
    To add keys to a file another pack defines, declare an [[overlay]] table with surface = "claude/settings" instead of a second [[config]].
    The defining pack's values still win a conflict. `yolo config diff claude` shows which pack set each key.
  ```
- **Why:** 5, 6 ("surface", "codec", "managed layer"), 7, P4 (the example is JSON, but the pack is TOML; see "Wrong today"). For a pack user rather than its author, add: "Or remove one of these packs with `yolo pack remove <name>`."

### Config list detail
- **Where:** `internal/packload/footprint.go:939-958`.
- **Before:**
  ```text
  appends 2 entries: "a", "b" (owner's entries kept; captured, computed and managed still win)
  removes 1 entry: "x" wherever the layers below hold them
  ```
- **After:**
  ```text
  adds 2 entries: "a", "b" (the defining pack's entries and your own settings stay)
  removes 1 entry: "x" from what the defining pack and other packs set
  ```
- **Why:** 6 ("captured, computed and managed" are internal layer names, as is "layers below").

### Config overlay detail
- **Where:** `internal/packload/footprint.go:868-871`.
- **Before:**
  ```text
  contributes keys when profile "x" is active for the owner's agent (owner still wins)
  ```
- **After:**
  ```text
  adds keys when profile "x" is active (the defining pack's values win a conflict)
  ```
- **Why:** 6 ("contributes", "owner").

### Autonomy detail
- **Where:** `internal/packload/footprint.go:651-685`, `:1681-1736`.
- **Before:**
  ```text
  autonomous+guarded postures; autonomous injects `claude --dangerously-skip-permissions`; guarded contributes keys to claude/settings (owner still wins)
  ```
- **After:**
  ```text
  sets how far the agent may act alone, in two modes. Autonomous mode adds `claude --dangerously-skip-permissions`. Guarded mode adds keys to claude/settings.
  ```
- **Why:** 6 ("posture", "injects", "contributes", "owner"), 7.

### MCP sign-in store detail
- **Where:** `internal/packload/footprint.go:640`.
- **Before:**
  ```text
  agent-neutral MCP sign-in store declaration at %s (format %s)
  ```
- **After:**
  ```text
  keeps MCP sign-ins for every agent at %s (format %s)
  ```
- **Why:** 4, 5 (noun stack).

### Profile detail
- **Where:** `internal/packload/footprint.go:701-702`.
- **Before:** `selection of provider %s`
- **After:** `selects provider %s`
- **Why:** 4 (noun for a verb).

### Host daemon detail
- **Where:** `internal/packload/providers.go:1831`.
- **Before:**
  ```text
  host daemon [cmd args] (runs at the host and on macos-user for a pack yolo ships or a local one; refused for a fetched pack)
  ```
- **After:**
  ```text
  runs `cmd args` on your machine. yolo runs it only for a pack it ships or a local pack, and refuses it for a downloaded pack.
  ```
- **Why:** 6 ("at the host", "macos-user"), 7. Use backticks for the command, as `postureInjects` does.

### Environment override detail
- **Where:** `internal/packload/footprint.go:352-370`.
- **Before:**
  ```text
  launch refused beside FOO + BAR unless BAZ is also delivered
  launch warned (may override) beside a host_files grant at ~/.aws/config
  ```
- **After:**
  ```text
  a launch stops if FOO and BAR are also set, unless BAZ is set too
  a launch warns if your config also passes ~/.aws/config into the jail, because it may win
  ```
- **Why:** 3 ("launch refused beside" has no verb), 6 ("delivered", "grant").

### Files and briefing addressing detail
- **Where:** `internal/packload/footprint.go:459-468,599-600`.
- **Before:**
  ```text
  read-only tree — addressed to claude (destination inferred from the pack that owns that name)
  concat — this destination IS claude's (`agents: ["claude"]` reaches it)
  read-only tree; each tree landing here is listed in claude/settings plugins
  ```
- **After:**
  ```text
  read-only tree for claude (it lands where the claude pack puts it)
  concat. This is claude's file, so `agents = ["claude"]` reaches it.
  read-only tree. yolo lists each tree placed here under plugins in claude/settings.
  ```
- **Why:** 6 ("destination", "surface"), P4 (the selector is JSON; a `pack.toml` writes `agents = ["claude"]`).

### Unreadable loophole declaration
- **Where:** `internal/packload/loopholesource.go:316-321`.
- **Before:** `declaration UNREADABLE — %s`
- **After:** `yolo cannot read this loophole's declaration: %s. Run `yolo pack lint <dir>` on the pack to see the problem.`
- **Why:** happy path (no next step).

### Patched fork launch sentence
- **Where:** `internal/packload/footprint.go:162-167`; the patched extension sentence at `:179-183` has the same shape.
- **Before:**
  ```text
  RUNS a program built from source, INSIDE THE JAIL (not on your machine), as pi: patched fork build: … — at the upstream's newest commit that rule names and the series applies to, checked at most hourly; the series is replayed on this machine with no code run, the build runs in a capture jail that gets no credentials, and a new build runs only once it is admitted: the UPSTREAM'S NEW CODE ARRIVES UNREVIEWED, as an npm agent's release does
  ```
- **After:**
  ```text
  RUNS a program built from source, INSIDE THE JAIL (not on your machine), as pi: patched fork build: …
    It follows the upstream's newest commit that the patches still apply to. yolo checks for one at most once an hour.
    The build runs in a separate jail with no credentials.
    The UPSTREAM'S NEW CODE ARRIVES UNREVIEWED, as an npm agent's release does.
  ```
- **Why:** 6 ("capture jail", "admitted", "series is replayed"), 7. The warning in capitals is the point of the line, and it is buried at the end of a 70-word sentence. Keep it, on its own line.

### Unknown pack name
- **Where:** `internal/cli/pack.go:1665-1667` (outside my area, but it is the footprint command's own refusal).
- **Before:**
  ```text
  yolo pack footprint: "x" is neither an embedded pack nor a local path/file:// source — see `yolo pack ls`, or pass a directory to footprint a pack you are authoring
  ```
- **After:**
  ```text
  yolo pack footprint: "x" is not a pack yolo ships or a local directory. Run `yolo pack ls` to see the packs yolo ships, or pass the path to your pack's directory.
  ```
- **Why:** 4 ("to footprint a pack"), 6 ("embedded").

## Wrong today

- `internal/packload/profiles.go:551-555`: the refusal tells the user to "declare it in your user config (%s)", then prints **JSON** (`"providers": {…}, "profiles": {…}`). `paths.UserConfigDisplayPath()` returns `config.toml` first (`internal/paths/paths.go:1269-1277`). On a TOML config, the snippet cannot be pasted in as written. The rewrite above uses `yolo config set` instead. *Not fixed: needs a ruling (see the report).*
- `internal/loopholes/loopholescmd.go:349-350`: the empty-list hint says "a workspace yolo-jail.jsonc may set enabled/jail_env". The workspace file is `yolo-jail.toml` now, and `.jsonc` is only the legacy spelling. `env` and `settings` may also be overridden there (`internal/config/validate_loopholes.go:864`), so the list of allowed keys is incomplete too. *Fixed in `c879f67ed`.*
- `internal/packload/served.go:166-167,201-205` (latent): the TOML advice names the **pack** (`allow.<l.Pack>`), and the JSONC advice names the **loophole** (`"loopholes": {"<l.Loophole>": …}`). Both formats key this setting by the same name, the loophole's. Under a TOML user config, `ConfigBlock` (`internal/loopholes/packswitch.go:44-96`) copies each `[allow.<name>]` table's other keys into `loopholes.<name>.settings`, under the same `<name>`. The loophole reads its settings from that block. So `allow.<pack>` reaches the loophole only when the pack and its loophole share a name. `internal/config/userlayerprojection.go` says the same thing from the other side: it moves a legacy `loopholes.<name>.settings` block to `allow.<name>` only when that table already exists, "because the name may be a loophole whose pack has another name". Today the only login loophole is `aws-auth`, served by the `aws-auth` pack (`packs/aws-auth/pack.toml`), so the advice works. If the two names ever differ, the TOML advice writes a setting that nothing reads. Both messages should print `l.Loophole`. The rewrites above do that. *Fixed in `c879f67ed`.*
- `internal/packdecl` problem bodies (P4): in a TOML manifest, the text still says `kind "mcp-auth"`, `kind "blocked-tool"` and `contributes[N]` for the second entry. A TOML author cannot find `[[mcp_auth]]` or `[[blocked_tool]]` from those words. `locateTOMLProblems` (`internal/packdecl/toml.go:1817-1856`) rewrites only the leading index. *Fixed in `c879f67ed`.*
- `internal/hostfloor/fresh.go:20,26,46`: these messages ("requires source preparation before floor consumption", "fresh install failed; repair and retry --update") are unreachable. `Floor.EnsureFresh` has no non-test caller (`rg 'EnsureFresh\('` matches only its definition). Delete them with the function. Do not rewrite them. *Fixed in `c879f67ed`.*
- `internal/packsrc/refresh.go:234`: "provide a lockfile" implies the user writes one. `yolo pack install` writes it (`lockeddefault.go:17,30` say so). *Fixed in `c879f67ed`.*
- `internal/packdecl/skew.go:209-211` and `internal/packdecl/packdecl.go:961-963`: these notes say "a build that knows the kind will render it". They are true, but no next step follows. The sibling notes (`skew.go:128,171`) say "update yolo". *Fixed in `c879f67ed`.*

- `internal/loopholes/discover.go:231`: a **missing** loophole directory gets the "outside selected pack ... staged tree" warning, not the "not a directory" warning at `:236`. `filepath.EvalSymlinks` returns an error for a path that does not exist, and the code treats that error as "outside the pack" (`discover.go:220-232`). So the warning names the wrong cause for the most likely mistake, a typo in `from`. The rewrite above says "missing or links outside". The better fix is a separate "does not exist" branch. *Fixed in `c879f67ed`.*
- `internal/packload/footprint.go:563` (with `internal/packdecl/kinds.go:475` and `internal/cli/config_ref.txt:2110`): the skills line says "merged (built-in < pack < user)". No user layer exists. A skill in `~/.claude/skills` on the host reaches the jail by no path (AGENTS.md, "Skills"). The real order, lowest first, is the workspace, then the built-in skills, then each selected pack, with the local pack `~/.config/yolo-jail/local` last (`docs/reference/agent-briefings.md:642-647`). The line should say "merged (workspace < built-in < packs < your local pack)". *Fixed in `c879f67ed` and `90b74519d`.*
- `internal/packload/footprint.go:1585-1591`: the config-owner collision tells a TOML author to write a JSON `{ "kind": "config-overlay", … }` entry. In `pack.toml`, that entry is an `[[overlay]]` table (`internal/packdecl/toml.go:891-893`). This is P4 again. *Fixed in `c879f67ed`.*

## Not covered

- The `internal/packdecl/contributes.go` validators: there are about 220 `problems = append` sites. I read all of them but list them only through patterns P4, P5 and P6, not one entry each.
- The roughly 120 validators in `internal/loopholedecl/callbroker.go`, `brokered.go` and `grants.go`: they are field-precise and mostly fine. I proposed nothing for them beyond P1 and P5.
- `internal/hostskills/archive.go` and `dangling.go`: these mostly wrap another error with a verb phrase ("checking out %s: %w"). They read as fine. I did not trace each one to the terminal.
- `packsrc` git errors: I traced the ones that `yolo pack install`, a launch, `yolo pack series check` and the patched-fork commands print. I did not trace `internal/cli/patchedadvance.go`, `patchfailure.go` or `packlintseries.go:197-198` line by line. They print the same `res.Err` values, so the "Git failed" and "Replay failed" rewrites apply there too.
- The footprint printers in `internal/cli` other than the two named in that section (`reportShippedSurfaceClash`, `printExpectsNotes`): they are outside my area, and I did not review them.
- I did not run anything. Each rewrite is checked against the code around it, but not against real output.
