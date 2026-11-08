# Message rewrites: CLI commands

Scope: the non-test `.go` files directly in `internal/cli/` (185 files), plus the embedded `internal/cli/config_ref.txt`. The `internal/cli/run` and `internal/cli/check` packages are not covered here.

Messages read: about 2,300 output sites were scanned as one-line excerpts. About 1,050 of them were read in full, with the code around them, over two passes. The second pass covered the patched-fork, preparation and capture files, the `yolo host apply` report, every structured report field (`Headline`, `Detail`, `Remedy`, `NoRemedy`, `Reason`, `Note`; 87 sites), every `*Usage` constant, and `config_ref.txt`. Rewrites proposed: about 85, plus 15 patterns that cover about 230 more sites. All counts are approximate.

Coverage was deepest on the commands people use most:

- `yolo --help`
- `yolo run --help`
- `approve`, `allow`, `request` and `active-provider`
- `config` and its `set`, `add` and `unset` verbs, plus `config migrate`
- `pack add` and `pack remove`
- `init` and `init-user-config`
- `stop`, `nix-roots`, `update` and `claude-auth`
- the launch and apply messages of `yolo host`

The second pass added the background build, saved-copy and `yolo host apply` report messages, the remaining usage texts, and the configuration reference. See the closing note for what is still uncovered.

## Patterns

### P1. Design-doc IDs and file paths inside user messages

A user cannot act on the design id or on `docs/design/host-notch-services.md`. Neither of those files ships with a release. (Rules 1, 6, 8.)

- **Before:**
  ```text
  … a host service lives only for the command that starts it (docs/design/host-notch-services.md OQ-HS3). Launch the agent as `%s`, …
  ```
- **After:** `… a host service lives only as long as the command that starts it. Launch the agent as `%s`, …`
- **Sites:** `host.go:3288`, `host.go:3384`, `host.go:3390`, `host.go:3470`, `host.go:3546`, `host.go:3549`, `hostinputs.go:318`, `hostinputs.go:416`, `hostinputs.go:423`, `hostinputs.go:444`, `hostmodelmenu.go:200`, `hostmodelmenu.go:388`, `hostmcponce.go:35`, `configpromotewrite.go:102`, `autocapture.go:157`, `describe.go:283`, `configmigrate.go:191`, `commands.go:374` (the last one is written out below).

  For `hostmodelmenu.go:388` and `configmigrate.go:191`: if a link is needed, point to the published user guide (docs.yolo-jail.mschulkind.dev), not to a repository path.

### P2. "notch" (and "the confinement dial")

Readers have not met these words. Say "on the host" or "in a jail", or name the setup. (Rule 6.)

- **Before:** `yolo host: %s is a jail-launch flag, and the host notch has no meaning for it, so it is refused rather than ignored.`
- **After:** `yolo host: %s only means something when yolo launches a jail, so yolo refuses it here instead of ignoring it.`
- **Sites:**
  - `host.go:40` (usage)
  - `host.go:157`
  - `host.go:487`
  - `host.go:2280` ("at this notch" becomes "on the host")
  - `host.go:2764` ("at every notch" becomes "on the host and in a jail")
  - `hostapplygate.go:345`
  - `hostapplynotch.go:525`
  - `hostapplynotch.go:540`
  - `hostapplyremedy.go:470`
  - `hostapplyremedy.go:499`
  - `configrenderhost.go:172`
  - `configrenderhost.go:175`
  - `config.go:191-198` (the `--setup does not apply here` reasons)
  - `describe.go:282`
  - `describe.go:304`
  - `configprovenance.go:371-403`
  - `configtarget.go:747`
  - `atremoved.go:33-66`
  - `apply.go:146-172`
  - `apply.go:287-292`
  - `runcmd.go:67`
  - `help.go:29`
  - `pack.go:111`, `pack.go:154`, `pack.go:245`
  - `apply.go:1738`, `apply.go:1760-1771` (usage)
  - `describe.go:347` (usage)
  - `hostapplyremedy.go:498`
  - `config_ref.txt` (17 places)
  - `surfacecmd.go:32`, `surfacecmd.go:90-131`
  - `checkdeps.go:521`

### P3. "surface" and "contribution" used without saying what they are

A user thinks in terms of "the agent's config file" and "what a pack adds". (Rule 6.)

- **Before:** `yolo config render: no surface %q for agent %q`
- **After:** `yolo config render: yolo does not manage a config file named %q for %q. Run `yolo surface ls` to list the ones it does.`
- **Sites:**
  - `config.go:35`, `config.go:380`, `config.go:392`, `config.go:424`, `config.go:497`, `config.go:677`
  - `configrenderhost.go:106-175`
  - `configdiff.go:68`, `configdiff.go:743`, `configdiff.go:880`
  - `configpromote.go:300`, `configpromote.go:455`, `configpromote.go:535`, `configpromote.go:581`
  - `hostsessionhome.go:752`, `hostsessionhome.go:760`
  - `hostapplyremedy.go:584`
  - `pack.go:720`, `pack.go:745`, `pack.go:823`, `pack.go:1080`
  - `host.go:156`
  - `privateclient.go:70`
  - `help.go:81`

  Keep "surface" where it names the `yolo surface` verb. Define it once, in that verb's usage text.

### P4. Advice spelled as JSON for a config that is now TOML

The rule says advice names a command, not a file to hand-edit. `paths.UserConfigDisplayPath()` names `config.toml` whenever that file exists, and also when no user config exists yet. So these messages tell the user to paste JSON into a TOML file. (Rule 1 and the AGENTS.md rule that advice names a command. Also listed under Wrong today.)

- **Before:** `… leave pack %s out of the floor with `"host_floor": {%q: false}` in the user config`
- **After:** `… or, to run your own copy, stop yolo installing pack %s on the host: yolo config set host_floor.%s false --user`
- **Sites:**
  - `hostreadiness.go:316`
  - `hostfloor.go:881`
  - `hostfloor.go:1185`
  - `hostapply.go:301` (becomes `yolo config set host_wrappers false --user`)
  - `host.go:4175` (same)
  - `hostmanagementgate.go:134` (becomes `yolo config set host_management own --user`)
  - `hostmanagementgate.go:208`
  - `configdiff.go:178`
  - `patchedadvance.go:2049`, `patchedadvance.go:2066`
  - `hostcapabilities.go:76` (becomes `yolo config set required_capabilities '["web_search"]' …`)
  - `programs.go:57-58` (becomes `yolo config set programs.autoprune true --user`)
  - `update.go:72-73` (becomes `yolo config set update_check false --user`)
  - `config_ref.txt`: 17 example headers and about 100 example lines (see the configuration reference section)

### P5. Help text and messages that name only the old `.jsonc` files

The workspace file is `yolo-jail.toml` and the user file is `config.toml`. The `.jsonc` names are a legacy format that yolo still reads. (Rule 1, and Keep it true.)

- **Before:** `Print the full configuration reference: every key accepted by a workspace yolo-jail.jsonc and by ~/.config/yolo-jail/config.jsonc, …`
- **After:** `Print the full configuration reference: every key that a workspace's yolo-jail.toml and your ~/.config/yolo-jail/config.toml accept, with its type, default and meaning. The older .jsonc files take the same keys.`
- **Sites:**
  - `commands.go:943-944`
  - `commands.go:909-911`
  - `help.go:75`, `help.go:76`
  - `runcmd.go:110` (`when yolo-jail.jsonc does not parse` becomes `when the workspace config does not parse`)
  - `programs.go:58`
  - `update.go:73`
  - `configdrift.go:124`
  - `apply.go:1754`
  - `nixroots.go:111`
  - `config_ref.txt:14-15`, `config_ref.txt:27`, `config_ref.txt:335`, `config_ref.txt:388`, `config_ref.txt:993` (and its other JSONC-first examples)

### P6. Config edit errors with no command prefix and no next step

These print a bare clause. The user cannot tell which command failed or what to do next. (Every stop names the next step, and rule 3.)

- **Before:** `unknown config edit flag "--projcet"`
- **After:** `yolo config set: unknown flag "--projcet". It takes --user, --project, --local, --file PATH and --dry-run (see `yolo config --help`).`
- **Sites:** `configeditverbs.go:45`, `configeditverbs.go:58`, `configeditverbs.go:69`, `configeditverbs.go:82`, `configeditverbs.go:91`, `configeditverbs.go:102`, `configeditverbs.go:112`, `configeditverbs.go:146`, `configeditverbs.go:185`, `configeditverbs.go:239`, `request.go:260`, `allow.go:400`. Use the verb's own name in the prefix (`set`, `add`, `unset`, `request`, `allow`).

### P7. The schema's internal words in config removal errors

These errors use words like "removal policy", "selected config schema" and "legacy schema … off-string removal". (Rules 1, 5, 6.)

- **Before:** `key has no removal policy in the selected config schema`
- **After:** `yolo config unset cannot remove this key. Edit the file by hand, then run `yolo check`.`
- **Sites:** `configeditverbs.go:393`, `configeditverbs.go:411-445`, `configeditverbs.go:497`, `configeditverbs.go:533`. Each of these reaches the user through `configeditverbs.go:174` or through `configEditRefusal`.

### P8. Messages that hand the user a `yolo host-run` line start mid-sentence, in lowercase, with no command name

(Rules 3, 7.)

- **Before:** `the pack list is the user config's, which only the host edits; run: yolo host-run -C %s --name pack-%s -- %s`
- **After:**
  ```text
  yolo pack %s: the pack list is in your user config, which only the host can change. Ask the human to paste this line into a host terminal:
    yolo host-run -C %s --name pack-%s -- %s
  ```
- **Sites:** `packadd.go:66`, `configeditverbs.go:344`, `setupcmd.go:350`.

### P9. "yolo's floor" and "host render"

These are internal names for the programs yolo installs on the host and for the config files `yolo host apply` writes. (Rule 6.)

- **Before:** `yolo host: could not check your host render (cannot resolve your home: %v) — launching %s anyway.`
- **After:** `yolo host: could not check whether your agent config files are up to date, because your home directory could not be found (%v). Launching %s anyway.`
- **Sites:** `hostapplygate.go:163`, `hostapplygate.go:172`, `hostapplygate.go:237`, `hostfloor.go:881`, `hostfloor.go:1185`, `hostreadiness.go:316`.

### P10. Internal bookkeeping errors that give the user no real next step

The patched-fork, preparation and capture code reports how its own records went wrong: "replay authority", "detached failure evidence", "incarnation", "lease", "admitted", "owner unproven", "typed claim". The user cannot act on any of these words. Say what failed in plain terms. Say whether the user's work was lost. Then name one command. (Rules 1, 6, 8, and every stop names the next step.)

- **Before:** `yolo pack: %s: replay authority changed and the current check record could not be read: %v`
- **After:** `yolo pack: %s: another yolo changed this fork's build record while this check ran, so the result was not saved (%v). Run yolo pack update %s again.`
- **Sites:**
  - `patchedfork.go:120`, `patchedfork.go:134`, `patchedfork.go:178`, `patchedfork.go:186`, `patchedfork.go:195`, `patchedfork.go:234`, `patchedfork.go:310`, `patchedfork.go:317`, `patchedfork.go:326`, `patchedfork.go:389-390`
  - `patchedadvance.go:724`, `patchedadvance.go:751`, `patchedadvance.go:846`, `patchedadvance.go:2273`, `patchedadvance.go:2687`, `patchedadvance.go:3163`, `patchedadvance.go:3200`
  - `preparationcontrol.go:120`, `preparationcontrol.go:140`, `preparationcontrol.go:157`, `preparationcontrol.go:195`, `preparationcontrol.go:314`, `preparationcontrol.go:337`, `preparationcontrol.go:351`
  - `preparationdemand.go:70`, `preparationdemand.go:124`, `preparationdemand.go:128`, `preparationdemand.go:162`
  - `preparationplain.go:21`, `preparationplain.go:24`
  - `shareddemand.go:57`, `shareddemand.go:110`, `shareddemand.go:203`, `shareddemand.go:595`, `shareddemand.go:811`, `shareddemand.go:838`
  - `buildpool.go:415`, `buildpool.go:424`, `buildpool.go:485`
  - `backgroundadvance.go:459`, `backgroundadvance.go:486`, `backgroundadvance.go:489`
  - `forkbuild.go:1105`, `forkbuild.go:1109`, `forkbuild.go:1118`
  - `programcaptureproducer.go:474`, `programcaptureproducer.go:502-533`
  - `programnpm.go:106-649` (for example "npm capture identity changed", "observation bound")
  - `capturematerialize.go:253`

  Where the code retries on its own (the next launch or the next timer run), say that, and print nothing at the default level. Several of these are only diagnostics. They belong in the launch log, not on the terminal.

### P11. "retry preparation", "retry --update", "retry status": the next step is not a command

These end with a verb the user cannot type. Give the exact command. (Every stop names the next step, and keep exact command spellings.)

- **Before:** `fresh requester lease missing; retry --update`
- **After:** `yolo: this launch lost its place in the build queue. Run the same command again with yolo -u.`
- **Sites:**
  - `preparationdemand.go:31` (already names `yolo check --no-build`: keep that, and drop "fresh preparation selection unavailable")
  - `preparationdemand.go:128`
  - `preparationplainrequest.go:29-140`
  - `programcaptureproducer.go:337-340` ("retry preparation")
  - `programcaptureproducer.go:502-533` ("retry selected preparation")
  - `shareddemand.go:110`
  - `preparationstatus.go:31` and `preparationstatus.go:128` ("retry status" becomes "run `yolo pack prepare status` again")
  - `buildpool.go:530`, `buildpool.go:541`, `hosttrees.go:191` ("repair the scheduler state" names no way to do it; say which file to remove, or run `yolo pack prepare stop` then `yolo pack prepare start`)
  - `treedelivery.go:168`, `programcapturescheduler.go:45`, `preparationscheduler.go:712` ("use a supported fresh launch", "start a fresh launch": say "start a new jail with `yolo`")

### P12. "sealed", "ordinary", "producer", "bounded installer", "supported installer route"

These name internal modes of the program-capture design. The user only needs to know whether yolo installs the program the normal way or reuses a saved copy. (Rules 1, 6, 8.)

- **Before:** `yolo host: %s sealed artifact reuse is unsupported (%s); retaining its authorized bounded host installer — use an independently matching baseline runtime for reuse`
- **After:** `yolo host: %s cannot reuse a saved copy here (%s), so yolo installs it the normal way.`
- **Sites:**
  - `programcapture.go:28`, `programcapture.go:124-153`
  - `programcaptureproducer.go:337-348`
  - `programnpm.go:106-649` ("unsupported for sealed capture", "retry in the sealed runtime", "sealed npm producer image CA bundle")
  - `programcapturedemand.go:117`, `programcapturedemand.go:131`, `programcapturedemand.go:191`, `programcapturedemand.go:265`, `programcapturedemand.go:287`, `programcapturedemand.go:293`, `programcapturedemand.go:423`, `programcapturedemand.go:448`, `programcapturedemand.go:471`
  - `programcapturehost.go:45`, `programcapturehost.go:50`
  - `programcapturescheduler.go:45`, `programcapturescheduler.go:95`, `programcapturescheduler.go:153`, `programcapturescheduler.go:167`
  - `capture.go:95`

  Where the message only says "yolo kept the normal install", it is not a problem. Print it at most once per launch, or only with `--verbose`.

### P13. "materialize", "receipt", "admitted", "generation", "cached good"

(Rule 6.) Say "unpack", "record", "ready to use", "build" and "last good build".

- **Before:** `yolo: materializing capture %s for %s FAILED: %v (falling back to the vendor installer; %s may hold a partial tree)`
- **After:** `yolo: could not unpack the saved copy of %[2]s (%[3]v), so yolo is installing it the normal way. %[4]s may hold a half-unpacked copy; yolo replaces it.`
- **Sites:**
  - `capturematerialize.go:89`, `capturematerialize.go:164`, `capturematerialize.go:170-202`, `capturematerialize.go:220`, `capturematerialize.go:253`, `capturematerialize.go:256`, `capturematerialize.go:327`, `capturematerialize.go:337`, `capturematerialize.go:351`, `capturematerialize.go:354`
  - `cachedgood.go:87`, `cachedgood.go:214`, `cachedgood.go:732`
  - `preparationstatus.go:49`, `preparationstatus.go:52`, `preparationstatus.go:63`
  - `preparationforks.go:135`, `preparationforks.go:164`, `preparationforks.go:181`
  - `preparationscheduler.go:162`, `preparationscheduler.go:295`, `preparationscheduler.go:707`
  - `patchedadvance.go:1911`
  - `treedelivery.go:227`, `treedelivery.go:278`, `treedelivery.go:282`
  - `commands.go:264` and following (`yolo prune --help`: "what a materialize would never choose again", "embedded-packs/", "lease", "union'd")

  The capital "FAILED" at `capturematerialize.go:220` is the only all-caps word in these lines. The launch continues, so it should read as a notice, not an error.

### P14. Help and reports that still name removed values or old manifests

(Keep it true.)

- **Before:** `'none' and 'assert' there is no store, so capture refuses` (`surfacecmd.go:104`)
- **After:** `Under "none" there is no store, so capture refuses.`
- **Sites:** `surfacecmd.go:104` (host_management "assert" is retired and refused), `pack.go:206` ("the pack.json manifest"; a pack may now use `pack.toml`), `apply.go:1754` (`yolo-jail.local.jsonc`), `config_ref.txt:163`, `config_ref.txt:179` (see Wrong today).

### P15. `yolo host apply` report groups: the headline is a design note, not a status

The remedy groups print a headline, items, and a `Remedy` or `NoRemedy` line. Several `NoRemedy` lines explain the internal rule instead of telling the user what happens to their file. (Rules 2, 6, 8.)

- **Before:** `NoRemedy: a pack's managed keys outrank every declaration at this notch`
- **After:** `A pack sets these keys on the host, so your own value cannot replace them. Remove the key from the pack to set it yourself.`
- **Sites:**
  - `hostapplyremedy.go:167-223` (unresolved and malformed packs)
  - `hostapplyremedy.go:386-398` (skills adopted)
  - `hostapplyremedy.go:470`, `hostapplyremedy.go:483`, `hostapplyremedy.go:498`, `hostapplyremedy.go:535`
  - `hostapplyremedy.go:584-587` (comments dropped)
  - `hostapplyfailures.go:220-221`
  - `hostapplyverdict.go:178-300` ("retired" for a pack you removed), `hostapplyverdict.go:377`, `hostapplyverdict.go:563`, `hostapplyverdict.go:567`
  - `hostapplynotch.go:519`, `hostapplynotch.go:525`, `hostapplynotch.go:540`
  - `configpromote.go:511-602` ("keyless surface", "`computed` layer", "the layers already produce it", "folds AFTER")
  - `applyhostlspplugin.go:91`

## Top-level help (`yolo --help`)

### host blurb
- **Where:** `help.go:29`. The user sees this in the command list of `yolo --help`.
- **Before:**
  ```text
  Run agents on the HOST notch instead: 'host -- <cmd>', 'host apply'
  ```
- **After:**
  ```text
  Run agents directly on this machine, outside a jail: 'host -- <cmd>', 'host apply'
  ```
- **Why:** 6 (notch).

### approve blurb
- **Where:** `help.go:69`. The user sees this in `yolo --help`.
- **Before:**
  ```text
  Host request and grant decisions, and the git working set
  ```
- **After:**
  ```text
  On the host: answer an agent's requests, and see or revoke what you allowed
  ```
- **Why:** 3, 5 (noun stack), 6 (working set).

### surface blurb
- **Where:** `help.go:81`. The user sees this in `yolo --help`.
- **Before:**
  ```text
  Inspect and manage composed agent-config surfaces: 'surface ls', 'render', 'diff', 'reset'
  ```
- **After:**
  ```text
  See and manage the agent config files yolo writes: 'surface ls', 'render', 'diff', 'reset'
  ```
- **Why:** 5, 6 (surface).

### apply blurb
- **Where:** `help.go:85`. The user sees this in `yolo --help`.
- **Before:**
  ```text
  Make the environment match its description (--setup <name>; at a container setup it runs the launch's readiness act)
  ```
- **After:**
  ```text
  Make the environment match its config without running anything (--setup <name>)
  ```
- **Why:** 6 ("readiness act"), 8.

### loopholes blurb
- **Where:** `help.go:63`. The user sees this in `yolo --help`.
- **Before:**
  ```text
  List and self-check host-capability loopholes
  ```
- **After:**
  ```text
  List the host features wired into jails, and check that each one works
  ```
- **Why:** 5, 6.

### init blurb
- **Where:** `help.go:75`. The user sees this in `yolo --help`. See also Wrong today.
- **Before:**
  ```text
  Scaffold yolo-jail.jsonc in the current workspace
  ```
- **After:**
  ```text
  Create yolo-jail.toml in the current workspace
  ```
- **Why:** this is wrong today. `init` writes `yolo-jail.toml`.

### Unknown command
- **Where:** `cli.go:160` and `cli.go:165`. The user sees this after mistyping a command name.
- **Before:**
  ```text
  yolo: unknown command %q
  ```
- **After:**
  ```text
  yolo: unknown command %q. Run `yolo --help` to list the commands, or `yolo -- <command>` to run a command in the jail.
  ```
- **Why:** every stop names the next step.

## Launching (`yolo run --help`)

### --profile and --with-credentials help
- **Where:** `runcmd.go:57-92`. The user sees this in `yolo run --help`.
- **Before:**
  ```text
                       ... A command
                       no profile reaches (bash, curl) gets a provider's key from
                       --with-credentials, below, at every notch.
  ...
                       naming the jail's set, part of it, or nothing enters. A -p
                       profile selected at an attach is not a grant: it still hands
                       its agent that profile's provider key, as ruled, and the
                       attach says so when the grant did not name the key.
  ```
- **After:**
  ```text
                       ... A command
                       that no profile reaches, such as bash or curl, gets a
                       provider's key only through --with-credentials, below.
  ...
                       naming the jail's providers, some of them, or none goes
                       ahead. A -p given when you attach still gives its agent
                       that profile's provider key, and yolo says so when
                       --with-credentials did not name that key.
  ```
- **Why:** 6 (notch, "as ruled"), 4 ("nothing enters").
- **Also:** line 57 is indented one column short of the other flags (`--profile <sel>   Select`).

## Approvals (`yolo approve`, `allow`, `request`)

### approve usage: the opening paragraph
- **Where:** `approve.go:36-62`. The user sees this in `yolo approve --help`.
- **Before:**
  ```text
  Host-only: list requests, grants and each workspace's working set, allow a
  request once, grant its requested set for a bounded duration, deny it with optional human feedback,
  revoke a grant or a working-set entry. --note is valid only with --deny, ... Without --for, Allow once covers that exact command for one use, for at most 15
  minutes and never beyond its set's ceiling. --for hands over the whole requested set, within its policy ceiling
  and use cap; session is 12 hours. ...
  ```
- **After:**
  ```text
  Run on the host. When an agent in a jail asks to do something that needs your
  approval, such as pushing a branch or commenting on a PR, it files a request.
  This command lists those requests and answers them.

  With no flags, `yolo approve <id>` allows that exact command once, within 15
  minutes. --for <duration> allows it for that long instead. "session" means 12
  hours. --deny refuses it. --note adds a message for the agent, and only works
  with --deny. Use --note=<text> when the message starts with a dash.

  The "working set" is the branches, PRs and issues an agent may change without
  asking again. It holds the ones the agent created, the ones you allowed, and the
  branches your project config lists. --revoke takes one out.
  ```
- **Why:** 2, 6 (ceiling, set, working set used before it is defined), 7 (one paragraph of about 60 lines that runs on).
- **Note:** the detailed exit codes and JSON fields that follow can stay, but they belong below the basics.

### approve: an id from an unconfirmed source
- **Where:** `approve.go:325`. The user sees this when answering a request whose pack yolo cannot currently confirm.
- **Before:**
  ```text
  yolo approve: source %s is %s and has no active core approval authority; it may be denied or revoked, but not granted. Next: keep it pending until its selected source and core authority can be confirmed, or deny it with `yolo approve %s --deny`.
  ```
- **After:**
  ```text
  yolo approve: this request came from %s, which is %s, so yolo cannot allow it. You can deny it with `yolo approve %s --deny`, or leave it waiting until `yolo check` shows the pack working again.
  ```
- **Why:** 5, 6 ("core approval authority").

### approve listing footer
- **Where:** `approve.go:402`. The user sees this under the list when some requests come from packs yolo cannot vouch for.
- **Before:**
  ```text
  Requests are source-labeled: only sources with active core authority can be granted; pack sources without a core adapter and removed or unknown sources are deny/revoke only. Run `yolo check` to resolve incomplete selection, or use `yolo approve <id> --deny` for a refusal.
  ```
- **After:**
  ```text
  Each request names the pack it came from. You can only allow requests from packs yolo can confirm. Requests from other packs, or from packs that were removed, can only be denied or revoked. Run `yolo check` to fix a pack, or `yolo approve <id> --deny` to refuse a request.
  ```
- **Why:** 5, 6, 7.

### approve listing footer (normal case)
- **Where:** `approve.go:405`. The user sees this under every non-empty list.
- **Before:**
  ```text
  Decide pending requests with `yolo approve <id>` (a GitHub write takes `--for <duration>` for its resource; a selected pack's write takes `--for <duration>` up to the smallest max_for of the cells it writes; a git push takes `--for <duration>` to allow its refs for that long, and a branch the agent pushes again lasts through its `yolo request git push <branch>` and `yolo config apply`; `--for <duration>` grants a whole set only where supported), or `--deny`; revoke with `yolo approve --revoke <grant|w-entry|c-entry|all> [--workspace <path>]` (a c- entry is a working-set branch declared in config and is removed there).
  ```
- **After:**
  ```text
  Allow a request once with `yolo approve <id>`, allow it for a while with `yolo approve <id> --for 15m`, or refuse it with `yolo approve <id> --deny`.
  Take back something you allowed with `yolo approve --revoke <id>`.
  For a branch the agent will push again, the agent runs `yolo request git push <branch>` and you run `yolo config apply`.
  ```
- **Why:** 2, 4 ("a GitHub write"), 6 (cells, set), 7 (one sentence with three nested parentheses).

### approve: empty list
- **Where:** `approve.go:399`. The user sees this when there is nothing to decide.
- **Before:**
  ```text
  No requests, grants or working-set entries. A windowed GitHub write, or a git push no grant or working-set entry covers, can file a pending Allow-once request; decide it with `yolo approve <id>`. Run `yolo approve --help` for the host store commands.
  ```
- **After:**
  ```text
  Nothing is waiting for an answer, and nothing you allowed is still active. When an agent asks to push a branch or write to GitHub, its request appears here. Answer it with `yolo approve <id>`.
  ```
- **Why:** 4 ("a GitHub write"), 6 ("windowed", "host store").

### approve: generic usage failure
- **Where:** `approve.go:202`. The user sees this after any bad flag combination.
- **Before:**
  ```text
  yolo approve: %v. See `yolo approve --help`; run `yolo approve` on the host to list requests, grants and working sets.
  ```
- **After:**
  ```text
  yolo approve: %v. Run `yolo approve --help` for the flags, or `yolo approve` to list what is waiting.
  ```
- **Why:** 2. "On the host" is redundant, because this path only runs on the host.

### approve: revoke a config-declared branch with no running jail
- **Where:** `approve.go:572-578`. The user sees this when running `--revoke c-<id>` after the jail has stopped.
- **Before:**
  ```text
  yolo approve: no running jail declares %s. A c- id is a working-set branch declared in config, listed only while a jail of its project runs; the broker recorded nothing to revoke.
  Next: to take the branch out, run `yolo config unset 'requests.git.push[<n>]' --project` in the project, <n> being the branch's position from 0 as `yolo config why requests.git.push` lists it, then `yolo config apply` there (a stopped jail reads it at its next launch); a project still on yolo-jail.jsonc, where it is a {"kind": "branch", …} entry under `brokered.git.grants`, converts first with `yolo config migrate`. Run `yolo approve` to list the ids of running jails.
  ```
- **After:**
  ```text
  yolo approve: %s is not listed now. A c- id is a branch your project config lets the agent push, and yolo lists it only while that project's jail runs.
  To stop allowing the branch, run these in the project:
    yolo config why requests.git.push        (find the branch's position, counting from 0)
    yolo config unset 'requests.git.push[<n>]' --project
    yolo config apply
  If the project still uses yolo-jail.jsonc, run `yolo config migrate` first.
  ```
- **Why:** 2, 7, 8 ("the broker recorded nothing").

### allow usage
- **Where:** `allow.go:29-34`. The user sees this in `yolo allow --help`.
- **Before:**
  ```text
  Allow a broker capability, or a grant kind a loophole pack declares, in your user config, in the
  file that already holds [allow], then offer this workspace's approval of it through the gate
  `yolo config apply` runs.
  ```
- **After:**
  ```text
  Let jails use a host feature, such as git fetch or GitHub reads, by adding it to
  your user config. If you run this inside a workspace, yolo then asks you to approve
  the change for that workspace, as `yolo config apply` does.
  ```
- **Why:** 5, 6 (broker capability, grant kind, loophole pack, gate).

### allow: in a jail
- **Where:** `allow.go:128`. An agent sees this when it runs `yolo allow` inside a jail. It is agent-facing, but a human may read it too.
- **Before:**
  ```text
  yolo allow writes your user config, which only the host can edit, and it is the human's own answer.
  ```
- **After:**
  ```text
  yolo allow changes the user config, which only the human can change, from the host.
  ```
- **Why:** 2. "The human's own answer" is a design reason (rule 8). The next line already says not to use `yolo host-run`.

### allow: kept the default list
- **Where:** `allow.go:175`. The user sees this the first time they add an `on_request` capability.
- **Before:**
  ```text
  %s %s %s (requestable by default) and %s: a written on_request list replaces that default, so it keeps them.
  ```
- **After:**
  ```text
  %s %s %s and %s. A project could already request the first ones by default, and writing this list replaces that default, so yolo added them to keep them.
  ```
- **Why:** 3, 7. The original sentence has no clear subject for "keeps".

### request usage
- **Where:** `request.go:24-30`. The user sees this in `yolo request --help`.
- **Before:**
  ```text
  Request a broker capability, or a grant kind a loophole pack declares, in this project's config,
  run `yolo check`, and name the host step that approves it (`yolo config apply`). A
  request your user config does not allow on request is reported and skipped, never an error.
  ```
- **After:**
  ```text
  Ask, in this project's config, for a host feature this project needs, such as pushing a branch
  or opening PRs. yolo then runs `yolo check`. The human approves the request on the host with
  `yolo config apply`. If your user config does not let projects ask for that feature, yolo
  says so and the project runs without it.
  ```
- **Why:** 6, 7. "Reported and skipped, never an error" is more internal than the user needs.

### request: after a skipped request
- **Where:** `request.go:178` (also `request.go:250`). The user sees this after every request, including one that `reportRequestSkipped` just said the project will run without.
- **Before:**
  ```text
  It is approved on the host: the human runs `yolo config apply` there, in this workspace, and answers y.
  ```
- **After (normal case):**
  ```text
  To approve it, the human runs `yolo config apply` on the host, in this workspace, and answers y.
  ```
- **After (when skipped):**
  ```text
  Approving the config change with `yolo config apply` will not enable it until the user config allows it (see above).
  ```
- **Why:** 3 ("It is approved" reads as if it already happened). The skipped case is misleading; see Wrong today.

## Config verbs (`yolo config …`)

### config usage: unset line and the surface sentence
- **Where:** `config.go:35-36` and `config.go:45`. The user sees this in `yolo config --help`.
- **Before:**
  ```text
  Edit and inspect yolo's own configuration. Generated config surfaces are
  managed separately by 'yolo surface'.
  ...
    unset <key> [target] [--dry-run]
                             Remove one key using the existing config removal policy.
  ```
- **After:**
  ```text
  Edit and inspect yolo's own configuration. The agent config files that yolo
  writes for you are managed by 'yolo surface'.
  ...
    unset <key> [target] [--dry-run]
                             Remove one key from a config file.
  ```
- **Why:** 6 (surface), 8 ("removal policy").

### config edit usage
- **Where:** `config.go:76-89`. The user sees this from `yolo config set --help`.
- **Before:**
  ```text
  set and add require one typed value; unset physically removes an ordinary recognized scalar from the named file, so a lower layer or default may reappear. Table, list, native, and null removals keep their schema-specific forms; unsupported removals refuse without writing. Edits require an existing config file except for the bounded, standard Project/Local TOML target, which may be created when absent.
  The default target is Project when it exists, even if User or Local currently wins. User and named-file targets must exist. A missing standard Project/Local TOML target may be created; --dry-run never creates it.
  ```
- **After:**
  ```text
  set and add take one value, written as TOML (quote strings that need it).
  unset removes the key from the file you name. If another file or a default also sets
  it, that value then applies. When yolo cannot remove a key safely, it says so and
  writes nothing.

  Without a target flag, yolo edits the project file (yolo-jail.toml) if it exists.
  It does this even when your user or local file currently decides the value. yolo
  creates yolo-jail.toml or yolo-jail.local.toml if it is missing. The other targets
  must already exist. --dry-run never creates a file.
  ```
- **Why:** 2, 5 ("bounded, standard Project/Local TOML target"), 6, 7.

### config set: success line
- **Where:** `configeditverbs.go:261`. The user sees this after a project or local edit.
- **Before:**
  ```text
  Written. Run `yolo check` in this workspace; the host must run `yolo config apply` to approve the workspace change.
  ```
- **After:**
  ```text
  Written. To approve the change, run `yolo config apply` on the host, in this workspace.
  ```
- **Why:** 7. yolo already runs the check right after this line (`config.go:133`), so telling the user to run it is redundant. See Wrong today.

### config set: post-write check failed
- **Where:** `configeditverbs.go:266` (also `request.go:304`).
- **Before:**
  ```text
  The config file was written, but the post-write check did not pass; follow its findings before continuing.
  ```
- **After:**
  ```text
  yolo wrote the config file, but `yolo check` found problems in it. Fix what the check reports above before you launch.
  ```
- **Why:** 3, 5 ("post-write check").

### config unset: cannot remove
- **Where:** `configeditverbs.go:174-178`.
- **Before:**
  ```text
  cannot unset config key %q in selected file %q: %v; edit the value manually in that file, then run `yolo check` in this workspace; run `yolo config apply` on the host to approve workspace changes
  ```
- **After:**
  ```text
  yolo config unset: cannot remove %q from %q (%v). Edit that file by hand, then run `yolo check`. For a workspace file, also run `yolo config apply` on the host.
  ```
- **Why:** 3, 7, P6.

### config set: convert_later
- **Where:** `configeditverbs.go:118`.
- **Before:**
  ```text
  yolo config %s: %s is written by hand, never by a command; nothing was written. To stop the launch offering the conversion, add "%s": true to %s yourself; to convert instead, run `yolo config migrate`
  ```
- **After:**
  ```text
  yolo config %s: yolo does not set %s for you, so nothing was written.
  To stop launches offering to convert your old config, add "%s": true to %s yourself.
  To convert it instead, run `yolo config migrate`.
  ```
- **Why:** 7.

### config migrate: in a jail
- **Where:** `configmigrate.go:134`.
- **Before (opening):**
  ```text
  In a jail only this workspace's config files migrate; the user config is the host's. …
  ```
- **After:**
  ```text
  In a jail, yolo converts only this workspace's config files. Your user config is on the host, so run `yolo config migrate` there to convert it.
  ```
- **Why:** 3. Each clause now has its own verb.

## Packs (`yolo pack …`)

### pack add: refused
- **Where:** `packadd.go:127`.
- **Before:**
  ```text
  yolo pack add: refused: %v — nothing written. `yolo pack ls` lists the packs yolo ships; `yolo pack --help` says how to name another
  ```
- **After:**
  ```text
  yolo pack add: %v. Nothing was written. Run `yolo pack ls` to list the packs yolo ships, or `yolo pack --help` to see how to name another one.
  ```
- **Why:** 3, 7.

### pack footprint: nothing embedded
- **Where:** `pack.go:1675`.
- **Before:**
  ```text
  yolo pack footprint: no embedded packs available
  ```
- **After:**
  ```text
  yolo pack footprint: this yolo build contains no packs. Pass a pack directory to measure one, or reinstall yolo.
  ```
- **Why:** 6 ("embedded"), every stop names the next step.

### pack status/install: the term "handle"
- **Where:** `packadd.go:40`, `packadd.go:44`, `packadd.go:56`, `packadd.go:233`.
- **Before:**
  ```text
  yolo pack %s: one pack handle at a time, got a second: %q
  ```
- **After:**
  ```text
  yolo pack %s: name one pack at a time; %q is a second one.
  ```
- **Why:** 6 ("handle" is defined only at `packadd.go:233`). Say "pack name" and give the `source/pack` example once.

## `yolo host` (agents on the host)

### host usage: opening
- **Where:** `host.go:38-41`.
- **Before:**
  ```text
  yolo host — configure and launch coding agents on the HOST

  The host is one notch of the confinement dial, and this is where its ergonomics
  live. Two things reach a host agent, split by what they carry:
  ```
- **After:**
  ```text
  yolo host — configure and launch coding agents directly on this machine, with no jail

  yolo gives an agent on the host two things:
  ```
- **Why:** 6 (notch, dial), 8.

### host --update: no installer route
- **Where:** `hostreadiness.go:150` and `hostreadiness.go:164`.
- **Before:**
  ```text
  yolo host --update: %s in pack %s has no supported fresh installer route — preserve its current owner and run without --update, or select a supported pack declaration
  ```
- **After:**
  ```text
  yolo host --update: yolo cannot update %s (from pack %s) itself. Run the command without --update to use the copy you have, or update the pack.
  ```
- **Why:** 5, 6 ("fresh installer route", "current owner", "plain-fork declaration").

### host: missing program refusal
- **Where:** `hostreadiness.go:313-321`.
- **Before:**
  ```text
  yolo host: REFUSING to launch: a program a selected pack declares could not be installed.
  ...
        Fix what each line names (an install needs the network), drop the pack from your packs list, or
        leave it out of yolo's floor with `"host_floor": {"x": false}` in the user config.
  ```
- **After:**
  ```text
  yolo host: refusing to launch: yolo could not install a program that one of your packs needs.
  ...
        Fix what each line says (installs need the network). Or remove the pack: yolo pack remove <pack>
        Or let yolo skip installing it on the host: yolo config set host_floor.<pack> false --user
  ```
- **Why:** 6 (floor), P4, 7.

### host: launch gate refusal
- **Where:** `hostmanagementgate.go:133-135`.
- **Before:**
  ```text
  yolo host: refusing to launch: host command launches require `host_management: "own"`; `host_management` is unset in %s, which means "none". No command was started and yolo provided no host launch assistance.
  Set `"host_management": "own"` in %s to opt in. Separate host configuration and inspection commands use their own rules; `yolo host env` and `yolo host wrappers status` are not subject to this launch gate.
  ```
- **After:**
  ```text
  yolo host: refusing to launch: yolo only launches agents on the host when you let it manage your agents' host config, and %s does not.
  To let it, run:  yolo config set host_management own --user
  (`yolo host env` and `yolo host wrappers status` work without this.)
  ```
- **Why:** 2, 5 ("host launch assistance", "launch gate"), P4.

### host: incomplete pack set
- **Where:** `hostapplygate.go:335-345`.
- **Before (last line):**
  ```text
    Refusing to launch %s: a launch never runs on part of the pack set your config asks for, at any notch.
  ```
- **After:**
  ```text
    Refusing to launch %s: yolo never launches with only some of the packs your config selects.
  ```
- **Why:** 6 (notch).

### host: services and the profile
- **Where:** `host.go:3285-3290`. The user sees this when `yolo host env` or `apply` meets a profile that needs a pack's service.
- **Before:**
  ```text
  profile %q pairs %s with provider %q through pack %q's %q service, and this command runs no process, so there is no command whose lifetime that service could follow: a host service lives only for the command that starts it (docs/design/host-notch-services.md OQ-HS3). Launch the agent as `%s`, or through its host wrapper, which starts the service for that command and stops it when %s exits
  ```
- **After:**
  ```text
  profile %q connects %s to provider %q through the %q service from pack %q. That service only runs while a command it serves runs, and this command starts none. Launch the agent with `%s`, or through its host wrapper. Either one starts the service and stops it when %s exits.
  ```
- **Why:** 7, P1.

## `yolo host apply` report

This report prints on every `yolo host apply` and on every wrapped host launch that has something to say. Most of its lines are covered by P2, P9 and P15. The ones below need their own rewrite.

### unresolved packs: remedy line
- **Where:** `hostapplyremedy.go:166-170`.
- **Before:**
  ```text
  configured packs not in the pack store, so nothing can be applied
  yolo pack install   (retries the fetch this apply already attempted — why it failed is named above for each pack; the next host launch retries it too)
  ```
- **After:**
  ```text
  yolo could not download these packs, so it changed nothing. The reason for each one is above.
  To try again now, run: yolo pack install
  ```
- **Why:** 2, 5 ("pack store"), 7. The next step goes on its own line.

### skills adopted with no local pack
- **Where:** `hostapplyremedy.go:385-399`.
- **Before:**
  ```text
  %d skills in your agent skill dirs are yours, not yolo's, and moved into your local pack
  no local pack location could be resolved, so each one is ARCHIVED instead — nothing is deleted, and nothing composes it back either
  ```
- **After:**
  ```text
  %d skills in your agent's skills folder were not made by yolo.
  yolo could not find your local pack, so it moved them to its archive. Nothing was deleted, but yolo will not put them back.
  Your local pack lives at ~/.config/yolo-jail/local. Create that folder, then copy the skills back from %s.
  ```
- **Why:** 3, 6 ("composes"), every stop names the next step. The current text names no archive path, so the user cannot find the skills. Add it as a `%s`. Check why the location could not be resolved (a missing home, perhaps) before you fix the wording of the next step.

### values a pack computes
- **Where:** `hostapplyremedy.go:495-499`.
- **Before:**
  ```text
  %d values of yours would be replaced by keys the owning pack computes
  the pack's derive computes them from nothing you configure, and a computed key outranks your file at this notch
  ```
- **After:**
  ```text
  A pack sets %d of these values itself, so your own values would be replaced.
  You cannot change them with config. Remove the pack to keep your values.
  ```
- **Why:** 4 ("derive"), 6 ("notch"), 8.

### comments dropped
- **Where:** `hostapplyremedy.go:582-587`.
- **Before:**
  ```text
  %d surfaces would lose comments of yours
  a comment above a key this render CHANGES would be left lying about a value that is gone, so it goes with it
  ```
- **After:**
  ```text
  %d config files would lose some of your comments.
  yolo removes a comment when it changes the value under it, so the comment does not describe an old value.
  ```
- **Why:** 6 ("surface", "render"), 3.

### a pack failed to apply
- **Where:** `hostapplyfailures.go:220-221`.
- **Before:**
  ```text
  %s failed to render: %s
  fix what it names, then `yolo host apply --assert`
  ```
- **After:**
  ```text
  yolo could not write the config from %s: %s
  Fix that, then run: yolo host apply --assert
  ```
- **Why:** 6 ("render"), 3.

### program on the host blocked by your own copy
- **Where:** `hostapplyverdict.go:377`, `hostfloor.go:874`.
- **Before:**
  ```text
  yolo's floor will not install %s over %s until you %s or remove %s
  ```
- **After:**
  ```text
  yolo will not install %s, because %s already exists. To let yolo install it, %s or remove %s.
  ```
- **Why:** 6 (P9).

## Pack preparation (`yolo pack prepare`)

### status: worker line
- **Where:** `preparationstatus.go:30`.
- **Before:**
  ```text
  Tree preparation worker: running (authenticated incarnation %s; pid %d is diagnostic)
  ```
- **After:**
  ```text
  Background builder: running (pid %d).
  ```
- **Why:** 6, 8. The identity string helps only a developer. Show it with `--format json`.

### status: one entry
- **Where:** `preparationstatus.go:49-63`.
- **Before:**
  ```text
  %s [%s/%s] generation %d (diagnostic only; not admission): %s
    retained diagnostic generation %d (previous declaration, not current admission): %s
    admitted entry: %s (diagnostic only; not admission)
  ```
- **After:**
  ```text
  %s [%s/%s] build %d: %s
    previous build %d (from your earlier config): %s
    ready to use: %s
  ```
- **Why:** 6 ("admission", "generation"), 8. "Diagnostic only" repeats on every line, so it says nothing.

### stop: nothing running
- **Where:** `preparationstatus.go:124`.
- **Before:**
  ```text
  No live preparation worker owns the scheduler lease; recorded state was retained.
  ```
- **After:**
  ```text
  The background builder is not running. Its records are kept.
  ```
- **Why:** 5, 6 ("lease").

### stop: requested
- **Where:** `preparationstatus.go:136`.
- **Before:**
  ```text
  Stop requested for the proven preparation worker; its owned child work will drain and state will be retained.
  ```
- **After:**
  ```text
  Asked the background builder to stop. It finishes the builds it has started, then exits. Run yolo pack prepare status to check.
  ```
- **Why:** 6 ("proven", "owned"), 7.

### scheduler reasons
- **Where:** `preparationscheduler.go:435`, `preparationscheduler.go:707`, `preparationscheduler.go:712`.
- **Before:**
  ```text
  another authorized advance owns this key; the next timer tick retries without changing its successful check stamp
  the selected contribution changed from this recorded preparation kind — start a fresh launch to register its current artifact route
  ```
- **After:**
  ```text
  Another build of this is running. yolo tries again on its next scheduled run.
  Your pack changed how this is built. Start a new jail with yolo to build it the new way.
  ```
- **Why:** 5, 6, 8.

## Patched forks (`yolo pack update`) and background builds

### cached good build
- **Where:** `cachedgood.go:87`.
- **Before:**
  ```text
  Cached good: %s at %s (%s, series %s, entry %s); this old build does not contain current edits or the current failure's output; this launch skips its advance.
  ```
- **After:**
  ```text
  Using the last good build of %s, from %s (%s). It does not include your newest patches. This launch does not try to rebuild.
  ```
- **Why:** 6 ("advance", "series", "entry"), 7.

### background failure kept for later
- **Where:** `backgroundadvance.go:459`.
- **Before:**
  ```text
  %s: kept its unclaimed typed failure generation %d in .json — the next fresh launch claims it and recovers it
  ```
- **After:**
  ```text
  %s: a background build failed. The next yolo launch reports the failure and tries again.
  ```
- **Why:** 5 ("unclaimed typed failure generation"), 6. The string also prints the bare word ".json", which looks like a path with its name missing. Check whether a `%s` was dropped there.

### earlier failure could not be read
- **Where:** `patchedadvance.go:846`.
- **Before:**
  ```text
  %s: could not classify its earlier replay error locally: %s
  ```
- **After:**
  ```text
  %s: yolo could not tell why the last patch run failed (%s). It runs the patches again.
  ```
- **Why:** 6 ("replay"), every stop names the next step. Check that the code does retry before using the second sentence.

### fork build gave no proof it finished
- **Where:** `forkbuild.go:1105`.
- **Before:**
  ```text
  The build capture did not return with trustworthy lifetime evidence; its workspace is retained and no output was admitted.
  ```
- **After:**
  ```text
  yolo could not confirm the build finished cleanly, so it did not use the result. The build folder was kept for you to inspect.
  ```
- **Why:** 5, 6. Name the folder with a `%s`; the user cannot inspect a folder they cannot find.

### patched-fork "advance" wording
- **Where:** `patchedadvance.go:561`, `patchedadvance.go:565`, `patchedadvance.go:592`, `patchedadvance.go:1883`, `patchedadvance.go:2025`, `patchedadvance.go:2055`, `patchedadvance.go:2072`, `backgroundadvance.go:1002-1027`.
- **Before:**
  ```text
  advance was interrupted … walked back to … the background advance …
  ```
- **After:**
  ```text
  the update was interrupted … went back to the last patch that applied … the background update …
  ```
- **Why:** 4 ("advance" as a noun), 6.

## Saved program copies (capture and unpacking)

### no saved copy
- **Where:** `capturematerialize.go:164`.
- **Before:**
  ```text
    yolo: no capture for %s (%s): %v
  ```
- **After:**
  ```text
    yolo: no saved copy of %s for %s (%v). Installing it the normal way.
  ```
- **Why:** 6, every stop names the next step. Check that the caller does fall back before adding the second sentence.

### unpacked
- **Where:** `capturematerialize.go:256`, `capturematerialize.go:354`.
- **Before:**
  ```text
    Materialized %s from capture %s by %s (%d files, %s)
  ```
- **After:**
  ```text
    Unpacked %s from a saved copy (%d files, %s).
  ```
- **Why:** 6. Keep the capture key and the "by" field for `--verbose`.

### capture-run outside a jail
- **Where:** `capture.go:95`.
- **Before:**
  ```text
  capture-run: artifact production requires a sealed jail — keep this installer on its supported legacy route until its sealed producer is available
  ```
- **After:**
  ```text
  capture-run: this only runs inside a jail started by yolo. Install the program the normal way instead.
  ```
- **Why:** 6 (P12), 8. Check what "sealed jail" means in code before shipping; if it is a specific launch mode, name the command that starts it.

### program capture with no route
- **Where:** `programcapture.go:28`.
- **Before:**
  ```text
  program capture: no selected producer/admission guard — use the supported installer route
  ```
- **After:**
  ```text
  yolo cannot save a copy of this program here. It installs it the normal way when a jail starts.
  ```
- **Why:** 5, 6, and "use the supported installer route" names no command.

### foreground-only install
- **Where:** `programcapturedemand.go:265`.
- **Before:**
  ```text
  %s: foreground-only installation preparation; the user resolver does not authorize these exact inputs for a detached worker
  ```
- **After:**
  ```text
  %s: yolo installs this program when a jail starts, not in the background.
  ```
- **Why:** 5, 6, 8.

## Remaining usage texts

These texts are reference material, so they may be longer than an error. They still follow the rules.

### `yolo pack --help`
- **Where:** `pack.go:135`, `pack.go:154`, `pack.go:206`, `pack.go:219`, `pack.go:245`, `pack.go:266`.
- **Before:**
  ```text
  content-only pack to carry install_hints for the host notch.
  validate the tree AND the pack.json manifest; print its footprint.
  [ref] = an embedded name OR a local path / file:// pack
  --setup host-self builds for the host notch, not the
  ```
- **After:**
  ```text
  a pack with no files, which only tells yolo how to install programs on the host.
  check the pack folder and its manifest (pack.toml or pack.json), then print what it adds.
  [ref] = the name of a pack yolo ships, or a local path or file:// address
  --setup host-self builds for the host instead of a jail.
  ```
- **Why:** 6 ("notch", "embedded"), Keep it true (pack.toml). `pack.go:135` ("schema-only today") says the kind does nothing yet. Say "yolo reads this but does not use it yet."

### `yolo apply --help`
- **Where:** `apply.go:1731-1782`.
- **Before:**
  ```text
  yolo apply --setup <name> … at that setup's notch for this run
  actually write: regenerate only the keys yolo manages (pure rmw), leaving your own keys; non-config kinds refused by name
  take yolo back OUT: remove the keys it asserted, on the authority of the provenance record it wrote, and forget the home.
  ```
- **After:**
  ```text
  yolo apply --setup <name>   … for that setup instead, for this run only
  write the changes: update only the keys yolo manages, and leave your own keys alone
  remove what yolo wrote, using the record it keeps of each change
  ```
- **Why:** 6 ("notch", "pure rmw", "provenance record", "asserting posture"), 1. The text uses "notch" at lines 1738, 1760, 1762, 1765 and 1771. Line 1754 names `yolo-jail.local.jsonc` (P14).

### `yolo surface --help`
- **Where:** `surfacecmd.go:12-14`, `surfacecmd.go:104`.
- **Before:**
  ```text
  Inspect the generated-config composition pipeline (the layered regeneration of
  agent settings/MCP/LSP/mise config). See 'yolo config-ref' and
  docs/reference/pack-system.md.
  ```
- **After:**
  ```text
  Show how yolo builds each agent config file it manages (settings, MCP, LSP and mise).
  yolo calls each of these files a "surface". See yolo config-ref.
  ```
- **Why:** 5, 6. This is the place to define "surface" once (P3). Drop the doc path (P1). Line 104 names the retired 'assert' (P14).

### `yolo update --help`
- **Where:** `update.go:39-41`, `update.go:72-73`.
- **Before:**
  ```text
  go install / pipx / uv      check only: these install the host binary without
                              the jail bundle, so update the host and separately
                              packaged jail bundle together
  To turn the background check, notice and offer off: "update_check": false in
  ~/.config/yolo-jail/config.jsonc, or YOLO_NO_UPDATE_CHECK=1 for one shell.
  ```
- **After:**
  ```text
  go install / pipx / uv      check only. These install the yolo command without
                              the files a jail needs, so update both the same way
                              you installed them.
  To turn off the update check and its notice, run
    yolo config set update_check false --user
  or set YOLO_NO_UPDATE_CHECK=1 for one shell.
  ```
- **Why:** 1 ("jail bundle"), P4, P5.

### `yolo programs --help`
- **Where:** `programs.go:57-59`.
- **Before:**
  ```text
  Nothing is removed at boot unless you ask for it. Set "programs": {"autoprune": true} in
  ~/.config/yolo-jail/config.jsonc (USER scope only) to make every launch remove what it
  catalogs.
  ```
- **After:**
  ```text
  yolo removes nothing at startup unless you ask. To make every launch remove orphans, run
    yolo config set programs.autoprune true --user
  ```
- **Why:** P4, P5, 1 ("USER scope", "catalogs").

### `yolo describe --help`
- **Where:** `describe.go:345-348`.
- **Before:**
  ```text
  yolo describe          human-readable summary (confinement — what enforces it and
                         whether agent autonomy is on — the resolved package
                         profile below the jail notch, packs, description hash)
  ```
- **After:**
  ```text
  yolo describe          a readable summary: what confines the agent and whether
                         it runs without asking, its packages, its packs, and a hash
  ```
- **Why:** 5, 6 ("notch").

### `yolo prune --help`
- **Where:** `commands.go:264-276`.
- **Before:**
  ```text
  superseded install captures (every store entry but the newest per program —
  what a materialize would never choose again), and the on-disk copies of yolo's
  built-in packs: other builds' trees under the state dir's embedded-packs/, and
  the per-process copies earlier builds left in TMPDIR. … no running yolo holds its lease …
  ```
- **After:**
  ```text
  old saved copies of programs (all but the newest of each), and copies of yolo's
  built-in packs that older versions of yolo left behind. yolo never removes a copy
  that a running yolo still uses.
  ```
- **Why:** 2, 6 (P13), 8.

### Usage texts that need no change
- `wait.go:23-58`, `hostrun.go:31-68`, `setupcmd.go:30-50`, `audit.go:24-52`, `history.go:24-47`, `features.go:19-37` and `programs.go` apart from the lines above.
- `features.go:28-31` uses "contribution kind" (P3), which a pack author understands. Leave it.

## The configuration reference (`yolo config-ref`)

`config_ref.txt` is 3,502 lines. I searched it for every rule-6 word and the JSONC names, and read lines 1-200, 1045-1135 and 1240-1350 in full. It is reference material for users who edit config, so it may explain more than an error does. These are the changes it needs.

### The whole reference is written in the old format
- **Where:** `config_ref.txt:3-18`, plus 17 "Example (~/.config/yolo-jail/config.jsonc)" headers and about 100 `"key": value` example lines.
- **Before:**
  ```text
  The rest of this reference documents every key in its legacy JSONC spelling (the `"key": value` examples),
  which a TOML file spells with the names THE TOML FORMAT lists.
  ```
- **After:** keep this sentence until the examples change. Then convert each example to TOML, or to the `yolo config set` command that writes it, and drop the sentence. Say "your config.toml" where it says `config.jsonc` (68 places).
- **Why:** 1, AGENTS.md (advice names a command), Keep it true. A reader who copies an example into `config.toml` gets a syntax error (P4).

### Design IDs and doc names
- **Where:** these lines of `config_ref.txt`, each followed by the text it prints:
  ```text
  config_ref.txt:2396  host-notch-services.md OQ-HS4, HS-D27, HS-D29
  config_ref.txt:2837  OQ-CS5
  config_ref.txt:2869  docs/reference/mcp-configuration.md
  config_ref.txt:3125  notch-convergence.md NC-D71
  ```
- **After:** delete the parenthetical in each case. The sentence before it already states the rule.
- **Why:** 6 (P1).

### "notch", "surface" and "ceiling"
- **Where:** "notch" 17 times (for example `config_ref.txt:467`, `config_ref.txt:1248`, `config_ref.txt:1298`, `config_ref.txt:1590`); "surface" 31 times (for example `config_ref.txt:1051`, `config_ref.txt:1096`, `config_ref.txt:1115-1118`, `config_ref.txt:1163-1166`); "ceiling" 5 times (`config_ref.txt:65`, `config_ref.txt:69`, `config_ref.txt:96`, `config_ref.txt:107`, `config_ref.txt:587`); "working set" at `config_ref.txt:626`.
- **Before:**
  ```text
  The value selects the surface mode the host notch renders in:
  ```
- **After:**
  ```text
  The value decides how yolo writes agent config files on the host:
  ```
- **Why:** 6. Define "surface" once near `config_ref.txt:1051` and keep it in the `host_files` section, where `yolo config ls` prints the word. Replace "your ceiling" with "what your user config allows".

### host_files modes: internal references
- **Where:** `config_ref.txt:1092-1096`, `config_ref.txt:1101`, `config_ref.txt:1128-1130`.
- **Before:**
  ```text
  (`yolo config reset` does NOT re-seed it: reset discards CAPTURED edits by deleting the §5 sidecars, and a `once` surface has none.)
  Note 0444 is DAC, not kernel enforcement
  ```
- **After:**
  ```text
  (yolo config reset does not re-create it: reset only throws away edits yolo saved, and a `once` file has none.)
  Note: mode 0444 is only file permissions, which the kernel does not enforce against root.
  ```
- **Why:** 1 (the section number points into a design doc the reader does not have), 6 ("DAC", "sidecars").

### host_management: "assert" paragraph
- **Where:** `config_ref.txt:1318-1325`, `config_ref.txt:1342-1344`.
- **After:** keep the paragraph. A user with an old config needs it. Add one line at the end: "To fix an old config, run `yolo config set host_management own --user`, or `yolo config unset host_management --user`." Also note that `--assert` at `config_ref.txt:1251`, `config_ref.txt:1274` and `config_ref.txt:1344` is a different thing: it is the `yolo host apply` flag that writes changes. Readers will mix the two up.
- **Why:** every stop names the next step, 1.

## Removed flags and other rare stops

### prune --keep-images
- **Where:** `commands.go:374-381`.
- **Before:**
  ```text
  Error: --keep-images was removed (OQ-LS3).
  Image retention is no longer a count: yolo keeps each workspace's CURRENT image (one pointer per workspace, plus every image a container is running on) and removes the superseded ones, because a superseded copy is an undo buffer nobody used.
  Fix: drop the flag. ...
  ```
- **After:**
  ```text
  yolo prune: --keep-images was removed. yolo now keeps each workspace's current image and every image a running jail uses, and removes the older ones.
  Drop the flag. To keep an image, launch its workspace once. `yolo prune` without --apply shows what it would remove.
  ```
- **Why:** 6 (the design id), 8 ("undo buffer nobody used").

### nix-roots: no workspace
- **Where:** `nixroots.go:110-111`.
- **Before:**
  ```text
  yolo nix-roots: no workspace here. Run it from inside a workspace yolo has launched (one with a .yolo directory or a yolo-jail.jsonc).
  ```
- **After:**
  ```text
  yolo nix-roots: no workspace here. Run it inside a workspace: a folder with a yolo-jail.toml, or one yolo has launched.
  ```
- **Why:** the original is wrong today; see below.

### nix-roots keep: no host path map
- **Where:** `nixroots.go:137-140`.
- **Before:**
  ```text
  yolo nix-roots keep: this jail's launcher stated no host path map, so no root made here can be spelled for the host. That is a jail without the host nix daemon, a macos-user jail (where `nix-store --add-root` already makes a root the host honors), or a launcher older than the map: restart the jail with a current yolo.
  ```
- **After:**
  ```text
  yolo nix-roots keep: this jail cannot tell the host where your link is, so yolo cannot keep it. Restart the jail with a current yolo. In a macos-user jail you do not need this: `nix-store --add-root` already makes a root the host keeps.
  ```
- **Why:** 6 ("host path map", "spelled"), 7.

### stop: no runtime
- **Where:** `stop.go:186`.
- **Before:**
  ```text
  yolo stop: no container runtime found (podman / container).
  ```
- **After:**
  ```text
  yolo stop: neither podman nor Apple's `container` is on PATH, so there is no jail to stop. Run `yolo check` to see what is missing.
  ```
- **Why:** 3, every stop names the next step.

### update: in a jail
- **Where:** `update.go:396`.
- **Before:**
  ```text
  yolo update: this is the jail's copy of the host's yolo, which the jail keeps until it is relaunched; …
  ```
- **After:**
  ```text
  yolo update: this jail runs the host's yolo and keeps that version until it restarts. …
  ```
- **Why:** 2.

### claude-auth refresh: success
- **Where:** `claudeauth.go:107`.
- **Before:**
  ```text
  Refreshed the machine's Claude login and rewrote every view.
  ```
- **After:**
  ```text
  Refreshed this machine's Claude login and updated every jail's copy of it.
  ```
- **Why:** 6 ("view").

### init usage: run-on line
- **Where:** `commands.go:852-855`.
- **Before:**
  ```text
  Scaffold this workspace: write a three-line pointer file, yolo-jail.toml, and
  print the agent briefing. No git ignore file is written: .yolo/ ignores itself. A project needs no file to run, so no settings are written. An
  existing yolo-jail.toml, yolo-jail.jsonc or yolo-jail.json is never overwritten
  or shadowed — re-running init on a configured workspace just reprints the briefing.
  ```
- **After:**
  ```text
  Set up this workspace: write a short yolo-jail.toml and print the agent briefing.
  The file holds no settings yet, because a project runs fine without them.
  yolo never overwrites an existing yolo-jail.toml, yolo-jail.jsonc or yolo-jail.json.
  Running init again just prints the briefing again.
  ```
- **Why:** 7, and the line is not wrapped like the others. "No git ignore file is written" says what yolo does not do, which the user does not need to know (rule 8).

## Wrong today

- **`runcmd.go:105` and `runcmd.go:107`:** `yolo run --help` lists `--update, -u` twice, with two different descriptions: "Check selected build inputs now; reuse unchanged good builds" and "Wait for fresh preparation of this launch's selected programs and trees". Keep one. Check which description matches what the parser does.
- **`help.go:75`:** `yolo --help` says `init` scaffolds `yolo-jail.jsonc`. It writes `yolo-jail.toml` (`init.go:42-48`, `commands.go:852`).
- **`init.go:91` and `commands.go:909-911`:** `yolo init-user-config` checks for and writes `paths.UserConfigPath()`, which only knows the legacy names. On a machine that already has `config.toml`, it creates `config.jsonc` beside it and prints "Created". The config reader refuses that pair (AGENTS.md: "a TOML file beside a legacy one is refused"). The usage text also says a workspace's `yolo-jail.jsonc` narrows these defaults. This is a code bug as well as a message bug. The JSON next steps at `init.go:138` match the JSONC file it writes, but not a TOML user config.
- **`nixroots.go:110-111`:** says a workspace is "one with a .yolo directory or a yolo-jail.jsonc". The marker check (`configtarget.go:420`) actually needs either a `.yolo/config-boot.json` file or a workspace config file (TOML or JSONC). A bare `.yolo` directory is not enough, and `yolo-jail.toml` counts.
- **P4 sites (`hostreadiness.go:316`, `hostfloor.go:881`, `hostfloor.go:1185`, `hostapply.go:301`, `host.go:4175`, `hostmanagementgate.go:134`, `hostmanagementgate.go:208`, `configdiff.go:178`, `patchedadvance.go:2049`, `patchedadvance.go:2066`, `hostcapabilities.go:76`):** these tell the user to put JSON such as `"host_management": "own"` into a file that `paths.UserConfigDisplayPath()` names as `config.toml` whenever that file exists or no user config exists. In TOML that is a syntax error.
- **`commands.go:943-944`:** `yolo config-ref --help` says the reference covers `yolo-jail.jsonc` and `config.jsonc`. Both TOML files are the current format, and `config_ref.txt:14-15` still lists the user defaults as `config.jsonc`.
- **`approve.go:109-111`:** `yolo approve --help` contains the empty-list message ("No requests, grants or working-set entries. … Run yolo approve --help for the host store commands."). The help text tells the reader to run the help they are already reading. It looks like text that was pasted into the usage constant by mistake. The real empty-list line is `approve.go:399`.
- **`configeditverbs.go:261`:** after a project edit, the message says "Run `yolo check` in this workspace". But `yolo config set/add/unset` always runs that check right after (`config.go:133` calls `runConfigEditWithPostWriteCheck`).
- **`request.go:178`:** after `reportRequestSkipped` says "this project runs without it", the next line still says "It is approved on the host: the human runs `yolo config apply` …". Approving the config change does not enable a capability the user config does not allow. The reader is led to think that approving will make it work.
- **`config_ref.txt:163` and `config_ref.txt:179`:** the reference says a jail sees a generated `~/.config/yolo-jail/config.jsonc` and `inherited-launch.jsonc`. A launch now writes `config.toml` and `inherited-launch.toml` (`internal/cli/run/inheritscope.go:66-67`). A jail's home holds only `.podman-placeholder` files under the `.jsonc` names.
- **`config_ref.txt` examples:** every example is JSON, and 17 of them are headed "Example (~/.config/yolo-jail/config.jsonc)". On a machine with `config.toml`, copying one in gives a syntax error. `config_ref.txt:5-8` admits this, but the examples are what readers copy.
- **`surfacecmd.go:104`:** `yolo surface --help` lists `'assert'` as a `host_management` value. That value is retired and refused (`config_ref.txt:1318-1320`).
- **`update.go:72-73` and `programs.go:57-58`:** usage texts tell the user to put JSON into `config.jsonc`. On a TOML machine that file must not exist beside `config.toml` (P4, P5).
- **`apply.go:1754`:** `yolo apply --help` names `yolo-jail.local.jsonc` as the undeclared input that `--sealed` refuses. It should name `yolo-jail.local.toml` too, or say "the local config file".
- **`pack.go:206`:** `yolo pack --help` says `lint` validates "the pack.json manifest". A pack may use `pack.toml`, which `pack.go:202` itself describes.
- **`backgroundadvance.go:459`:** the message says the failure was kept "in .json". The format string has no file name before `.json`, so the user sees a bare extension.
- **`hostapplyremedy.go:398`:** the skills-archived message tells the user their skills are in an archive, but it does not say where the archive is. Nothing in the group names the path.

## Not covered

- **Rare message lines that are only clauses.** In the background build files (`patchedadvance.go`, `shareddemand.go`, `programnpm.go`), I checked every site in the excerpts for the rule-6 words and covered them with P10-P13. I did not write a separate rewrite for each of the roughly 150 `fmt.Errorf` clauses that end up wrapped inside another message. Their wording depends on the outer message, which is in `internal/cli/run`.
- **`config_ref.txt`:** I read about 400 of its 3,502 lines in full and searched the rest for the rule-6 words, the JSONC names and design IDs. I did not rewrite the reference line by line. Converting its examples to TOML is a writing task of its own, not a message fix.
- **Long prose in usage texts that I judged acceptable** (`wait`, `host-run`, `setup`, `audit`, `history`, `features`) was read once for the rules. It was not edited for length.
- **Messages from shared helpers outside this package** that `internal/cli` only passes through (for example `packsrc`, `capture`, `entrypoint` errors inside a `%v`). These belong to the parts that own those packages.
- **Whether each proposed next step is real.** Several rewrites say "Check … before shipping" where the code path that follows was not traced to the end. Those are marked in their **Why** lines.
