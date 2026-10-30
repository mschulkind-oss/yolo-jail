---
title: "A selected pack runs its loophole and grants nothing: access is approved, never switched"
date: 2026-10-30
status: accepted
stage: DESIGN
next: "Write the implementation plan from §12; OQ-LG1 to OQ-LG3 are ruled (LG-D12 to LG-D14)"
depends-on: [readable-config.md]
tags: [loopholes, packs, config, grants, approval, design]
summary: "Selecting a pack installs its loophole and runs its machinery; neither gives any project access. Access is a grant in two layers, the user's ceiling (allow) and the project's request (requests), approved at the config-change gate, the shape the brokers already have. The six loopholes that today grant the moment they are switched on get the same shape, default_enabled and the enabled switch go, and the migration carries every access the owner has today into a grant."
---

# A selected pack runs its loophole and grants nothing: access is approved, never switched

**Status:** 2026-10-30. Nothing built. Evidence verified against the worktree at `e4345e69d`
([Appendix A](#appendix-a-evidence)). Written to reconcile the maintainer's 2026-10-30 position
([Appendix B](#appendix-b-the-owners-words)) with L4 of the readable-config redesign, which they
approved on 2026-10-25 ([OQ-RC1](readable-config.md#decision-ledger)).

> **In short.** Whether a loophole's host machinery *runs* is yolo's business and the user never
> sets it; whether a *project* has *access* through it is always an approval the user gave. L4 is
> right that `packs` is the only switch, and wrong only where it let that switch stand in for the
> approval: under L4 alone, `packs = ["journal"]` hands every project the host's journal.

**Why it matters.** Six shipped loopholes (`journal`, `host-processes`, `serial`, `audio`,
`macos-log`, `cgroup-delegate`) grant the moment they are switched on, with no per-project step; the
brokers were ruled the other way six days ago ([BB-D104](boundary-broker.md#BB-D104)). Two rules for
one word is the conflation the maintainer named.

**The shape.** Four words with one owner each ([§1](#1-the-verdict-in-four-words)); one rule for
every loophole ([§4.1](#41-one-rule-for-every-loophole)); the brokers' two-layer grant, `allow` and
`requests` at the config-change gate, extended to the six ([§4.2](#42-the-approval-in-two-layers)).

**Cost.** `default_enabled`, `loopholes.<name>.enabled`, the per-workspace switch file and
`yolo loopholes enable|disable` go ([§4.5](#45-what-goes-away)); `serial` loses its default device
globs; eight rulings in `loophole-system.md` are amended ([§8](#8-what-this-does-not-license) lists
them).

**Start at [§4](#4-the-shape).** Everything else falls out of it.

**Ruled 2026-10-30:** an `always` grant shows once at each project's gate
([LG-D12](#LG-D12)); the cgroup delegate is ungated and its pack selected by default
([LG-D13](#LG-D13)); the migration writes ceiling lines from approvals the owner gave at a launch,
and the journal's `read` is requestable with no ceiling line at all ([LG-D14](#LG-D14)).

**Reads with:** [`boundary-broker.md`](boundary-broker.md) (the precedent, BB-D104 to BB-D110),
[`config-user.md`](config-user.md) (the `allow` table this reuses),
[`config-project.md`](config-project.md) (the `requests` table), [`config-migration.md`](config-migration.md)
(the one command [§6](#6-migration) extends),
[`../reference/loophole-system.md`](../reference/loophole-system.md) (the as-built account this amends).

---

## 1. The verdict, in four words

Today one word, "enabled", carries three jobs, and the design splits it. Each word below is
*(coined here)* in this sense; each names one state with one owner, and the table says what the
user sees of it.

| Word | Means | Who decides | What the user sees |
| :--- | :--- | :--- | :--- |
| **Selected** | The pack is named in `packs` in the user config, so its code may run on this machine and its loophole is installed. Not *running*: a selected pack's loophole may be inert on this setup. | The user, on the host | The name in `packs`; `yolo pack add`, `yolo pack remove` |
| **Running** | The loophole's host-side machinery is up for this launch: a daemon spawned, a bind or device admitted. Not *granted*: a running daemon with no grant refuses every call. | yolo, from *selected* and the setup ([§4.1](#41-one-rule-for-every-loophole)); never a config key | One disclosure line at launch; nothing to set |
| **Granted** | This project may use the loophole for one named thing (a **grant kind**, below). Not *running*: a grant for an inert loophole serves nothing and says so. | The user: a ceiling in the user config, a request in the project, a y at the gate | `[allow.<pack>]`, `[requests.<pack>]`, the gate's lines, the refusal that names the missing one |
| **Configured** | A parameter of a running loophole that widens or narrows nothing by itself: the AWS profile, the serial baud rate, the `ps` columns. Not a grant: it never admits a project. | The user, in `[allow.<pack>]` | The same table as the ceiling |

Three terms the rest of the document leans on:

- **Grant kind** *(coined here)* — one named thing a loophole can be granted for, declared by its
  manifest: `fetch` and `push` for git today, `read` for the journal under this design. Not a
  setting (a setting admits nothing) and not a capability in the pack system's `serves` sense (that
  is supersession, not access). Spelled as [`config-user.md`](config-user.md#6-what-a-jail-may-do-with-your-logins)
  spells the broker kinds.
- **Valued kind** *(coined here)* — a grant kind whose grant carries a value the project supplies:
  `devices = ["/dev/ttyUSB*"]`, `view = ["postgres"]`, git's `push = ["main"]`. The value is approved
  line by line at the gate.
- **Ceiling** and **request** — [`config-user.md`](config-user.md#3-principles)'s words, principle 6:
  `allow` says what every project gets (`always`) and what one may ask for (`on_request`);
  `requests` in a project is the ask. A request outside the ceiling is skipped and reported, never
  an error.

## 2. What exists today

Verified 2026-10-30; the symbols are in [Appendix A](#appendix-a-evidence).

**Twelve shipped loopholes, in four groups.** The manifest's `default_enabled` is the pack
author's default (absent means off, R2); `loopholes.<name>.enabled` in config is the user's answer
from either scope ([`loophole-system.md`](../reference/loophole-system.md#default_enabled-versus-enabled)).

| Group | Loopholes | What switches it on | What a project gets once on |
| :--- | :--- | :--- | :--- |
| **Brokers** (`brokered` in the manifest) | `git-broker`, `github-broker` | Selection alone; `default_enabled: true` is refused at load and every config switch is ignored ([BB-D106](boundary-broker.md#BB-D106)) | Nothing until a read grant covers the project's approved scope; every call refused with `no read grant` until then ([BB-D108](boundary-broker.md#BB-D108)) |
| **Login and credential services** | `claude-oauth-broker`, `openai-auth-broker` (`default_enabled: true`), `aws-auth` (`false`) | The default, or the switch | What the host login holds; `aws-auth` refuses at spawn without a user-scope `profile` and a narrowing |
| **The six** | `journal`, `host-processes`, `serial`, `audio`, `macos-log`, `cgroup-delegate` (all `false`) | The switch, from either scope | **Everything the loophole does**, at once: the user's journal, the mic and speakers, any device matching five default globs, cgroup control. `host-processes` is the one exception: its `visible` allowlist defaults to empty, so it shows nothing until a list is written |
| **No crossing** | `hello-daemon` (`false`) | The switch | A jail-side daemon only; its manifest says it crosses nothing: no host daemon, bind, device or intercept |

**Two settings are workspace-scope**, so an agent can write them: `host-processes`' `visible` and
`fields`, `serial`'s `allowed_devices` and `default_baud`. Both go through the config-change gate
and need a restart, which is why a workspace-written allowlist was accepted
([`pack-system.md`](../reference/pack-system.md#a-packs-own-config-keys)). A workspace may also
switch an installed non-broker loophole **on**; the launch discloses the file and goes ahead
(`loopholeScopeEnableProblems`).

**The in-jail clients know nothing of config.** Each looks for its endpoint variable or socket and,
finding none, prints the three-step "select the pack, enable the loophole, restart" message
([§5](#5-every-stop-and-its-next-step) quotes two). Three of them tell the user to put `enabled` in
the user config; two say "either scope".

**What L4 did and did not settle.** [`config-user.md` §7](config-user.md#7-one-switch-and-the-nudge),
approved 2026-10-25: `packs` is the only switch; `loopholes.<name>.enabled` goes; a settings table
such as `[allow.aws-auth]` selects nothing; a listed login pack with no profile serves nothing and
the check says what to set. Its [appendix A.4](config-user.md#a4-what-l4-amends) amends R1, R2, R4
and R5 and keeps a per-workspace **off**. It says nothing about what a project *gets* when a pack
in the third group is selected, and the rename table already built for the migration answers for
it: `loopholes.*.enabled` → `off`, which the migration then declines as having "no lossless
conversion to off yet", so a config with any loophole switched on cannot migrate today
(`configmigrate` `lower.go`).

**The TOML reader is ahead of the rulings in one place.** `[allow.<pack>]` already accepts a declared
setting of any pack and `always` / `on_request` for a broker source (`ResolveReadableAllowance`);
`requests.<pack>.<setting>` has a resolver path and no schema row, and nothing yet hands
`[allow.<pack>]` settings to a daemon. Both are build facts for the plan, not design points.

## 3. The gap

1. **Two rules for one word.** A broker grants nothing on selection; a journal grants everything on
   its switch. The user guide's table has a column "On when you select the pack?" that answers
   "Yes, but it grants nothing" for two rows and "No" for seven, and the "No" rows are the ones that
   grant on enable ([`loopholes.md`](../../userguide/guides/loopholes.md#the-loopholes-yolo-ships)).
2. **L4 alone makes selection the grant.** With the switch gone and nothing in its place,
   `packs = ["journal"]` reads the user's journal from every project on the machine. That is what
   the maintainer refused: *"the project shouldn't have access until it has been approved."*
3. **A valued setting is a grant wearing a setting's name.** `serial`'s `allowed_devices` and
   `host-processes`' `visible` decide what the project reaches, in the agent's file, approved only
   as lines in a config diff. `serial` is worse: an empty list falls back to five broad globs, so
   the one spelling that looks like "nothing" means "everything" (`serialdaemon` `loadSettings`).
4. **Running and granted are confused at every stop.** Each in-jail refusal says "turn it on", so a
   user who did and still holds no access has nowhere to go. The Happy Path rule 1 wants the next
   step, and today's step is the wrong one ([`happy-path-principle.md`](../reference/happy-path-principle.md#the-rules)).

## 4. The shape

### 4.1 One rule for every loophole

**A loophole's machinery runs whenever its pack is selected and this setup can run it, and it
grants nothing until a grant covers the project.** That is [BB-D104](boundary-broker.md#BB-D104)
made the rule rather than the exception. The two login services and `aws-auth` already behave this
way with a different approval ([§4.3](#43-each-loopholes-access-and-its-approval)); the six and the
experiment move to it, and `cgroup-delegate`, which can limit nothing but the jail itself, moves
to it ungated and selected by default ([LG-D13](#LG-D13)).

Running is decided by yolo and nothing else:

- **The pack author has no say**, and `default_enabled` goes ([§4.5](#45-what-goes-away)). Their
  2026-10-30 reading of their earlier words, *"including the pack should be able to enable it"*, is
  met for every pack at once: selecting a pack runs its loophole. A pack that wants its machinery
  to run on selection gets exactly that; none can want more, because running grants nothing.
- **The user has no say either**, and there is no key for it. Whether the journal bridge is up in a
  project that holds no grant is, in the maintainer's words, *"an implementation decision hidden
  from the user."* The one visible trace is the launch's disclosure line, which already exists for
  brokers: `journal: running, grants nothing here` and the grant to add.
- **A lazy start is an optimization yolo may make later** (start the daemon at the first granted
  call) and must change nothing a user sees: not the gate, not a refusal, not a disclosure. It is
  named here so nobody turns it into a knob.
- **The setup decides inertness exactly as today**: platform, backend and binary
  ([`loophole-system.md`](../reference/loophole-system.md#where-a-loophole-does-nothing)). An inert
  loophole runs nothing and its grants serve nothing ([§7](#7-parity-across-backends-and-notches)).

### 4.2 The approval, in two layers

The brokers' grant model, reused whole. Nothing here is new vocabulary; what is new is that every
loophole speaks it.

1. **The manifest declares the loophole's grant kinds**, as `brokered.grants` declares a broker's
   today: a list of names, each marked valued or not, with one line of user-facing text per kind
   that the gate and the refusals print ("read your user journal", "see these host processes").
   A loophole that declares none is **ungated**: it runs on selection and whatever it serves is
   served, gated by its own approval (a login, a setting) or by having nothing of the user's to
   approve. Of the twelve, four are ungated and one, the `hello-daemon` experiment, crosses
   nothing and so has nothing to declare
   ([§4.3](#43-each-loopholes-access-and-its-approval)); a test holds that list closed
   ([§11](#11-tests-that-pin-it)). This is a precedent for yolo's packs, not a rule on every pack,
   exactly as BB-D104 said of the brokers.
2. **The user's ceiling**, `[allow.<pack>]` in the user config: `always` lists the kinds every
   project gets; `on_request` lists the kinds a project may ask for; any other key is a declared
   setting. **A valued kind may appear only in `on_request`**: its value is the project's, so there
   is no value for `always` to carry; `yolo check` refuses `always = ["devices"]` naming
   `on_request`. Absent table, absent list: nothing is granted and nothing is requestable, and a
   request is reported and skipped ([`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)),
   except for a kind the manifest marks requestable by default (below).
   `yolo allow <pack> [always|on_request] <kind>` writes it and offers the approval, as it does for
   git ([`config-commands.md` §4](config-commands.md#4-the-commands)).
3. **The project's request**, `[requests.<pack>]` in the project file: `<kind> = true` for an
   unvalued kind, `<kind> = [...]` for a valued one. The agent may write it; `yolo request <pack>
   <kind> [value]` writes it from inside the jail and prints the host step.
4. **The gate grants.** At every fresh launch and at `yolo config apply`, the config-change gate
   shows, for each selected loophole, one line per grant it would hold and one per request it
   skips, in the broker's spelling: `+ journal: read your user journal (from your user config, for
   every project)`, `+ host-processes: see postgres, redis-server (requested by yolo-jail.toml:14)`,
   `– serial: devices /dev/ttyUSB* (not allowed on request by your config: yolo allow serial
   on_request devices)`. The human's y, or `--accept-config-changes`, or `yolo config apply`,
   records the grants in the approval record beside the config part, as broker grants are recorded
   today ([BB-D107](boundary-broker.md#BB-D107)). An N records nothing. **An `always` line appears
   at each project's gate once**, bundled with whatever else that launch asks, and never again
   until the ceiling changes; the user's edit alone grants no project anything
   ([LG-D12](#LG-D12)).
5. **The grant file.** The launch writes what the gate approved into one core-written,
   launch-stamped file per loophole, the file the `{settings}` token already names, now carrying
   settings and grants together; the daemon reads only it, never the config
   ([§4.6](#46-how-a-grant-reaches-the-machinery)).

Per project, per user, or both: **both, in these two layers, and never a third.** A grant is held
by the workspace, as a broker grant is ([BB-D99](boundary-broker.md#BB-D99)), so every jail of that
project shares it across restarts. There is no per-session grant and no per-call doorbell for these
loopholes ([§8](#8-what-this-does-not-license)).

**Default ceiling: what the manifest marks requestable, and nothing more.** A grant kind may be
declared **requestable by default** *(coined here)*: with no `on_request` list in the user's
`[allow.<pack>]`, that kind sits in the ceiling's `on_request` as if the user had written it. It is
never a default `always`, and it is never a valued kind, so a project still has to ask and the
human still answers y at the gate. A user's own `on_request` list replaces the default whole,
so `on_request = []` withdraws it. The shipped defaults are `journal`'s `read` and, by parity,
`macos-log`'s `read`: the owner's own journal, never `read_all` ([LG-D14](#LG-D14)). So a fresh
`packs = ["journal"]` with no `[allow.journal]` runs the bridge in every jail and serves no line of
the journal to any of them until a project requests `read` and the human approves it. That is the
maintainer's *"on or off makes absolutely no difference"* made literal.

### 4.3 Each loophole's access and its approval

What "access" means per loophole, what approves it, and what a project gets with nothing written
beyond the pack's name. The kind names are this design's proposal; they follow
[`config-user.md`](config-user.md#3-principles) principle 1, name the decision.

| Loophole | Access means | Approval | Grant kinds | With only the pack selected |
| :--- | :--- | :--- | :--- | :--- |
| `claude-oauth-broker` | Use the host's shared Claude login | **The login**: nothing is served until a human has logged in; the broker only keeps that login fresh. Ungated | none | Runs; serves nothing until someone logs in. Unchanged |
| `openai-auth-broker` | Use the host's shared ChatGPT login | **The login**, completed in a browser by a human even when a jail starts it. Ungated | none | Runs; serves nothing until someone logs in. Unchanged |
| `aws-auth` | Bedrock credentials minted from the host's SSO session | **A user-scope setting**, `allow.aws-auth.profile` with a narrowing, plus the agent's provider selecting Bedrock and a live `aws sso login`. Ungated | none | Runs; refuses to mint without a profile, as today (`awsauth.Settings.Resolve`) |
| `git-broker` | Fetch and push with the host's credentials | Existing two-layer grants | `fetch`, `push`, `new_branches`, `push_tags` | Runs; refuses every call naming the read grant. Unchanged |
| `github-broker` | `gh` with the host's login | Existing two-layer grants; `repos` is the scope, not a kind | `read`, `create_pr`, `create_issue`; [`github-write-scopes.md`](github-write-scopes.md) proposes four work kinds beside them | Unchanged |
| `journal` | Read the host's systemd journal | **New grant.** `read` is the launching user's own journal (`--user` forced, as today); `read_all` is the whole journal, which today's user-scope `full` setting opened. `read` is requestable by default ([LG-D14](#LG-D14)) | `read`, `read_all` | Runs; `yolo-journalctl` is refused naming `yolo request journal read` |
| `macos-log` | Read the Mac's unified log | **New grant.** `read` is the sandbox account's entries (today's narrowing); `read_all` is every entry (today's `full`). `read` is requestable by default, as the journal's is | `read`, `read_all` | Runs; `yolo-log` is refused naming `yolo request macos-log read` |
| `host-processes` | See named host processes through `yolo-ps` | **New grant**, valued: the process names, today's workspace-scope `visible`. `fields` becomes a user setting, `allow.host-processes.fields` | `view` (valued) | Runs; `yolo-ps` is refused naming the request |
| `serial` | Open host serial devices through `yolo-serial` | **New grant**, valued: the device globs, today's `allowed_devices`. **No default list**: a project names what it needs. `default_baud` becomes a user setting | `devices` (valued) | Runs; `yolo-serial` lists nothing and is refused naming the request |
| `audio` | The host's microphone and speakers | **New grant.** One socket, so one kind; mic and playback are not separable at this crossing | `use` | Nothing is bound or passed; the launch line and the briefing say how to ask |
| `cgroup-delegate` | Set limits on the jail's own processes through `yolo-cglimit`: child cgroups inside the container's own cgroup, `cpu`, `memory` and `pids` only, bounded ([`security-shim.md`](../reference/security-shim.md)) | **None needed**: it can only narrow the jail, and reads and changes nothing of the host's. Ungated, and **its pack is selected by default** on every setup ([LG-D13](#LG-D13)) | none | Runs wherever the setup can run it, with no line in any config; inert elsewhere, as today |
| `hello-daemon` | Nothing on the host: a jail-side daemon that proves a pack can ship an executable | **None needed**; its manifest declares no host crossing, so there is nothing to grant | none | Runs inside the jail on selection; `default_enabled` is simply dropped |

Two consequences for today's workspace-scope settings, which is the question this table had to
answer: **a project can no longer widen access by editing a setting.** `visible` and
`allowed_devices` become request values, approved one line per value at the gate; `fields` and
`default_baud` move to the user's table because they admit nothing. The `scope: "workspace"`
declaration on a loophole setting then has no shipped user and is refused at load, so a fetched
pack cannot reintroduce the shape ([§11](#11-tests-that-pin-it)).

### 4.4 Mediated and unmediated crossings

Where the check runs depends on the shape of the crossing, and the manifest already tells the two
apart.

| Shape | Loopholes | The check runs | Without a grant |
| :--- | :--- | :--- | :--- |
| **Mediated** *(coined here)*: a host daemon answers each request | brokers, the login services, `aws-auth`, `journal`, `macos-log`, `host-processes`, `serial` | In the daemon, per request, against the grant file; as the brokers do ([BB-D108](boundary-broker.md#BB-D108)) | The daemon runs and refuses with the message in [§5](#5-every-stop-and-its-next-step); the endpoint is published so the client can print it |
| **Unmediated** *(coined here)*: the crossing is container argv, a bind, a device or a socket the launch publishes | `audio` (two sockets, an ALSA file, `/dev/snd`); `cgroup-delegate`'s socket is this shape too, but ungated, so it is always published where it runs | At launch, in the argv and spawn builders, before the container exists | Nothing crosses. The launch's disclosure line and the jail's briefing section carry the standing and the next step, since there is no daemon to say it |

The split is read from the manifest (a `host_daemon` or a fronted `jail_daemon` against bare binds
and devices), never from a tool name. It also fixes where `yolo config apply` can act
([§4.6](#46-how-a-grant-reaches-the-machinery)).

### 4.5 What goes away

| Today | Fate | Why |
| :--- | :--- | :--- |
| `default_enabled` on the manifest | **Deleted**; the key is refused at load as the manifest's old `enabled` key is refused today, with the message naming the grant declaration | Running follows selection for every loophole ([§4.1](#41-one-rule-for-every-loophole)); a key no reader consults is a key that lies. The in-flight manifest conversion carries it unchanged today; it is the plan's job to drop it there |
| `loopholes.<name>.enabled` in any config | **Gone**, as L4 ruled. In a TOML file it is refused naming the replacement from the rename table ([`config-migration.md` §5](config-migration.md#5-every-case)); in a legacy file it is read until the legacy reader goes, and the migration converts it ([§6](#6-migration)) | It was running and granted in one key |
| The per-workspace switch file (`~/.config/yolo-jail/workspaces/<slug>.jsonc`) | **Retired**; the migration converts each one ([§6](#6-migration)) | Its one job was the per-project switch, which is now a grant held in the approval record |
| `yolo loopholes enable` / `disable` | **Refuse for every loophole**, naming `yolo request` and `yolo allow`, as they refuse for brokers today ([BB-D109](boundary-broker.md#BB-D109)); `disable` first removes a switch an older yolo wrote and exits 0. Retired after one release. `list` and `status` stay and show the standing per kind | One vocabulary |
| L4's per-workspace `off = true` on a pack entry | **Kept as a pack switch**, not a loophole one: a pack turned off for a workspace runs none of its loophole machinery there, holds no grant there, and the gate reports its requests as skipped for that reason. Not built yet; its mechanism is [`config-project.md`](config-project.md#3-the-local-file-and-turning-something-off)'s | A workspace may want a pack gone for reasons other than privilege (its skills, its briefing, a daemon's cost). Nothing turns a pack *on* per workspace |
| The three-gate reading of `host-processes` ([OQ-A5](../reference/loophole-system.md#oq-a5)) | **Two gates**: select, and grant with the list as the grant's value | The middle gate asked "is it running", which the user no longer answers |
| `serial`'s five default globs | **Deleted**; an empty request grants no device | An empty list meaning "everything" is the typo-widens-access shape the settings design refused ([`loophole-system.md`](../reference/loophole-system.md#settings)) |

### 4.6 How a grant reaches the machinery

- **One writer.** The launch (or `yolo config apply`) writes the grant file from the gate's
  in-memory result, after the y and before the spawn, as the broker's scope file is written
  ([BB-D32](boundary-broker.md#BB-D32)). Nothing else writes it; a daemon never reads config.
- **Contents.** Per declared kind, its **standing** *(coined here)*: `granted` (with the value for a
  valued kind), `requestable` (in the ceiling's `on_request`, not requested), `pending` (requested,
  not yet approved: a request written after the last y), or `not_allowed` (outside the ceiling);
  plus the settings. The standing is what lets a refusal name the exact next step
  ([§5](#5-every-stop-and-its-next-step)) instead of a three-step recipe.
- **Live apply.** For a mediated loophole, `yolo config apply` rewrites the file and the daemon
  reads it at its next request, as the brokers take a grant "at its next call"
  ([BB-D108](boundary-broker.md#BB-D108)); for an unmediated one the apply records the grant and
  says it lands at the next restart, the "at next restart" outcome
  [`live-config-apply.md`](live-config-apply.md) already has.
- **Never into the jail.** The file stays outside every mount, as the settings file does today
  (`refuseSettingsFileCrossingIntoTheJail`); the standing the client prints comes back over the
  loophole's own transport in the refusal, not from a file the agent can read or write.
- **Revocation** is the same path backwards: remove the request or the ceiling line, apply, and the
  next request is refused. The approval record's grant part is rewritten at that y, as a withdrawn
  broker grant is ([BB-D110](boundary-broker.md#BB-D110)).

## 5. Every stop, and its next step

Every refusal names the problem, then the step, per the Happy Path's rule 1 and rung 2 or 3
([`happy-path-principle.md`](../reference/happy-path-principle.md#the-next-step-ladder)). The
standing decides which step; the shapes are shown once and each client fills its noun. `<dir>` is
the host path of the workspace, as every in-jail `host-run` line prints it.

**A mediated client, standing `requestable`** (the ceiling allows it on request, the project has
not asked):

```text
yolo-journalctl: this project holds no grant to read the host's journal, so nothing ran.
  The journal pack is selected and its service is running; it is waiting on a grant.
  Ask for this project:   yolo request journal read
  then the human runs, on the host, in this workspace:  yolo config apply   and answers y.
```

**Standing `not_allowed`** (outside the ceiling):

```text
yolo-ps: this project holds no grant to see host processes, so nothing ran.
  Your user config does not allow host-processes view on request, so a request would be skipped.
  The human allows it, on the host:   yolo allow host-processes on_request view
    (or:  yolo host-run -C <dir> --name allow-host-processes -- yolo allow host-processes on_request view)
  then this project asks:             yolo request host-processes view postgres redis-server
  then the human runs `yolo config apply` on the host, in this workspace, and answers y.
```

**Standing `pending`** (requested, not yet approved):

```text
yolo-serial: this project's request for devices /dev/ttyUSB* is not approved yet, so nothing ran.
  The human runs, on the host, in this workspace:  yolo config apply   and answers y.
```

**An unmediated loophole has no client to refuse**, so the launch line and the briefing carry it.
The launch, in the pack disclosure block, for every selected loophole whose standing is not
`granted`:

```text
  audio: selected, grants nothing here (not allowed on request by your config).
    To let this project ask:  yolo allow audio on_request use   then  yolo request audio use
```

and the briefing's loophole section states the same standing in one line per loophole, so the agent
does not debug a missing socket.

**A grant for an inert loophole** says so instead of any of the above, in the inert report's words
([`loophole-system.md`](../reference/loophole-system.md#where-a-loophole-does-nothing)), and the
gate does not show its lines ([§7](#7-parity-across-backends-and-notches)):

```text
yolo-journalctl: the journal loophole does nothing on this setup (macOS has no systemd journal); nothing is missing to install.
```

**`yolo check`**, a request outside the ceiling (the shape [`config-user.md` §6](config-user.md#6-what-a-jail-may-do-with-your-logins)
already has):

```text
yolo-jail.toml:14: this project requests host-processes view postgres, redis-server.
  Your config does not allow view on request, so this project runs without it.
  To allow it for every project you work on (in your config, not this file):
    yolo allow host-processes on_request view
```

**`yolo check`**, a valued kind under `always`:

```text
~/.config/yolo-jail/config.toml:9: allow.serial.always lists devices, which carries a value only a project can give.
  Move it:  yolo config unset allow.serial.always devices   then  yolo allow serial on_request devices
```

**`yolo check`**, a manifest with `default_enabled` (the `[[grants]]` spelling is the manifest
conversion's to settle; the message names whatever it picks):

```text
packs/journal/loopholes/journal/loophole.toml: default_enabled is gone. A loophole runs whenever its pack
  is selected and grants nothing until a grant covers the project; declare what can be granted under
  [[grants]] instead, and remove the key.
```

**`yolo loopholes enable journal`**:

```text
yolo loopholes enable is retired: a loophole runs whenever its pack is selected, and a project gets access by a
  grant, not a switch.
  For this project:    yolo request journal read     (in the workspace), then `yolo config apply` here and y
  For every project:   yolo allow journal read
```

Every `yolo …` command in these strings resolves against the dispatcher by
`TestEveryHintedYoloCommandExists`, so a retired verb cannot survive in a message.

## 6. Migration

`yolo config migrate` is the one whole-file rewrite, it proves it lost nothing, and it runs on the
host for user files and in a jail for the project's
([`config-migration.md` §4](config-migration.md#4-what-yolo-config-migrate-does)). The rule for this
design: **no access the owner holds today is lost or gained silently.** Lost means a workspace that
reads the journal today cannot after; gained means one that could not can. Loud is allowed: a
difference the migration prints with the command that closes it.

| Legacy line | Where | Becomes | Loses or gains? |
| :--- | :--- | :--- | :--- |
| `loopholes.<lp>.enabled: true` (`cgroup-delegate`: see below) | User config | `[allow.<lp>] always = ["<default kind>"]` for an unvalued kind (`read`, `use`, `limits`); `on_request = ["<kind>"]` for a valued one | Neither: each project sees the `always` line once at its gate ([LG-D12](#LG-D12)), which the broker read grant already does |
| `loopholes.<lp>.settings.full: true` | User config | `read_all` joins the same list | Neither |
| `loopholes.<lp>.settings.<user setting>` | User config | `allow.<lp>.<setting>` (the rename table's existing row) | Neither |
| `loopholes.<lp>.enabled: false` | User config | Dropped: absent means nothing granted | Neither |
| Per-workspace switch file, `enabled: true` | Host, yolo-owned | **A grant in that workspace's approval record** for the default kind (or, for a valued kind, the value the old code resolved: the record's `visible` list; `serial`'s five globs when none was written), plus `on_request` for that kind in the user ceiling if absent and not requestable by default ([LG-D14](#LG-D14)). The file is deleted | Neither. The human ran `yolo loopholes enable` on the host, which was the approval |
| Per-workspace switch file, `enabled: false` | Host, yolo-owned | The pack's per-workspace `off` in the file yolo owns for it | Neither |
| `loopholes.<lp>.enabled: true` in an **approved** workspace snapshot | Host, the approval record | The record gains the grant as above, and the user ceiling gains `on_request` for the kind unless it is requestable by default, printed as a hunk like every other edit ([LG-D14](#LG-D14)) | Neither |
| `loopholes.<lp>.enabled: true` | Project file, migrated in a jail | `[requests.<lp>] <kind> = true`, or for a valued kind the value the old settings gave | Neither if the ceiling admits it; otherwise **loud**: skipped and named at every check and launch |
| `loopholes.<lp>.settings.visible` / `allowed_devices` | Project file | `requests.host-processes.view = [...]` / `requests.serial.devices = [...]` | Neither |
| `loopholes.<lp>.enabled: false` | Project or local file | The pack's `off` for this workspace, per L4 | Neither |

Three mechanics the equality proof needs:

- **The canonical form gains a standing per loophole per kind, per workspace**, so the before and
  after compare access and not spellings; a grant that would vanish is a printed difference and
  nothing is written. This is the only way "nothing is lost" is a proof rather than a promise.
- **A record grant the project file does not state** is reported by `yolo check` in the jail with
  the `yolo request` line that writes it, at every check, until it does; the grant stands meanwhile.
  It is the one place the migration leaves a difference between what a file says and what a
  workspace holds, and it says so in its output.
- **A workspace never seen on this host** (a repository with `enabled: true` in its committed file,
  cloned after the migration) meets the normal path: its request is a request, and the first launch
  shows it skipped with the `yolo allow` line. Nothing silent happens; nothing is gained.

**`cgroup-delegate`** needs no row: any legacy `enabled` or switch for it is dropped, since its
pack is selected by default and holds no grant ([LG-D13](#LG-D13)); `enabled: false` becomes the
pack's `off` like any other. The user config's `brokered` table already converts to
`[allow.<source>]` ([RC-D42](readable-config.md#RC-D42)), so this design's rows extend a migration
that handles brokers rather than one that declines them.

## 7. Parity across backends and notches

The same configuration is accepted everywhere, the gate runs the same everywhere, and what differs
is only whether the machinery can run, said in the inert report's words.

| Setup | Running | Granted | What differs |
| :--- | :--- | :--- | :--- |
| podman, Linux | Every selected loophole the platform allows | As designed | The reference |
| podman, macOS | The same, minus Linux-only loopholes (`journal`, `audio`, `cgroup-delegate`, `host-processes`'s `ps` present) | As designed | Inert lines for the platform axis |
| Apple Container | Only what `HostServiceAdmittedOn` admits today (the OpenAI service, the Claude service with the credential view); everything else inert | **A request for an inert loophole stays out of the gate** and is reported inert, as a broker source that can grant nothing stays out ([BB-D110](boundary-broker.md#BB-D110)): a y for a grant that serves nothing teaches the user to press y | The gate shows fewer lines; `yolo check` says why |
| macos-user | Host services start as on a container launch; Linux-only loopholes inert; jail daemons the sandbox declines are declined as today | As designed; every invocation reads the gate, since there is no attach | Two sessions in one workspace share the workspace's grants, as they share its broker grants |
| `yolo host` (no jail) | Doorways only ([HS-D15](host-notch-services.md#HS-D15)); every other loophole "does not apply", since its only client is a container | Grants are accepted in config and reported as not applying: the agent is the user and already holds the user's journal | `yolo host apply`'s report names each one, as it names loopholes today |
| A nested jail | The outer jail's config is the inner jail's user scope ([OQ-LP9](../reference/loophole-system.md#oq-lp9)) | `yolo allow` inside the outer jail writes the outer jail's own config, which is legitimately the agent's | Nothing special |

## 8. What this does not license

- **No per-call doorbell for these loopholes.** A journal unit the grant does not cover is refused,
  not asked for; the broker's request store, toast and `yolo approve` stay the brokers'. Git's push
  is the one place a per-resource ask exists, and it stays there.
- **No narrowing inside a kind.** `read` for the journal means what `--user` means today; `use` for
  audio is the whole socket. Finer kinds are a later manifest change, not a config feature.
- **No enforcement on packs yolo does not ship.** A fetched pack may ship an ungated loophole; the
  user selected it, the footprint and the launch disclosure name it, and that is the trust model
  ([`pack-system.md`](../reference/pack-system.md#why-there-is-no-approval-gate)).
- **The pack-level `off` is not designed here.** It survives ([§4.5](#45-what-goes-away)); its
  spelling and file are [`config-project.md`](config-project.md#3-the-local-file-and-turning-something-off)'s.
- **No change to what the login services and `aws-auth` serve**, or to how. Their approval is named
  ([§4.3](#43-each-loopholes-access-and-its-approval)) so that nobody re-derives a grant for them.

What this amends, for the orchestrator to apply: in
[`loophole-system.md`](../reference/loophole-system.md#principles), R2 (the author's default) is
deleted, R4 is restated as "access is never granted by default", R5 becomes "install is user
scope; access is a grant in two layers", and the vocabulary's "two verbs" become the four words
of [§1](#1-the-verdict-in-four-words); [OQ-A1](../reference/loophole-system.md#oq-a1),
[OQ-A3](../reference/loophole-system.md#oq-a3), [OQ-A4](../reference/loophole-system.md#oq-a4),
[OQ-A5](../reference/loophole-system.md#oq-a5), [OQ-A13](../reference/loophole-system.md#oq-a13) and
[OQ-LP12](../reference/loophole-system.md#oq-lp12) are superseded in the ways
[§4.5](#45-what-goes-away) states; [OQ-A4](../reference/loophole-system.md#oq-a4) (the delegate is
opt-in) is overruled, and [OQ-A7](../reference/loophole-system.md#oq-a7) (every shipped pack still
needs selecting) gains its one exception, `cgroup-delegate`, by [LG-D13](#LG-D13); [`AGENTS.md`](../../AGENTS.md)'s
"nothing is active by default" gains the same exception when it is built; [`config-user.md` A.4](config-user.md#a4-what-l4-amends) gains the
sentence that selection grants nothing.

## 9. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **L4 as approved, nothing more**: selection runs and grants | Rejected. `packs = ["journal"]` would hand every project the journal, which the maintainer refused in as many words. |
| **Keep `enabled` as the per-project switch, gated at the config-change prompt** | Rejected. A switch approved at a gate *is* a grant under another name, and the brokers would keep a second vocabulary for the same act. |
| **Pack author keeps `default_enabled`, meaning "runs on selection"** | Rejected. Every loophole runs on selection, so the key would have one legal value and no reader. The author's one remaining choice, what can be granted, is the `grants` declaration. |
| **Run the machinery only once a grant exists** (lazy, visible) | Rejected as a user-visible rule; kept as a hidden optimization yolo may make ([§4.1](#41-one-rule-for-every-loophole)). As a rule it reintroduces "is it running?" as a question the user must answer. |
| **A per-call doorbell for the six**, like a broker write | Deferred. No shipped loophole has a resource finer than its kind that a human would want to answer per call; git's refs are the exception and already have one. |
| **A user ceiling on valued kinds' values** (`allow.serial.devices = ["/dev/ttyUSB*"]` bounding what a project may request) | Deferred. Two valued kinds exist; the gate shows every value, and the y is the ceiling. Add when a third valued kind or a shared-repository case asks for it. |
| **A grant for `cgroup-delegate`** like every other loophole, for one rule with no exception | Rejected by the owner ([LG-D13](#LG-D13)): it can only limit the jail, so a grant would approve nothing of the user's. `hello-daemon` is a different case: it crosses nothing at all. |
| **The migration prints `yolo allow` for a ceiling line rather than writing it** | Rejected ([LG-D14](#LG-D14)): the y at that launch was the approval, and a migration the user typed prints every hunk it writes. |

## 10. Risks

| Risk | Mitigation |
| :--- | :--- |
| **Prompt fatigue**: six more loopholes putting lines through the gate | Lines appear only where a grant or request exists; an `always` grant shows once per project ([LG-D12](#LG-D12)); inert loopholes stay out ([§7](#7-parity-across-backends-and-notches)). The broker precedent has run this way since 2026-10-24. |
| **A fetched pack ships `scope: "workspace"` on a setting to regain the widening** | Refused at load once no shipped manifest uses it ([§4.3](#43-each-loopholes-access-and-its-approval)); the pack vanishes with a message rather than leaking. |
| **The migration's equality proof cannot see per-workspace access** | The canonical form gains the standing per workspace ([§6](#6-migration)); without it the proof passes on spellings and the rule is a promise. |
| **A daemon keeps reading `loopholes.<name>.settings` after the config moved** | The grant file is the only input, and a test runs each shipped daemon's settings load against it with the config path absent ([§11](#11-tests-that-pin-it)). The survey found no delivery of `[allow.<pack>]` settings to a daemon yet; this is the build's first gap. |
| **The in-flight manifest conversion lands `default_enabled` in `loophole.toml`** | The decoder refuses the key, so the conversion fails loudly rather than carrying a dead value; sequence the refusal after the conversion lands ([§12](#12-what-i-would-build-in-order)). |
| **`serial` users lose the default globs at the migration** | The switch-file and record rows carry the five globs into the grant; a fresh project writes its devices, which is the intended cost. |

## 11. Tests that pin it

Each fails when its production call site is deleted, by driving the production entry point rather
than the helper; names are proposals.

| Decision | Test | Fails if |
| :--- | :--- | :--- |
| Runs on selection, grants nothing | `TestASelectedLoopholePackRunsItsServiceAndGrantsNothing`: a launch with `packs = ["journal"]` and no `allow` spawns the bridge through `startLoopholesMatching`, and a request through the daemon's handler is refused with the `requestable`-less standing text | the spawn filter or the handler's grant check goes |
| Default ceiling is only what the manifest marks; a request outside it is skipped and named | `TestARequestOutsideTheCeilingIsSkippedAndNamed`, through `yolo check`'s runner on a project file with `requests.journal.read_all = true` and a user config with no `allow.journal` | the ceiling check or its message goes |
| A kind requestable by default needs no ceiling line, and a user's list replaces it | `TestARequestableByDefaultKindNeedsNoCeilingLine`: `requests.journal.read = true` with no `allow.journal` reaches the gate as a line; with `allow.journal.on_request = []` it is skipped. A sibling refuses a manifest marking a valued kind requestable by default | the default stops reaching the ceiling, or cannot be withdrawn |
| An `always` grant shows at the gate once per project | `TestAnAlwaysGrantShowsOnceAtEachProjectsGate`, through `CheckConfigAndScopeChanges`: first launch shows the line, second does not, a withdrawn ceiling shows it removed once | the gate's grant rows go |
| Unmediated crossings only under a grant | `TestAnUnmediatedCrossingIsAddedOnlyUnderAGrant`: `RuntimeArgsFor` yields no `/dev/snd`, no socket binds without a grant and all three with one | the grant check in the argv builder goes |
| The cgroup delegate is ungated and selected by default | `TestTheCgroupDelegateRunsWithAnEmptyConfig`: a launch with no `packs` key resolves `cgroup-delegate` as selected and `startCgroupDelegate` publishes its socket; with the pack's `off` it does not | the default selection or the ungated spawn goes |
| Valued kinds are `on_request` only | `TestAValuedKindUnderAlwaysIsRefused`, through the TOML validator | the refusal goes |
| `default_enabled` is refused | `TestDefaultEnabledIsRefusedOnEveryManifest`: feeds the key to `loopholedecl.Decode` and expects the grant-naming refusal; a sibling walks every shipped manifest and fails on the key | the decoder tolerates it |
| Four ungated, and only four | `TestNoShippedLoopholeIsUngatedWithoutAReason`: every shipped manifest declares grants, or declares no host crossing at all (no host daemon, bind, device or intercept, as `hello-daemon`), or is one of `claude-oauth-broker`, `openai-auth-broker`, `aws-auth`, `cgroup-delegate`, each row with its approval's name or its reason | a fifth ungated manifest ships |
| No `scope: "workspace"` setting | `TestAWorkspaceScopeSettingIsRefusedAtLoad` | the refusal goes |
| The daemon reads only the grant file | `TestEachShippedDaemonReadsOnlyItsGrantFile`: each daemon's settings load runs with no config on disk and a grant file present, and refuses a request the file does not grant | a daemon re-reads config |
| Live apply reaches a mediated daemon, not an unmediated crossing | `TestConfigApplyRewritesTheGrantFileAndSaysRestartForAudio`, through `yolo config apply`'s runner | the apply skips the file, or claims audio applied |
| Inert requests stay out of the gate | `TestAnInertLoopholesRequestStaysOutOfTheGate`, on a `container` runtime fixture | the gate shows a line for a loophole that serves nothing |
| `yolo loopholes enable` refuses for all | `TestLoopholesEnableRefusesNamingRequestAndAllow` (extends `TestLoopholesEnableRefusesABroker`) | the verb grants again |
| Migration preserves access | `TestLegacyLoopholeEnabledMigratesToAllowAndRequests` and `TestTheMigrationCarriesASwitchFileIntoTheRecord`, through the migration's planner on fixtures with each row of [§6](#6-migration), asserting the equality proof passes and the standing is unchanged per workspace | a row is dropped or the proof stops seeing access |
| Every refusal names a live command | `TestEveryHintedYoloCommandExists` (exists) | a message names a retired verb |
| End to end | `TestJournalWithNoGrantRefusesInTheJail` and `TestJournalReadGrantServesTheUserJournal` in `integration/`, a real jail | the whole path |

## 12. What I would build, in order

1. The grants declaration in `loopholedecl`, the standing, and the grant file: the brokers'
   `brokered.grants` generalized, with the ungated allowlist test. No behavior change yet.
2. The ceiling and request readers for non-broker packs (the schema rows that are missing), the gate
   rows, the record's grant part. The six still grant on their switch here.
3. The daemons and the argv builders read the grant file; the switch stops deciding; the refusals.
   This is the step that changes what a project gets, so it lands with the migration's user-file
   rows and the switch-file conversion in one release.
4. The manifest refusal of `default_enabled`, after the TOML conversion of the manifests has
   landed, and the retirement of `yolo loopholes enable|disable`.
5. The project-file rows of the migration, the user guide and READMEs, the amendments to
   `loophole-system.md` listed in [§8](#8-what-this-does-not-license).

## 13. Decision Ledger

Decisions taken here under rulings already made; each names the ruling it rests on. Built from the
tree, none yet. The owner's three rulings of 2026-10-30 are LG-D12 to LG-D14; their question ids,
[OQ-LG1](#OQ-LG1) to [OQ-LG3](#OQ-LG3), now point at those rows.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="LG-D1"></a>LG-D1 | **Maintainer ruling,** 2026-10-30: selecting a pack gives no project access through its loophole; whether the machinery runs is an implementation decision hidden from the user; the project has no access until approved. Quoted in [Appendix B](#appendix-b-the-owners-words) | 2026-10-30 | [§1](#1-the-verdict-in-four-words) | — |
| <a id="LG-D2"></a>LG-D2 | *Implementation decision.* Every loophole's machinery runs whenever its pack is selected and the setup can run it; no key, manifest or config, decides it; a lazy start is a hidden optimization and never a rule. Rests on LG-D1 and [BB-D104](boundary-broker.md#BB-D104) | 2026-10-30 | [§4.1](#41-one-rule-for-every-loophole) | — |
| <a id="LG-D3"></a>LG-D3 | *Implementation decision.* `default_enabled` is deleted and refused at load with the grant-naming message; the pack author's remaining declaration is the grant kinds. Rests on LG-D2 and the readable-config plan's G1 | 2026-10-30 | [§4.5](#45-what-goes-away) | — |
| <a id="LG-D4"></a>LG-D4 | *Implementation decision.* The approval is the brokers' two layers, `allow` and `requests` at the config-change gate, with the manifest declaring the kinds; a loophole declaring none is ungated, and the ungated set yolo ships is the two login services and `aws-auth`, each approved by its own login or setting, and `cgroup-delegate` ([LG-D13](#LG-D13)), held closed by test. Rests on [BB-D105](boundary-broker.md#BB-D105) to [BB-D107](boundary-broker.md#BB-D107) and the maintainer's words on Claude and AWS | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers), [§4.3](#43-each-loopholes-access-and-its-approval) | — |
| <a id="LG-D5"></a>LG-D5 | *Implementation decision.* A valued kind is `on_request` only; its value is approved per line at the gate; no ceiling on values in this design. Today's workspace-scope settings `visible` and `allowed_devices` become request values, `fields` and `default_baud` become user settings, `serial`'s default globs are deleted, and `scope: "workspace"` on a setting is refused at load | 2026-10-30 | [§4.3](#43-each-loopholes-access-and-its-approval) | — |
| <a id="LG-D6"></a>LG-D6 | *Implementation decision.* A mediated loophole checks grants in its daemon per request; an unmediated one is admitted at launch only under a grant; the split is read from the manifest's shape, never a tool name | 2026-10-30 | [§4.4](#44-mediated-and-unmediated-crossings) | — |
| <a id="LG-D7"></a>LG-D7 | *Implementation decision.* One core-written grant file per loophole per launch carries settings and each kind's standing (`granted`, `requestable`, `pending`, `not_allowed`); the daemon reads only it; `yolo config apply` rewrites it live for mediated loopholes and records a restart for unmediated ones. Rests on [BB-D32](boundary-broker.md#BB-D32), [BB-D108](boundary-broker.md#BB-D108) and the settings-file rulings | 2026-10-30 | [§4.6](#46-how-a-grant-reaches-the-machinery) | — |
| <a id="LG-D8"></a>LG-D8 | *Implementation decision.* `yolo loopholes enable` and `disable` refuse for every loophole naming `yolo request` and `yolo allow`, `disable` removing an old switch first; the per-workspace switch file is retired by the migration; `list` and `status` show the standing. Extends [BB-D109](boundary-broker.md#BB-D109) | 2026-10-30 | [§4.5](#45-what-goes-away) | — |
| <a id="LG-D9"></a>LG-D9 | *Implementation decision.* L4's per-workspace `off = true` survives as a pack switch: a pack off for a workspace runs no loophole machinery and holds no grant there. Its mechanism stays [`config-project.md`](config-project.md#3-the-local-file-and-turning-something-off)'s | 2026-10-30 | [§4.5](#45-what-goes-away) | — |
| <a id="LG-D10"></a>LG-D10 | *Implementation decision.* A request for a loophole inert on this setup stays out of the gate and is reported inert; `yolo host` accepts the grants and reports them as not applying. Rests on [BB-D110](boundary-broker.md#BB-D110) and the inert report | 2026-10-30 | [§7](#7-parity-across-backends-and-notches) | — |
| <a id="LG-D11"></a>LG-D11 | *Implementation decision.* The migration converts every legacy enable and setting as [§6](#6-migration)'s table says, its equality proof compares a standing per loophole per kind per workspace, and a record grant the project file does not state is reported by `yolo check` until written. Rests on [OQ-RC18](readable-config.md#decision-ledger) and, for the record-to-ceiling row, [LG-D14](#LG-D14) | 2026-10-30 | [§6](#6-migration) | — |
| <a id="LG-D12"></a><a id="OQ-LG1"></a>LG-D12 | **Owner ruling,** 2026-10-30, on [OQ-LG1](#OQ-LG1), option A: an `always` grant in the user ceiling shows once at each project's config-change gate and is recorded there; the user's edit alone grants no project anything. *"A, once at each project's gate: it is what you ruled for the broker read grant six days ago, and your words today, the project shouldn't have access until it has been approved, say the same."* | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers), [§6](#6-migration) | — |
| <a id="LG-D13"></a><a id="OQ-LG2"></a>LG-D13 | **Owner ruling,** 2026-10-30, on [OQ-LG2](#OQ-LG2): `cgroup-delegate` is ungated and its pack is selected by default, a shipped pack in the default selection rather than core code, so every loophole stays a pack's ([OQ-A10](../reference/loophole-system.md#oq-a10)). The ruling rests on the premise, checked in [`security-shim.md`](../reference/security-shim.md), that the delegate can only limit the jail: child cgroups inside the container's own, three controllers, bounded values. *"If that is true, I would even consider just making this a built-in … it should just be default-enabled. … this is basically just like making a feature possible that should be possible always."* *Implementation decisions under it:* the pack's per-workspace `off` ([LG-D9](#LG-D9)) still turns it off; a default-selected pack inert on this setup is not reported at launch, only by `yolo loopholes status`, since the user did not choose it | 2026-10-30 | [§4.3](#43-each-loopholes-access-and-its-approval), [§4.4](#44-mediated-and-unmediated-crossings) | — |
| <a id="LG-D14"></a><a id="OQ-LG3"></a>LG-D14 | **Owner ruling,** 2026-10-30, on [OQ-LG3](#OQ-LG3): yes, the migration may write a user-ceiling `on_request` line from an approval the owner gave at a launch, printing it as a hunk; and the journal needs none, because its `read` is requestable by default. *"Yes, I think it should be able to, but for this specific case, I think we've decided elsewhere that journals should just be just default enabled as a thing that can be requested."* *Implementation decisions under it:* a manifest may mark an unvalued kind requestable by default, never `always` and never a valued kind; a user's own `on_request` list replaces the default; `macos-log`'s `read` gets the same default as the journal's by [backend parity](backend-parity.md); `read_all` is not requestable by default for either | 2026-10-30 | [§4.2](#42-the-approval-in-two-layers), [§6](#6-migration) | — |

## Appendix A: evidence

Verified 2026-10-30 against `e4345e69d`. Symbols, not line numbers.

| Claim | Where |
| :--- | :--- |
| A loophole's switch is seeded `DefaultEnabled || Brokered != nil` | `resolve` in [`load.go`](../../internal/loopholes/load.go) |
| The config switch is applied only to non-brokered loopholes; one shared reader for launch and `yolo check` | `applyWorkspaceOverrides`, `SwitchedByConfig`, `EnabledUnder` in [`discover.go`](../../internal/loopholes/discover.go); `LoopholeEnabledOverride` in [`validate_loopholes.go`](../../internal/config/validate_loopholes.go) |
| A host daemon starts from `Set.Enabled()` through `Active()` and the crossing gate | `startLoopholesMatching` in [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go); `manifestHostDaemonSpecs` in [`runtime.go`](../../internal/loopholes/runtime.go) |
| Jail-side binds, devices and endpoints are admitted per loophole | `admitsJailSideEffects`, `RuntimeArgsFor` in [`runtime.go`](../../internal/loopholes/runtime.go) |
| The cgroup delegate starts in-process, gated the same way | `cgroupDelegateHonored`, `startCgroupDelegate` in [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go) |
| A brokered manifest with `default_enabled: true` is refused at load | the `brokered` check in [`loopholedecl.go`](../../internal/loopholedecl/loopholedecl.go) |
| The old manifest `enabled` key is refused, never tolerated | [`loopholedecl.go`](../../internal/loopholedecl/loopholedecl.go) |
| `host-processes` shows nothing with an empty `visible` | `disabled`, `BuildHandler` in [`hostprocesses.go`](../../internal/hostprocesses/hostprocesses.go) |
| `serial` falls back to five default globs on an empty or unreadable list | `DefaultAllowedDevices`, `loadSettings` in [`settings.go`](../../internal/serialdaemon/settings.go) |
| `journal` forces `--user` unless `full`; `macos-log` narrows to the sandbox account unless `full` | `LoadSettings`, `ParseRequest` in [`journald.go`](../../internal/journald/journald.go); `PlanMacosLog`, `macosLogKeep` in [`macoslog.go`](../../internal/journald/macoslog.go) |
| `audio` is argv only: two socket binds, an ALSA file and `/dev/snd`; no daemon, no client | [`manifest.jsonc`](../../packs/audio/loopholes/audio/loophole.toml) |
| `aws-auth` refuses to spawn without a profile and a narrowing; all four settings user-scope | `Settings.Resolve` in [`settings.go`](../../internal/awsauth/settings.go); [`manifest.jsonc`](../../packs/aws-auth/loopholes/aws-auth/loophole.toml) |
| A workspace may switch an installed non-broker loophole on; disclosed only | `loopholeScopeEnableProblems` in [`validate_loopholes.go`](../../internal/config/validate_loopholes.go) |
| Per-setting scope, `user` by default, `workspace` on four shipped settings | `DefaultSettingScope` in [`settings.go`](../../internal/loopholedecl/settings.go); `loopholeSettingsScopeViolations` in [`validate_loopholesettings.go`](../../internal/config/validate_loopholesettings.go) |
| In-jail clients read an endpoint variable or socket and print a three-step recipe | [`yolo-ps`](../../cmd/yolo-ps/main.go), [`yolo-journalctl`](../../cmd/yolo-journalctl/main.go), [`yolo-serial`](../../cmd/yolo-serial/main.go), [`yolo-log`](../../cmd/yolo-log/main.go), [`yolo-cglimit`](../../cmd/yolo-cglimit/main.go) |
| `[allow.<pack>]`: `always`/`on_request` for a broker source, declared settings for any pack; user-scope only | `ResolveReadableAllowance` in [`readableallow.go`](../../internal/config/readableallow.go); `ReadableSchemaKeys` in [`schema.go`](../../internal/config/schema.go) |
| `yolo allow` writes the user config and offers the approval; refuses in a jail. `yolo request` writes the project file and works in a jail | `allowRunW` in [`allow.go`](../../internal/cli/allow.go); `requestRunW` in [`request.go`](../../internal/cli/request.go) |
| `yolo approve` decides broker store requests only, host-only | [`approve.go`](../../internal/cli/approve.go) |
| The gate, its prompt, the flag and the records | `CheckConfigAndScopeChanges` in [`snapshot.go`](../../internal/config/snapshot.go); `changePrompter` in [`preflight.go`](../../internal/cli/run/preflight.go); [`scopeapproval.go`](../../internal/config/scopeapproval.go) |
| The per-workspace switch file, written only by `yolo loopholes enable|disable` on the host | `WorkspaceFilePath` in [`workspacefile.go`](../../internal/config/workspacefile.go); [`loopholescmd.go`](../../internal/loopholes/loopholescmd.go) |
| The rename table maps `loopholes.*.enabled` → `off` and `loopholes.*.settings.*` → `allow.*.*`; the migration declines the switch | [`renames.go`](../../internal/config/renames.go); [`lower.go`](../../internal/configmigrate/lower.go) |
| Apple Container admits two host services; `yolo host` opens doorways only | `HostServiceAdmittedOn` in [`loopholesruntime.go`](../../internal/cli/run/loopholesruntime.go); [`hostdoorways.go`](../../internal/cli/run/hostdoorways.go) |
| The readable-config plan already intends to remove `enabled`/`default_enabled` as selection mechanisms | G1 in [`readable-config-plan.md`](readable-config-plan.md) |

## Appendix B: the owner's words

Dictated 2026-10-30, lightly punctuated, read for meaning. Each row says what the design takes from it.

| They said | The design takes |
| :--- | :--- |
| *"Adding the pack should not by default give any jail any privileges given this loophole"* | Selected grants nothing ([§1](#1-the-verdict-in-four-words)); default ceiling is nothing ([§4.2](#42-the-approval-in-two-layers)) |
| *"Claude no privileges until logged in, get none until granted, github none until granted"* | The login is the approval for the two login services; the grants are the brokers' ([§4.3](#43-each-loopholes-access-and-its-approval)) |
| *"the AWS one … it can just be on because until it's configured it doesn't do anything"* | `aws-auth` runs on selection and the profile setting is its approval; ungated ([§4.3](#43-each-loopholes-access-and-its-approval)) |
| *"the journal it can be on or off I don't really care … the project shouldn't have access until it has been approved"* | Running is yolo's and hidden ([§4.1](#41-one-rule-for-every-loophole)); access is a grant at the gate ([§4.2](#42-the-approval-in-two-layers)) |
| *"on or off makes absolutely no difference, that's an implementation decision hidden from the user"* | No key for running; `default_enabled` and `enabled` go ([§4.5](#45-what-goes-away)); a lazy start is an optimization, never a rule |
| Earlier: *"default enabled should also be the choice of a pack … including the pack should be able to enable it"* | Met for every pack by running on selection; the pack's remaining choice is what can be granted ([§4.1](#41-one-rule-for-every-loophole), [§9](#9-alternatives-considered)) |
| *"I think we're conflating a few things here"* | The four words, one owner each ([§1](#1-the-verdict-in-four-words)) |
