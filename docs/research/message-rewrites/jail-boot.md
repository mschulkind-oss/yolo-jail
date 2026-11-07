# Message rewrites: Inside the jail: start-up, briefing, in-jail tools

Scope: `internal/entrypoint` (the jail's start-up, the "boot"), `internal/jailcontent` (the briefing and skills an agent reads, marked **agent-facing**), and every binary under `cmd/` (`yolo-entrypoint`, `yolo-jaild`, `yolo-ps`, `yolo-journalctl`, `yolo-log`, `yolo-serial`, `yolo-cglimit`, `goprobe`). Messages read: about 760 (approximate: every `warn`, `fmt.Fprint*`, refusal builder and shell `echo` in the partition, the `fmt.Errorf` texts a failed boot step or `yolo host apply` prints, and the three built-in skills as rendered in `internal/jailcontent/testdata/mechanics-*.golden`). Rewrites proposed: 105 entries, plus 9 patterns that cover about 60 more sites.

## Patterns

### P1. Internal decision IDs and design-doc paths in messages a user reads

Messages cite ruling IDs and repository doc paths. A user cannot act on `OQ-R2`. Most users do not have the repository, so a path such as `docs/reference/...` gives them nothing to open. Drop the ID. Replace the doc path with the published user guide when a link helps, or leave it out.

- **Before:** `There is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here is copied, renamed or deleted for you.`
- **After:** `yolo does not move these for you.`
- **Sites:** `internal/entrypoint/darwinhomelayout.go:443`, `internal/entrypoint/darwinhomelayout.go:495`, `internal/entrypoint/darwinhomelayout.go:1385`, `internal/entrypoint/reachability.go:516`, `internal/entrypoint/reachability.go:522`, `internal/entrypoint/requiredservice.go:210`, and the closing `docs/reference/loopback-tls-reachability.md` line of every constant in `internal/entrypoint/reachability.go:637`–`internal/entrypoint/reachability.go:707` and of the override notices (`internal/entrypoint/reachability.go:574`, `internal/entrypoint/requiredservice.go:218`).

### P2. Boot step names (`generate_bashrc`) as the subject of a message

When a boot step fails, the boot prints `Error: <step>: <error>`. It then refuses with a list of `<step>: <error>`. The step name is a Go-side identifier that the user never sees anywhere else. Say what yolo was doing in plain words. The boot already records that for the host, in `GenFailure.Doing`. Use it on the terminal too, and keep the step name only in `boot.log`.

- **Before:** `Error: generate_ca_bundle: open /etc/ssl/...: permission denied`
- **After:** `Error: yolo could not build the jail's trusted-certificate list: open /etc/ssl/...: permission denied`
- **Sites:** `internal/entrypoint/boot.go:994` (every step in `internal/entrypoint/bootsteps.go:158`…), `internal/entrypoint/bootsteps.go:342`, `internal/entrypoint/env.go:196`, `internal/entrypoint/surfaceloop.go:231`, `internal/entrypoint/packsurfaces.go:300`.

### P3. Legacy config spellings in advice (`config.jsonc`, `yolo-jail.jsonc`, `packages`, `env_sources`, `profile`)

Config is TOML now. A TOML user who follows this advice looks for a file or key that is not there. The rule from AGENTS.md, and from decision RC-D40 in `docs/design/readable-config.md`, is that advice names a verb, not a file to hand-edit. Use `yolo config add install.nix <pkg>`, `yolo config add env.files <file> --user`, and `yolo active-provider <agent> <provider>`. The same sites are listed under [Wrong today](#wrong-today).

- **Before:** `add it to `packages` in yolo-jail.jsonc`
- **After:** `add it to this project's packages with `yolo config add install.nix <name>``
- **Sites:** `internal/entrypoint/requires.go:78`, `internal/entrypoint/shims.go:92`, `internal/entrypoint/prism.go:1497`, `internal/entrypoint/prism.go:1671`, `internal/entrypoint/prism.go:1703`, `internal/entrypoint/catrust.go:285`, `internal/entrypoint/egress.go:611`, `internal/entrypoint/boot.go:357`, `internal/entrypoint/providerkeys.go:131`.

### P4. "Drop the pack from your packs list" instead of the command

Several refusals tell the user to edit a list by hand. `yolo pack remove <pack>` does that. Name the pack, too: the refusal already knows it, and it is in `$_who`.

- **Before:** `Make a Node >=$_floor available (installing one needs the network), or drop the pack from your packs list.`
- **After:** `Connect to the network and launch again so yolo can install Node >=$_floor. Or stop using the pack: yolo pack remove <pack>`
- **Sites:** `internal/entrypoint/shell.go:480`, `internal/entrypoint/shell.go:577`.

### P5. Endpoint-file faults: four clients, four copies, with internal detail

`yolo-ps`, `yolo-serial`, `yolo-journalctl` and `yolo-log` each print the same three faults in their own words. Each message says "endpoint", "its dir was removed", "stale relative to the running daemon" and "written by an older yolo". The user needs one fact and one action: the connection details are missing or out of date, so relaunch. Keep the path. The token rule still holds: never print the file's contents.

- **Before:** `yolo-ps: the host-processes daemon rejected this jail's token.  The endpoint file %s is stale relative to the running daemon; relaunch the jail.`
- **After:** `yolo-ps: the host-processes service on the host did not accept this jail's key, because the key in %s is out of date. Restart the jail to get a new one.`
- **Other faults, same shape:** missing → `yolo-ps: the host-processes service never started for this jail (%s does not exist). Restart the jail.` Malformed → `yolo-ps: %s is incomplete. Restart the jail to rewrite it.`
- **Sites:** `cmd/yolo-ps/main.go:193`, `cmd/yolo-ps/main.go:197`, `cmd/yolo-ps/main.go:201`, `cmd/yolo-serial/main.go:424`, `cmd/yolo-serial/main.go:427`, `cmd/yolo-serial/main.go:430`, `cmd/yolo-journalctl/main.go:155`, `cmd/yolo-journalctl/main.go:159`, `cmd/yolo-journalctl/main.go:163`, `cmd/yolo-log/main.go:115`, `cmd/yolo-log/main.go:118`, `cmd/yolo-log/main.go:121`; the boot's own copy is in `reachabilityWarning` (`internal/entrypoint/reachability.go:1029`).

### P6. "Authenticated, so yolo's front is up and the daemon behind it is not"

A service that drops the connection mid-answer gets an explanation of yolo's proxy layout ("yolo's front"). The user needs to know that the service on the host stopped, where its log is, and what to do.

- **Before:** `yolo-ps: the host-processes daemon never answered.  The endpoint at %s authenticated, so yolo's front is up and the daemon behind it is not — it crashed or was killed on the host.  Relaunch the jail; the reason is in the host's ~/.local/share/yolo-jail/logs/host-service-host-processes.log.`
- **After:** `yolo-ps: the host-processes service on the host never answered. It has stopped. Restart the jail. The host's ~/.local/share/yolo-jail/logs/host-service-host-processes.log says why it stopped.`
- **Sites:** `cmd/yolo-ps/main.go:163`, `cmd/yolo-serial/main.go:451`.

### P7. A stray `yolo-entrypoint:` prefix inside an error that gets wrapped again

`cmd/yolo-entrypoint` prefixes every fatal error with `yolo-entrypoint:`. Boot steps wrap errors as `Error: <step>: …`. These errors carry their own prefix as well, so the user sees `Error: generate_agent_launchers: yolo-entrypoint: …` or `yolo-entrypoint: yolo-entrypoint: …`. Drop the inner prefix. The `warn` lines that start with `yolo-entrypoint: pack …` are not wrapped, but they mix prefixes with the `pack %s:` lines near them. Use the same `pack %s:` lead for all of them.

- **Before:** `yolo-entrypoint: %s: %w — stop the jail and rerun yolo to reprepare its private clients`
- **After:** `%s is not something this yolo can read (%w). Stop the jail and run yolo again, so yolo prepares these programs again.`
- **Sites:** `internal/entrypoint/privateclients.go:298`, `internal/entrypoint/privateclients.go:326`, `internal/entrypoint/shims.go:673`, `internal/entrypoint/shims.go:836`, `internal/entrypoint/forklauncher.go:103`, `internal/entrypoint/patchedtrees.go:86`.

### P8. Six severity spellings on one screen

The boot prints `warning:`, `Warning:`, `Note:`, `notice:`, `Error:`, `yolo:`, `yolo: ERROR:`, `boot catalog:`, `boot autoprune:`, `boot reconcile:` and a bare `  ⚠ `. The lines all come out on the same launch stream, one after another. Pick three: `Error:` (the launch stops), `Warning:` (something does not work) and `Note:` (it works, but differently). Drop `boot catalog:`, `boot reconcile:` and `boot autoprune:`. They name code, not anything the user knows.

- **Before:** `notice: MCP server 'tavily' skipped — required env not set: TAVILY_API_KEY`
- **After:** see the MCP entry below; the lead becomes `Warning:`.
- **Sites (representative):** `internal/entrypoint/mcp.go:95`, `internal/entrypoint/catalog.go:65`, `internal/entrypoint/orphanremove.go:52`, `internal/entrypoint/reconcile.go:51`, `internal/entrypoint/serverrefresh.go:655`, `internal/entrypoint/runtime.go:737`, `internal/entrypoint/sessionhome.go:243`, and the `e.warn("warning: could not …` family in `internal/entrypoint/prism.go:188`, `internal/entrypoint/hostleafrecord.go:76`, `internal/entrypoint/sessioncapture.go:380`.

### P9. "Could not write the X record for agent/name" with no consequence and no step

Record-keeping failures name an internal record ("selection record", "provenance", "computed-leaf record", "config-list record") and stop. The user cannot tell whether anything is broken. Say what will go wrong, in the user's terms, and give one action. For these records the action is to check that the workspace's `.yolo` folder is writable and has free space, then launch again (or run `yolo host apply` again for the host record). Each consequence below is the one the code's own comments state.

- **Selection record** (`internal/entrypoint/prism.go:188`, `internal/entrypoint/prism.go:201`, `internal/entrypoint/prism.go:207`). A missing record makes a later launch treat every key as the user's own and keep it (`readSelectionRecord` in `prism.go`).
  - **Before:** `warning: could not write the selection record for claude/settings: <err>`
  - **After:** `Warning: yolo could not save which claude settings came from your provider choice (<err>). If you switch provider later, yolo may keep this launch's provider settings as if you had set them. Check that the workspace's .yolo folder is writable and has free space, then launch again.`
- **Provenance** (`internal/entrypoint/prism.go:249`, `internal/entrypoint/prism.go:258`). `yolo config diff` and `yolo config why` read this record (`internal/cli/configdiff.go`).
  - **After:** `Warning: yolo could not save where each claude setting came from (<err>), so `yolo config diff claude` and `yolo config why` may not show it. Check that the workspace's .yolo folder is writable and has free space, then launch again.`
- **Config-list record** (`internal/entrypoint/prism.go:2116`). A lost record means "every entry is the user's", so nothing a pack added is removed (`writeListRecord` in `prism.go`).
  - **After:** `Warning: yolo could not save which entries in claude's settings came from packs (<err>). If you remove a pack later, the entries it added stay. Check that the workspace's .yolo folder is writable and has free space, then launch again.`
- **Computed-leaf record** (host only: `internal/entrypoint/hostleafrecord.go:76`, `internal/entrypoint/hostleafrecord.go:81`, `internal/entrypoint/hostleafrecord.go:86`). `yolo host apply` removes a value it wrote only when this record says it wrote it (`hostleafrecord.go`; the example there is `CLAUDE_CODE_USE_BEDROCK` left behind after a switch from Bedrock to Codex).
  - **After:** `Warning: yolo could not save which values it wrote to ~/.claude/settings.json (<err>). If you change provider later, a value from this provider may stay in that file. Fix the error, then run yolo host apply again.`
- **Why:** 6 (record names), 2 (no next step), 8 is respected: each clause says what the user will see, not how the record works.

## The launch: refusals that stop the jail

### Boot step failure refusal
- **Where:** `internal/entrypoint/boot.go:902` — the user sees this after one or more start-up steps failed, as the last thing before the container goes away.
- **Before:**
  ```text
  refusing to start the jail: 2 boot step(s) failed:
    - generate_bashrc: <error>
    - configure_git: <error>
  ```
- **After:**
  ```text
  yolo did not start the jail, because 2 start-up steps failed:
    - writing the shell's startup file: <error>
    - setting up git: <error>
  Fix what each error names, then launch again. To look inside the failed jail first, launch with YOLO_HOLD_ON_REFUSAL=1.
  ```
- **Why:** 2 (step IDs; see P2), 3 ("refusing to start the jail: …" has no subject), and the happy-path rule: the message names no next step. Today it ends at the list, and only the hold offer that follows hints at what to do.

### Hold offer
- **Where:** `internal/entrypoint/hold.go:108` — printed after a refused boot, when the container is about to be removed.
- **Before:**
  ```text
  yolo-entrypoint: this container is about to be removed WITH the failed state inside it (--rm).
    Re-run with YOLO_HOLD_ON_REFUSAL=1 to hold it open instead and `exec` in to look around.
  ```
- **After:**
  ```text
  yolo-entrypoint: yolo removes this container now, along with what failed inside it.
    To keep it running so you can look inside, launch again with YOLO_HOLD_ON_REFUSAL=1.
  ```
- **Why:** 1 (`--rm`, `exec`), 4 ("the failed state").

### Node version refusal
- **Where:** `internal/entrypoint/shell.go:476` — printed during provisioning when a pack's program needs a newer Node than yolo could install.
- **Before:**
  ```text
  yolo: REFUSING to start this jail: a selected pack needs a Node that is not here.
        Needed by: $_who
        Floor:     Node >=$_floor (installing node@$_floor failed)
        Available: ${_avail:-none}
        Make a Node >=$_floor available (installing one needs the network), or drop
        the pack from your packs list.
  ```
- **After:**
  ```text
  yolo: yolo did not start this jail, because a pack you selected needs a newer Node.
        Needed by: $_who
        Needs:     Node $_floor or newer (installing node@$_floor failed)
        Found:     ${_avail:-none}
        Installing Node needs the network. Check the connection and launch again.
        Or stop using the pack: yolo pack remove <pack>
  ```
- **Why:** 6 ("Floor"), and P4: the message should name the verb. The pack name is in `$_who`, so the template can print it.

### Missing program refusal
- **Where:** `internal/entrypoint/shell.go:574` — printed at the end of provisioning when a pack's program could not be installed.
- **Before:**
  ```text
  yolo: REFUSING to start this jail: a program a selected pack declares could not be installed.
        <who>: its install exited <rc>
            <error lines>
        The full install output is above.  Fix what stopped it (an install needs the
        network), or drop the pack from your packs list.  To start the jail without it:
            YOLO_ALLOW_MISSING_PROGRAMS=1 yolo <your command>
        and each missing program installs the first time it is run (one the host builds,
        at the next launch: its retry is named above).
  ```
- **After:**
  ```text
  yolo: yolo did not start this jail, because it could not install a program a pack you selected needs.
        <who>: its install exited <rc>
            <error lines>
        The full install output is above. Fix what it names (installs need the network), then launch again.
        Or stop using the pack: yolo pack remove <pack>
        Or start the jail without the program, which then installs the first time you run it:
            YOLO_ALLOW_MISSING_PROGRAMS=1 yolo <your command>
  ```
- **Why:** 2 (the clause "(one the host builds, at the next launch: its retry is named above)" is too much for this moment), 7, P4.

### Missing program, launched anyway
- **Where:** `internal/entrypoint/shell.go:567` — printed when `YOLO_ALLOW_MISSING_PROGRAMS` lets the jail start.
- **Before:**
  ```text
    ⚠ YOLO_ALLOW_MISSING_PROGRAMS is set, so this jail starts WITHOUT what a selected pack declares:
  <list>
      Each installs the first time it is run (one the host builds, at the next launch); its
      install output is above.
  ```
- **After:**
  ```text
    ⚠ YOLO_ALLOW_MISSING_PROGRAMS is set, so this jail starts without these programs:
  <list>
      Each one installs the first time you run it. Its install output is above.
  ```
- **Why:** 2, 7.

### Service unusable: the refusal
- **Where:** `internal/entrypoint/reachability.go:512` with `internal/entrypoint/reachability.go:542` — printed when a host service the jail is set up to use cannot be reached, and the launch stops.
- **Before:**
  ```text
  Error: yolo requested host-loopback forwarding for this jail and service 'journal' is unusable from inside it.
    That is a FAULT rather than a limitation of this host: the forwarding WAS
    asked for, so no network option is missing — the other end is down, never
    published, or answering with a credential this jail does not hold.
    Refusing to start: an enabled jail-facing service the jail cannot use is a
    failed launch (docs/reference/loopback-tls-reachability.md, OQ-R2).
    If this jail is knowingly fine without those services — you are debugging the
    host daemon itself, or you only need a shell — launch anyway:
        YOLO_ALLOW_UNREACHABLE_SERVICES=1 <your yolo command>
  ```
- **After:**
  ```text
  Error: the jail cannot reach the host service 'journal', so yolo did not start it.
    The service on the host has stopped, never started, or does not accept this jail's key.
    Run `yolo check` on the host to see whether it is running, then launch again.
    If you only need a shell, or can do without this service, launch anyway:
        YOLO_ALLOW_UNREACHABLE_SERVICES=1 <your yolo command>
  ```
- **Why:** 1 ("host-loopback forwarding", "published"), 6 (P1), 8 (it argues fault against limitation), 7. The check step comes from `reachabilityFaultExplanation`, which prints just above this message. The two can merge.
- **Same rewrite for:** the warn-mode variant in the same function ("(OQ-R2 rules this a failed launch; this witness is running in warn mode …)"). It becomes `Warning: the jail cannot reach the host service 'journal'. The jail starts anyway, and anything that uses the service will fail.` The two other leads in `unusableServicesLead` ("SHARES the network namespace", "the Mac's own network stack") become one sentence each: `This jail uses the network of the program that launched it, so the service itself is the problem.`

### Service unusable: the explanation blocks
- **Where:** `internal/entrypoint/reachability.go:707`, `internal/entrypoint/reachability.go:637`, `internal/entrypoint/reachability.go:668` — printed under the warning lines when a service is unreachable.
- **Before (`reachabilityExplanation`):**
  ```text
    Every in-jail client of those services will fail the same way.
    yolo's host daemons bind the host's LOOPBACK and advertise the container
    runtime's gateway name, which only works if the runtime forwards that name to
    the host's loopback. Whether it does is a property of the rootless network
    stack AND of the address podman writes for that name: FALSE for pasta —
    podman's default since 5.0, which forwards to the host's global address — and
    true for slirp4netns only when allow_host_loopback is set AND the gateway name
    is pinned at slirp's own host address, which podman does not do by itself.
    yolo emits both. On the host, `podman info --format
    '{{.Host.RootlessNetworkCmd}}'` says which stack is in use.
    yolo asks for that forwarding itself on the default network.mode; if it could
    not — old passt, an explicit network.mode, a rootful or unrecognised runtime,
    or YOLO_NO_HOST_LOOPBACK — it said so in this jail's launch output above.
    docs/reference/loopback-tls-reachability.md is the whole map.
  ```
- **After:**
  ```text
    Every program in the jail that uses these services will fail the same way.
    yolo could not tell whether this host's container network lets the jail reach the host.
    If it could not set that up, the launch output above says why and what to change.
    Run `yolo check` on the host to see whether the services are running.
  ```
- **Before (`reachabilityLimitationExplanation`, first lines):** `This is a KNOWN LIMITATION of this host, not a fault in this jail. yolo could not get this host's rootless network stack to forward the host's LOOPBACK into the jail — an old passt, or a capability it could not confirm — and launched anyway rather than refusing to run on a machine whose owner may not be able to change it. …`
- **After:** `This host's container network cannot reach services on the host, so these services do not work in this jail. Everything else does. The launch output above names what to upgrade.`
- **Before (`reachabilitySharedExplanation`):** 10 lines about `--net=host`, podman-in-podman, pasta and slirp4netns, ending with "Look at the daemon instead".
- **After:** `This jail uses the network of the program that launched it, so the network is not the problem. Check that the service is running: run `yolo check` where you launched this jail. For a jail started inside another jail, that means the outer jail.`
- **Why:** 8 (how it works and why it was designed so), 2, 5 ("rootless network stack", "gateway name"), 6 (P1).

### Unreachable service warning without a next step
- **Where:** `internal/entrypoint/reachability.go:1060` — printed for each host service the jail could not connect to.
- **Before:**
  ```text
  warning: host service 'journal' is enabled but UNREACHABLE from inside this jail
    Dialing <addr> failed: <err>
  ```
- **After:**
  ```text
  Warning: the jail cannot connect to the host service 'journal' at <addr>: <err>
  ```
- **Why:** 1 ("Dialing"). The next step comes from the explanation block printed right after it, so this line must not drop that block. If the line ever prints alone, it needs the `yolo check` step.

### Required in-jail service did not start
- **Where:** `internal/entrypoint/requiredservice.go:204` — printed when a background service the jail needs failed to start inside it.
- **Before:**
  ```text
  Error: required in-jail service 'wire-bridge' (from pack "wire-bridge", which pack "claude" needs) did not start, so this jail cannot use it.
    Why: <cause>
    <Logs …>
    Refusing to start: a required service that did not start is a failed launch
    (docs/reference/loopback-tls-reachability.md, OQ-R8).
    If you only need a shell — to fix this from inside, or because this jail can do
    without it — launch anyway:
        YOLO_ALLOW_UNREACHABLE_SERVICES=1 <your yolo command>
  ```
- **After:**
  ```text
  Error: the service 'wire-bridge', which pack "claude" needs, did not start inside the jail, so yolo did not start the jail.
    Why: <cause>
    <Logs …>
    If you only need a shell, to fix this from inside or because you can do without it, launch anyway:
        YOLO_ALLOW_UNREACHABLE_SERVICES=1 <your yolo command>
  ```
- **Why:** 6 (P1), 2 (the "Refusing to start" sentence says only that a refusal is a refusal), 5 ("required in-jail service").

### Override notices
- **Where:** `internal/entrypoint/reachability.go:574`, `internal/entrypoint/requiredservice.go:218` — printed when `YOLO_ALLOW_UNREACHABLE_SERVICES` lets the jail start anyway.
- **Before:**
  ```text
  warning: YOLO_ALLOW_UNREACHABLE_SERVICES is set — CONTINUING with service 'journal' is unusable from inside this jail.
    Nothing was repaired; the launch was merely allowed to proceed, and every
    in-jail client of those services will still fail. Unset it to have an
    unusable service refuse the launch again.
    docs/reference/loopback-tls-reachability.md
  ```
- **After:**
  ```text
  Warning: YOLO_ALLOW_UNREACHABLE_SERVICES is set, so the jail starts although it cannot reach the service 'journal'.
    Anything in the jail that uses it will fail.
  ```
- **Why:** 3 (the first line is ungrammatical: "CONTINUING with service 'journal' is unusable"), 2, 6 (P1). The sentence "Unset it to have …" tells the user to undo the setting they just chose.

## The launch: warnings during start-up

### MCP server left out: env not set
- **Where:** `internal/entrypoint/mcp.go:95` — printed when an MCP server in the config is left out because a variable it needs is not set.
- **Before:**
  ```text
  notice: MCP server 'tavily' skipped — required env not set: TAVILY_API_KEY
  ```
- **After:**
  ```text
  Warning: yolo left out the MCP server 'tavily', because TAVILY_API_KEY is not set. Put the variable in a dotenv file, add it with `yolo config add env.files <file> --user`, and launch again.
  ```
- **Why:** 3, P8, and the happy-path rule: today there is no next step.

### MCP server for one agent only
- **Where:** `internal/entrypoint/mcp.go:217` — printed when an MCP server's variable comes only from one agent's provider.
- **Before:** `notice: MCP server 'tavily' configured only for claude — its required env (TAVILY_API_KEY) reaches only the agent that selected its provider`
- **After:** `Note: only claude gets the MCP server 'tavily', because only claude's provider sets TAVILY_API_KEY.`
- **Why:** 5 ("reaches only the agent that selected its provider"), 7.

### Entries dropped from an agent's config
- **Where:** `internal/entrypoint/prism.go:1576`, `internal/entrypoint/prism.go:1630`, `internal/entrypoint/prism.go:1669`, `internal/entrypoint/prism.go:1702` — printed when yolo removes entries (MCP servers, for example) from an agent's config file at start-up.
- **Before (not in config):**
  ```text
  claude/settings: dropping from mcpServers (not in config): foo — yolo rebuilds mcpServers from its config at every boot; to keep it, declare it in ~/.config/yolo-jail/config.jsonc on the host, or in this workspace's yolo-jail.jsonc (an MCP server under `mcp_servers`, …), reaching every agent
  ```
- **After:**
  ```text
  claude/settings: yolo removed foo from mcpServers, because yolo's config does not list it. yolo writes mcpServers from its config at every start. To keep foo for every agent, add it to the config: an MCP server goes under `mcp_servers` (`yolo config-ref` shows the key).
  ```
- **Before (required env, other agents):** one 70-word sentence that ends "select for claude a profile that delivers X, as those do, with the `profile` key in ~/.config/yolo-jail/config.jsonc on the host, then launch again".
- **After:** `claude/settings: yolo left out tavily, because claude does not get TAVILY_API_KEY. codex gets it from its provider. To give it to claude too, pick a provider for claude that sets it: yolo active-provider claude <provider>. Then launch again.`
- **Before (required env, no agent):** `… — X is unset for claude, so the `requires_env` gate left it out; it stays declared under `mcp_servers`, and to deliver it, set X in a dotenv file listed under `env_sources` in ~/.config/yolo-jail/config.jsonc on the host, then launch again`
- **After:** `claude/settings: yolo left out tavily, because TAVILY_API_KEY is not set. Put it in a dotenv file, add the file with `yolo config add env.files <file> --user`, and launch again.`
- **Before (withheld):** `… (in config, withheld by capability): X (provides web search) — provider "p" does that job itself, so yolo does not deliver it to this agent; it stays declared under `mcp_servers`, and to deliver it anyway, remove its `provides``
- **After:** `claude/settings: yolo left out X, because provider "p" already does web search itself. To use X anyway, remove `provides` from its entry under `mcp_servers`.`
- **Why:** 7 (each is one sentence chained with em-dashes and semicolons), 5 ("withheld by capability", "the `requires_env` gate"), 6 ("deliver"), and P3: the file and key are wrong for TOML users.

### MCP gate: session env file unreadable
- **Where:** `internal/entrypoint/boot.go:356` — printed when the per-session variable file cannot be read.
- **Before:** `warning: could not read the session env file <path> (<err>), so an MCP server gated by requires_env on an env_sources variable is left out of the agent configs this launch. Relaunch to rewrite the file.`
- **After:** `Warning: yolo could not read <path> (<err>). Any MCP server that needs a variable from your env files is left out this time. Launch again to rewrite the file.`
- **Why:** 5, 6 (`requires_env`, `env_sources`).

### Required program not on PATH
- **Where:** `internal/entrypoint/requires.go:76` — printed when a pack needs a command that is not installed.
- **Before:** `warning: pack fzf-keys needs program fzf, which is not on PATH in this jail and no selected pack provides it (yolo installs nothing for a `program` with no recipe — add it to `packages` in yolo-jail.jsonc, or select a pack whose `program` fzf carries one) [host hints: …]`
- **After:** `Warning: pack fzf-keys needs fzf, which is not installed in this jail. Add it with `yolo config add install.nix fzf`, or add a pack that installs it, then launch again.`
- **Why:** 2, 6 ("a `program` with no recipe"), 7, P3. Keep the host hints if they help, but say what they are: "Hints for your host: …".

### Blocked tool not blocked
- **Where:** `internal/entrypoint/shims.go:89` — printed when a blocked-tool rule is skipped because its replacement is missing.
- **Before:** `not blocking grep: its replacement rg is not on PATH in this jail, and a block with no working alternative removes the capability instead of redirecting it (add rg to `packages` to restore the block)`
- **After:** `Note: yolo did not block grep, because the tool it points to instead, rg, is not installed. To block grep, install rg: yolo config add install.nix rg`
- **Why:** 8 (the rationale clause), P3.

### No launcher: something else already provides the program
- **Where:** `internal/entrypoint/shims.go:595`, `internal/entrypoint/shims.go:1002` — printed when yolo does not install a pack's program because one is already on PATH.
- **Before:** `pack claude: no launcher for claude — <why>, and a lazy installer must never shadow it (drop the `program`'s "via" if asserting the binary is present is what you meant)`
- **After:** `Note: pack claude: yolo did not set up claude, because <why>. That copy runs instead.`
- **Why:** 6 ("lazy installer", "shadow", "agent-class launcher"), 8. The pack-author advice about `via` belongs in `yolo check` on the host, not in every launch.

### Launch flags: zsh note
- **Where:** `internal/entrypoint/shell.go:153` — printed on macOS sandboxes whose login shell is zsh.
- **Before:** `pack launch flags are written as bash aliases in ~/.bashrc, and this account's login shell is zsh, which does not read it. They reach claude at this prompt anyway, through ~/.yolo/bin/launch/<name>, which is on this account's PATH — the alias is the redundant carrier here, not the delivering one.`
- **After:** delete it, or log it to `boot.log` only (`e.note`).
- **Why:** 8. The message says that everything works and explains the internals. The user has nothing to do.

### Launch flags: commands changed
- **Where:** `internal/entrypoint/shell.go:164` and `internal/entrypoint/launchwrapper.go:335` — printed when packs add flags to commands such as `claude`.
- **Before:** `yolo adds pack-declared launch flags to EVERY invocation of claude in this jail — a script's and an agent's, not just the ones you type (~/.yolo/bin/launch/<name> is the script; YOLO_NO_LAUNCH_FLAGS=1 runs one without them).`
- **After:** `Note: in this jail, yolo adds your packs' flags every time claude runs, whether you, a script or an agent runs it. To run it without them, set YOLO_NO_LAUNCH_FLAGS=1.`
- **Why:** 5 ("pack-declared launch flags"), 7, 1 (the script path is internal).

### Wrapper notes
- **Where:** `internal/entrypoint/launchwrapper.go:176`, `internal/entrypoint/launchwrapper.go:344` — printed under the launch-flag note.
- **Before:** `  gh: yolo installs nothing for these names — a wrapper in ~/.yolo/bin/launch sources each one's own env file (what this launch scoped to it alone) and runs whatever PATH already provides.`
- **After:** `  gh: yolo does not install this. yolo only adds its settings, then runs the gh you already have.`
- **Why:** 8, 5.

### Programs no pack uses
- **Where:** `internal/entrypoint/catalog.go:208` — printed when programs from packs you no longer use are still installed.
- **Before:** `boot catalog: 3 installed programs are declared by no selected pack — boot.log names them (`yolo programs ls` for sizes; `programs.autoprune` removes them)`
- **After:** `Note: 3 installed programs belong to no pack you use. `yolo programs ls` lists them with their sizes. To remove such programs at every start, run `yolo config set programs.autoprune true --user`.`
- **Why:** P8 (prefix), 5 ("declared by no selected pack"), and the next step should be a verb. `programs.autoprune` is a user-scope key (`internal/config/schema.go`).

### Autoprune blocked by a link
- **Where:** `internal/entrypoint/catalog.go:563` — printed when automatic removal is on but a symlink is in the way.
- **Before:** `boot autoprune: programs.autoprune is ON, and this boot removes nothing: a symbolic link sits on the way to a directory it reads (above). Remove the link and relaunch, or remove the orphans with `yolo programs remove` inside a session`
- **After:** `Warning: yolo removed no unused programs this time, because a symbolic link is in the way (named above). Remove the link and launch again, or run `yolo programs remove` inside the jail.`
- **Why:** P8, 5 ("orphans").

### Variable already set: no per-session config directory
- **Where:** `internal/entrypoint/sessionhome.go:243` — printed when a session starts with the variable that yolo would set to give it its own config directory.
- **Before:** `Note: pack claude: CLAUDE_CONFIG_DIR is already set in this session, so the session reads the directory you set and gets no config directory of its own; another session's profile can change what it reads, so unset the variable for yolo to give each session its own`
- **After:** `Note: pack claude: CLAUDE_CONFIG_DIR is already set, so this session uses that directory. Other sessions can change what is in it. Unset CLAUDE_CONFIG_DIR to give each session its own.`
- **Why:** 7, 5 ("another session's profile").

### Host config key dropped in the jail
- **Where:** `internal/entrypoint/providerkeys.go:128` — printed when your host's agent settings set a key that the jail's provider choice controls.
- **Before:** `claude/settings: your host ~/.claude/settings.json sets model = "x", and in a jail claude's provider selection decides that key, so this jail's copy leaves it out (it is absent unless the selected provider sets it). Your host file is unchanged; to use it here, select a profile that sets it (`yolo config-ref`: profile, providers).`
- **After:** `claude/settings: your host ~/.claude/settings.json sets model = "x". In the jail, claude's provider decides that key, so the jail's copy leaves it out. Your host file is unchanged. To set it in the jail, pick a provider that sets it: yolo active-provider claude <provider>`
- **Why:** 7, P3 (`profile` is the legacy key), and the next step should be a verb.

### Captured edit dropped
- **Where:** `internal/entrypoint/providerkeys.go:108` — printed when an edit made inside an earlier jail sets a key the provider controls.
- **Before:** `claude/settings: dropped the captured in-jail edit model = x from ~/.claude/settings.json: claude's provider selection decides that key, so no captured edit may set it (it is absent unless the selected provider sets it). `yolo config diff claude` shows the captured edits that remain; `yolo config reset claude` discards them all.`
- **After:** `claude/settings: yolo did not keep your earlier change model = x, because claude's provider decides that key. `yolo config diff claude` shows the changes yolo kept. `yolo config reset claude` discards them all.`
- **Why:** 5 ("captured in-jail edit"), 2.

### Node extra certificates unreadable
- **Where:** `internal/entrypoint/catrust.go:283` — printed when `NODE_EXTRA_CA_CERTS` points at a file the jail cannot read.
- **Before:** `Warning: your configured environment sets NODE_EXTRA_CA_CERTS to /x, which the jail could not read, so Node does not add its certificates. Check the path in your env_sources: it is read inside the jail, so it must be a path the jail sees, such as one under /workspace.`
- **After:** `Warning: NODE_EXTRA_CA_CERTS names /x, which the jail cannot read, so Node does not use those certificates. The jail reads this path itself, so it must be one the jail can see, such as a file under /workspace. Fix it in the env file that sets it.`
- **Why:** P3 (`env_sources`), 7.

### Orphaned in-jail service processes
- **Where:** `internal/entrypoint/runtime.go:228` — printed when old service processes with no manager still hold the jail's ports.
- **Before:**
  ```text
  orphaned in-jail daemon(s) hold this jail's service ports with no supervisor: wire-bridge (pid 42)
  yolo will not kill a process on the strength of its command line. Inspect it, then `kill <pid>` if it is yours to reclaim
  ```
- **After:**
  ```text
  Old service processes still hold this jail's ports, and nothing manages them: wire-bridge (pid 42).
  yolo does not stop them for you. Check each one with `ps -p <pid> -f`, then stop it with `kill <pid>` and start the session again.
  ```
- **Why:** 6 ("supervisor", "daemon(s)"), 8 ("on the strength of its command line"), and the step should say what comes after the kill.

### Port settings unreadable
- **Where:** `internal/entrypoint/runtime.go:49`, `internal/entrypoint/runtime.go:505`, `internal/entrypoint/runtime.go:534` — printed when the launcher passed port settings the jail cannot parse.
- **Before:** `Warning: invalid YOLO_FORWARD_HOST_PORTS: <raw>`
- **After:** `Warning: yolo could not read the jail's forwarded-port settings (<raw>), so no host port is forwarded. Check the ports in your config with `yolo check` on the host, then launch again.`
- **Why:** 1 (an internal environment variable), and the happy-path rule: there is no next step.

### socat missing
- **Where:** `internal/entrypoint/runtime.go:581` — printed when the tool that forwards host ports is missing.
- **Before:** `Warning: socat not found, cannot forward host ports`
- **After:** `Warning: socat is not installed in this jail, so yolo cannot forward host ports. Add it with `yolo config add install.nix socat` and launch again.`
- **Why:** 3, and the happy-path rule: there is no next step.

### Slow boot step
- **Where:** `internal/entrypoint/runtime.go:737` — printed when a time-limited start-up step was slow.
- **Before:** `Note: <what> took 12s (bound 30s)`
- **After:** `Note: <what> took 12s (the limit is 30s).`
- **Why:** 3, 6 ("bound").

### Boot check missing
- **Where:** `internal/entrypoint/packbootchecks.go:119` — printed when a pack's start-up check script is missing.
- **Before:** `  ⚠ mypack: its boot check ~/.mypack/check.sh is not in this home, so it did not run; the pack's files contribution delivers it — run `yolo check` on the host to see what the pack declares`
- **After:** `  ⚠ mypack: its start-up check ~/.mypack/check.sh is missing, so it did not run. The pack should install that file. Run `yolo check` on the host to see what the pack sets up.`
- **Why:** 6 ("files contribution"), 7.

### config-list on an unrendered file
- **Where:** `internal/entrypoint/packsurfaces.go:653` — printed when a pack adds list entries to a file yolo does not write.
- **Before:** `config-list  no effect — claude/foo is declared `unrendered`, so yolo writes no file to append to (pack x)`
- **After:** `Warning: pack x adds entries to claude/foo, but yolo does not write that file, so the entries do nothing.`
- **Why:** 3, 6 (`config-list`, `unrendered`).

### Witness record not kept (macOS)
- **Where:** `internal/entrypoint/reachability.go:807` — printed on macOS when the service check has no workspace to log to.
- **Before:** `reachability: the host-service witness's record is not kept in a boot log: YOLO_DARWIN_WORKSPACE is not set, so this process names no workspace to append it to. The witness's verdict on this terminal is the whole record; a macos-user launch always sets the variable, so if this came from a launch, report it.`
- **After:** `Warning: yolo did not save the host-service check to boot.log, because it does not know which workspace this is. The result above is the only copy. If you started this with yolo, report it: <issues URL>`
- **Why:** 6 ("witness", "verdict"), 1 (variable name), 7. `IssuesURL` already exists in this package (`modelreferences.go`).

## macOS sandbox sessions: setting up the home folder

These messages appear only on a Mac using yolo's macOS sandbox account (`seatbelt-self` or `seatbelt-yolo-account`). Most stop the launch as a failed boot step, so P2 also applies. Many of them list good `rm` commands but end without saying to launch again. Each rewrite below adds that last step.

### Launch did not come from yolo's macOS launcher
- **Where:** `internal/entrypoint/darwinhomelayout.go:328` — printed when the sandbox start-up runs without the settings yolo's macOS launcher passes.
- **Before:** `the per-workspace home /Users/x/... cannot be laid: the launch named no YOLO_DARWIN_MACHINE_HOME or YOLO_DARWIN_SHARED_HOME. Only a macos-user launch sets these; run `yolo` in the workspace again, and if it repeats, report it with this line`
- **After:** `yolo cannot set up this workspace's home folder, because this start-up did not come from yolo's macOS launch. Run yolo in the workspace again. If this repeats, report it with this message: <issues URL>`
- **Why:** 1 (variable names), 5 ("cannot be laid"), 6 ("macos-user").

### Real files where this workspace's links belong
- **Where:** `internal/entrypoint/darwinhomelayout.go:439` — printed when this workspace's home folder has real files or folders where yolo puts links to state shared by every workspace.
- **Before (head):**
  ```text
  the per-workspace home layout cannot be laid: 2 paths are real, where a symlink belongs.
  There is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here is copied, renamed or deleted for you.
  ```
- **After (head):**
  ```text
  yolo cannot set up this workspace's home folder: 2 paths are real files or folders where yolo needs to put a link.
  yolo does not move these for you.
  ```
- **After (new last line):** `Then run yolo again.`
- **Why:** 5 ("cannot be laid", "layout"), P1, and every stop names the next step. The command list in the middle is good and stays.

### Real files in the sandbox account or the workspace's folder
- **Where:** `internal/entrypoint/darwinhomelayout.go:491` — printed when the sandbox account's home, or this workspace's own folder under `.yolo`, holds real folders where yolo needs links.
- **Before (sections):**
  ```text
  the per-workspace home layout cannot be laid: 1 path is real, where the workspace tier's symlink belongs.
  In the WORKSPACE SIDECAR — and `sudo rm -rf /Users/_yolo` does NOT touch these, so resetting the account would lose your credentials and leave the launch refusing:
  Each is a real directory where a mirror of a machine-scope directory belongs — a copy stranded by a container-era launch, whose live original is the path in parentheses. Remove the WORKSPACE copy:
  ```
- **After:**
  ```text
  yolo cannot set up this workspace's home folder: 1 path is a real file or folder where yolo needs to put a link.
  In this workspace's own folder (resetting the sandbox account does not remove these):
  Each is an old copy of a folder that every workspace on this Mac now shares. The shared folder is the path in parentheses, and it is the one in use. Remove the old copy:
    sudo rm -rf <path>
  Then run yolo again.
  ```
- **Why:** 5 ("workspace tier", "machine-scope", "mirror"), 6 ("sidecar"), 8 ("container-era launch"). The warning about credentials is true, but it only matters if the user resets the account, so a shorter version keeps the point.

### A path yolo owns is not yolo's link yet
- **Where:** `internal/entrypoint/darwinhomelayout.go:719`, `internal/entrypoint/darwinoverlay.go:301` — printed when a file cannot be written because the home folder setup failed first.
- **Before:** `~/.claude/settings.json was not written: /Users/_yolo/.claude is not this launch's layout link to /Users/x/.../.claude (the darwin_home_layout step says why), and a path the layout owns is never written as anything else`
- **After:** `yolo did not write ~/.claude/settings.json: /Users/_yolo/.claude should be yolo's link to /Users/x/.../.claude, and it is not. The home folder error earlier in this output says why. Fix that first, then run yolo again.`
- **Why:** P2 (step name), 5 ("layout link").

### A link yolo did not make is on the way
- **Where:** `internal/entrypoint/darwinhomelayout.go:761`, `internal/entrypoint/darwinoverlay.go:322`, `internal/entrypoint/darwinoverlay.go:332`, `internal/entrypoint/darwinoverlay.go:813` — printed when a link that yolo did not create sits on the path to a file yolo writes.
- **Before:** `~/.claude/x was not written: /Users/_yolo/.claude -> /elsewhere, on its way, is a symbolic link yolo did not lay, and this write runs outside the sandbox, so following it would land where the session's sandbox profile does not protect. Remove the link (what it points at is left alone):\n  sudo rm /Users/_yolo/.claude`
- **After:**
  ```text
  yolo did not write ~/.claude/x: /Users/_yolo/.claude is a link yolo did not make (it points to /elsewhere).
  Writing through it would put the file outside the sandbox's protection.
  Remove the link (the place it points to is not touched), then run yolo again:
    sudo rm /Users/_yolo/.claude
  ```
- **Why:** 5 ("lay"), 7, and the last step. The overlay's "typically another workspace's, for a pack this launch does not select" clause can go (rule 8).

### A yolo link points outside the home
- **Where:** `internal/entrypoint/darwinhomelayout.go:695` — printed when one of yolo's own planned links points outside the sandbox home.
- **Before:** `~/.x was not written: a link yolo lays on its way points at /elsewhere, outside the account home /Users/_yolo, which no link of yolo's does`
- **After:** `yolo did not write ~/.x: one of yolo's own links on the way points at /elsewhere, outside the sandbox home /Users/_yolo. This is a yolo bug. Report it with this message: <issues URL>`
- **Why:** 5, and the stop names who can act.

### Path climbs out with ".."
- **Where:** `internal/entrypoint/darwinhomelayout.go:746` — printed when a file's path goes through a missing folder and then uses `..`.
- **Before:** `~/.x/y was not written: /Users/_yolo/.x does not exist, and the path continues through it with "../y"`
- **After:** `yolo did not write ~/.x/y: the path goes through the missing folder /Users/_yolo/.x and then back up with "..". This is a yolo bug. Report it with this message: <issues URL>`
- **Why:** the stop names who can act.

### Staged copy has no file list
- **Where:** `internal/entrypoint/darwinoverlay.go:159` — printed when the copy of agent files yolo prepared for the sandbox is missing its index file.
- **Before:** `the home overlay /x carries no destination list (.yolo-home-overlay.json), so nothing says which paths it replaces; refusing to guess, because replacing a parent of a destination deletes the agent state beside it`
- **After:** `yolo cannot copy agent files into the sandbox home: the prepared copy at /x is missing its file list (.yolo-home-overlay.json). Run yolo again so it prepares the copy again. If this repeats, report it with this message: <issues URL>`
- **Why:** 6 ("home overlay"), 8 (the reason for refusing), and no next step today.

### Staged copy does not match its file list
- **Where:** `internal/entrypoint/darwinoverlay.go:224`, `internal/entrypoint/darwinoverlay.go:228`, `internal/entrypoint/darwinoverlay.go:784`, `internal/entrypoint/darwinoverlay.go:787` — printed when the prepared copy and its file list disagree.
- **Before:** `the home overlay lists ~/.claude/x but carries nothing there: <err>`
- **After:** `yolo cannot copy ~/.claude/x into the sandbox home: the prepared copy lists it but does not contain it (<err>). Run yolo again. If this repeats, report it with this message: <issues URL>`
- **Why:** 6 ("home overlay"), and no next step today. The other three sites take the same ending.

### A pack's file would replace yolo's link
- **Where:** `internal/entrypoint/darwinoverlay.go:306` — printed when a pack delivers a file at a path where yolo keeps a link.
- **Before:** `not installing ~/.x: it is this launch's layout link to /y, and a destination is replaced whole, which would put a real path where the link belongs`
- **After:** `yolo did not install ~/.x: yolo keeps a link there, and a pack's file would replace it. This is a bug in yolo or in the pack. Report it with this message: <issues URL>`
- **Why:** 5, 6 ("layout link", "destination").

### Folder on the way leads outside
- **Where:** `internal/entrypoint/darwinoverlay.go:1001` — printed when a folder on the way to a file resolves outside the sandbox's own folders.
- **Before:** `not installing ~/.x: its directory /a resolves to /b, outside the sandbox home and the workspace's own sidecar (/h, /s). A symbolic link on the way there points out of the jail's own directories; remove it and launch again`
- **After:** `yolo did not install ~/.x: its folder /a leads to /b, outside the sandbox's own folders (/h, /s). A link on the way points out of them. Find it with ls -l along /a, remove it, then run yolo again.`
- **Why:** 6 ("sidecar"), and the step should say how to find the link.

### Install list not saved
- **Where:** `internal/entrypoint/darwinoverlay.go:433` — warning on macOS.
- **Before:** `the home overlay's install record (.yolo-home-overlay-installed.json in /x) was not saved: <err>. A destination a later launch stops delivering may then stay in the home unnamed; launch again to retry`
- **After:** `Warning: yolo could not save its list of the files it put in the sandbox home (<err>). If a pack stops providing one of them later, yolo will not remove it or tell you about it. Launch again to retry.`
- **Why:** 6 ("home overlay", "install record", "destination"), P9. The consequence is the code's own.

### Your yolo user config not copied into the sandbox
- **Where:** `internal/entrypoint/darwinoverlay.go:828`, `internal/entrypoint/darwinoverlay.go:833`, `internal/entrypoint/darwinoverlay.go:905`, `internal/entrypoint/darwinoverlay.go:912`, `internal/entrypoint/darwinoverlay.go:917`, `internal/entrypoint/darwinoverlay.go:889` — printed when yolo cannot place its copy of your yolo user config (`~/.config/yolo-jail/config.toml`, or `config.jsonc`) in the sandbox home.
- **Before:** `refusing to install inherited core config ~/.config/yolo-jail/config.toml: its staged source is not a sealed 0444 regular file; repair the staged overlay and relaunch`
- **After:** `yolo did not copy your yolo user config to ~/.config/yolo-jail/config.toml in the sandbox: the prepared copy is not the read-only file yolo made. Run yolo again so it prepares the copy again.`
- **Before:** `refusing to install inherited core config ~/.config/yolo-jail/config.toml: an existing destination is neither a delivery yolo recorded nor the staged file an earlier yolo linked there; preserve it, inspect the path (move it aside if it is yours to keep), then relaunch`
- **After:** `yolo did not copy your yolo user config to ~/.config/yolo-jail/config.toml in the sandbox, because a file yolo did not put there is in the way. Move it aside if you want to keep it, or delete it. Then run yolo again.`
- **Before (changed during install):** `refusing to replace inherited core config ~/...: it changed during installation; preserve it, inspect the path, then relaunch`
- **After:** `yolo did not replace ~/.config/yolo-jail/config.toml in the sandbox, because something changed it while yolo was working. yolo left it as it is. Check that nothing else is writing to it, then run yolo again.`
- **Why:** 5 ("inherited core config", "sealed 0444 regular file"), 6 ("staged overlay", "delivery"). "Repair the staged overlay" is not something the user can do. "Inspect the path" does not say what to look for.

### Old agent file left in the sandbox home
- **Where:** `internal/entrypoint/darwinoverlay.go:565` — warning on macOS when a file from a pack you removed is still in the sandbox home.
- **Before:** `left in place: ~/.x: no pack this launch selects delivers it any more, but it changed since yolo delivered it — remove it with `rm -r /Users/_yolo/.x` if you do not want it`
- **After:** `Left in place: ~/.x. No pack in use provides it any more, but it changed after yolo put it there. If you do not want it, remove it: rm -r /Users/_yolo/.x`
- **Why:** 7, 6 ("selects", "delivered").

### Host package caches not shared
- **Where:** `internal/entrypoint/darwinhomelayout.go:995`, `internal/entrypoint/darwinhomelayout.go:1008` — warning on macOS when the list of host caches to share is malformed.
- **Before:** `package caches: YOLO_DARWIN_CACHE_ALIASES is not a JSON object of link to target (<err>); no host cache is shared this launch, and the sandbox keeps its own`
- **After:** `Warning: yolo could not share your host's package caches with the sandbox this launch (<err>), so the sandbox uses its own copies. This is a yolo bug. Report it with this message: <issues URL>`
- **Why:** 1 (variable name), 5 ("JSON object of link to target"). The launcher writes this variable, so the user cannot fix it.

### cache_relocations: a link or file in the way
- **Where:** `internal/entrypoint/darwinhomelayout.go:1233`, `internal/entrypoint/darwinhomelayout.go:1238` — printed when the sandbox's `~/.cache` is a link or a file.
- **Before:** `cache_relocations: /Users/_yolo/.cache -> /x is a symbolic link yolo did not lay, where the sandbox's own cache directory belongs; no relocation is laid through it, because the links would land wherever it points. Remove the LINK (what it points at is left alone):\n  sudo rm /Users/_yolo/.cache`
- **After:**
  ```text
  cache_relocations: /Users/_yolo/.cache is a link yolo did not make (it points to /x). yolo needs a real folder there, so it set up no cache relocation.
  Remove the link (the place it points to is not touched), then run yolo again:
    sudo rm /Users/_yolo/.cache
  ```
- **Why:** 5 ("lay", "laid"), and the last step.

### cache_relocations: folder changed
- **Where:** `internal/entrypoint/darwinhomelayout.go:1249`.
- **Before:** `cache_relocations: /Users/_yolo/.cache changed while it was being opened; nothing was laid`
- **After:** `cache_relocations: /Users/_yolo/.cache changed while yolo was opening it, so yolo set up no cache relocation. Run yolo again.`
- **Why:** 5 ("laid"), no next step.

### cache_relocations: bad entry
- **Where:** `internal/entrypoint/darwinhomelayout.go:1274` — printed when a `cache_relocations` entry is malformed.
- **Before:** `cache_relocations: "a/b" → "rel" is not a cache subdir and an absolute target`
- **After:** `cache_relocations: the entry "a/b" = "rel" is not valid. Its name must be one folder under ~/.cache, and its target must be an absolute path. Fix it with yolo config set cache_relocations.<name> <absolute path> --user, or remove it with yolo config unset cache_relocations.<name> --user.`
- **Why:** 5 (the sentence does not parse), and no next step. `cache_relocations` is a user-scope key (AGENTS.md).

### cache_relocations: real folders in the way
- **Where:** `internal/entrypoint/darwinhomelayout.go:1380`.
- **Before (head):** `cache_relocations: 2 paths in the sandbox's cache are real, where the relocation's link belongs.\nThere is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here is copied, renamed or deleted for you.`
- **After (head):** `cache_relocations: 2 folders in the sandbox's cache are real folders where yolo needs to put a link. yolo does not move them for you.`
- **After (new last line):** `Then run yolo again.`
- **Why:** P1, 5, and the last step.

### Linked workspace folder
- **Where:** `internal/entrypoint/darwinhomelayout.go:876` — printed when this workspace's folder under `.yolo` is itself a link.
- **Before (middle):** `Nothing is laid or delivered through a link the layout did not lay: the agent's state, and the skills and briefings this launch delivers, would land wherever it points, where the session's sandbox profile does not protect them.`
- **After:** `yolo does not write through a link it did not make. Writing there would put the agent's files wherever the link points, outside the sandbox's protection.`
- **Why:** 5 ("laid", "layout"), 7. The command list that follows is good and stays.

## Start-up self-repair: symlink loops and graphics drivers

These messages stop the launch as a failed boot step, so P2 applies. Most end with "inspect the path and relaunch" or "repair the mount", which does not tell the user what to look for or do. The fixes are small and concrete.

### Agent config file prefix: "surface claude/settings:"
- **Where:** `internal/entrypoint/packsurfaces.go:695`, `internal/entrypoint/packsurfaces.go:699`, `internal/entrypoint/packsurfaces.go:702` — the prefix on every loop-repair error.
- **Before:** `surface claude/settings: symlink loop at /home/agent/.claude leaves the jail home; it was left untouched, so repair or remove that link yourself, then relaunch`
- **After:** `claude's settings file (~/.claude/settings.json): the link /home/agent/.claude is part of a loop that leaves the jail's home, so yolo left it alone. Remove it with rm /home/agent/.claude, then start the jail again.`
- **Why:** 6 ("surface"), and the step should name the command.

### Loop changed before yolo could remove it
- **Where:** `internal/entrypoint/looprepair.go:51`, `internal/entrypoint/looprepair.go:54` (and the same text three lines later), `internal/entrypoint/looprepair.go:172`, `internal/entrypoint/looprepair.go:161`.
- **Before:** `refusing to repair changed symlink /home/agent/.x; preserve the current entry, inspect it, then relaunch`
- **After:** `yolo found a symlink loop at /home/agent/.x and was about to remove it, but the path changed first, so yolo left it alone. If /home/agent/.x is still a link, remove it with rm /home/agent/.x. Then start the jail again.`
- **Why:** 5 ("preserve the current entry"), and "inspect it" does not say what to do.

### Loop could not be moved aside
- **Where:** `internal/entrypoint/looprepair.go:63`, `internal/entrypoint/looprepair.go:70`, `internal/entrypoint/looprepair.go:74`, `internal/entrypoint/looprepair.go:83`, `internal/entrypoint/looprepair.go:100`, `internal/entrypoint/looprepair.go:146`.
- **Before:** `cannot create a private quarantine beside /home/agent/.x: <err>; leave the link untouched, inspect it, then relaunch`
- **After:** `yolo found a symlink loop at /home/agent/.x but could not move it aside safely (<err>), so it left it alone. Remove it with rm /home/agent/.x, then start the jail again.`
- **Why:** 6 ("quarantine"), 8 (how the repair works). "Leave the link untouched" contradicts the only fix.

### Something new appeared where the loop was
- **Where:** `internal/entrypoint/looprepair.go:112`, `internal/entrypoint/looprepair.go:115` (each text appears twice in the file).
- **Before:** `refusing to remove changed entry for /home/agent/.x; the moved entry was preserved at /home/agent/.yolo-q/x and the concurrent destination was left untouched (<err>); inspect both paths before relaunch`
- **After:** `yolo moved the looping link /home/agent/.x to /home/agent/.yolo-q/x, then found something new at /home/agent/.x, so it stopped. Nothing was deleted. Keep what you want from the two paths, delete the rest, then start the jail again.`
- **Why:** 5 ("concurrent destination"), 7.

### Moved loop could not be deleted
- **Where:** `internal/entrypoint/looprepair.go:137`, `internal/entrypoint/looprepair.go:142`.
- **Before:** `the verified loop was moved out of /home/agent/.x but could not be unlinked; it is preserved at /home/agent/.yolo-q/x; inspect it, then relaunch: <err>`
- **After:** `yolo moved the looping link out of /home/agent/.x to /home/agent/.yolo-q/x but could not delete it (<err>). Delete it with rm /home/agent/.yolo-q/x, then start the jail again.`
- **Why:** 5 ("unlinked", "verified loop"). The empty-folder case takes `rmdir <folder>` the same way.

### Graphics driver list not saved
- **Where:** `internal/entrypoint/graphics.go:86` — printed when GPU support is on and yolo cannot write its driver list.
- **Before:** `write graphics driver boot provenance /run/yolo/graphics-drivers.json: <err>; check that /run/yolo is mounted writable and has free space/inodes, repair the mount or permissions, then relaunch`
- **After:** `yolo could not save the list of graphics drivers it set up (/run/yolo/graphics-drivers.json: <err>). /run/yolo is yolo's own folder for this launch, so restart the jail: the human runs yolo stop on the host, then yolo. If it fails again, report it with this message: <issues URL>`
- **Why:** 5 ("boot provenance", "free space/inodes"). The user does not own that mount and cannot "repair" it.

### Graphics drivers could not be set up
- **Where:** `internal/entrypoint/graphics.go:260`, `internal/entrypoint/graphics.go:264`, `internal/entrypoint/graphics.go:268`, `internal/entrypoint/graphics.go:274`, `internal/entrypoint/graphics.go:285`, `internal/entrypoint/graphics.go:296`.
- **Before:** `stage driver mesa for /run/opengl-driver in /run: <err>; ensure the mount is writable with free space/inodes, repair the mount or permissions, then relaunch`
- **After:** `yolo could not set up the graphics driver mesa at /run/opengl-driver (<err>). Restart the jail: the human runs yolo stop on the host, then yolo. If it fails again, report it with this message: <issues URL>`
- **Why:** 5 ("stage", "publish", "inodes"), same reason as above.

### Graphics driver list missing from the packages or image
- **Where:** `internal/entrypoint/graphics.go:109`, `internal/entrypoint/graphics.go:123`.
- **Before:** `image is missing /etc/yolo-jail/graphics-drivers.json; rebuild the jail image with this yolo version and retry`
- **After:** `The jail image has no graphics driver list (/etc/yolo-jail/graphics-drivers.json), so it was built by an older yolo. Restart the jail so yolo builds a current image: the human runs yolo stop on the host, then yolo. If YOLO_ALLOW_STALE_IMAGE is set on the host, unset it first.`
- **Before (store packages):** `store-delivered packages have no share/yolo-jail/graphics-drivers.json metadata in their declared profiles; rebuild the package profile with this yolo version and retry`
- **After:** `The packages yolo took from the Nix store have no graphics driver list (share/yolo-jail/graphics-drivers.json). Restart the jail so yolo builds them again for this yolo version. To use the image's packages instead, unset YOLO_STORE_PACKAGES on the host.`
- **Why:** 5 ("store-delivered", "declared profiles"). No command rebuilds the image on its own (`internal/cli/dispatch.go` has none), so "rebuild the jail image" has no spelling the user can find. A launch builds the image.

### Graphics driver list unreadable
- **Where:** `internal/entrypoint/graphics.go:134`, `internal/entrypoint/graphics.go:140`, `internal/entrypoint/graphics.go:144`, `internal/entrypoint/graphics.go:147`, `internal/entrypoint/graphics.go:151`.
- **Before:** `decode graphics driver metadata /etc/yolo-jail/graphics-drivers.json: drivers must be an array; rebuild the package profile or image and retry`
- **After:** `yolo could not read the graphics driver list in /etc/yolo-jail/graphics-drivers.json (drivers must be a list). yolo built this file, so this is a yolo bug. Restart the jail once. If it fails again, report it with this message: <issues URL>`
- **Why:** 5 ("decode", "metadata"). The user cannot repair a file yolo generated.

## In-jail tools (`cmd/`)

### yolo-ps: not set up in this jail
- **Where:** `cmd/yolo-ps/main.go:53` — printed when `yolo-ps` runs in a jail where the host-processes feature is not turned on.
- **Before:**
  ```text
  yolo-ps: no host-processes endpoint in this jail, so nothing ran.
    The host-processes loophole is not running here: its pack is not selected.
    Ask for this project:   yolo request host-processes view <process names>
    then the human selects the pack, on the host:   yolo pack add host-processes
    and restarts the jail, answering y at its config-change prompt.
  ```
- **After:**
  ```text
  yolo-ps: host process viewing is not turned on in this jail, so nothing ran.
    1. On the host, the human adds it:            yolo pack add host-processes
    2. Then this project asks to see processes:   yolo request host-processes view <process names>
    3. On the host, in this workspace:            yolo config apply   (answer y)
    4. Restart the jail.
  If step 2 says your config does not allow asking, it prints the yolo allow command to run on the host first.
  ```
- **Why:** 6 ("endpoint", "loophole", "selected"), and the order is wrong today (see Wrong today). `yolo request` only looks at packs already selected, so it fails before `yolo pack add`. When the user's config does not allow the request, `yolo request` itself prints `yolo allow host-processes on_request view` (`reportRequestSkipped` in `internal/cli/request.go`), so the message does not need to repeat it.

### yolo-journalctl / yolo-log: not set up in this jail
- **Where:** `cmd/yolo-journalctl/main.go:138`, `cmd/yolo-log/main.go:103` — printed when the tool runs in a jail where the feature is not turned on.
- **Before:**
  ```text
  yolo-journalctl: the host journal bridge is not running in this jail, so nothing ran.
    YOLO_SERVICE_JOURNAL_ENDPOINT is not set: the journal pack is not selected here.
    The human selects it, on the host:   yolo pack add journal
  ```
- **After:**
  ```text
  yolo-journalctl: reading the host's journal is not turned on in this jail, so nothing ran.
    On the host, the human adds it:   yolo pack add journal
  ```
  The remaining step lines stay as they are.
- **Why:** 1 (the variable name tells the user nothing), 6 ("bridge", "selected").

### yolo-serial: not set up in this jail
- **Where:** `cmd/yolo-serial/main.go:174` — the same case for `yolo-serial`.
- **Before:** `yolo-serial: no endpoint, so nothing ran.  The serial loophole is not running in this jail.`
- **After:** `yolo-serial: serial device access is not turned on in this jail, so nothing ran.`
- **Why:** 6 ("endpoint", "loophole").

### Dial failure with no next step
- **Where:** `cmd/yolo-ps/main.go:206`, `cmd/yolo-journalctl/main.go:168` — printed when the tool cannot connect for any other reason.
- **Before:** `yolo-ps: cannot reach the host-processes daemon named by %s: %v`
- **After:** `yolo-ps: cannot connect to the host-processes service (%v). Restart the jail. If that does not help, run `yolo loopholes list` on the host to see whether the service is running.`
- **Why:** the happy-path rule: the message has no next step. `yolo-log` and `yolo-serial` already give one in this case.

### yolo-cglimit: not set up in this jail
- **Where:** `cmd/yolo-cglimit/main.go:75` — printed when `yolo-cglimit` runs in a jail without the cgroup feature.
- **Before:**
  ```text
  Error: cgroup delegation not available — host daemon socket not found.
  The cgroup delegate is a loophole shipped by the cgroup-delegate pack, and it runs
  whenever that pack is selected. On the host, select it:
    yolo pack add cgroup-delegate
  Then restart the jail. It needs a Linux host with cgroup v2; if the pack is already
  selected, the jail's launch output says why the delegate did not start.
  ```
- **After:**
  ```text
  Error: resource limits are not turned on in this jail.
  On the host, add the pack that turns them on:
    yolo pack add cgroup-delegate
  Then restart the jail. This works only on a Linux host with cgroup v2.
  If the pack is already added, the jail's launch output says why it did not start.
  ```
- **Why:** 6 ("delegation", "loophole", "socket", "selected"), 8.

### yolo-cglimit: daemon unreachable, unknown option
- **Where:** `cmd/yolo-cglimit/main.go:145`, `cmd/yolo-cglimit/main.go:205` — printed when the request fails, or when an option is mistyped.
- **Before:** `Error: failed to contact cgroup daemon: %v` / `Unknown option: %s`
- **After:** `Error: cannot reach the host's resource-limit service (%v). Restart the jail.` / `Error: unknown option %s. Run `yolo-cglimit --help` for the options.`
- **Why:** 6 ("daemon"), and the happy-path rule: neither message names a step.

### yolo-cglimit help footer
- **Where:** `cmd/yolo-cglimit/main.go:56` — the last line of `yolo-cglimit --help`.
- **Before:** `Resource limits are enforced by the kernel via cgroup v2 and cannot be exceeded.` / `The host-side daemon handles all privileged cgroup operations securely.`
- **After:** `The kernel enforces these limits, so the command cannot go over them.` (drop the second line)
- **Why:** 8. Note that the integration suite searches this text for `--cpu`, so keep the options block.

### yolo-journalctl help: grants
- **Where:** `cmd/yolo-journalctl/main.go:67` — part of `yolo-journalctl --help`.
- **Before:** `A project's `read` grant reads your own journal (--user is forced); a `read_all` grant passes the arguments unchanged.  Without a grant the bridge refuses and names the step: `yolo request journal read`.`
- **After:** `With `read` access, you see only your own user journal (yolo adds --user). With `read_all` access, yolo passes your arguments as they are. Without access, yolo-journalctl refuses and tells you to run `yolo request journal read`.`
- **Why:** 4 ("a grant reads"), 6 ("bridge"), 7.

### yolo-log help: grants
- **Where:** `cmd/yolo-log/main.go:52` — part of `yolo-log --help`.
- **Before:** `Under a project's `read` grant the bridge reads only `show` and `stream`, prints ndjson, and sends only entries logged by processes running as the macos-user sandbox account — this sandbox's own processes, and any other sandbox's on this Mac.`
- **After:** `With `read` access, only `show` and `stream` work. The output is one JSON object per line. It holds only what the sandbox account logged, which covers this sandbox and every other sandbox on this Mac.`
- **Why:** 6 ("bridge", "macos-user"), 7.

### yolo-serial subcommand usage lines
- **Where:** `cmd/yolo-serial/main.go:213` and the `write`, `monitor` and `pty` lines after it — printed when a device or data argument is missing.
- **Before:** `usage: yolo-serial read <device> [options]`
- **After:** `yolo-serial read needs a device, such as /dev/ttyUSB0. Usage: yolo-serial read <device> [options]. Run `yolo-serial --help` for the options.`
- **Why:** 3, and the happy-path rule: the line does not say what was missing or where to look.

### yolo-serial symlink warning
- **Where:** `cmd/yolo-serial/main.go:321` — printed when `--link` cannot be created.
- **Before:** `yolo-serial: warning: failed to create symlink %s -> %s: %v`
- **After:** `yolo-serial: warning: could not create the link %s (%v). Point your serial tool at %s instead.`
- **Why:** the happy-path rule: the next step is in the banner printed below, but this warning should name it.

### yolo-jaild usage
- **Where:** `cmd/yolo-jaild/main.go:80` — printed when `yolo-jaild` is run by hand without a subcommand.
- **Before:** `usage: yolo-jaild <supervise|oauth-terminator|…|durable-size> [args...]`
- **After:** `yolo-jaild runs yolo's background services inside the jail, and yolo starts it for you. Usage: yolo-jaild <supervise|oauth-terminator|…|durable-size> [args...]`
- **Why:** rare, but the line gives a human no hint that they never need to run this.

## Host apply: `yolo host apply` output from this partition

`yolo host apply` prints one row per agent config file: `<agent>/<name>  <action>  <path>` (`internal/cli/apply.go`). The action texts below come from `internal/entrypoint`. Many name yolo's internal model ("surface", "derive", "census", "computed layer"), and several refusals give no next step.

### Skipped: nothing written for this file
- **Where:** `internal/entrypoint/hostrender.go:445`.
- **Before:** `skipped: nothing is rendered for this surface`
- **After:** `skipped: no pack writes anything to this file`
- **Why:** 6 ("surface", "rendered").

### Skipped: workspace-only settings
- **Where:** `internal/entrypoint/hostrender.go:519`.
- **Before:** `skipped: only ${workspace}-keyed keys, which have no host referent`
- **After:** `skipped: every setting for this file is tied to a workspace, and a host file is not`
- **Why:** 5 ("${workspace}-keyed keys", "host referent").

### Skipped: pack list entries for a file yolo does not manage
- **Where:** `internal/entrypoint/hostrender.go:477`.
- **Before:** ``skipped: config-list has no effect — this surface is declared `unrendered`, so yolo writes no file to append to``
- **After:** `skipped: packs add entries to this file, but yolo does not manage it, so their entries were not added`
- **Why:** 6 ("config-list", "surface", "unrendered").

### Skipped: a condition on another file is not met
- **Where:** `internal/entrypoint/surfaceloop.go:107` — also printed in the jail.
- **Before:** `claude/x not rendered: no entry of claude/settings mcpServers matches "foo"`
- **After:** `claude/x was not written: it is written only when ~/.claude/settings.json has an mcpServers entry matching "foo", and it has none.`
- **Why:** 6 ("surface" ID as a location), 5.

### Refused: this kind of file has no meaning here
- **Where:** `internal/entrypoint/hostrender.go:542`, text from `render.Excludes` (`internal/render/modes.go`).
- **Before:** `refused: capture has no stated meaning at this confinement level`
- **After:** `refused: yolo does not write this kind of file on the host. This is a bug in the pack that declares it. Report it with this line: <issues URL>`
- **Why:** 6 ("capture", "confinement level"). The text lives outside this partition; this site is where host users see it.

### Refused: yolo cannot write this file the way the pack asks
- **Where:** `internal/entrypoint/hostrender.go:550`.
- **Before:** ``refused: this contract's census renders a surface declaring capture through copy, which `yolo host apply` does not implement``
- **After:** `refused: yolo host apply cannot write this file in the way it is declared. This is a yolo bug. Report it with this line: <issues URL>`
- **Why:** 6 ("contract", "census", "surface"). The stop names who can act.

### Refused: provider settings could not be worked out
- **Where:** `internal/entrypoint/hostrender.go:569`.
- **Before:** `refused: its derive failed at the host (<err>) — the file is untouched`
- **After:** `refused: yolo could not work out this file's provider settings (<err>). The file is unchanged. Fix what the error names, then run yolo host apply again.`
- **Why:** 4 ("derive" as a noun), 6, no next step.

### Refused: a jail-only path in host settings
- **Where:** `internal/entrypoint/hostrender.go:579`.
- **Before:** `refused: its computed layer names a path that exists only inside a jail (/home/agent/x), and yolo never writes one into your real home — the file is untouched`
- **After:** `refused: the settings yolo worked out for this file name a path that exists only inside a jail (/home/agent/x). yolo does not write such a path into your real home, so the file is unchanged. Report it with this line: <issues URL>`
- **Why:** 6 ("computed layer"), no next step.

### Ignored key
- **Where:** `internal/entrypoint/hostrender.go:1594`.
- **Before:** `model IGNORED (declared by matt, owned by claude-auth)`
- **After:** `model: ignored. The matt pack sets it, but the claude-auth pack decides it.`
- **Why:** 5 ("declared by", "owned by"), P8 (capitals as severity).

### Broken link at the file's path
- **Where:** `internal/entrypoint/hostbrokenlink.go:62` — printed by `yolo host apply` and in the jail.
- **Before:** ``~/.claude/settings.json is a symlink to /x/settings.json, whose directory does not exist — remove the link (`rm ~/.claude/settings.json`) or recreate /x, then apply again; nothing was written there``
- **After:** ``~/.claude/settings.json is a link to /x/settings.json, and /x does not exist, so nothing was written. Remove the link (`rm ~/.claude/settings.json`) or create /x, then run `yolo host apply` again.``
- **Why:** 7, and "apply again" should name the command. In the jail the last step is "start the jail again".

### Local pack briefing not moved: `from` names the old file
- **Where:** `internal/entrypoint/hostbriefing.go:923`.
- **Before:** ``~/.config/yolo-jail/local/pack.json contributes[0] names "AGENTS.md" as its briefing `from`, and a root AGENTS.md is no longer read as pack prose — so the moved file would be named by no declaration and reach EVERY agent, not the audience that contribution routes it to. Change that `from` to "briefing/local.md" in ~/.config/yolo-jail/local/pack.json, then re-run `yolo host apply` to move the file``
- **After:** ``~/.config/yolo-jail/local/pack.json: its first briefing entry reads from "AGENTS.md". yolo no longer reads a top-level AGENTS.md in a pack, so moving the file now would send it to every agent instead of the ones that entry names. Change that `from` to "briefing/local.md", then run `yolo host apply` again.``
- **Why:** 5 ("contributes[0]", "pack prose", "declaration"), 6, 7.

### Local pack briefing not moved: both files exist
- **Where:** `internal/entrypoint/hostbriefing.go:936`.
- **Before:** `both ~/.config/yolo-jail/local/AGENTS.md and ~/.config/yolo-jail/local/briefing/local.md exist, and yolo will not choose between them. Merge what you want to keep into … and delete … — a root AGENTS.md is no longer read as pack prose`
- **After:** `Both ~/.config/yolo-jail/local/AGENTS.md and ~/.config/yolo-jail/local/briefing/local.md exist, and yolo does not choose between them. yolo no longer reads the first one. Copy what you want to keep into the second, delete the first, then run yolo host apply again.`
- **Why:** 6 ("pack prose"), and the last step.

### Old briefing files not cleaned up
- **Where:** `internal/entrypoint/hostbriefing.go:1041`.
- **Before:** `host briefing prune: refusing to prune with an unknown active pack set`
- **After:** `yolo host apply: yolo could not tell which packs are in use, so it removed no old briefing files. Run yolo host apply again. If this repeats, report it with this message: <issues URL>`
- **Why:** 5 (headline shorthand), 6, no next step.

### Revert finished but its record file stayed
- **Where:** `internal/entrypoint/hostrevert.go:275`, `internal/entrypoint/hostrevert.go:279`, `internal/entrypoint/hostrevert.go:286`, `internal/entrypoint/hostrevert.go:296` — printed by `yolo host apply --revert`.
- **Before:** `claude/settings: removing the provenance record <path>: <err>`
- **After:** `claude/settings: yolo removed its settings from ~/.claude/settings.json, but could not delete its record file <path> (<err>). Delete it yourself: rm <path>`
- **After (last site only, the saved-edit files):** add `If it stays, a later yolo host apply treats the removed settings as ones you deleted.`
- **Why:** P9 (record names, no step). The settings file is written before these removals. Only the last site has a consequence the code states (the comment above the `forget` loop), so only it gets the extra clause.

## Briefing and skills (agent-facing)

### Provisioning failed section
- **Where:** `internal/jailcontent/briefing.go:595` — **agent-facing**; at the top of the briefing after a failed provisioning step.
- **Before:** `The last boot's provisioning failed — project tools may be missing. Read `/workspace/.yolo/startup.log` and self-serve (e.g. run `mise install` in /workspace, then re-run the step that failed).`
- **After:** `Setting up this jail's tools failed at the last start, so some project tools may be missing. Read `<workspace>/.yolo/startup.log` to see what failed. Then fix it yourself: for example, run `mise install` in the workspace and run the failed step again.`
- **Why:** 5 ("self-serve"), and see Wrong today: the path is hard-coded to `/workspace`.

### Skills section: "promote to the host"
- **Where:** `internal/jailcontent/briefing.go:860` — **agent-facing**.
- **Before:** `User-level skills dirs (`~/.<agent>/skills/`) are **read-only** in-jail (kernel-enforced); workspace-level ones (`/workspace/.<agent>/skills/`) are writable — develop there, then ask the human to promote to the host.`
- **After:** `You cannot write the skills folders in your home (`~/.<agent>/skills/`). You can write the workspace's (`/workspace/.<agent>/skills/`), so write new skills there. To use a skill in every workspace, ask the human to add it to a pack.`
- **Why:** 5 ("promote to the host" does not say what to do). AGENTS.md says a skill in the host's `~/.claude/skills` reaches no jail, so "the host" points the wrong way. The "add it to a pack" wording should be checked with the owner. The local pack at `~/.config/yolo-jail/local` is the documented way.

### Limitations: credentials
- **Where:** `internal/jailcontent/briefing.go:845` — **agent-facing**.
- **Before:** `… Only without such jail-local credentials do authenticated operations fail.`
- **After (when the git or GitHub broker runs):** `Host credentials are not copied into the jail. The host's `~/.ssh`, `~/.gitconfig` and tokens are not visible here. Git and gh can still use them through yolo's brokers (see **git remotes** and **gh** below). Other authenticated calls need a credential of the jail's own, such as a token in `.env`.`
- **Why:** 7, and see Wrong today.

### Storage section: no durable folder this launch
- **Where:** `internal/jailcontent/persistencesection.go:240` — **agent-facing**; in the storage section when the launch could not make the durable folder.
- **Before:** ``**No durable directory this launch**: `.yolo/durable` is a symbolic link. Tell the user. Until it is fixed, put nothing that must survive a restart under `/tmp`, and nothing in `/workspace` without asking: it is the user's project.``
- **After:** ``**No durable folder this launch**: `.yolo/durable` is a symbolic link. Tell the user. Once they fix that on the host, the next launch makes the folder. Until then, put nothing that must survive a restart under `/tmp`. Put nothing in `/workspace` without asking, because it is the user's project.``
- **Why:** 7, and the stop should say what fixing it leads to. The rest of the storage section was read in full and reads well; no other rewrite is proposed.

### Workspace skills: a link that leaves the workspace
- **Where:** `internal/jailcontent/workspaceskills.go:1171`, `internal/jailcontent/workspaceskills.go:1173` — the reason in the launch line `Workspace skills: refused <path> — <reason>`.
- **Before:** `Workspace skills: refused .claude/skills/x — a symlink that resolves outside the workspace; nothing it points at was read`
- **After:** `Workspace skills: skipped .claude/skills/x — it is a link to a place outside the workspace, so yolo did not read it. Copy the skill into the workspace instead of linking to it.`
- **After (chain):** `… it is a chain of links too long to follow, and it may loop. Fix the link.`
- **Why:** no next step. "Refused" is also stronger than what happened (P8).

### Workspace skills: too big to copy
- **Where:** `internal/jailcontent/workspaceskills.go:821`, `internal/jailcontent/workspaceskills.go:826`.
- **Before:** `copying it would pass this launch's cap of 32 MiB on workspace skills: it adds at least 2 MiB to the 31 MiB already copied, so none of it is delivered`
- **After:** `it is too big to copy. Skills from the workspace may use 32 MiB in all, and this one would add at least 2 MiB to the 31 MiB already copied, so yolo copied none of it. Make the skill smaller, or move large files out of its folder.`
- **Why:** 5 ("cap", "delivered"), no next step. The limits are fixed in code (`maxWorkspaceSkillBytes`, `maxWorkspaceSkillEntries`), so the only step is a smaller skill.

### Workspace skills: path too long, unreadable, changed
- **Where:** `internal/jailcontent/workspaceskills.go:898`, `internal/jailcontent/workspaceskills.go:1347`, `internal/jailcontent/workspaceskills.go:1337`, `internal/jailcontent/workspaceskills.go:1322`.
- **Before:** `its path inside the skill is longer than 512 bytes, more than every agent's skills directory can be counted on to hold`
- **After:** `its path inside the skill is longer than 512 bytes, which some agents cannot read. Shorten the file or folder names.`
- **Before:** `could not be staged (no space left on device)`
- **After:** `yolo could not copy it (no space left on device). Fix that on the host, then start the jail again.`
- **Before:** `changed while it was being read`
- **After:** `it changed while yolo was copying it. Start the jail again to copy it.`
- **Why:** 5 ("staged", "counted on to hold"), no next step.

### jail-mechanics: the opening lines
- **Where:** `internal/jailcontent/builtinskills/templates/index.tmpl:15`, `internal/jailcontent/builtinskills/templates/index.tmpl:12` — **agent-facing**; the first screen of the `jail-mechanics` skill.
- **Before:** ``Run `yolo config drift` to see what is pending now. The command's own result is authoritative if runtime probes narrow an expected outcome.``
- **After:** ``Run `yolo config drift` to see which changes are waiting for a restart. If a command's report disagrees with the steps here, trust the command.``
- **Before:** ``- Restart: `yolo stop`.``
- **After:** ``- Restart: the human runs `yolo stop` on the host, then `yolo`.``
- **Why:** 5 ("runtime probes narrow an expected outcome"). `yolo stop` alone stops the jail. It runs on the host, and the user must start the jail again.

### jail-mechanics: "the command's report wins" in every step
- **Where:** `internal/jailcontent/mechanicsrender.go:407`, `internal/jailcontent/mechanicsrender.go:372`, `internal/jailcontent/mechanicsrender.go:446` — **agent-facing**; the `recipes/workspace-config.md` file.
- **Before:** ``3. Expected: the change applies now in every session after `yolo config apply`.  The command's report wins.``
- **After:** ``3. After `yolo config apply`, the change works at once in every session.``
- **Before (lead):** `4. The command's report wins over the expected outcomes below.`
- **After (lead):** `4. If yolo config apply reports something different from the steps below, trust its report.`
- **Before (loophole step):** ``Restart using `yolo stop` so the switch is read at launch. The command's report wins.``
- **After:** ``Ask the human to restart the jail (`yolo stop` on the host, then `yolo`), so the pack change takes effect.``
- **Why:** 5 ("wins" as shorthand), and say it once in the lead. Most change-timing rows carry no reason, so the "Expected" line prints two spaces before "The command's report wins". The loophole step says "the switch", but the same step says loopholes have no switch.

### jail-mechanics: the path map and delivery words
- **Where:** `internal/jailcontent/mechanicsrender.go:294`, `internal/jailcontent/mechanicsrender.go:473`, `internal/jailcontent/mechanicsrender.go:477`, `internal/jailcontent/mechanicsrender.go:330` — **agent-facing**; `host-map.md` and `recipes/skills-and-briefing.md`.
- **Before:** `| Path | Original host source | Backing source | Mode | Lifetime / class | Delivery |`
- **After:** `| Path | On the host | Stored in | Access | Kept until | How it arrives |`
- **Before:** `2. Delivery is live read-only bind; the map lists exact paths and lifetimes.` / ``3. The next `yolo` entry from `/home/u/project` refreshes the live staging.``
- **After:** ``2. These files are shared read-only from the host; `host-map.md` lists each path. 3. The next `yolo` run in `/home/u/project` updates them.``
- **Before:** `Home rest-of-tree policy: read-only outside listed writable mounts.`
- **After:** `The rest of your home folder is read-only, apart from the writable paths listed above.`
- **Why:** 5 (noun stacks: "Lifetime / class", "rest-of-tree policy", "live staging"), 4 ("entry"). The values (`live read-only bind`, `baked image`) come from `internal/cli/run/mechanicsfacts.go`, outside this partition, and need the same plain words.

### jail-mechanics: the tools recipe
- **Where:** `internal/jailcontent/mechanicsrender.go:487`, `internal/jailcontent/mechanicsrender.go:489` — **agent-facing**; `recipes/tools.md`.
- **Before:**
  ```text
  For a one-off tool, install it in the current environment; that installation follows the path lifetimes in `host-map.md`. Permanent tool delivery for this launch is baked image.
  For a permanent image package, edit `packages`, validate with `yolo check --no-build`, then ask the human to run `yolo config apply` and restart using `Please run `yolo stop` in `/home/test/project` on the host; this ends the jail's sessions.`; the image rebuilds first.
  ```
- **After:**
  ```text
  For a one-off tool, install it now; `host-map.md` says whether it survives a restart.
  This jail's permanent packages are built into its image. To add one:
  1. Run `yolo config add install.nix <package>`, then `yolo check --no-build`.
  2. Please run `yolo config apply` in `/home/test/project` on the host.
  3. Please run `yolo stop` in `/home/test/project` on the host, then `yolo`; this ends the jail's sessions. The image is rebuilt first.
  ```
- **Why:** 5 ("Permanent tool delivery … is baked image"), P3 (`packages`), and see Wrong today: the handoff sentence is pasted inside backticks.

### jail-mechanics: the packs and loopholes recipe
- **Where:** `internal/jailcontent/mechanicsrender.go:471` — **agent-facing**; `recipes/packs-and-loopholes.md`.
- **Before:** ``Selected packs for this jail: claude. Edit the packs list in `~/.config/yolo-jail/config.jsonc`, run `yolo check` there, then start a fresh `yolo` launch from `/home/test/project`; workspace `yolo config apply` does not apply user-scope pack changes. For brokered repository scope, edit `brokered.<source>.repos`; its timing is shown for `brokered` in the workspace-config recipe. A loophole enable/disable is a host-side switch, not a pack selection; use `yolo config-ref` for the exact switch command.``
- **After:**
  ```text
  Packs in this jail: claude.
  To add or remove a pack, the human runs `yolo pack add <pack>` or `yolo pack remove <pack>` on the host, then starts the jail again from `/home/test/project`. `yolo config apply` does not apply pack changes.
  A loophole runs whenever its pack is in use. To ask for something a loophole may do, run `yolo request <pack> <kind>` here; the human then runs `yolo config apply` on the host.
  To let gh or git reach another repository, run `yolo request <source> repos OWNER/REPO`.
  ```
- **Why:** 6 ("selected", "brokered repository scope"), 7, and see Wrong today: the last sentence is false.

### configuring-the-jail: examples in the old JSONC spelling
- **Where:** `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:137`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:146`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:130`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:132`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:53`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:57` — **agent-facing**.
- **Before:**
  ````text
  ```jsonc
  "mise_tools": { "neovim": "stable", "kubectl": "latest" }
  ```
  ```jsonc
  "packages": ["ffmpeg", "postgresql"]
  ```
  ````
- **After:**
  ````text
  ```toml
  [install]
  mise = { neovim = "stable", kubectl = "latest" }
  nix = ["ffmpeg", "postgresql"]
  ```
  Or with a verb: `yolo config add install.nix ffmpeg`.
  ````
- **After (others):** `"packs": ["claude"]` becomes `yolo pack add claude`. `"mcp_servers": { "foo": null }` becomes `mcp_servers.foo = "off"`. The key lists name `install.nix`, `install.mise` and `env.files` instead of `packages`, `mise_tools` and `env_sources`, and drop `loopholes` (the same skill says no loophole has a switch).
- **Why:** P3. The skill's own opening says the verbs write `yolo-jail.toml`, then every example is JSONC. The mounts examples (`"mounts": ["~/code/sysadmin"]`, the object form) and the pack briefing example in `pack.json` style need the same change.

### configuring-the-jail: history and internals
- **Where:** `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:189`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:282`, `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:61`, `internal/jailcontent/mechanicsrender.go:73` — **agent-facing**.
- **Before:** `**Not true before 2026-08-17**: if you "know" the host's localhost is unreachable from a jail, that memory is stale.`
- **After:** `If you think the host's localhost cannot be reached from a jail, check: it can.`
- **Before:** `When you touch packages (or trigger vaapi), say so in your handoff — on the yolo-jail dev repo a rebuild also needs a host just load.`
- **After:** `When you change packages, tell the user that the next start rebuilds the image.`
- **Cut:** "Every one of these was a real wrong belief that reached a config edit …" and "The change-timing recipe is generated from this jail's launch decisions."
- **Why:** 8 (dates, origin stories, and yolo's own dev repo are not the reader's concern).

### configuring-the-jail: the opening run-on
- **Where:** `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:28` — **agent-facing**.
- **Before:** `That command uses the launch's same approval gate; approved read-only and user-scope read-write mounts under /ctx, and a running GitHub broker's repository scope, can apply live, while other changes wait for the next restart. It never restarts the jail or offers to do so. Assume that. This skill covers the parts that rule leaves out.`
- **After:** `That command asks the human the same question a launch does. Some changes apply at once: approved mounts under /ctx, and the repositories gh may reach. Other changes wait for the next restart. The command never restarts the jail.`
- **Why:** 7, 5 ("approval gate", "repository scope").

### diagnosing-the-jail: internals an agent does not need
- **Where:** `internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md:70`, `internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md:81`, `internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md:119` — **agent-facing**.
- **Before:** `This backend never applies ACLs during a launch — the walk is ~0.16ms per object, so seconds to tens of seconds on a large repo — which is why the fix is a command you run once rather than something a launch does for you.`
- **After:** `A launch does not fix these permissions for you; run the command once.`
- **Before:** the PATH-order paragraph about `~/.yolo/bin/block/` and `~/.yolo/bin/launch/`.
- **After:** `Blocked tools are in ~/.yolo/bin/block/. Programs yolo installs on first use are in ~/.yolo/bin/launch/.`
- **Before:** `the jail-facing services yolo itself ships would have refused the launch`
- **After:** `yolo's own services would have stopped the launch if they could not be reached`
- **Why:** 8, 6 ("jail-facing services").

### diagnosing-the-jail: "what's enabled"
- **Where:** `internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md:96` — **agent-facing**.
- **Before:** `yolo loopholes list      # what's enabled + status`
- **After:** `yolo loopholes list      # which loopholes this jail has, and whether each is running`
- **Why:** 5, and see Wrong today.

## Wrong today

- `internal/entrypoint/prism.go:1497`: every "dropping from …" line tells the user to declare the entry in `~/.config/yolo-jail/config.jsonc`. The user config is TOML now (`config.toml`). Decision RC-D40 in `docs/design/readable-config.md` rules: "never a hard-coded `config.jsonc`". A TOML user is sent to a file they do not have.
- `internal/entrypoint/prism.go:1671`: tells the user to set the `profile` key. The TOML spelling is `active_provider`, and the verb is `yolo active-provider <agent> <provider>` (RC-D40, RC-D52).
- `internal/entrypoint/providerkeys.go:131`: points to the legacy `profile` key in the same way.
- `internal/entrypoint/requires.go:78`: says `packages` in `yolo-jail.jsonc`. The TOML key is `install.nix` (this repo's own `yolo-jail.toml` uses `[install] nix = [...]`).
- `internal/entrypoint/shims.go:92`: `packages` is the legacy key here too.
- `internal/entrypoint/catrust.go:285`, `internal/entrypoint/egress.go:611`, `internal/entrypoint/boot.go:357`, `internal/entrypoint/prism.go:1703`: `env_sources` is the legacy key. The TOML spellings are `env.files` and `env.sources` (RC-D50), and the verb is `yolo config add env.files <file> --user`.
- `internal/jailcontent/briefing.go:595` (**agent-facing**): the "Provisioning failed" section always names `/workspace/.yolo/startup.log` and "in /workspace". On macOS sandboxes (macos-user) the same briefing says "There is no `/workspace` on this backend", and the log is at `<workspace>/.yolo/startup.log` (the `diagnosing-the-jail` skill says so). The section does not check `IsMacOS` or the workspace path the way the Environment section does.
- `internal/jailcontent/briefing.go:845` (**agent-facing**): "Only without such jail-local credentials do authenticated operations fail" is false when the git broker or GitHub broker runs. In that case `git fetch`/`push` and `gh` authenticate with the host's credentials and need no credential in the jail. The section prints unconditionally, so the same briefing contradicts its own "`git` remotes in this jail" section.
- `internal/entrypoint/reachability.go:574`: the first line reads "CONTINUING with service 'x' is unusable from inside this jail". The sentence does not parse (`serviceListPhrase` returns "service 'x' is"). The facts are right, but the line is garbled.
- `cmd/yolo-ps/main.go:53`: the steps are in the wrong order. The message says to run `yolo request host-processes view …` first and `yolo pack add host-processes` second. `yolo request` looks only at packs already selected (`allowSelectedBrokers` in `internal/cli/allow.go`). With the pack not selected it fails with "no selected pack declares a broker source or grant kinds named "host-processes"" and says to run `yolo pack add <pack>` first. So the first command the message gives always fails. The message also skips `yolo config apply`, which `yolo request` says is how the request is approved.
- `internal/jailcontent/mechanicsrender.go:471` (**agent-facing**): `recipes/packs-and-loopholes.md` says a loophole is turned on by a host-side switch and points to `yolo config-ref` "for the exact switch command". There is no such switch. The same file's workspace-config recipe says "`yolo loopholes enable` and `disable` are retired" (`internal/jailcontent/mechanicsrender.go:441`), and `configuring-the-jail` says "No loophole has a switch". A loophole runs whenever its pack is selected. The change-timing reason "this loophole is enabled by its host-side switch" (`internal/changetiming/changetiming.go`, outside this partition) makes the same claim.
- `internal/jailcontent/mechanicsrender.go:489` (**agent-facing**): `changetiming.HostCommand` returns a whole sentence with its own backticks ("Please run `yolo stop` in `…` on the host; this ends the jail's sessions."). The tools recipe wraps that sentence in another pair of backticks, so the rendered Markdown breaks: the golden file `testdata/mechanics-rootless-container.golden` shows ``restart using `Please run `yolo stop` in `/home/test/project` on the host; this ends the jail's sessions.`; the image rebuilds first``. The store-delivered branch on the next line has the same defect. The recipe also says to edit `packages`, the legacy key (P3).
- `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md:297` (**agent-facing**): says the jail's `~/.pi` is stored at `<workspace>/.yolo/home/pi`. That is the legacy layout. `paths.LegacyHomeState` says "No launch creates it", and each setup's home is now `<workspace>/.yolo/setups/<setup>/home` (`internal/paths/setupstate.go`). The storage section of the briefing already gives the new path (`internal/jailcontent/persistencesection.go:144`), so the two texts an agent reads disagree.
- `internal/jailcontent/builtinskills/diagnosing-the-jail/SKILL.md:96` (**agent-facing**): "what's enabled" suggests loopholes are switched on and off. They are not (see the first bullet above).
