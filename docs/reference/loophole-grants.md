---
status: current
verified: 2026-11-02
verified_commit: 14801eccf
covers:
  - internal/loopholedecl/grants.go
  - internal/loopholedecl/keys.go
  - internal/loopholegrant/file.go
  - internal/loopholegrant/live.go
  - internal/loopholegrant/refusal.go
  - internal/config/loopholegrants.go
  - internal/config/loopholegrantgate.go
  - internal/config/loopholegrantlegacy.go
  - internal/config/loopholegrantcarried.go
  - internal/cli/run/loopholegrants.go
  - internal/cli/allow.go
  - internal/cli/request.go
  - internal/loopholes/loopholescmd.go
  - internal/configmigrate/loopholegrants.go
tags: [loopholes, packs, config, grants, approval, reference]
summary: "Selecting a pack installs its loophole and runs its machinery; neither gives any project access. Access is a grant in two layers, the user's ceiling (allow) and the project's request (requests), approved at the config-change gate and recorded per workspace. A host daemon checks the grant per request against a launch-written grant file; an argv-only crossing is admitted at launch only under a grant. default_enabled, the enabled switch and yolo loopholes enable|disable are gone, and the migration carries every access the owner held into a grant."
---

# Loophole grants: a selected pack runs its loophole and grants nothing

**Status:** 2026-11-03. Built and verified against `14801eccf`; of the two decisions of
2026-11-03 under [`legacy-config-compat.md`](../design/legacy-config-compat.md), a legacy
workspace block launches unchanged ([LG-D16](#LG-D16)) and a project's `fields` is its columns
request ([LG-D15](#LG-D15)); `yolo config migrate` writes both (2026-11-04), and the gate's columns
line is not built yet. [OQ-LG5](#OQ-LG5), ruled 2026-11-03 as [LG-D17](#LG-D17), is not built yet. This is the as-built account of the loophole grant system. The design
argument it graduated from, and its build plan, live in git history:

```console
$ git log --diff-filter=D -- docs/design/loophole-access-approval.md docs/design/loophole-access-approval-plan.md
```

Code comments cite this document by section number ([§4.3](#43-each-loopholes-access-and-its-approval)), by decision id (LG-D7, PD-9) and by
build slice (T6, F4); all three resolve here.

> **In short.** Whether a loophole's host machinery *runs* is yolo's business and the user never
> sets it; whether a *project* has *access* through it is always an approval the user gave.
> `packs = ["journal"]` runs the journal bridge in every jail and serves no line of the journal
> to any project until that project holds a grant.

**Start at [§4](#4-the-shape).** [§1](#1-the-verdict-in-four-words) defines the four words the rest
leans on; [§5](#5-every-stop-and-its-next-step) is every message a user meets;
[§6](#6-migration) is how a legacy config becomes grants.

**Reads with:** [`loophole-system.md`](loophole-system.md) (what a loophole is, its trust model
and its manifest), [`boundary-broker.md`](../design/boundary-broker.md) (the brokers, whose grant
model this generalizes, BB-D104 to BB-D110), [`config/user-config.md`](config/user-config.md) (the
`allow` table), [`config/project-config.md`](config/project-config.md) (the `requests` table),
[`pack-system.md`](pack-system.md#loophole) (the manifest keys).

---

## 1. The verdict, in four words

"Enabled" used to carry three jobs. Each word below is *(coined here)* in this sense; each names one
state with one owner.

| Word | Means | Who decides | What the user sees |
| :--- | :--- | :--- | :--- |
| **Selected** | The pack is named in `packs` in the user config, so its code may run on this machine and its loophole is installed. Not *running*: a selected pack's loophole may be inert on this setup. | The user, on the host | The name in `packs`; `yolo pack add`, `yolo pack remove` |
| **Running** | The loophole's host-side machinery is up for this launch: a daemon spawned, a bind or device admitted. Not *granted*: a running daemon with no grant refuses every call. | yolo, from *selected* and the setup ([§4.1](#41-one-rule-for-every-loophole)); never a config key | One disclosure line at launch; nothing to set |
| **Granted** | This project may use the loophole for one named thing (a **grant kind**, below). Not *running*: a grant for an inert loophole serves nothing and says so. | The user: a ceiling in the user config, a request in the project, a y at the gate | `[allow.<pack>]`, `[requests.<pack>]`, the gate's lines, the refusal that names the missing one |
| **Configured** | A parameter of a running loophole that widens or narrows nothing by itself: the AWS profile, the serial baud rate, the `ps` columns. Not a grant: it never admits a project. | The user, in `[allow.<pack>]` | The same table as the ceiling |

Terms the rest of the document leans on:

- **Grant kind** *(coined here)* — one named thing a loophole can be granted for, declared by its
  manifest: `fetch` and `push` for git, `read` for the journal. Not a setting (a setting admits
  nothing) and not a capability in the pack system's `serves` sense (that is supersession, not
  access).
- **Valued kind** *(coined here)* — a grant kind whose grant carries a value the project supplies:
  `devices = ["/dev/ttyUSB*"]`, `view = ["postgres"]`, git's `push = ["main"]`. The value is approved
  line by line at the gate.
- **Ceiling** and **request** — [`config/README.md`](config/README.md#principles)'s words,
  principle 6: `allow` says what every project gets (`always`) and what one may ask for
  (`on_request`); `requests` in a project is the ask. A request outside the ceiling is skipped and
  reported, never an error.
- **Grant-declaring loophole** *(coined here)* — a loophole whose manifest has at least one
  `[[grants]]` entry. One that declares none is **ungated** *(coined here)*.

## 2. Where it lives

| Piece | Owner |
| :--- | :--- |
| The manifest's `[[grants]]` and `access` keys, and their refusals | `internal/loopholedecl` (`grants.go`); the key list is in [`pack-system.md`](pack-system.md#loophole) |
| The grant file, its standing type, its read and live replace, and every refusal text | `internal/loopholegrant`; nothing else parses the file's `grants` key ([PD-3](#PD-3)) |
| Ceiling and request readers, the gate's rows, the approval record's grant part | `internal/config` (`loopholegrants.go`, `loopholegrantgate.go`) |
| The legacy JSONC reading of switches as grants | `internal/config` (`loopholegrantlegacy.go`, `loopholegrantcarried.go`) |
| The launch: write the grant file, gate unmediated crossings, disclose the standing | `internal/cli/run` (`loopholegrants.go`) |
| `yolo allow`, `yolo request` | `internal/cli` (`allow.go`, `request.go`) |
| The retired `yolo loopholes enable|disable` | `internal/loopholes` (`loopholescmd.go`) |
| The migration's loophole rows and its standing layer | `internal/configmigrate` (`loopholegrants.go`) |

## 3. Why there is no switch

The four words had collapsed into one. A broker granted nothing on selection, while the journal
granted everything the moment its switch was on, so one word meant two rules. Removing the switch
and keeping only `packs`, as the readable-config redesign's L4 first did, would have made selection
the grant: `packs = ["journal"]` reading the user's journal from every project on the machine. The
owner refused that in as many words ([Appendix B](#appendix-b-the-owners-words)):
*"the project shouldn't have access until it has been approved."* A valued setting that decided what
a project reached (`serial`'s `allowed_devices`, `host-processes`' `visible`) was a grant wearing a
setting's name, written in the agent's file; and `serial`'s empty list meaning five broad globs made
the one spelling that looked like "nothing" mean "everything". Every in-jail refusal said "turn it
on", so a user who had and still held no access had nowhere to go.

## 4. The shape

### 4.1 One rule for every loophole

**A loophole's machinery runs whenever its pack is selected and this setup can run it, and it
grants nothing until a grant covers the project.** That is [BB-D104](../design/boundary-broker.md#BB-D104)
made the rule rather than the exception. The two login services and `aws-auth` behave this way with
an approval of their own ([§4.3](#43-each-loopholes-access-and-its-approval)), and `cgroup-delegate`,
which can limit nothing but the jail itself, is ungated ([LG-D13](#LG-D13)).

Running is decided by yolo and nothing else:

- **The pack author has no say.** `default_enabled` is refused at load ([§4.5](#45-what-goes-away)).
  Selecting a pack runs its loophole; the author's remaining declaration is what can be granted.
- **The user has no say either**, and there is no key for it. The one visible trace is the
  launch's disclosure line ([§5](#5-every-stop-and-its-next-step)).
- **A lazy start is an optimization yolo may make later** (start the daemon at the first granted
  call) and must change nothing a user sees: not the gate, not a refusal, not a disclosure. It is
  named here so nobody turns it into a knob.
- **The setup decides inertness**: platform, backend and binary
  ([`loophole-system.md`](loophole-system.md#where-a-loophole-does-nothing)). An inert loophole runs
  nothing and its grants serve nothing ([§7](#7-parity-across-backends-and-notches)).

### 4.2 The approval, in two layers

The brokers' grant model, reused whole for every grant-declaring loophole.

1. **The manifest declares the loophole's grant kinds** under `[[grants]]`: a name, whether it is
   valued, and one line of user-facing text the gate and the refusals print ("read your user
   journal"). A top-level `access` names the whole crossing for the refusals ("read the host's
   journal"). Brokers keep their own `brokered.grants`; a manifest with both is refused. The
   shipped ungated set is closed by test ([§11](#11-tests-that-pin-it)).
2. **The user's ceiling**, `[allow.<pack>]` in the user config: `always` lists the kinds every
   project gets; `on_request` lists the kinds a project may ask for; any other key is a declared
   setting. **A valued kind may appear only in `on_request`**: its value is the project's, so there
   is nothing for `always` to carry, and `yolo check` refuses it naming the move
   ([§5](#5-every-stop-and-its-next-step)). `yolo allow <pack> [always|on_request] <kind>` writes it
   (`always` when neither is named) and offers the approval.
3. **The project's request**, `[requests.<pack>]` in the project file: `<kind> = true` for an
   unvalued kind, `<kind> = [...]` for a valued one. The agent may write it; `yolo request <pack>
   <kind> [value...]` writes it from inside the jail and prints the host step.
4. **The gate grants.** At every fresh launch and at `yolo config apply`, the config-change gate
   shows one line per grant the project would hold, in the broker's spelling:
   `journal: read your user journal (from your user config, for every project)`,
   `host-processes: see postgres, redis-server (requested by yolo-jail.toml:14)`. A request the
   ceiling does not admit is shown skipped with the command that admits it:
   `– serial: devices /dev/ttyUSB* (not allowed on request by your config: yolo allow serial on_request devices)`.
   The human's y, `--accept-config-changes`, or `yolo config apply` records the grants in the
   approval record's grant part, `<cname>.loophole-grants.json`, beside the config part
   ([PD-4](#PD-4)). An N records nothing. **An `always` line appears at each project's gate once**,
   and never again until the ceiling changes; the user's edit alone grants no project anything
   ([LG-D12](#LG-D12)).
5. **The grant file.** The launch writes what the gate approved into one launch-stamped file per
   loophole, the private settings snapshot the `{settings}` token names, now carrying settings and
   grants together; the daemon reads only it, never the config
   ([§4.6](#46-how-a-grant-reaches-the-machinery)).

A grant is held **by the workspace**, as a broker grant is
([BB-D99](../design/boundary-broker.md#BB-D99)), so every jail of that project shares it across
restarts. There is no per-session grant and no per-call doorbell for these loopholes
([§8](#8-what-this-does-not-license)).

**Default ceiling: what the manifest marks requestable, and nothing more.** A grant kind may be
declared **requestable by default** *(coined here)*: with no `on_request` list in the user's
`[allow.<pack>]`, that kind sits in the ceiling's `on_request` as if the user had written it. It is
never a default `always`, and never a valued kind (the manifest is refused), so a project still has
to ask and the human still answers y at the gate. A user's own `on_request` list replaces the
default whole, so `on_request = []` withdraws it. The shipped defaults are `journal`'s `read` and
`macos-log`'s `read`, never `read_all` ([LG-D14](#LG-D14)).

### 4.3 Each loophole's access and its approval

| Loophole | Access means | Approval | Grant kinds | With only the pack selected |
| :--- | :--- | :--- | :--- | :--- |
| `claude-oauth-broker` | Use the host's shared Claude login | **The login**: nothing is served until a human has logged in. Ungated | none | Runs; serves nothing until someone logs in |
| `openai-auth-broker` | Use the host's shared ChatGPT login | **The login**, completed in a browser by a human even when a jail starts it. Ungated | none | Runs; serves nothing until someone logs in |
| `aws-auth` | Bedrock credentials minted from the host's SSO session | **A user-scope setting**, `allow.aws-auth.profile` with a narrowing, plus a provider on Bedrock and a live `aws sso login`. Ungated | none | Runs; refuses to mint without a profile |
| `git-broker` | Fetch and push with the host's credentials ([`git-broker.md`](git-broker.md)) | The brokers' two-layer grants | `fetch`, `push`, `new_branches`, `push_tags` | Runs; tries a public HTTPS fetch anonymously, with no host credential, and refuses every other call naming the read grant |
| `github-broker` | `gh` with the host's login | The brokers' two-layer grants; `repos` is the scope, not a kind | `read`, `create_pr`, `create_issue` and the work kinds | Runs; refuses every call naming the read grant |
| `journal` | Read the host's systemd journal | `read` is the launching user's own journal (`--user` forced); `read_all` is the whole journal. `read` is requestable by default | `read`, `read_all` | Runs; `yolo-journalctl` is refused naming `yolo request journal read` |
| `macos-log` | Read the Mac's unified log | `read` is the sandbox account's entries; `read_all` is every entry. `read` is requestable by default | `read`, `read_all` | Runs; `yolo-log` is refused naming `yolo request macos-log read` |
| `host-processes` | See named host processes through `yolo-ps` | Valued: the process names, and beside them the columns the project sees of each (`fields`), requested in the same table, approved at the same gate, frozen at launch; the user's `allow.host-processes.fields` or the default when a project asks for none ([LG-D15](#LG-D15)) | `view` (valued) | Runs; `yolo-ps` is refused naming the request |
| `serial` | Open host serial devices through `yolo-serial` | Valued: the device globs. **No default list**: a project names what it needs. `default_baud` is a request-scope setting ([LC-D17](../design/legacy-config-compat.md#LC-D17)) | `devices` (valued) | Runs; `yolo-serial` lists nothing and is refused naming the request |
| `audio` | The host's microphone and speakers | One socket, so one kind; mic and playback are not separable at this crossing | `use` | Nothing is bound or passed; the launch line and the briefing say how to ask |
| `cgroup-delegate` | Set limits on the jail's own processes through `yolo-cglimit`: child cgroups inside the container's own cgroup, `cpu`, `memory` and `pids` only ([`security-shim.md`](security-shim.md)) | **None needed**: it can only narrow the jail. Ungated ([LG-D13](#LG-D13)) | none | Runs wherever the setup can run it |
| `hello-daemon` | Nothing on the host: a jail-side daemon that proves a pack can ship an executable | **None needed**; its manifest declares no host crossing | none | Runs inside the jail on selection |

A journal or macos-log daemon picks its mode from the grant: `read_all` granted is full mode, else
`read` granted is user mode, else it refuses naming `read` ([PD-11](#PD-11)).

**A project cannot widen access by editing a setting the gate does not show.** The old
workspace-scope settings `visible` and `allowed_devices` are request values now, approved one
line per value at the gate. `default_baud` admits nothing, so it is a request-scope setting like `fields` below: the user's value
is every project's default, and a project's replaces it there; a legacy workspace value is that
project's ([LC-D17](../design/legacy-config-compat.md#LC-D17)).
`fields` is a **request-scope setting** *(coined here: a setting the user may set under
`[allow.<pack>]` as every project's default, and a project may ask for under `[requests.<pack>]`,
shown as a gate line, replacing the user's for that project, frozen at launch)*: `args` carries
command lines and a project may ask for a list without it, so the columns are approved beside the
names and replace the user's list for that project, never merged with it ([LG-D15](#LG-D15)).
`scope = "workspace"`, which meant either file union-merged, is refused at load, so a fetched
pack cannot reintroduce the shape ([LG-D5](#LG-D5)).

### 4.4 Mediated and unmediated crossings

Where the check runs depends on the shape of the crossing, and the manifest tells the two apart.

| Shape | Loopholes | The check runs | Without a grant |
| :--- | :--- | :--- | :--- |
| **Mediated** *(coined here)*: a host daemon answers each request | brokers, the login services, `aws-auth`, `journal`, `macos-log`, `host-processes`, `serial` | In the daemon, per request, against the grant file, as the brokers do ([BB-D108](../design/boundary-broker.md#BB-D108)) | The daemon runs and refuses with the message in [§5](#5-every-stop-and-its-next-step), exit 64 ([PD-7](#PD-7)); the endpoint is published so the client can print it |
| **Unmediated** *(coined here)*: the crossing is container argv, a bind, a device or a socket the launch publishes | `audio` (two sockets, an ALSA file, `/dev/snd`); `cgroup-delegate`'s socket is this shape too, but ungated, so it is always published where it runs | At launch, in the argv and spawn builders, before the container exists | Nothing crosses. The launch's disclosure line and the jail's briefing carry the standing and the next step, since there is no daemon to say it |

The split is read from the manifest's shape (a `host_daemon` or a fronted `jail_daemon` against bare
binds and devices), never from a tool name. It also decides what `yolo config apply` can change
live ([§4.6](#46-how-a-grant-reaches-the-machinery)).

### 4.5 What goes away

| Before | Now |
| :--- | :--- |
| `default_enabled` on the manifest | **Refused at load**, as the manifest's older `enabled` key is, with the message in [§5](#5-every-stop-and-its-next-step) naming `[[grants]]` ([LG-D3](#LG-D3)) |
| `loopholes.<name>.enabled` in any config | **Switches nothing.** In a legacy JSONC file, for a grant-declaring loophole it is read as a grant through the legacy projection ([§6](#6-migration)); for any other pack loophole `yolo check` and the launch say the key switches nothing and name what to do instead. The TOML rename table names `off` as its replacement |
| The per-workspace switch file (`~/.config/yolo-jail/workspaces/<slug>.jsonc`) | **Read, never written.** A switch in it is read as a legacy grant until the migration carries it into the approval record and removes it ([§6](#6-migration)); see [`storage-and-config.md`](storage-and-config.md) |
| `yolo loopholes enable` / `disable` | **Refuse for every loophole**, naming `yolo request` and `yolo allow` (a broker's names its read grant; an ungated one names its pack). `disable` first removes a switch an older yolo wrote and exits 0; otherwise it also names `yolo pack remove <pack>`. `list` and `status` show the standing per kind ([LG-D8](#LG-D8)) |
| The three-gate reading of `host-processes` ([OQ-A5](loophole-system.md#oq-a5)) | **Two gates**: select, and grant with the list as the grant's value |
| `serial`'s five default globs | **Gone from the daemon**: an empty grant opens no device. The manifest keeps the five as `legacy_default_values` so the migration carries them into a grant where an old switch relied on them |

### 4.6 How a grant reaches the machinery

- **One writer.** The launch (or `yolo config apply`) writes the grant file from the gate's
  in-memory result, after the y and before the spawn, as the broker's scope file is written
  ([BB-D32](../design/boundary-broker.md#BB-D32)). A daemon never reads config.
- **The file** is the per-launch private settings snapshot: its flat settings keys, plus one
  reserved key `grants` carrying the pack, the workspace's host path, the container, a launch id and
  pid, the `access` text, and per declared kind its text, its value noun, its values and its
  **standing** *(coined here)*: `granted` (with the values for a valued kind), `requestable` (in the
  ceiling's `on_request`, not requested), `pending` (requested and admitted, not yet in the record),
  or `not_allowed` (outside the ceiling). Every grant-declaring loophole gets the file even with no
  settings ([PD-2](#PD-2)). The standing is what lets a refusal name the exact next step
  ([§5](#5-every-stop-and-its-next-step)).
- **Live apply.** For a mediated loophole, `yolo config apply` rewrites the `grants` block of each
  running launch's file (found by workspace, container and a live pid) and the daemon reads it at
  its next request; settings stay as the launch froze them. For an unmediated one the apply
  records the grant and says it lands at the next restart.
- **Never into the jail.** The file stays outside every mount, as the settings snapshot always has;
  the standing the client prints comes back over the loophole's own transport in the refusal.
- **A request written after launch** shows as `requestable` until the next launch or
  `yolo config apply` ([PD-10](#PD-10)); `yolo request` already prints that host step.
- **Revocation** is the same path backwards: remove the request or the ceiling line, apply, and the
  next request is refused. The record's grant part is rewritten at that y, as a withdrawn broker
  grant is ([BB-D110](../design/boundary-broker.md#BB-D110)).

## 5. Every stop, and its next step

Every refusal names the problem, then the step
([`happy-path-principle.md`](happy-path-principle.md#the-next-step-ladder)). The standing decides
which step; each client fills its noun. An allow is the human's own answer, so it is printed
alone, to run directly in a host terminal, never as a `yolo host-run` line. The texts are
rendered by `internal/loopholegrant`, and a refused call exits 64.

**Standing `requestable`:**

```text
yolo-journalctl: this project holds no grant to read the host's journal, so nothing ran.
  The journal pack is selected and its service is running; it is waiting on a grant.
  Ask for this project:   yolo request journal read
  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.
```

**Standing `not_allowed`:**

```text
yolo-ps: this project holds no grant to see host processes, so nothing ran.
  Your user config does not allow host-processes view on request, so a request would be skipped.
  The human allows it, directly in a host terminal (not through `yolo host-run`):
    yolo allow host-processes on_request view
  then this project asks:             yolo request host-processes view <process names>
  then the human runs `yolo config apply` on the host, in this workspace, and answers y.
```

**Standing `pending`:**

```text
yolo-serial: this project's request for devices /dev/ttyUSB* is not approved yet, so nothing ran.
  The human runs, on the host, in this workspace:  yolo config apply   and answers y.
```

**Standing `granted`, but not covering this call** (a device or process name outside the values):
the client says the grant does not cover it and names the `yolo request` line that would.

**An unmediated loophole has no client to refuse**, so the launch's pack disclosure carries it,
for every selected grant-declaring loophole that holds no grant here:

```text
  audio: selected, grants nothing here (not allowed on request by your config).
    To let this project ask:  yolo allow audio on_request use   then  yolo request audio use
```

A mediated loophole's line says `running` instead of `selected`. One that holds a grant prints
`<name>: grants for this workspace: <kinds>`. The briefing's loophole section states the same
standing in one line per loophole, so the agent does not debug a missing socket.

**An inert loophole** says so instead of any of the above. The launch sets
`YOLO_LOOPHOLE_INERT_<NAME>` to the reason, and the client prints it before trying the endpoint
([PD-8](#PD-8)):

```text
yolo-journalctl: the journal loophole does nothing on this setup (macOS has no systemd journal); nothing is missing to install.
```

**`yolo check`**, a valued kind under `always`:

```text
~/.config/yolo-jail/config.toml:9: allow.serial.always lists devices, which carries a value only a project can give.
  Move it:  yolo config unset allow.serial.always devices   then  yolo allow serial on_request devices
```

**A manifest with `default_enabled`:**

```text
packs/journal/loopholes/journal/loophole.toml: default_enabled is gone. A loophole runs whenever its pack
  is selected and grants nothing until a grant covers the project; declare what can be granted under
  [[grants]] instead, and remove the key.
```

**`yolo loopholes enable journal`:**

```text
yolo loopholes enable is retired: a loophole runs whenever its pack is selected, and a project gets access by a
  grant, not a switch.
  For this project:    yolo request journal read     (in the workspace), then `yolo config apply` here and y
  For every project:   yolo allow journal read
```

Every `yolo …` command in these strings resolves against the dispatcher by
`TestEveryHintedYoloCommandExists`, so a retired verb cannot survive in a message.

## 6. Migration

`yolo config migrate` is the one whole-file rewrite, and it proves it lost nothing
([`config-migration.md`](../design/config-migration.md#4-what-yolo-config-migrate-does)). The rule:
**no access the owner held is lost or gained silently.** Loud is allowed: a difference the
migration prints with the command that closes it.

Until a file is migrated, a legacy JSONC file is read through the **legacy projection**
*(coined here)*: the read-time function that turns a legacy `loopholes.<name>` block into the
ceiling, request and record lines the migration would write. The launch's gate and the migration
call the same function, so the two cannot disagree ([PD-9](#PD-9)). Which kind a legacy line
becomes is declared by the manifest's `legacy_*` keys ([`pack-system.md`](pack-system.md#loophole)).

| Legacy line | Where | Becomes |
| :--- | :--- | :--- |
| `loopholes.<lp>.enabled: true` | User config | `[allow.<pack>] always = ["<legacy_enable kind>"]` for an unvalued kind; `on_request` for a valued one |
| `loopholes.<lp>.settings.<legacy_setting>: true` (journal's `full`) | User config | That kind (`read_all`) joins the same list |
| `loopholes.<lp>.settings.<user setting>` | User config | `allow.<pack>.<setting>` |
| `loopholes.<lp>.enabled: false` | User config | Dropped: absent means nothing granted |
| `loopholes.<lp>.enabled: true` | Project file | `[requests.<pack>] <kind> = true`, or for a valued kind the values the old settings gave (`visible`, `allowed_devices`); **and the kind in this launch's ceiling as `on_request`**, since the old switch needed no ceiling: the gate's y is the approval, as it was, and a file the record already approved carries its grant and prompts nothing ([LG-D16](#LG-D16)) |
| `loopholes.<lp>.settings.<request setting>` (host-processes' `fields`) | Project file | `[requests.<pack>] <setting> = [...]`, the same list, shown as a columns line at the gate ([LG-D15](#LG-D15)) |
| Per-workspace switch file, `enabled: true` | Host, yolo-owned | A **carried grant** *(coined here)* in that workspace's approval record, kept under `carried:<pack>` because the project's files do not ask for it; for a valued kind, the value the old code resolved (`serial`'s five globs when none was written). The ceiling gains `on_request` for the kind unless it is requestable by default ([LG-D14](#LG-D14)), and the switch is removed from the file |
| `enabled: true` in an **approved** workspace snapshot | Host, the approval record | The same carried grant: the human's y at that launch was the approval |

Three mechanics the equality proof needs:

- **The proof compares a standing per loophole per kind per workspace** (its layer is "the loophole
  grants each workspace holds"), so before and after compare access and not spellings; a grant
  that would vanish or appear is a printed difference and nothing is written.
- **A carried grant the project file does not state** is reported by `yolo check` with the
  `yolo request` line that writes it, at every check, until it does; the grant stands meanwhile.
- **Switch files are carried only by a host migration of the user config**, since the ceiling they
  need is the user's and the record they move into is the host's; that migration carries every
  switch file on the host, not only this workspace's.

A workspace never seen on this host (a repository with `enabled: true` in its committed file,
cloned after the migration) meets the gate as a fresh workspace does: the legacy switch is its
request and its admission, so the first launch shows the names line and the y grants.

## 7. Parity across backends and notches

The same configuration is accepted everywhere, the gate runs the same everywhere, and what differs
is only whether the machinery can run, said in the inert report's words.

| Setup | Running | Granted |
| :--- | :--- | :--- |
| `podman-self` / Linux | Every selected loophole the platform allows | As above |
| `podman-self` / macOS | The same, minus the Linux-only loopholes | As above; inert lines for the platform axis |
| `apple-container-self` | Only the host services the setup admits; everything else inert | **A request for an inert loophole stays out of the gate** and is reported inert, so a y never approves a grant that serves nothing ([LG-D10](#LG-D10)) |
| `seatbelt-self` and `seatbelt-yolo-account` | Host services start as on a container setup; Linux-only loopholes inert | As above; every invocation reads the gate, since there is no attach |
| `yolo host` (no jail) | Doorways only; every other loophole does not apply, since its only client is a container | Grants are accepted in config and reported as not applying |
| A nested jail | The outer jail's config is the inner jail's user scope ([OQ-LP9](loophole-system.md#oq-lp9)) | `yolo allow` inside the outer jail writes the outer jail's own config |

## 8. What this does not license

- **No per-call doorbell for these loopholes.** A journal unit the grant does not cover is refused,
  not asked for; the broker's request store and `yolo approve` stay the brokers'.
- **No narrowing inside a kind.** `read` for the journal means what `--user` means; `use` for audio
  is the whole socket. Finer kinds are a later manifest change, not a config feature.
- **No ceiling on a valued kind's values.** The gate shows every value, and the y is the ceiling.
- **No enforcement on packs yolo does not ship.** A fetched pack may ship an ungated loophole; the
  user selected it, the footprint and the launch disclosure name it, and that is the trust model
  ([`pack-system.md`](pack-system.md#why-there-is-no-approval-gate)).
- **No change to what the login services and `aws-auth` serve**, or to how. Their approval is named
  ([§4.3](#43-each-loopholes-access-and-its-approval)) so that nobody re-derives a grant for them.

This system amended [`loophole-system.md`](loophole-system.md#principles): R2 (the author's
default) is gone, R4 reads "access is never granted by default", R5 reads "install is user scope;
access is a grant in two layers", and the vocabulary's two verbs became the four words of
[§1](#1-the-verdict-in-four-words). Its rows [`OQ-A1`](loophole-system.md#oq-a1), [`OQ-A3`](loophole-system.md#oq-a3), [`OQ-A4`](loophole-system.md#oq-a4), [`OQ-A5`](loophole-system.md#oq-a5), [`OQ-A7`](loophole-system.md#oq-a7), [`OQ-A13`](loophole-system.md#oq-a13) and [`OQ-LP12`](loophole-system.md#oq-lp12)
say how each was superseded.

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Selection runs and grants** (L4 alone) | Rejected. `packs = ["journal"]` would hand every project the journal. |
| **Keep `enabled` as the per-project switch, gated at the config-change prompt** | Rejected. A switch approved at a gate *is* a grant under another name, and the brokers would keep a second vocabulary for the same act. |
| **Pack author keeps `default_enabled`, meaning "runs on selection"** | Rejected. Every loophole runs on selection, so the key would have one legal value and no reader. |
| **Run the machinery only once a grant exists**, as a visible rule | Rejected as a rule; kept as a hidden optimization ([§4.1](#41-one-rule-for-every-loophole)). As a rule it makes "is it running?" a question the user must answer. |
| **A per-call doorbell**, like a broker write | Deferred. No shipped loophole has a resource finer than its kind that a human would want to answer per call. |
| **A user ceiling on valued kinds' values** | Deferred until a third valued kind or a shared-repository case asks for it. |
| **A grant for `cgroup-delegate`** | Rejected by the owner ([LG-D13](#LG-D13)): it can only limit the jail, so a grant would approve nothing of the user's. |
| **The migration prints `yolo allow` for a ceiling line rather than writing it** | Rejected ([LG-D14](#LG-D14)): the y at that launch was the approval, and a migration prints every hunk it writes. |

## 10. The build, by slice

Code comments cite the build plan's slices. Each landed as its own commit; `git log --grep` on the
slice's subject finds it.

| Slice | What it built |
| :--- | :--- |
| <a id="T1"></a>T1 | `[[grants]]` and `access` in the manifest, and the closed ungated set |
| <a id="T2"></a>T2 | `internal/loopholegrant`: the standing, the grant file, the refusal texts |
| <a id="T3"></a>T3 | Ceiling and request readers for grant-declaring packs |
| <a id="T4"></a>T4 | The gate's rows and the approval record's grant part |
| <a id="T5"></a>T5 | `yolo allow` and `yolo request` for loophole kinds |
| <a id="T6"></a>T6 | The launch writes the grant file and gates unmediated crossings |
| <a id="T7"></a>T7, T8, T9 | The daemons and clients enforce grants: `journal` and `macos-log` (T7), `host-processes` (T8), `serial` (T9) |
| <a id="T10"></a>T10 | The legacy projection |
| <a id="F1"></a>F1 to F5 | The **flips** *(coined in the plan)*: each gave one shipped loophole its `[[grants]]` and deleted its ungranted path — F1 `journal`, F2 `macos-log`, F3 `host-processes`, F4 `serial`, F5 `audio` |
| <a id="T11"></a>T11 | Live apply of the grant file |
| <a id="T12"></a>T12 | The migration's loophole rows and the standing layer |
| <a id="T13"></a>T13 | `default_enabled` and workspace-scope settings refused |
| <a id="T14"></a>T14 | `yolo loopholes enable|disable` retired |
| <a id="T15"></a>T15 | `cgroup-delegate` selected by default ([PD-13](#PD-13)) |
| <a id="T16"></a>T16 | These docs |

## 11. Tests that pin it

Each drives a production entry point, so deleting the call site fails it.

| Decision | Test |
| :--- | :--- |
| Runs on selection, grants nothing | `TestASelectedLoopholePackRunsItsServiceAndGrantsNothing` (`internal/cli/run`) |
| A request outside the ceiling is skipped and named | `TestARequestOutsideTheCeilingIsSkippedAndNamed` (`internal/cli/check`) |
| A kind requestable by default needs no ceiling line, and a user's list replaces it | `TestARequestableByDefaultKindNeedsNoCeilingLine` (`internal/config`); `TestAValuedKindRequestableByDefaultIsRefused` (`internal/loopholedecl`) |
| An `always` grant shows at the gate once per project | `TestAnAlwaysGrantShowsOnceAtEachProjectsGate` (`internal/config`) |
| Unmediated crossings only under a grant | `TestAnUnmediatedCrossingIsAddedOnlyUnderAGrant` (`internal/loopholes`) |
| Valued kinds are `on_request` only | `TestAValuedKindUnderAlwaysIsRefused` (`internal/config`) |
| `default_enabled` and workspace-scope settings are refused | `TestDefaultEnabledIsRefusedOnEveryManifest`, `TestAWorkspaceScopeSettingIsRefusedAtLoad` (`internal/loopholedecl`) |
| The ungated set is closed | `TestNoShippedLoopholeIsUngatedWithoutAReason` (`internal/loopholes`) |
| Each daemon reads only its grant file | `TestJournaldReadsOnlyItsGrantFile`, `TestMacosLogReadsOnlyItsGrantFile`, `TestHostprocessesReadsOnlyItsGrantFile`, `TestSerialdaemonReadsOnlyItsGrantFile` |
| Each client prints the inert line | `TestYoloJournalctlPrintsTheInertLine`, `TestYoloLogPrintsTheInertLine`, `TestYoloPsPrintsTheInertLine`, `TestYoloSerialPrintsTheInertLine` |
| Live apply reaches a mediated daemon, not an unmediated crossing | `TestConfigApplyRewritesTheGrantFileAndSaysRestartForAudio` (`internal/cli/run`) |
| Inert requests stay out of the gate | `TestAnInertLoopholesRequestStaysOutOfTheGate` (`internal/cli/check`), `TestAnInertLoopholesRequestStaysOutOfTheLaunchGate` (`internal/cli/run`) |
| `yolo loopholes enable` refuses for all | `TestLoopholesEnableRefusesNamingRequestAndAllow`, `TestLoopholesEnableRefusesABroker` (`internal/cli`) |
| The migration preserves access | `TestLegacyLoopholeEnabledMigratesToAllowAndRequests`, `TestTheMigrationCarriesASwitchFileIntoTheRecord`, `TestAWorkspaceSwitchUnderATOMLUserWritesTheCeilingLine` (`internal/configmigrate`); `TestCheckReportsARecordGrantTheProjectFileDoesNotState` (`internal/cli/check`) |
| Every refusal names a live command | `TestEveryHintedYoloCommandExists` (`internal/cli`) |
| End to end, in a real jail | `TestJournalWithNoGrantRefusesInTheJail`, `TestJournalReadGrantServesTheUserJournal` (`integration/`; they skip where the host has no readable user journal, [PD-14](#PD-14)) |

## 12. What is not built

- **The pack-level per-workspace `off`** ([LG-D9](#LG-D9)): a pack turned off for one workspace
  would run none of its loophole machinery there and hold no grant there. It lands with the
  readable-config redesign's `off`, whose mechanism is
  [`config/project-config.md`](config/project-config.md#the-local-file-and-turning-something-off)'s.
  The half for a pack that ships an off-switch loophole is built: `[requests.<pack>] off = true`
  ([LC-D15](../design/legacy-config-compat.md#LC-D15)). For a grant-declaring pack a legacy
  `enabled: false` in a project file converts to nothing: an absent request asks for nothing,
  and the migration's standing layer proves no grant changed.
- **The project's columns line at the gate** ([LG-D15](#LG-D15)): `fields` is a request-scope
  setting, a project's is read as its request, and `yolo config migrate` writes it under
  `[requests.host-processes]`; still to build is its own columns line at the gate.

## 13. Open Questions

None open.

## 14. Decision Ledger

LG-D12 to LG-D14 are the owner's rulings of 2026-10-30; their question ids, [OQ-LG1](#OQ-LG1) to
[OQ-LG3](#OQ-LG3), point at those rows. PD-1 to PD-14 are the build plan's decisions.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="LG-D1"></a>LG-D1 | **Maintainer ruling:** selecting a pack gives no project access through its loophole; whether the machinery runs is an implementation decision hidden from the user; the project has no access until approved ([Appendix B](#appendix-b-the-owners-words)) | 2026-10-30 | [§1](#1-the-verdict-in-four-words) | Yes |
| <a id="LG-D2"></a>LG-D2 | Every loophole's machinery runs whenever its pack is selected and the setup can run it; no key decides it; a lazy start is a hidden optimization, never a rule. Rests on LG-D1 and [BB-D104](../design/boundary-broker.md#BB-D104) | 2026-10-30 | [§4.1](#41-one-rule-for-every-loophole) | Yes |
| <a id="LG-D3"></a>LG-D3 | `default_enabled` is refused at load with the grant-naming message; the pack author's remaining declaration is the grant kinds. For a legacy `manifest.jsonc` only, revised by [LC-D10](../design/legacy-config-compat.md#LC-D10): the key reads as what it meant until the old reader is removed | 2026-10-30 | [§4.5](#45-what-goes-away) | Yes (T13) |
| <a id="LG-D4"></a>LG-D4 | The approval is the brokers' two layers, `allow` and `requests` at the config-change gate, with the manifest declaring the kinds; a loophole declaring none is ungated, and the ungated set yolo ships (the two login services, `aws-auth`, `cgroup-delegate`) is held closed by test. Rests on [BB-D105](../design/boundary-broker.md#BB-D105) to [BB-D107](../design/boundary-broker.md#BB-D107) | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers), [§4.3](#43-each-loopholes-access-and-its-approval) | Yes |
| <a id="LG-D5"></a>LG-D5 | *Implementation decision* (the marker the 2026-11-02 graduation dropped). A valued kind is `on_request` only; its value is approved per line at the gate. `visible` and `allowed_devices` became request values, `default_baud` a user setting, `serial`'s default globs left the daemon, and `scope = "workspace"` on a setting is refused at load (in a legacy `manifest.jsonc`, it reads as request scope, [LC-D19](../design/legacy-config-compat.md#LC-D19)). Its placement of `fields` in the user's table rested on "admits nothing", which the `args` column falsifies; superseded for `fields` by [LG-D15](#LG-D15), and for `default_baud` by [LC-D17](../design/legacy-config-compat.md#LC-D17) | 2026-10-30 | [§4.3](#43-each-loopholes-access-and-its-approval) | Yes |
| <a id="LG-D6"></a>LG-D6 | A mediated loophole checks grants in its daemon per request; an unmediated one is admitted at launch only under a grant; the split is read from the manifest's shape, never a tool name | 2026-10-30 | [§4.4](#44-mediated-and-unmediated-crossings) | Yes |
| <a id="LG-D7"></a>LG-D7 | One core-written grant file per loophole per launch carries settings and each kind's standing; the daemon reads only it; `yolo config apply` rewrites it live for mediated loopholes and records a restart for unmediated ones. Rests on [BB-D32](../design/boundary-broker.md#BB-D32) and [BB-D108](../design/boundary-broker.md#BB-D108) | 2026-10-30 | [§4.6](#46-how-a-grant-reaches-the-machinery) | Yes |
| <a id="LG-D8"></a>LG-D8 | `yolo loopholes enable` and `disable` refuse for every loophole naming `yolo request` and `yolo allow`, `disable` removing an old switch first; `list` and `status` show the standing. Extends [BB-D109](../design/boundary-broker.md#BB-D109) | 2026-10-30 | [§4.5](#45-what-goes-away) | Yes (T14) |
| <a id="LG-D9"></a>LG-D9 | L4's per-workspace `off` survives as a pack switch: a pack off for a workspace runs no loophole machinery and holds no grant there | 2026-10-30 | [§12](#12-what-is-not-built) | No |
| <a id="LG-D10"></a>LG-D10 | A request for a loophole inert on this setup stays out of the gate and is reported inert; `yolo host` accepts the grants and reports them as not applying. Rests on [BB-D110](../design/boundary-broker.md#BB-D110) | 2026-10-30 | [§7](#7-parity-across-backends-and-notches) | Yes |
| <a id="LG-D11"></a>LG-D11 | The migration converts every legacy enable and setting as [§6](#6-migration)'s table says; its equality proof compares a standing per loophole per kind per workspace; a record grant the project file does not state is reported by `yolo check` until written | 2026-10-30 | [§6](#6-migration) | Yes (T10, T12) |
| <a id="LG-D12"></a><a id="OQ-LG1"></a>LG-D12 | **Owner ruling** on [`OQ-LG1`](#OQ-LG1): an `always` grant in the user ceiling shows once at each project's config-change gate and is recorded there; the user's edit alone grants no project anything. *"A, once at each project's gate: it is what you ruled for the broker read grant six days ago, and your words today, the project shouldn't have access until it has been approved, say the same."* | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers) | Yes |
| <a id="LG-D13"></a><a id="OQ-LG2"></a>LG-D13 | **Owner ruling** on [`OQ-LG2`](#OQ-LG2): `cgroup-delegate` is ungated and its pack is selected by default, a shipped pack in the default selection rather than core code, so every loophole stays a pack's ([OQ-A10](loophole-system.md#oq-a10)). It rests on the delegate being able only to limit the jail ([`security-shim.md`](security-shim.md)). *"If that is true, I would even consider just making this a built-in … it should just be default-enabled."* Under it: the pack's per-workspace `off` ([LG-D9](#LG-D9)) still turns it off; a default-selected pack inert on this setup is reported only by `yolo loopholes status`, since the user did not choose it | 2026-10-30 | [§4.3](#43-each-loopholes-access-and-its-approval) | Ungated: yes. Default selection: T15 ([PD-13](#PD-13)) |
| <a id="LG-D14"></a><a id="OQ-LG3"></a>LG-D14 | **Owner ruling** on [`OQ-LG3`](#OQ-LG3): the migration may write a user-ceiling `on_request` line from an approval the owner gave at a launch, printing it as a hunk; the journal needs none, because its `read` is requestable by default. Under it: a manifest may mark an unvalued kind requestable by default, never `always` and never a valued kind; a user's own `on_request` list replaces the default; `read_all` is not requestable by default. **Owner ruling** on `macos-log`: its `read` is requestable by default whenever the pack is selected | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers), [§6](#6-migration) | Yes |
| <a id="LG-D15"></a>LG-D15 | **Columns are the project's.** `fields` is a request-scope setting (`scope = "request"`): a project asks for it under `[requests.host-processes]` beside `view`, the gate shows it as a columns line naming the delta from the list in force, the y freezes it, and it replaces the user's `allow.host-processes.fields` for that project, never unions with it. Reason: `args` carries command lines and the owner's project drops it per project; under user scope that is inexpressible. A second grant kind rejected: "not granted" would mean the default with `args` | 2026-11-03 | [§4.3](#43-each-loopholes-access-and-its-approval) | Partly: `scope = "request"` and a project `fields` read as its request (`TestALegacyProjectFieldsIsTheColumnsRequest`), and the migration writes a project's `fields` under `[requests.host-processes]` (`TestMigrateWritesAProjectsRequestScopeSettingsUnderRequests`); the gate's columns line is not |
| <a id="LG-D16"></a>LG-D16 | **A legacy workspace block launches unchanged and means what it meant.** The legacy projection reads a workspace `enabled: true` as the request and its admission (the kind joins this launch's ceiling as `on_request`), a file the record already approved carries its grant with no prompt, and a legacy `settings.fields` is the project's columns request. No `yolo allow`, no user-config change, no edit, under the owner's ruling of 2026-11-03 (*"the configs keep working"*, [`legacy-config-compat.md`](../design/legacy-config-compat.md)) and [§6](#6-migration)'s rule; `yolo config migrate` writes the same lines, with the `on_request` ceiling line LG-D14 licenses. The v0.16.0 refusal and the skipped request were regressions, fixed forward with no hatch | 2026-11-03 | [§6](#6-migration) | Yes at the launch (`TestALegacyWorkspaceSwitchGrantsWithoutACeilingLine`, `TestAnApprovedLegacyWorkspaceLaunchesWithNoPrompt`); and in the migration, which writes the `on_request` line into a TOML user config in place when it converts the workspace alone (`TestAWorkspaceSwitchUnderATOMLUserWritesTheCeilingLine`). There is no per-workspace ceiling to write it in instead: the per-workspace file holds only switches, so the line is user-wide. The launch's offer names the file and the line before its question, and takes the line back when the launch stops before its config-change gate passes, naming the `yolo allow` command that admits the request (`TestTheOfferNamesTheUserCeilingLineBeforeTheQuestion`, `TestANoAtTheGateTakesBackTheUserCeilingLine`, `TestAYesAtTheGateKeepsTheUserCeilingLine`). An `on_request` list the user wrote, `[]` included, is never rewritten: the conversion then refuses, naming that command (`TestAUserOnRequestListIsNeverRewritten`) |
| <a id="LG-D17"></a><a id="OQ-LG5"></a>LG-D17 | **Owner ruling** on [`OQ-LG5`](#OQ-LG5): `yolo-ps --tree` honors the project's columns. With `args` absent from the project's `fields`, the tree prints `pid`, `ppid` and `comm` only, so a narrowing approved at the gate holds in every mode; a project whose list keeps `args` sees the tree as before. The tree still matches on names (`/proc/<pid>/comm`, `ucomm` on a Mac), never on `args`. The owner chose A (2026-11-03): *"a narrowing the human approved at the gate should hold in every mode, and the tree matches on names, not on the column it would lose"*. A change from v0.15.1, outside the TOML-switch rule | 2026-11-03 | [§13](#13-open-questions) | not built |
| <a id="PD-1"></a>PD-1 | **Manifest spelling:** a top-level `[[grants]]` array with `kind`, `text`, `valued`, `requestable_by_default`, `value_noun` and the migration keys `legacy_enable`, `legacy_setting`, `legacy_values` (and `legacy_default_values`), plus a top-level `access`. Kinds unique per manifest; `[brokered]` with `[[grants]]` refused; a setting named `grants` refused | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers) | Yes (T1) |
| <a id="PD-2"></a>PD-2 | **The grant file is the private settings snapshot** with one reserved key `grants`; every grant-declaring loophole gets one even with no settings; live apply finds files by workspace, container and a live pid | 2026-10-30 | [§4.6](#46-how-a-grant-reaches-the-machinery) | Yes (T2, T6) |
| <a id="PD-3"></a>PD-3 | **One package, `internal/loopholegrant`**, owns the standing type, the file's read, write and live replace, and the refusal renderer; nothing else parses the `grants` key | 2026-10-30 | [§2](#2-where-it-lives) | Yes (T2) |
| <a id="PD-4"></a>PD-4 | **The approval record gains one part**, `<cname>.loophole-grants.json`, listing per pack the recorded grants as `kind` or `kind=value`; loophole grant sources ride the existing scope check, so there is one gate and one record writer | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers) | Yes (T4) |
| <a id="PD-5"></a>PD-5 | **Gate lines** use the brokers' grant spelling; a skipped request is a gate line only when the gate asks anyway, otherwise it prints in the launch's pack disclosure; a line cites `file:line` only where provenance carries a line | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers) | Yes (T4) |
| <a id="PD-6"></a>PD-6 | **Schema:** one generic `requests.*.*` row; the resolver types it from the declarations, as `allow.*.*` works | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers) | Yes (T3) |
| <a id="PD-7"></a>PD-7 | **A grant refusal exits 64**, the broker and `gh` precedent | 2026-10-30 | [§5](#5-every-stop-and-its-next-step) | Yes (T7 to T9) |
| <a id="PD-8"></a>PD-8 | **Inert reaches the client by environment:** `YOLO_LOOPHOLE_INERT_<NAME>` carries the inert note's reason, and the client prints the inert line before trying the endpoint | 2026-10-30 | [§5](#5-every-stop-and-its-next-step) | Yes (T6 to T9) |
| <a id="PD-9"></a>PD-9 | **Legacy JSONC in the window** is read through the legacy projection, the same function the migration's rows call | 2026-10-30 | [§6](#6-migration) | Yes (T10) |
| <a id="PD-10"></a>PD-10 | **Pending** is computed where the request is read: requested, in the ceiling, not in the record; a request written after launch shows `requestable` until the next launch or `yolo config apply` | 2026-10-30 | [§4.6](#46-how-a-grant-reaches-the-machinery) | Yes |
| <a id="PD-11"></a>PD-11 | **Journal and macos-log modes:** `read_all` granted is full mode; else `read` granted is user mode; else refuse naming `read` | 2026-10-30 | [§4.3](#43-each-loopholes-access-and-its-approval) | Yes (T7) |
| <a id="PD-12"></a>PD-12 | **Transitional daemon path:** until its flip, each daemon enforced the `grants` key when present and kept the old behavior when absent; each flip deleted the absent path, which now fails closed | 2026-10-30 | [§10](#10-the-build-by-slice) | Yes; every flip landed |
| <a id="PD-13"></a>PD-13 | **Default selection of `cgroup-delegate`** is a `pack.toml` key `selected_by_default = true`, honored for embedded packs only, appended in `config.LoadPacks` beside the implicit local pack; `warnIfNoPacks` does not count it. Its per-workspace `off` half lands with L4's `off` ([LG-D9](#LG-D9)) | 2026-10-30 | [§4.3](#43-each-loopholes-access-and-its-approval) | T15 |
| <a id="PD-14"></a>PD-14 | **The end-to-end journal tests skip** with a reason when the test host has no readable user journal (`journalctl --user -n 1` fails), as in a nested jail | 2026-10-30 | [§11](#11-tests-that-pin-it) | Yes |

## Appendix B: the owner's words

Dictated 2026-10-30, lightly punctuated, read for meaning.

| They said | What the system takes |
| :--- | :--- |
| *"Adding the pack should not by default give any jail any privileges given this loophole"* | Selected grants nothing ([§1](#1-the-verdict-in-four-words)); the default ceiling is nothing beyond what a manifest marks requestable ([§4.2](#42-the-approval-in-two-layers)) |
| *"Claude no privileges until logged in, get none until granted, github none until granted"* | The login is the approval for the two login services; the grants are the brokers' ([§4.3](#43-each-loopholes-access-and-its-approval)) |
| *"the AWS one … it can just be on because until it's configured it doesn't do anything"* | `aws-auth` runs on selection and the profile setting is its approval ([§4.3](#43-each-loopholes-access-and-its-approval)) |
| *"the journal it can be on or off I don't really care … the project shouldn't have access until it has been approved"* | Running is yolo's and hidden ([§4.1](#41-one-rule-for-every-loophole)); access is a grant at the gate ([§4.2](#42-the-approval-in-two-layers)) |
| *"on or off makes absolutely no difference, that's an implementation decision hidden from the user"* | No key for running; `default_enabled` and `enabled` go ([§4.5](#45-what-goes-away)) |
| Earlier: *"default enabled should also be the choice of a pack … including the pack should be able to enable it"* | Met for every pack by running on selection; the pack's remaining choice is what can be granted ([§9](#9-alternatives-considered)) |
| *"I think we're conflating a few things here"* | The four words, one owner each ([§1](#1-the-verdict-in-four-words)) |
