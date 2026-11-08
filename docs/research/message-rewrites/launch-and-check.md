# Message rewrites: Launching a jail, and yolo check

Scope: `internal/cli/run` and `internal/cli/check` (non-test `.go` files). Messages read: about 1,270 (approximate: about 800 print sites in `run`, about 300 `fmt.Errorf`/`errors.New` strings in `run`, and about 330 graded findings in `check`). Rewrites proposed: about 95 (approximate: 70 individual entries, plus 12 patterns that cover about 145 more sites). A second pass read about 140 error strings in eight `run` files, and about 30 texts defined in other packages that the launch or `yolo check` prints.

## Patterns

### P1. The word "keeper" reaches the terminal on every launch

The launch, attach, quit and refusal lines call the background process that holds a jail "the keeper". A reader who has not read the design docs does not know the word. Say "the background yolo process" once. After that, say what it does: it keeps the jail running, or it is still shutting the jail down.

- **Before:**
  ```text
  The keeper of %s (pid %s) has not finished after %s. Its log: %s. Its last line: %q. `kill %s` ends it in order.
  ```
- **After:**
  ```text
  The yolo process shutting down %s (pid %s) is still running after %s. Its last log line was %q (full log: %s). Run `kill %s` to let it finish shutting down.
  ```
- **Sites:** `internal/cli/run/keeperspawn.go:304`, `:366`, `:415`, `:498`, `:596`, `:634`, `:643`, `:665`, `:672`, `:696`, `:812`, `:845`, `:877`, `:1039`, `:1145`, `:1154`, `:1163`, `:1180`, `:1193`, `:1196`, `:1426`, `:1461`, `:1482`, `:1494`, `:1514`, `:1546`, `:1550`, `:1588`, `:1595`, `:1597`, `:1620`, `:1675`, `:1697`, `:1703`, `:1877`, `:1885`, `:1938`; `internal/cli/run/run.go:2666`, `:2711`, `:2748`, `:3310`; `internal/cli/run/contracttags.go:584`, `:587`; `internal/cli/run/keeper.go:382`, `:422`, `:431`, `:646`, `:671`, `:678`, `:1312`, `:1319`; `internal/cli/run/attachmcp.go:77`, `:78`. Several of these are covered individually below.

### P2. "Doorway" and "front" name internal parts

On the macOS sandbox and `yolo host`, a "doorway" is a small local server that yolo starts outside the sandbox and that forwards to a host service. A "front" is the authenticated listener in front of a host service. Users meet both words in launch lines and `yolo check` findings without a definition. Say "the local connection to the X service" for a doorway and "the service's listener" for a front.

- **Before:**
  ```text
  Opened the %q doorway (pack %q, pid %d) on %s for this launch, outside the sandbox: it answers only this launch's caller token, forwards to the host's %q service, and stops when the command exits. Its log: %s
  ```
- **After:**
  ```text
  Started a local connection to the host's %q service (pack %q, pid %d) on %s. It runs outside the sandbox and accepts only this launch's token. It stops when the command exits. Its log: %s
  ```
- **Sites:** `internal/cli/run/macosuserdoorways.go:166`, `:172`, `:206`; `internal/cli/run/hostdoorways.go:502`, `:515`; `internal/cli/run/doorwayplacement.go:67`; `internal/cli/run/jaildaemondecline.go:77`; `internal/cli/run/keeperspawn.go:1546`, `:1766`; `internal/cli/run/loopholesruntime.go:813`, `:1924`, `:2100`; `internal/cli/run/keeperwatch.go:71`, `:75`; `internal/cli/check/sections_loopholes.go:335`, `:499`, `:519`, `:527`, `:536`, `:549`.

### P3. Decision IDs and design-doc sections are printed to the user

Launch lines end with parentheticals such as these:

```text
(providers.md OQ-CN7)
(OQ-ED2)
(FP-D9: …)
```

These IDs mean nothing to a user and give no next step. Drop them. Where the user needs to read more, link the user guide.

- **Before:**
  ```text
  Not started: the %s jail daemon, because %s, which it serves; select one (`-p <agent>=%s`) to start it (providers.md OQ-CN7).
  ```
- **After:**
  ```text
  yolo did not start the %s service, because %s. To start it, select a profile it serves (`-p <agent>=%s`).
  ```
- **Sites**, each followed by the id or section the message prints:
  ```text
  internal/cli/run/packservices.go:319             OQ-CN7
  internal/cli/run/packservices.go:333             NC-D59
  internal/cli/run/credentialnotes.go:105          OQ-CN9
  internal/cli/run/profiledaemons.go:55            OQ-CN7
  internal/cli/run/loopholeinert.go:487            OQ-ED2
  internal/cli/run/jailgrant.go:405                OQ-ES5
  internal/cli/run/seal.go:315, :328               FP-D9, FP-D19
  internal/cli/check/sections_loopholes.go:338     HS-D34
  internal/cli/check/section_autogc.go:78          storage §1
  internal/cli/check/section_packageprofile.go:135 provisioner-evidence.md §3.4
  internal/cli/check/section_packageprofile.go:165 HP-DIR3
  ```
  The housekeeping log lines (`currentimage.go:71`, `autoreapimages.go:61`, `housekeeping.go:693`) carry IDs too, but they go to `.yolo/housekeeping.log`, not the terminal, so they are low priority.

### P4. A yolo failure tells the user to repair yolo's own internals

When a host service cannot get its launch input, the refusal tells the user to "wire the core producer", "fix the core input producer" or "repair private host state". These are yolo's internals, and the user cannot fix them. Say that yolo failed, say what the user can try (launch again), and point to the issue tracker when retrying does not help. Keep the specific permission advice where the code really did fail on a directory the user owns.

- **Before:**
  ```text
  Host service '%s' has no verified private launch-input destination and complete jail mount argv; it was not started. Wire the core producer and full assembled mount topology before retrying.
  ```
- **After:**
  ```text
  yolo did not start the host service '%s': it could not prepare the service's private settings file. This is a yolo bug. Launch again, and if it happens again, report it at https://github.com/mschulkind-oss/yolo-jail/issues with the output of `yolo --version`.
  ```
- **Sites:** `internal/cli/run/launchinput.go:236`, `:244`, `:263`, `:273`, `:279`, `:288`, `:295`, `:332`, `:336`, `:486`, `:494`, `:498`, `:505`, `:547`, `:782`; `internal/cli/run/loopholesruntime.go:625`, `:1311`, `:1773`. (`:302`, `:372`, `:392`, `:511`, `:532` do name a directory the user owns, so keep their "fix the permissions" step and drop only "core".)

### P5. The private-HTTP identity errors read as a wall of internal state

The errors that `run.go:3191`, `:3211` and `:3219` wrap into "Refusing to attach: %s. Stop this jail with …" spell out internal records: "running HTTP front %q prepared identity does not match its frozen running pack tree". The user's next step is always the same (stop the jail and launch again). Say that in plain words. Keep the specific detail on a second, dimmed line for a bug report.

- **Before:**
  ```text
  Refusing to attach: running HTTP front "github" prepared identity does not match its host scope. Stop this jail with yolo stop, then relaunch so its running HTTP identity can be verified.
  ```
- **After:**
  ```text
  Refusing to attach: yolo cannot confirm that the running jail's "github" service is the one this launch set up.
  Stop the jail with `yolo stop`, then launch again.
  (Detail: the prepared identity does not match its host scope.)
  ```
- **Sites:** `internal/cli/run/run.go:3175`, `:3178`, `:3191`, `:3211`, `:3219`; `internal/cli/run/privatehttpclients.go:65`, `:130`–`:179`, `:402`–`:444`, `:465`–`:540`, `:573`–`:586`, `:600`–`:691`, `:751`, `:781`, `:784`; `internal/cli/run/privatehttpownership.go:183`–`:351`.

### P6. `yolo check` headlines use dash shorthand: "Inside jail — X skipped"

Each line has no subject and no verb. Write one sentence.

- **Before:**
  ```text
  [SKIP] Inside jail — loophole checks skipped
  ```
- **After:**
  ```text
  [SKIP] yolo check does not check host services from inside a jail
  ```
- **Sites:** `internal/cli/check/sections_loopholes.go:31`, `:567`; `internal/cli/check/check.go:900`, `:1076`; `internal/cli/check/sections_macos.go:83`; `internal/cli/check/sections_devices.go:149`, `:158`, `:251`; `internal/cli/check/sections_misc.go:19`.

### P7. "found but not working: exec failed" headlines

These `yolo check` headlines stack a label on a status. Say what happened.

- **Before:**
  ```text
  [FAIL] podman found but not working: exec failed
  ```
- **After:**
  ```text
  [FAIL] podman is installed, but yolo could not run it
  ```
- **Sites:** `internal/cli/check/check.go:382` (exec failed → "could not run it"), `:387` (timeout → "did not answer in time"), `:409`, `:429` (liveness probe failed → "did not answer a test command"); `internal/cli/check/section_nix_probe.go:48`, `:57`; `internal/cli/check/section_nixld.go:35`, `:56` ("could not run the mise node probe: exec failed" → "yolo could not run the mise node test").

### P8. "…, then: yolo check" ends about 50 notes

The `recheck` constant (`internal/cli/check/nextsteps.go:27`) is appended to notes as "…, then: yolo check". The colon form has no verb. Change the constant once to "then run `yolo check` again". (`nixRecheck` at `nextsteps.go:157` has the same form: "then, in a new terminal: yolo check" → "then open a new terminal and run `yolo check` again".)

- **Before:**
  ```text
  Fix the `packs` list in ~/.config/yolo-jail/config.toml (`yolo pack --help` shows an entry's forms), then: yolo check
  ```
- **After:**
  ```text
  Fix the `packs` list in ~/.config/yolo-jail/config.toml (`yolo pack --help` shows the forms an entry can take), then run `yolo check` again.
  ```
- **Sites:** every use of `recheck` in `internal/cli/check`. `rg -n '\brecheck\b' internal/cli/check` lists them; they are in `check.go`, `packs.go`, `nextsteps.go`, `sections_devices.go`, `sections_loopholes.go`, `section_iopriority.go`, `section_hostwrappers.go`, `sections_macos_platform.go`, `sections_gpu.go`, `helpers.go` and `reporter.go`.

### P9. "the floor" in `yolo check` and `yolo host` lines

"Floor" is yolo's private install directory for agent programs that `yolo host` runs. The section header and its lines use the word without defining it. Call it "yolo's own copies" or "yolo's install folder".

- **Before:**
  ```text
  claude — no floor entry: %s
  -> `yolo host` refuses to launch until the floor holds it, or until `host_floor` leaves pack claude out (`"host_floor": {"claude": false}` in the user config)
  ```
- **After:**
  ```text
  claude — yolo has not installed its own copy: %s
  -> `yolo host` will not start claude until yolo installs it. To use the claude on your PATH instead, add `host_floor = { claude = false }` to ~/.config/yolo-jail/config.toml.
  ```
  (Verified: no `yolo config` verb can set this today. The readable config schema in `internal/config/schema.go` has only the top-level `host_floor` key and no `host_floor.<pack>` rule, so `yolo config set host_floor.claude false --user` would refuse with "key has no value type". `yolo config set host_floor false --user` stops yolo installing a copy of every agent, not just claude. The code should print the spelling that matches the user's config file, through `paths.UserConfigDisplayPath()`: `"host_floor": {"claude": false}` for a legacy `config.jsonc`. See "Wrong today" for the missing verb.)
- **Sites:** `internal/cli/check/section_hostfloor.go:53` (header "Host agent floor" → "yolo's copies of your agents"), `:66`, `:69`, `:83`, `:90`, `:94`, `:108`, `:113`, `:135`, `:143`; `internal/cli/check/modellists.go:359`, `:367`, `:369`, `:376`, `:380`.

### P10. A pack's file-delivery conflict tells the user to rename files they do not own

These conflicts stop the launch (printed bold red at `internal/cli/run/run.go:2241`, and as a `yolo check` failure at `internal/cli/check/packs.go:413` and `:416`). They say "give them different file names, or drop one", which is advice for the pack's author. The user usually did not write the pack. "Claims", "contributions" and "merge" are also pack-manifest words.

- **Before:**
  ```text
  pack %s and pack %s both deliver different content for ~/%s — give them different file names, or drop one
  pack %s claims ~/%s as a file, and pack %s claims ~/%s, inside it — cannot nest inside a file
  ```
- **After:**
  ```text
  Packs %s and %s both put a different file at ~/%s, so yolo cannot start this jail. Remove one of them with `yolo pack remove <name>`, or ask its author to rename the file.
  Pack %s puts a file at ~/%s, and pack %s puts another file inside that path, so yolo cannot start this jail. Remove one of them with `yolo pack remove <name>`, or ask its author to move the file.
  ```
- **Sites:** `internal/cli/run/packfiles.go:296` (directory and file at one path), `:366`, `:370`, `:401`, `:405`, `:446`, `:450`, `:529`, `:533`, `:554`, `:558`.
- **Why:** 1, 6 ("contributions", "claims"), plus the next step was for the wrong person.

### P11. Home-folder setup errors name an internal folder and give no next step

The launch builds a read-only skeleton of the jail's home folder before it starts the container. Its fatal errors print bare in bold red (`internal/cli/run/run.go:2367`), and its best-effort errors print as "Warning: %s" (`run.go:2375`). Both start with "home skeleton:", which the user cannot act on, and neither says what happens next.

- **Before:**
  ```text
  home skeleton: cannot create the mountpoint %s: %w
  Warning: home skeleton: cannot create the mountpoint %s: %w
  ```
- **After (fatal):**
  ```text
  yolo could not prepare the jail's home folder at %s: %w. Check that the disk has space and that you own ~/.local/share/yolo-jail, then launch again.
  ```
- **After (warning):**
  ```text
  Warning: yolo could not prepare ~/%s inside the jail (%w). The jail starts, but anything the config puts at that path is missing.
  ```
- **Sites (fatal):** `internal/cli/run/homeskeleton.go:91`, `:95`, `:107`, and the symlinks for the fixed home-file redirects through `:133`. **Sites (warning text, through `warn` at `:139`):** `homeskeleton.go:286`, `:298`, `:316`, `:320`, `:324`, `:350`, `:356`.
- **Why:** 1 ("home skeleton", "mountpoint"), 8, plus the next step was missing.

### P12. Messages point to repository paths the user does not have

Users who install a release do not have the source tree. A path such as `docs/reference/loopback-tls-reachability.md` or `userguide/guides/macos.md` is a dead end for them. Print the published page on docs.yolo-jail.mschulkind.dev instead.

- **Before:**
  ```text
    docs/reference/loopback-tls-reachability.md
  See userguide/guides/macos.md — Podman on a Mac.
  ```
- **After:**
  ```text
    More: <published URL for that page>
  ```
- **Sites:** `internal/cli/run/hostloopback.go:434`, `:497`, `:643`, `:674`, `:707`, `:732`, `:781`; `internal/runtime/machineshares.go:353` (printed by `internal/cli/check/sections_macos_platform.go:124` and the launch). `docs/reference/` pages are not in the published user guide, so a user-facing page has to exist first.
- **Why:** 1, plus the next step was unusable.

## Every launch: lines the user always sees

### Background-process line
- **Where:** `internal/cli/run/keeperspawn.go:415` (printed at `run.go:2655`). Every fresh container launch prints it.
- **Before:**
  ```text
  keeper: yolo internal daemon jail-keeper will hold this jail's host services (the github service, 1 port forward) and its container until its last session leaves; log: %s
  ```
- **After:**
  ```text
  A background yolo process keeps this jail and its host services (the github service, 1 port forward) running until the last session quits. Its log: %s
  ```
- **Why:** 1 (`yolo internal daemon jail-keeper`), 3, 6 (keeper), 8.

### Background-process start
- **Where:** `internal/cli/run/run.go:2711` and `internal/cli/run/keeperspawn.go:1482`. Every fresh launch prints it.
- **Before:**
  ```text
  keeper: started, pid %d
  ```
- **After:**
  ```text
  Started the background yolo process (pid %d).
  ```
- **Why:** 3, 6.

### Pack environment heading
- **Where:** `internal/cli/run/run.go:1534`. It prints before the list of what each pack sets up.
- **Before:**
  ```text
  Pack environment this launch:
  ```
- **After:**
  ```text
  Your packs set up the following for this launch:
  ```
- **Why:** 3, 5.

### Patched extensions heading
- **Where:** `internal/cli/run/patchedtrees.go:179`. It prints when a launch mounts a patched build of an extension.
- **Before:**
  ```text
  Patched extensions this launch:
  ```
- **After:**
  ```text
  This launch uses these patched extensions:
  ```
- **Why:** 3, 5.

### Session the background process cannot count (fresh launch)
- **Where:** `internal/cli/run/run.go:2666`. It prints when yolo cannot take the session lock for the first session.
- **Before:**
  ```text
  This jail's keeper cannot count this session, so it will not end the jail when its sessions leave; %s ends it.
  ```
- **After:**
  ```text
  yolo could not register this session, so the jail will keep running after every session quits. Run %s to stop it.
  ```
- **Why:** 6 (keeper), 4 ("when its sessions leave").

### Session the background process cannot count (attach)
- **Where:** `internal/cli/run/run.go:3310`. It prints when an attach cannot take the session lock.
- **Before:**
  ```text
  The jail's keeper cannot see this session, so it may end the jail under it once the sessions it counts have left.
  ```
- **After:**
  ```text
  yolo could not register this session. The jail may stop while you are still in it, as soon as the other sessions quit.
  ```
- **Why:** 6, 7.

## Launch refusals

### yolo cannot find its build files
- **Where:** `internal/cli/run/run.go:275` (default reason) and `:279`; `internal/cli/run/run.go:314` and `:318` (macOS sandbox wording); `internal/cli/check/nextsteps.go:41` (`yolo check`). A launch prints it when yolo was installed without the Nix recipe it builds jails from.
- **Before:**
  ```text
  Cannot find yolo-jail repo root.
  No complete executable-relative yolo-jail bundle was found. Build one with `just build-dev-bundle` and invoke its printed launcher path, or run `just install`.
  ```
- **After:**
  ```text
  yolo cannot find its build files (the Nix recipe it builds each jail from).
  Reinstall yolo from Homebrew or a release archive, which include them. If you build yolo from source, run `just install`, or run `just build-dev-bundle` and use the launcher path it prints.
  ```
- **Why:** 1 ("repo root", "executable-relative bundle"), 6. The original names only developer commands; see "Wrong today".

### Launch from the live workspace inside a jail
- **Where:** `internal/cli/run/run.go:74`. It prints when an agent inside a jail runs `yolo` in `/workspace`.
- **Before:**
  ```text
  Refusing to launch: the workspace is /workspace and this yolo is already inside a jail — /workspace is the LIVE bind of the running session's own workspace, whose .yolo overlay IS that session's home; a fresh launch here regenerates its agent config under it.
  Run nested launches from a throwaway workspace (AGENTS.md, Nested-jail verification): mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested && yolo … — any directory but /workspace. Set YOLO_ALLOW_LIVE_WORKSPACE=1 if you truly mean it.
  ```
- **After:**
  ```text
  Refusing to launch: you are already inside a jail, and /workspace is this jail's own workspace. A new jail here would overwrite this session's agent settings.
  Launch from any other directory, for example: mkdir -p /tmp/yolo-nested && cd /tmp/yolo-nested && yolo
  To launch here anyway, set YOLO_ALLOW_LIVE_WORKSPACE=1.
  ```
- **Why:** 1 (AGENTS.md), 7, 8 ("LIVE bind", ".yolo overlay").

### --update on the macOS sandbox
- **Where:** `internal/cli/run/run.go:250`. It prints when a user passes `--update` with the macos-user setup.
- **Before:**
  ```text
  Refusing --update on macos-user: its program updater has no supported fresh-launch route. Use a supported container runtime for this update, or launch without --update.
  ```
- **After:**
  ```text
  Refusing --update: the macOS sandbox setup cannot update programs during a launch. Launch without --update, or run this update with a container setup (`--setup podman-self`).
  ```
- **Why:** 5 ("fresh-launch route"), 6 (macos-user).

### Resource group selected
- **Where:** `internal/cli/run/resourcegroups.go:80`–`:81` (jail launch) and `:117`–`:118` (`yolo host`). Every launch whose config puts the workspace in a resource group prints it.
- **Before:**
  ```text
  Resource group %q selected via %s, but placement is unsupported/unknown; this launch was not started.
  Run `yolo check --no-build` to inspect the selection and supported placement, or remove the mapping with `yolo config unset --user resource_groups`.
  ```
- **After:**
  ```text
  Refusing to launch: your config puts this workspace in resource group %q (set by %s), and yolo cannot place a jail in a resource group yet.
  Remove the setting with `yolo config unset --user resource_groups`, then launch again.
  ```
- **Why:** 2, 5 ("unsupported/unknown"). See "Wrong today" about "supported placement".

### Resource group cannot be resolved
- **Where:** `internal/cli/run/resourcegroups.go:54`–`:55`, `:90`–`:91`, `:110`–`:111`. It prints when the `resource_groups` setting cannot be read.
- **Before:**
  ```text
  Cannot resolve resource-group selection: %s
  Fix the owner-scope `resource_groups` mapping, then run `yolo check --no-build` before retrying.
  ```
- **After:**
  ```text
  Refusing to launch: yolo cannot read the `resource_groups` setting in your user config: %s
  Fix that setting, run `yolo check --no-build`, then launch again.
  ```
- **Why:** 5, 6 ("owner-scope").

### Resource group I/O plan lines
- **Where:** `internal/cli/run/resourcegroups.go:63`, `:65`, `:68`, `:75`, `:77`. They print just before the resource-group refusal.
- **Before:**
  ```text
  Resource-group I/O policy: unknown/unavailable; the selected group supplied no device identities and no host-wide device scan was performed.
  Read-only I/O child plan (not applied or enforced):
  ```
- **After:**
  ```text
  yolo cannot work out the disk limits for this resource group: the group names no disks.
  yolo would set these disk limits, but does not apply them yet:
  ```
- **Why:** 3, 5.

### Previous jail still shutting down
- **Where:** `internal/cli/run/keeperspawn.go:304` (container), `:1145` (macOS sandbox). It prints when the previous jail in this workspace has not finished shutting down in time.
- **Before:**
  ```text
  Refusing to launch: the previous jail's keeper is still running.
  ```
- **After:**
  ```text
  Refusing to launch: the previous jail in this workspace is still shutting down.
  ```
- **Why:** 6. The line just above it (P1's rewrite) gives the `kill` command.

### Background process died before starting
- **Where:** `internal/cli/run/run.go:2748`. It prints when the background process exits before it reports that it started.
- **Before:**
  ```text
  This jail's keeper ended (status %d) before it started; its log: %s
  ```
- **After:**
  ```text
  The jail did not start: its background yolo process exited with status %d. Its log: %s
  ```
- **Why:** 6.

### Unreadable roster from another yolo version
- **Where:** `internal/cli/run/keeperspawn.go:1193`. It prints on the macOS sandbox when another yolo version holds the workspace's host services.
- **Before:**
  ```text
  Refusing to launch: this workspace's macos-user host services are held by a keeper (pid %d) of yolo %s, whose roster (contract %d) this yolo (%s, contract %d) cannot read; %s.
  ```
- **After:**
  ```text
  Refusing to launch: a different yolo version (%s, pid %d) is running this workspace's host services, and this yolo (%s) cannot work with it. %s.
  ```
  (Keep the contract numbers in the launch log, not on the terminal.)
- **Why:** 1, 6 (keeper, roster, contract).

### Host service settings missing a gate approval
- **Where:** `internal/cli/run/brokeredscope.go:208`. It prints when a call broker has no approved scope.
- **Before:**
  ```text
  %s: no gate-approved call-broker scope; the host daemon will not start
  ```
- **After:**
  ```text
  %s will not start, because you have not approved what it may reach. Launch again and approve the config change when yolo asks.
  ```
- **Why:** 5, 6 ("gate", "call-broker scope"), plus the next step was missing.

### Host service could not confirm its final save
- **Where:** `internal/cli/run/loopholesruntime.go:822`. It prints at shutdown when a host service cannot confirm that it finished.
- **Before:**
  ```text
  Host service '%s' final harvest was not confirmed (%v). Inspect its private host-service log and repair the workspace store or lock before relaunching.
  ```
- **After:**
  ```text
  The host service '%s' did not shut down cleanly (%v). Check its log, ~/.local/share/yolo-jail/logs/host-service-%s.log, before you launch again.
  ```
  The `%v` comes from `loopholesruntime.go:1863`, `:1871` and `:1874`. Rewrite those too:
  ```text
  daemon exited before final harvest                      ->  it stopped before yolo asked it to
  daemon exited unsuccessfully; inspect its private log   ->  it exited with an error
  daemon did not finish within %s; its private input remains until its exit is observed  ->  it did not stop within %s
  ```
- **Why:** 1 ("private host-service log", "workspace store or lock"), 5 ("final harvest"), 8. Verified path: `loopholesruntime.go:1462`–`:1466` builds it as `paths.GlobalStorage()/logs/host-service-<name>.log`, which is `~/.local/share/yolo-jail/logs/` on the host. The code should print the path it built, not a fixed `~` spelling.

### Attach refused for a changed environment
- **Where:** `internal/cli/run/contracttags.go:490`. It prints when `YOLO_ALLOW_ATTACH_SKEW=1` lets an attach go ahead anyway.
- **Before:**
  ```text
  This entry delivers nothing: its provider/profile channel is withheld whole%s, and the jail keeps the environment its last entry gave it. %s, then a launch, ends the difference.
  ```
- **After:**
  ```text
  This session gets none of its provider or profile settings%s. The jail keeps the settings it started with. To use the new settings, run %s, then launch again.
  ```
- **Why:** 4 ("entry", "a launch"), 5 ("provider/profile channel"), 6.

### Attach refused after the user declines a restart
- **Where:** `internal/cli/run/contracttags.go:415`–`:417`.
- **Before:**
  ```text
  Refusing to attach: the jail was not restarted.
  %s once its sessions are done, then launch again; or attach without what it cannot receive: %s=1.
  ```
- **After:**
  ```text
  Refusing to attach: the running jail cannot take this launch's settings, and you chose not to restart it.
  When its sessions are done, run %s and launch again. To attach anyway without the new settings, set %s=1.
  ```
- **Why:** 3, 7.

### Background process refused to start (old process still running)
- **Where:** `internal/cli/run/keeper.go:382`.
- **Before:**
  ```text
  Refusing to start this jail's keeper: %v. The keeper of the previous jail named %s is still running; %s, then a launch, ends it.
  ```
- **After:**
  ```text
  Refusing to start the jail: the previous jail named %s is still shutting down (%v). Run %s, then launch again.
  ```
- **Why:** 4 ("a launch"), 6.

## Launch notices and warnings

### Nested launch drops published ports
- **Where:** `internal/cli/run/assemble.go:354`. It prints when a jail is launched inside another jail with `network.ports` or `network.forward_host_ports` set.
- **Before:**
  ```text
  Warning: nested launch — podman-in-podman is forced onto this container's network namespace (--net=host; netavark cannot create one without NET_ADMIN), so network.ports are NOT applied — the jail shares this process's network stack, so a port it binds is already reachable on the same localhost, with no forwarding hop to publish. Launch from the host for published ports.
  ```
- **After:**
  ```text
  Warning: a jail inside a jail shares the outer jail's network, so yolo does not apply network.ports. A port the inner jail opens is already reachable on localhost here. To publish ports, launch from the host.
  ```
- **Why:** 2, 7, 8.

### Apple Container pack staging failed
- **Where:** `internal/cli/run/assemble.go:972`.
- **Before:**
  ```text
  Warning: could not stage the pack trees for Apple Container (%s) — NO pack will render in this jail: no pack-declared program will install and no pack launch flag will apply, though shims and briefings still will, so the jail will LOOK provisioned. The source is %s and the destination is %s; a source that has gone missing means a concurrent launch's housekeeping reaped it (re-run), and a destination that cannot be written usually means a full or read-only disk.
  ```
- **After:**
  ```text
  Warning: yolo could not copy your packs into this jail (%s). No pack program will install, so the jail may look ready when it is not.
  If %s is missing, launch again. If yolo cannot write %s, check that the disk is not full or read-only.
  ```
- **Why:** 2, 6 ("render"), 7.

### Apple Container cannot hide files
- **Where:** `internal/cli/run/assemble.go:862`.
- **Before:**
  ```text
  Not shadowing %s: on Apple Container a /dev/null bind arrives with the wrong device node, so reading it fails instead of returning empty. The agent will see the workspace's real path. Launch with `--setup podman-self` to shadow it.
  ```
- **After:**
  ```text
  The agent can see %s: Apple Container cannot hide workspace files. To hide them, launch with `--setup podman-self`.
  ```
- **Why:** 5 ("shadowing"), 8.

### Host nvim config on the macOS sandbox
- **Where:** `internal/cli/run/loopholeinert.go:487`.
- **Before:**
  ```text
  Host nvim config (~/.config/nvim) is not delivered on macos-user: the sandbox's nvim starts with its own. Where host editor config goes is an open design question (OQ-ED2), so there is nothing to change until it is ruled.
  ```
- **After:**
  ```text
  The macOS sandbox does not use your nvim config (~/.config/nvim), so nvim starts with its default settings there.
  ```
- **Why:** 6 (the design id), 8.

### Credential scope on the macOS sandbox
- **Where:** `internal/cli/run/credentialnotes.go:105`.
- **Before:**
  ```text
  Credential scope on macos-user: this invocation starts claude, whose own scoped values ride the sandbox session; codex started inside it read theirs from their own env files (providers.md OQ-CN9).
  ```
- **After:**
  ```text
  This launch starts claude with its credentials. If you start codex inside the sandbox, it reads its credentials from its own settings file.
  ```
- **Why:** 3, 5 ("Credential scope"), 6 (the design id), 8 ("ride the sandbox session").

### Duplicate service name across packs
- **Where:** `internal/cli/run/packservices.go:333`.
- **Before:**
  ```text
  Service %q: pack %s's declaration is shadowed by pack %s's, the later in the pack order, so this launch uses only pack %s's (its jail daemon and its endpoint). Rename one service to keep both; `yolo pack footprint` reports the pair (notch-convergence NC-D59).
  ```
- **After:**
  ```text
  Packs %s and %s both declare a service named %q. This launch uses only pack %s's, because it comes later in your pack list. To keep both, rename one of the services. `yolo pack footprint` shows the pair.
  ```
- **Why:** 6 (NC-D59), 7.

### Profile daemon missing on attach
- **Where:** `internal/cli/run/profiledaemons.go:55`. It appears in the attach refusal when a profile needs a service the jail never started.
- **Before:**
  ```text
  Its launch selected no profile that daemon serves, so it was not started (providers.md OQ-CN7), and an attach starts no daemon: the pointer this entry would hand an agent would point at nothing.
  ```
- **After:**
  ```text
  The jail started without this service, because no profile chosen at launch needed it. An attach cannot start a service, so the agent would have nothing to connect to.
  ```
- **Why:** 6 (the design id), 8 ("pointer this entry would hand").

### Grant summary line beyond the grant
- **Where:** `internal/cli/run/jailgrant.go:405`.
- **Before:**
  ```text
    and beyond the grant: %s's %s profile, selected by this entry, delivers %s into %s's own env file, which every process of this jail can read. An attach's profile brings its provider's key, as ruled (credential-sources-separation.md, OQ-ES5 (attach -p))
  ```
- **After:**
  ```text
    Also: the %[2]s profile you chose for %[1]s puts %[3]s into %[4]s's settings file, which every process in this jail can read.
  ```
- **Why:** 6 (the design id, "as ruled"), 8.

### Sealed build withheld line
- **Where:** `internal/cli/run/seal.go:330` (reasons at `:315`, `:328`).
- **Before:**
  ```text
  Sealed build: 2 env claims declared by pack x are withheld, as are 3 of your mise_tools (FP-D9: a build jail gets no credential and no host file; FP-D19: a build line fetches any tool it needs)
  ```
- **After:**
  ```text
  This build runs sealed, so yolo withholds 2 env settings from pack x and 3 of your mise_tools. A build gets no credentials or host files, and it fetches any tool it needs itself.
  ```
- **Why:** 6 (FP-D9, FP-D19), 7.

### Claude credential view notices
- **Where:** `internal/cli/run/claudecredentialview.go:110`, `:114`, `:131`, `:134`. They print on the macOS sandbox when Claude's login setup fails or has no login yet.
- **Before:**
  ```text
  Claude: this machine has no Claude login yet; account-mode /login uses the scoped security adapter's fast Keychain-write refusal and Claude's file fallback.
  ```
- **After:**
  ```text
  Claude: this machine has no Claude login yet. Run /login in the sandbox to sign in.
  ```
  For `:114`: "Warning: yolo could not set up Claude's login for this workspace (%s). Run `yolo claude-auth status` to see why, then launch again."
- **Why:** 8 ("scoped security adapter's fast Keychain-write refusal", "session TMPDIR"), 6 ("account-mode", "self-mode").

### Migration lines that let projects ask
- **Where:** `internal/cli/run/migrateoffer.go:349`, `:362`. They print when a config migration cannot take back lines it added.
- **Before:**
  ```text
  %s changed after the conversion wrote its ceiling lines, so they were left in place:
  ```
- **After:**
  ```text
  %s changed after the conversion added these lines, so yolo left them in place. They let projects ask you for these permissions:
  ```
- **Why:** 6 ("ceiling").

### Session start failed
- **Where:** `internal/cli/run/run.go:2818` (called from `:2791` and `:3530`); `internal/cli/run/keeper.go:526`. It prints when yolo cannot start the session process.
- **Before:**
  ```text
  Configured runtime '%s' not found on PATH.
  Run `yolo check` to validate runtime availability before restarting.
  ```
- **After:**
  ```text
  yolo could not start the jail's session with %s: %v
  Run `yolo check` to see whether %s is installed and working, then launch again.
  ```
- **Why:** The original is often false (see "Wrong today"). The rewrite needs the error passed in.

## yolo check

### Accept config changes inside a jail
- **Where:** `internal/cli/check/check.go:776`.
- **Before:**
  ```text
  [WARN] --accept-config-changes is host-only (disabled inside running jail)
  ```
- **After:**
  ```text
  [WARN] --accept-config-changes works only on the host, not inside a jail
  ```
- **Why:** 3, 5.

### Host service the launch will refuse
- **Where:** `internal/cli/check/sections_loopholes.go:335`.
- **Before:**
  ```text
  [FAIL] loophole %s: the launch will refuse its doorway
  -> The next launch refuses: the %q doorway forwards to this host service, which the placement rule refuses to start, so no backend opens the doorway (docs/design/host-notch-services.md#HS-D34). Move %s's files out of that tree, then run `yolo check --no-build` again.
  ```
- **After:**
  ```text
  [FAIL] %s: the next launch will refuse to start this host service
  -> yolo will not start the %q host service from where its files are now (the lines above name the folder). Move pack %s's files out of that folder, then run `yolo check --no-build` again.
  ```
- **Why:** 6 (doorway, placement rule, notch, HS-D34), 7.

### Settings validator not run (yolo bug)
- **Where:** `internal/cli/check/sections_loopholes.go:176`.
- **Before:**
  ```text
  [WARN] loophole %s: settings validator not run
  -> A pack-shipped validator is host execution, and nothing vouched for this pack's module (a yolo bug, not a config problem). Report it at %s with the output of `yolo --version`.
  ```
- **After:**
  ```text
  [WARN] %s: yolo did not check this service's settings
  -> This is a yolo bug, not a problem with your config. Report it at %s with the output of `yolo --version`.
  ```
- **Why:** 2, 8 ("nothing vouched for this pack's module").

### Broker endpoint invisible in the jail
- **Where:** `internal/cli/check/sections_loopholes.go:536`.
- **Before:**
  ```text
  [FAIL] %s: broker ok on host, endpoint invisible in-jail
  -> The host-services dir was recreated after the container mounted it (host /tmp cleanup or a teardown/startup race): the jail's bind mount still points at the old, deleted directory, so in-jail auth requests 502 even though the host-side front answers.  That directory now holds this jail's CREDENTIAL, so re-reading cannot recover it either.  Relaunch the jail to remount the directory.
  ```
- **After:**
  ```text
  [FAIL] %s: the service runs on the host, but the jail cannot reach it
  -> Something removed and recreated the folder the jail uses to reach host services, so sign-in requests from the jail fail. Stop the jail with `yolo stop`, then launch it again.
  ```
- **Why:** 2, 5, 6 (front), 8.

### Broker front problems
- **Where:** `internal/cli/check/sections_loopholes.go:498`, `:505`, `:519`, `:526`, `:549`.
- **Before:**
  ```text
  [FAIL] %s: front up, broker unreachable
  -> The front authenticated and accepted, but this jail's broker did not answer the spliced ping.  `yolo broker logs` shows why; leave the jail and launch it again, which starts a fresh broker.
  ```
- **After:**
  ```text
  [FAIL] %s: the sign-in service accepted the connection but did not answer
  -> `yolo broker logs` shows why. Leave the jail and launch it again to start a fresh sign-in service.
  ```
  Apply the same treatment to `:519` ("broker front rejected this jail's token" → "the sign-in service rejected this jail's token") and `:526` ("broker endpoint dead" → "the sign-in service has stopped").
- **Why:** 5, 6 (front), 8 ("spliced ping").

### Packages inert at a notch
- **Where:** `internal/cli/check/section_packageprofile.go:130`.
- **Before:**
  ```text
  [WARN] `packages:` declares 3 entries that nothing materializes at the `guest` notch
  -> A notch with no baked image gets its tools from a nix profile, and only the macos-user backend builds one today, which is also the guest notch on macOS — a Linux `guest` has no package layer yet (docs/design/provisioner-evidence.md §3.4).  Nothing to run: the entries are inert here, not wrong.
  ```
- **After:**
  ```text
  [WARN] `packages:` lists 3 entries, and this setup does not install them
  -> This setup has no way to install packages yet. The entries do no harm here; you do not need to change anything.
  ```
- **Why:** 6 (notch, materializes), 8.

### Packages inert on the host
- **Where:** `internal/cli/check/section_packageprofile.go:163`.
- **Before:**
  ```text
  `packages:` declares 3 entries, inert at the `host` notch by design: `yolo host` never applies the workspace's jail-only keys (HP-DIR3, docs/reference/host-agent-environment.md)
  ```
- **After:**
  ```text
  `packages:` lists 3 entries, which `yolo host` ignores: it never applies settings meant only for jails.
  ```
- **Why:** 6 (notch, HP-DIR3).

### Nix automatic garbage collection is off
- **Where:** `internal/cli/check/section_autogc.go:75`.
- **Before:**
  ```text
  [WARN] nix min-free = 0 — the daemon's automatic GC is OFF, so the store grows unbounded
  -> Set a min-free/max-free floor so the daemon reclaims UNROOTED store paths automatically under space pressure. With the running image now GC-rooted (storage §1) this is safe — a rooted closure is never a casualty. Add to …
  ```
- **After:**
  ```text
  [WARN] nix never cleans up its store automatically (min-free = 0), so the store keeps growing
  -> Set min-free and max-free so nix frees space when the disk runs low. This never removes the image a running jail uses. Add to …
  ```
- **Why:** 3, 6 (the storage section number), 8.

### Resource group finding
- **Where:** `internal/cli/check/resourcegroups.go:74`.
- **Before:**
  ```text
  [FAIL] Resource group selected: %s via %s; placement unsupported/unknown
  -> This build cannot verify host resource-group placement or policy. Remove the mapping with `yolo config unset --user resource_groups` or wait for a supported placement adapter; do not treat selection as enforcement.
  ```
- **After:**
  ```text
  [FAIL] Your config puts this workspace in resource group %s (set by %s), and yolo cannot place a jail in a resource group yet
  -> Every launch will refuse until you remove the setting: `yolo config unset --user resource_groups`.
  ```
- **Why:** 5 ("unsupported/unknown", "placement adapter"), 8.

### Model list contributes nothing
- **Where:** `internal/cli/check/modellists.go:77`.
- **Before:**
  ```text
  [WARN] Model list: %s's supplied list for provider %q contributes no callable model (%s)
  -> Profile %q governs %s here; the program is declared by pack %q. %s. %s. This list-only warning does not say the program lacks its own default or that a literal profile model cannot run
  ```
- **After:**
  ```text
  [WARN] Model list: the list for %s and provider %q has no model it can call (%s)
  -> Profile %q sets up %s, and pack %q installs it. %s. %s.
  ```
- **Why:** 6 ("contribution"), 7, 8 (the last sentence explains what the warning does not mean).

### Model menu not narrowed
- **Where:** `internal/cli/check/modellists.go:118`.
- **Before:**
  ```text
  [WARN] Model list: yolo does not narrow %s's menu for provider %q to its list, because profile %q turns enforce_models off
  -> pack %s says %s's menu can show exactly a list only through a filter that also refuses every model off it, so yolo writes that filter only while the profile's enforce_models is on. With it off, …
  ```
- **After:**
  ```text
  [WARN] Model list: %s's menu for provider %q can show models outside the list, because profile %q turns enforce_models off
  -> With enforce_models off, %s offers every model in its own catalog for that provider and runs whichever you pick. To limit the menu to the list, remove "enforce_models": false from profile %q. That also refuses models that are not on the list.
  ```
- **Why:** 2, 7, 8.

### PATH that `yolo host` searches
- **Where:** `internal/cli/check/section_hostlaunchpath.go:44`.
- **Before:**
  ```text
  - where `yolo host` looks for the programs your packs need and for a command yolo keeps no copy of: read here from the PATH this check was started with, then host_path's folders. A launcher with another PATH (a Waybar button, cron, a hotkey launcher) searches its own
  ```
- **After:**
  ```text
  - `yolo host` looks for your packs' programs on your PATH first, then in the folders listed in host_path. This check uses the PATH it was started with. A launcher with a different PATH (a Waybar button, cron, a hotkey tool) searches that PATH instead.
  ```
- **Why:** 3, 7.

### Package built from source on a Mac
- **Where:** `internal/cli/check/sections_nix.go:138`.
- **Before:**
  ```text
  [WARN] A package must be built from source (%s) — `yolo` will offload it to a container builder
  -> `yolo check` can't run that Linux build locally, but a normal `yolo` run handles it automatically: it offloads the build to an on-demand container builder on your active runtime (podman/Apple Container), then tears it down.  Just make sure the runtime is up (`podman machine start` or `container system start`) and run `yolo`.
  ```
- **After:**
  ```text
  [WARN] A package must be built from source (%s), and yolo check cannot build it here
  -> A normal `yolo` launch builds it in a temporary container. Start your container runtime (`podman machine start` or `container system start`), then run `yolo`.
  ```
- **Why:** 2, 8.

### Podman Machine memory warning
- **Where:** `internal/cli/check/sections_gpu.go:98`.
- **Before:**
  ```text
  [WARN] Podman Machine '%s' memory: %d MB (below %d MB recommended floor)
  ```
- **After:**
  ```text
  [WARN] Podman Machine '%s' has %d MB of memory, less than the %d MB yolo recommends
  ```
- **Why:** 3, 5.

### Interrupted floor installs
- **Where:** `internal/cli/check/section_hostfloor.go:143`.
- **Before:**
  ```text
  [WARN] 2 interrupted install(s) left in the floor
  ```
- **After:**
  ```text
  [WARN] 2 interrupted installs left files in yolo's install folder
  ```
- **Why:** 6 (floor; see P9).

## Launch errors from packs, home setup and inherited config

These come from the second pass over `packfiles.go`, `inheritscope.go`, `hostloopback.go`, `macosctxtree.go`, `homeskeleton.go`, `ctxmountrecord.go`, `shareddirsources.go` and `liveapplyplan.go`. Each was traced to the line that prints it. P10 and P11 above cover the file conflicts and the home-folder errors.

### A pack's files vanished during the launch
- **Where:** `internal/cli/run/packfiles.go:667`, printed at `:625` as a yellow warning.
- **Before:**
  ```text
  Warning: pack %s's staged tree is GONE, so its `files` claim on %s cannot be delivered: nothing at %s. yolo staged that directory earlier in this launch, so this is not a problem with the pack's `from` or its filters — something removed it since. Expect the container to fail on the same path (`statfs …: no such file or directory`); re-run the launch.
  ```
- **After:**
  ```text
  Warning: the files of pack %s disappeared during this launch, so yolo cannot put them at %s (nothing is at %s now). The jail will probably fail to start with "no such file or directory". Launch again.
  ```
- **Why:** 2, 5 ("staged tree", "`files` claim"), 7, 8.

### A pack's files are missing from the pack
- **Where:** `internal/cli/run/packfiles.go:673`, printed at `:625`.
- **Before:**
  ```text
  Warning: pack %s declares a `files` tree that is not in its staged content, skipping: %s (`from` is "%s", and nothing staged at %s — check that path in the pack, and any only/exclude filters; `yolo pack lint <pack-dir>` reports this without a launch)
  ```
- **After:**
  ```text
  Warning: pack %s lists files for %s that are not in the pack, so the jail starts without them. The pack looks for them at "%s". If you maintain the pack, run `yolo pack lint <pack-dir>` to find the problem.
  ```
- **Why:** 2, 5 ("staged content"), 7. The sentence that puts the step last and in parentheses hides it.

### A pack's read-only files cover another pack's settings file
- **Where:** `internal/cli/run/packfiles.go:1229`. The launch refuses with it at `internal/cli/run/packs.go:293` (each line prefixed "packs: "), and `yolo check` fails with it at `internal/cli/check/packs.go:415`.
- **Before:**
  ```text
  pack %s claims ~/%s as a `files` tree, which is mounted READ-ONLY — but pack %s renders the config surface %s at ~/%s, inside it. The jail would refuse to start (read-only file system) with an error naming the surface, not this claim. Narrow the `files` into a subdirectory the agent does not write.
  ```
- **After:**
  ```text
  Pack %s puts a read-only folder at ~/%s, and pack %s needs to write its settings file (%s) at ~/%s, inside that folder. yolo stops here because the agent could not write the file. Remove one of the packs with `yolo pack remove <name>`. If you maintain pack %s, have it put its files in a folder the agent does not write.
  ```
- **Why:** 1, 6 ("claims", "config surface"), 8 (what the error would have said), plus the step was for the pack's author only.

### Config keys yolo did not classify
- **Where:** `internal/cli/run/inheritscope.go:209`, a yellow warning on every launch that has such a key.
- **Before:**
  ```text
  Warning: config keys with no inherit classification were dropped from the jail's user scope: %s — classify them in internal/config/inherit.go
  ```
- **After:**
  ```text
  Warning: yolo left these user config keys out of the jail, because it has no rule for whether they belong there: %s. This is a bug in yolo; please report it with this line.
  ```
- **Why:** 1, 6 ("user scope"), and the step asked the user to edit yolo's source (pattern P4).

### Jail's copy of the user config written as JSONC
- **Where:** `internal/cli/run/inheritscope.go:216`.
- **Before:**
  ```text
  Warning: inherited %s scope uses generated JSONC because %s. No user-owned sibling file was changed.
  ```
- **After:**
  ```text
  Warning: yolo wrote the jail's copy of your %s config as JSONC because %s. Your own config files were not changed.
  ```
- **Why:** 5 ("inherited %s scope", "user-owned sibling file").

### User config could not be copied into the jail
- **Where:** `internal/cli/run/inheritscope.go:495` (containers; the launch goes on) and `internal/cli/run/run.go:1194` (macos-user; the launch stops and prints the error bare in bold red).
- **Before:**
  ```text
  Warning: could not render this jail's inherited user config (%s)
  ```
- **After:**
  ```text
  Warning: yolo could not copy your user config into this jail (%s). The jail starts without your user-level settings.
  ```
  On macos-user the same failure stops the launch, so the bare error there needs a first sentence of its own: "yolo could not copy your user config into the macOS sandbox, so it stopped." The inner errors below then give the step.
- **Why:** 5 ("inherited user config"), 8, plus the consequence was missing.

### Jail-home destination for the user config is broken
- **Where:** `internal/cli/run/inheritscope.go:267` and `:283`.
- **Before:**
  ```text
  cannot establish inherited %s user-config destination %s: %w. Restore this jail-home path as a real directory tree, preserving its data, then relaunch
  ```
- **After:**
  ```text
  yolo could not create %s in the jail's home folder for your %s config (%w). Make that path a normal folder, not a file or a link, and keep anything inside it. Then launch again.
  ```
- **Why:** 4 ("destination"), 5, 7.

### Two config files in the jail's home
- **Where:** `internal/cli/run/inheritscope.go:271`, `:286`, `:297`.
- **Before:**
  ```text
  refusing inherited %s user config: legacy files %s and TOML %s exist in the jail's writable home; preserve both families, move one aside only after review, then relaunch
  ```
- **After:**
  ```text
  The jail's home folder has an old config file (%s) and a TOML one (%s), and yolo will not guess which to use. Compare them, move the one you do not want somewhere else, then launch again.
  ```
  `:271` ("existing legacy JSON file %s would remain beside generated JSONC; … arrange for one unambiguous inherited file") and `:297` ("its legacy JSONC payload needs %s, but the jail already has %s") take the same shape with their own two file names.
- **Why:** 2, 5 ("both families", "writable home", "legacy JSONC payload"), 7.

### Internal mapping failures in the inherited config
- **Where:** `internal/cli/run/inheritscope.go:379`, `:384`, `:388`, `:393`, `:406`, `:417`, `:421`, `:448`, `:457`, `:506`, `:904`, `:922`, `:1095`. These can happen only when yolo's own map of home folders is wrong.
- **Before (example):**
  ```text
  no proven home bind covers %s; preserve user files and retry after the destination mapping is restored
  ```
- **After:**
  ```text
  yolo could not work out where %s is inside the jail. This is a bug in yolo; please report it with this line. Your files were not changed.
  ```
- **Why:** 1 ("home bind", "destination mapping"), and "retry after the mapping is restored" names nothing the user can do (pattern P4).

### Jail's copy of the user config could not be written
- **Where:** `internal/cli/run/inheritscope.go:561`.
- **Before:**
  ```text
  Warning: could not stage the %s user config at %s (%s) — this jail gets no %s user scope
  ```
- **After:**
  ```text
  Warning: yolo could not write the jail's copy of your %s config to %s (%s). This jail starts without your %s settings.
  ```
- **Why:** 5 ("user scope"), 6.

### Host-loopback warnings name internal services and give no step
- **Where:** `internal/cli/run/hostloopback.go:430` (opt-out set), `:491` (explicit network mode), `:632` (pasta too old), `:662` (rootful podman), `:776` (slirp4netns unconfirmed). They print at `internal/cli/run/assemble.go:670`.
- **Before (:430):**
  ```text
  Warning: YOLO_NO_HOST_LOOPBACK is set — NOT requesting host-loopback forwarding (%s).
    Jail-facing services (Claude OAuth broker, yolo-ps, yolo-journalctl) will be
    unreachable from inside this jail. Unset it to restore the fix.
    docs/reference/loopback-tls-reachability.md
  ```
- **After (:430):**
  ```text
  Warning: YOLO_NO_HOST_LOOPBACK is set, so yolo did not connect the jail to services on the host.
    Host services yolo provides, such as Claude login refresh, will not work in this jail.
    To turn them back on, unset YOLO_NO_HOST_LOOPBACK and launch again.
  ```
- **Before (:491):**
  ```text
  Warning: network.mode is set to '%s', so yolo is not requesting host-loopback forwarding for it.
    On the default 'bridge' mode yolo asks %s to forward the host's
    loopback into the jail; with an explicit mode that is your call to make, and
    without it jail-facing services (Claude OAuth broker, yolo-ps,
    yolo-journalctl) may be unreachable from inside the jail.
  ```
- **After (:491):**
  ```text
  Warning: network.mode is set to '%s', so yolo did not connect the jail to services on the host.
    Host services yolo provides, such as Claude login refresh, may not work in this jail.
    To let yolo connect them, run `yolo config unset network.mode`.
  ```
  (When the mode came from `--network`, the step is "launch without `--network`".)
- **Before (:662, abridged):**
  ```text
  Warning: this host's podman is ROOTFUL, and a rootful bridge does not forward the host's LOOPBACK into a jail — its gateway reaches the host, but only for a listener bound globally, and yolo's daemons bind 127.0.0.1 by construction. … Launching either way — nothing else changes.
  ```
- **After (:662):**
  ```text
  Warning: podman on this host runs as root, so yolo cannot connect the jail to services on the host.
    Host services yolo provides, such as Claude login refresh, will not work in this jail.
    To fix it, run podman as your own user, or set network.mode to "host" (this removes the jail's network isolation).
  ```
- **After (:632, pasta):** "Warning: this host's pasta cannot connect the jail to services on the host, so Claude login refresh and other yolo host services will not work. Upgrade passt to %s or newer, then check with `pasta --version`."
- **Why:** 1 ("Claude OAuth broker", "yolo-ps", "yolo-journalctl", "loopback", "gateway", "daemons"), 2, 7, 8. "Restore the fix" names no fix. ":491" had no next step. "Launching without it — nothing else changes" reads as headline shorthand (rule 5). Pattern P12 covers the doc path at the end of each. The two cyan notices, `:700` and `:722`, are fine apart from the service list and the doc path.

### Sandbox copy refusals on macos-user
- **Where:** `internal/cli/run/macosctxtree.go:562` (the context-mount refusal, printed at `:549`).
- **Before:**
  ```text
  Refusing the macos-user launch: it can't deliver context mount.
  ```
- **After:**
  ```text
  yolo cannot start the macOS sandbox, because it cannot give the sandbox this folder from `mounts`:
  ```
  With more than one, "these %d folders from `mounts`:".
- **Why:** 3 (the noun is missing its article when there is one mount), 6 ("context mount"). The rest of the refusal, and the copy errors at `:850`, `:861`, `:867` and `:877`, already give the user a step.

### Live-mount record could not be saved
- **Where:** `internal/cli/run/run.go:2393` and `:2431`. The inner errors come from `internal/cli/run/ctxmountrecord.go:87`, `:90`, `:93`.
- **Before:**
  ```text
  Warning: live context mounts are off for this jail: could not write this jail's live-mounts record (%s): %w. A `mounts` change will need `yolo stop`, then `yolo`.
  ```
- **After:**
  ```text
  Warning: yolo could not save its list of the folders this jail mounts (%s). The jail starts, but a later change to `mounts` takes effect only after `yolo stop`, then `yolo`.
  ```
  Inner errors: "could not make the directory for this jail's live-mounts record (%s)" → "could not create %s"; "could not narrow %s to its owner" → "could not make %s private to you".
- **Why:** 5 ("live context mounts", "live-mounts record"), 7.

### Jail notes could not be saved
- **Where:** `internal/cli/run/run.go:2435`.
- **Before:**
  ```text
  Warning: applied guidance snapshot unavailable: %s
  ```
- **After:**
  ```text
  Warning: yolo could not save its notes about this jail (%s). The agent's notes will not be updated by a later attach or `yolo config apply`; `yolo stop`, then `yolo`, fixes it.
  ```
- **Why:** 3, 5 ("applied guidance snapshot"), plus the consequence and the step were missing. The consequence is from `run.go:3338`–`:3347` and `mechanicsrecord.go:108`–`:115`, which keep the old notes when the snapshot is missing.

### Pack shared folder errors
- **Where:** `internal/cli/run/shareddirsources.go:38` and `:43`, printed bare in bold red at `internal/cli/run/run.go:2260`.
- **Before:**
  ```text
  cannot create %s, the bind source of the machine-scope shared dir ~/%s: %w
  ```
- **After:**
  ```text
  yolo could not create %s, which holds ~/%s for every jail on this machine (%w). Check that the disk has space and that you own it, then launch again.
  ```
  `:38` ("a selected pack declares the machine-scope shared dir %q, which is not a path inside the machine store %s") → "Pack settings ask for a shared folder, %q, outside yolo's own storage, so yolo stopped. Remove that pack with `yolo pack remove <name>`, and tell its author." It should name the pack; today the message cannot, because `packload.SharedDirs` returns only the paths.
- **Why:** 1 ("bind source", "machine-scope", "machine store"), plus the step was missing.

### `yolo config apply` plan reasons
- **Where:** `internal/cli/run/liveapplyplan.go:68`, `:106`, `:125`, `:393`. They print after each planned change, following " — ".
- **Before:**
  ```text
  no change-timing row is available for this key
  the host invalidated this jail's mount record after a failed update; the jail must restart to restore known state
  ```
- **After:**
  ```text
  yolo does not know when this key takes effect, so it assumes the next restart
  an earlier update to this jail's folders failed, so the jail must restart before yolo knows what it has
  ```
- **Why:** 1 ("change-timing row"), 5 ("mount record", "restore known state").

## Text from other packages that the launch or `yolo check` prints

Each entry names the file that defines the text and the line that prints it.

### Active provider set
- **Where:** defined at `internal/packload/profiledisclosure.go:471`; printed on every launch at `internal/cli/run/run.go:1600` when an agent has more than one provider.
- **Before:**
  ```text
  Active set for %s: %s — every entry's provider is live in one session, and a fresh session starts on %s unless the agent keeps a pick of its own inside the set
  ```
- **After:**
  ```text
  %s can switch between these providers in one session: %s. A new session starts on %s, unless the agent remembers a different choice from that list.
  ```
- **Why:** 5 ("Active set", "entry's provider is live"), 6, 7.

### Update notice for go install, pipx and uv
- **Where:** defined at `internal/selfupdate/apply.go:196`; printed by `yolo check` at `internal/cli/check/section_updates.go:56`, whose step at `:54` repeats the same sentence ("Update the host binary and its separate jail source together.").
- **Before:**
  ```text
  ⬆ yolo-jail %s is available (this is %s) — update the host binary and its separate jail source together
  ```
- **After:** name the command for each install method. For example, for go install: "yolo-jail %s is available (you have %s). Update yolo and the jail files it ships with at the same time: <exact commands>." I could not find these commands in `internal/selfupdate`, so they need to come from the install guide before this ships.
- **Why:** 1 ("jail source"), plus the next step was missing.

### Retired loopholes folder
- **Where:** defined at `internal/loopholes/retired.go:96`; printed by `yolo check` at `internal/cli/check/sections_loopholes.go:42`.
- **Before (opening lines):**
  ```text
  the hand-placed loopholes directory is RETIRED and is no longer read:
  Still there and now INERT (no daemon, no mounts, no jail_env): %s
  Move each module into the conventional local pack, which is selected implicitly — no `packs` config line needed:
  ```
- **After:**
  ```text
  yolo no longer reads this folder:
    %s
  These host services in it do nothing now: %s.
  To keep using them, move each one into your local pack (yolo always loads it):
  ```
- **Why:** 6 ("loophole", "conventional local pack"), 1 ("jail_env", "module"). See also "Wrong today": it tells the user to write `pack.json`.

### Workspace loophole switch
- **Where:** defined at `internal/config/validate_loopholes.go:508`; printed by `yolo check` at `internal/cli/check/sections_loopholes.go:136`.
- **Before:**
  ```text
  config.loopholes.%s.enabled: true in %s cannot turn the loophole '%s' on: it runs whenever its pack is selected, and an off the user scope sets (`enabled: false`, or `off = true` under [allow.%s]) is the user's to lift, never a workspace file's. Remove the key.
  ```
- **After:**
  ```text
  loopholes.%s.enabled = true in %s does nothing. The host service '%s' runs whenever its pack is selected. Only your user config can turn it off, and only your user config can turn it back on. Remove the key.
  ```
- **Why:** 3 ("an off the user scope sets" has no verb for its subject), 4 ("an off"), 6 ("user scope"), 7.

### Session folder for host services could not be made
- **Where:** defined at `internal/cli/run/servicessession.go:268`; printed at `internal/cli/run/loopholesruntime.go:468`.
- **Before:**
  ```text
  Warning: could not create this session's host-services dir (%v); no host service starts for it, so the sandbox cannot reach any.
  ```
- **After:**
  ```text
  Warning: yolo could not create a folder for this session's host services (%v), so none of them start. Features such as Claude login refresh will not work in this session. Check that /tmp is writable, then launch again.
  ```
- **Why:** 5 ("host-services dir"), plus the step was missing. Confirm the folder's real parent before naming /tmp.

### Host service startup diagnostics
- **Where:** defined at `internal/cli/run/loopholesruntime.go:1193`; printed at `:1523`.
- **Before:**
  ```text
  Failed to prepare host service '%s' startup diagnostics: %s — the service was not started.
  ```
- **After:**
  ```text
  yolo could not create the log for host service '%s' (%s), so the service did not start.
  ```
  The rest ("Retry the launch; if this repeats, … `ulimit -n` …") is fine.
- **Why:** 5 ("startup diagnostics"). Check that a log is what "diagnostics" prepares.

### Podman Machine shared folders
- **Where:** defined at `internal/runtime/machineshares.go:330`; printed by `yolo check` at `internal/cli/check/sections_macos_platform.go:124`.
- The text is clear and gives exact commands. Only its last line breaks a rule: "See userguide/guides/macos.md — Podman on a Mac." (pattern P12).

### Checked and fine as written
`config.NoPacksMessage` and `config.NoPacksGuidance` (`internal/config/packs.go:58`–`:61`; printed at `run.go:1477`–`:1478` and `check/packs.go:161`); `config.InterruptedMigrationRefusal` (`internal/config/migrationjournal.go:55`); `run.LoopholeSettingsFixStep` (`internal/cli/run/loopholesettings.go:87`); `selfupdate.SnapshotSummary` and `SnapshotSteps` (`internal/selfupdate/apply.go:52`–`:53`); `run.openAIServiceRefusal` (`internal/cli/run/openaiauthbackend.go:25`); `attachPendingNotice` (`internal/cli/run/liveapply.go:596`); `configmigrate.RenamesHeading`, `RenamesClosing` and `LinkReport` (`internal/configmigrate/records.go:46`, `:70`; `graph.go:586`); `hostwrap.PathLine` (`internal/hostwrap/hostwrap.go:367`).

### Setup notices
- **Where:** defined in `internal/setupcensus/configkeys.go` (`Says`/`Then` at `:91`, `:130`, `:269`, `:300`, `:320`, `:335`) and the fallback at `internal/setupcensus/census.go:203`. They are printed at `internal/cli/run/assemble_parts.go:60` and `internal/cli/run/loopholeinert.go:457`, `:462`, `:466`.
- **Before:**
  ```text
  Warning: resources are NOT enforced on macos-user — macOS has no cgroups and there is no VM to size, so these are read and ignored; the agent runs with your user's own limits.
  Warning: `kvm` is not read on macos-user — it asks for /dev/kvm inside a container, and there is neither a container nor a /dev/kvm on macOS.
  Warning: `%s` is not honored on %s
  ```
- **After:**
  ```text
  Warning: macos-user does not apply `resources`, so the agent runs with your own user's limits. To enforce them, use a container setup: `--setup podman-self`.
  Warning: macos-user ignores `kvm`, because macOS has no /dev/kvm. Remove it with `yolo config unset kvm`.
  Warning: `%s` has no effect on %s. Remove it with `yolo config unset %s`, or use a setup that supports it.
  ```
- **Why:** 1 ("cgroups"), 8, plus the step was missing. Only `cache_relocations` (`:130`) names a step today. `gpu` (`:320`) and `devices` (`:300`) need the same kind of last sentence.

## Wrong today

- **`internal/cli/run/run.go:2818` (from `:2791` and `:3530`) and `internal/cli/run/keeper.go:526`.** These say "Configured runtime '%s' not found on PATH" for any error that starting the session returns. The code calls it for any `exec` start failure, and on Linux for any error from the terminal proxy. In `keeper.go` it is also called when `os.Pipe` fails (`jailmain.go:140`–`:158`). In every case the code drops the real error, so the user is told the runtime is missing when it may be installed.
- **`internal/cli/run/run.go:531`** can never print. `run.go:250` already returns for every macos-user `--update` launch that is not a dry run, and that is a superset of `:531`'s condition. The two messages also give different advice: `:250` says to use a container runtime, and `:531` says to run `yolo host --update`. Pick one and delete the other.
- **`internal/cli/run/run.go:275`, `:314`, and `internal/cli/check/nextsteps.go:41`.** These tell the user to run `just build-dev-bundle` or `just install`, which exist only in a source checkout. The user guide (`userguide/guides/troubleshooting.md`, "Cannot find yolo-jail repo root") says the fix is to install from Homebrew or a release archive. A user who installed with `go install` gets no step they can take.
- **`internal/cli/run/resourcegroups.go:81` and `:118`.** These say "`yolo check --no-build` to inspect the selection and supported placement". No placement is supported: every selected resource group refuses (`refuseUnsupportedResourceGroup` always returns true once a group is selected). `internal/cli/check/resourcegroups.go:75` likewise suggests "wait for a supported placement adapter", which does not exist in this build.
- **`internal/cli/run/macosuserrelocations.go:95` and `:145`.** These tell the user to edit `~/.config/yolo-jail/config.jsonc`. The user config is `config.toml` once migrated, and repo advice should name a `yolo config` verb (`yolo config set` / `yolo config unset`) rather than a file to edit by hand.
- **`internal/cli/check/section_hostfloor.go:91` and `internal/cli/hostfloor.go:881`.** These tell the user to write `"host_floor": {"%s": false}` in the user config. That is JSON, and it does not match a TOML user config. No `yolo config` verb can set it either: the readable schema (`internal/config/schema.go`) has only the top-level `host_floor` key, so `yolo config set host_floor.<pack> false --user` refuses. That breaks the rule that advice names a verb. Either add a `host_floor.*` schema rule so the verb works, or print the spelling that matches the user's file. `yolo config-ref` also shows only `config.jsonc` examples for this key.
- **`internal/loopholes/retired.go:117`.** The retired-loopholes notice tells the user to declare each module in `pack.json`. Shipped packs now use `pack.toml`, and `pack.json` is the legacy spelling. The notice should show the `pack.toml` form.
- **`internal/selfupdate/apply.go:196` and `internal/cli/check/section_updates.go:54`.** For go install, pipx and uv installs, the notice says to "update the host binary and its separate jail source together" and gives no command. It looks like a step, but the user cannot act on it.

## Not covered

- The second pass read every message in `packfiles.go`, `inheritscope.go`, `hostloopback.go`, `macosctxtree.go`, `homeskeleton.go`, `ctxmountrecord.go`, `shareddirsources.go` and `liveapplyplan.go`, and traced each to its print site. The `fmt.Errorf` strings in other `run` files that only wrap a lower error ("%s: %w") were not traced one by one.
- `internal/cli/run/inheritscope.go:439`, `:443`, `:690`, `:692`, `:751` and `:791` were read and left as they are. They already name a step, though `:791` is long.
- The `setupcensus` notices for keys other than the six listed above have no `Then` text and use the generic fallback. Each one needs its own step, and that census is not in this partition.
- The `hostpath.Launch.MissLine` text (`internal/hostpath/hostpath.go:322`, printed at `internal/cli/check/section_hostlaunchpath.go:84` and `:86`) and `config.BrokerGrantAdviceFor`/`BrokeredSetsDisclosure` (`internal/config/brokered.go:339`, `:648`; printed at `internal/cli/run/brokeredscope.go:236` and `:304`) were read only in part. They appear correct, but nobody checked them against every caller.
- Comment headers written into generated env files (`agentenvfiles.go:406`, `:454`; `providerenvfiles.go:111`; `jailgrant.go:161`) contain decision IDs. A user may open these files, but they were not reviewed as messages.
- `keeper_linux.go` / `keeper_other.go` return strings ("keeper: no systemd user bus…") go only to the background process's log, so they were skipped as log-only lines.
