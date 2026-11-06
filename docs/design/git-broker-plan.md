---
title: "Plan: the broker core, and git as its second source (step 1)"
date: 2026-10-23
status: accepted
stage: DECIDED
next: "Every task is built except GB-D28's removal of --add and --remember; retire this plan when the design graduates into docs/reference/git-broker.md"
depends-on: []
tags: [plan, broker, git, credentials, approvals, loopholes]
---

# Plan: the broker core, and git as its second source (step 1)

**Status:** 2026-10-23. Completed against the tree, written against `9f77afa19`.
**Design:** [`git-broker.md`](git-broker.md), every question ruled
([decision ledger](git-broker.md#12-decision-ledger)), on the broker model of
[`boundary-broker.md`](boundary-broker.md) (its ledger through [BB-D98](boundary-broker.md#BB-D98)).

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a
moved line, a renamed helper with the same role): follow it and say so in the report. This plan
is advice and the first thing to be wrong; never twist the code to match it. **Stop and report,
do not commit,** when a test does not fail the way a task predicts, a named file is dirty with
someone else's work, a named symbol is gone, the fix needs a file outside the task's fence, or a
gate fails outside the task's scope.

**Scope.** Step 1 of [the maintainer's path](boundary-broker.md#11-the-build-in-the-maintainers-five-steps)
as [`git-broker.md` §9](git-broker.md#9-the-first-slice) slices it: the broker-core factoring
([§3](git-broker.md#3-the-broker-core-what-step-1-factors-out)), the git source's `read` set, and
the `create-branch` grant with the working set. The GitHub source's resource model (write kinds,
retiring the windowed sets and `admit`, PR creation) is not in this plan.

**Terms used here.** *Broker core*, *source*, *working set*, *user grant*: as
[`boundary-broker.md` §1.3](boundary-broker.md#13-terms) and
[`git-broker.md`](git-broker.md) define them. *Repository identity* *(coined here)*: the
`host/path` string a git remote reduces to, defined in [Implementation decisions](#implementation-decisions)
(ID-3). *Remote-URL scope* *(coined here)*: a scope whose entries are exact remote URLs rather
than `OWNER/REPO` (ID-2).

## Map

| Path | Change |
| :--- | :--- |
| `internal/brokercore/lease.go` | new — the lifetime lease, moved from `internal/ghbroker/lease.go` and parameterized by source |
| `internal/brokercore/authority.go` | new — `BindLease`, `CloseLease`, `Live`: the source-neutral half of `ghbroker.Authority` |
| `internal/brokercore/rundir.go` | new — `RunRoot`, `NewRunDir`, `SweepRunDirs`, per source |
| `internal/brokercore/audit.go` | new — `StoreAudit(log, service)` |
| `internal/brokercore/lifetimes.go` | new — `Lifetimes` (jail tracking and `EndAll`), moved from `Broker.trackJail`/`endLifetimes` |
| `internal/brokercore/daemon.go` | new — `Daemon`: the `--socket`/`--scope-file` flags, fail-closed scope read, chdir, signals, `on_request`, `hostservice.ServeFrontedUnix` |
| `internal/brokercore/exit.go` | new — the shared exit codes (64, 69, 75, 77, 124) |
| `internal/brokercore/core_test.go` | new — the fake second source |
| `internal/ghbroker/{authority,lease,rundir,window,daemon,runner}.go` | thin delegates onto `brokercore`; every symbol the package's tests name survives |
| `internal/loopholedecl/brokered.go` | `remote_host: "*"`; new `grants` key |
| `internal/brokerscope/{file,live,remotes}.go` | `File.Form`, `File.Grants`; form-aware `ValidateCurrent` and `ReplaceRepos`; `ReadAllRemotes` |
| `internal/config/{gateread,scopeapproval,brokered}.go` | the remote-URL scope at the gate; `brokered.<source>.grants` (workspace) and `brokered.<source>.grant_kinds` (user) |
| `internal/cli/run/{brokeredscope,liveapply}.go` | write `Form` and `Grants` into the scope file; `yolo config apply` replaces them live |
| `internal/gitproto/` | new — pkt-line, side-band, receive-pack command and report-status parsing and writing; URL to repository identity |
| `internal/gitremote/` | new — the jail's remote helper (`git-remote-yolo`), framing git's `connect` stream to the broker |
| `internal/gitbroker/` | new — the `git-broker` daemon: policy, SSH and HTTPS upstreams, the working set, audit |
| `internal/brokerstore/working.go` | new — working-set records (`w-` ids) |
| `internal/brokeraudit/brokeraudit.go` | `Event.Remote`, `Event.Refs` (omitempty) |
| `internal/internaldaemon/internaldaemon.go` | `case "git-broker"`, and the name in the usage line |
| `internal/cli/{cli,dispatch,help,subhelp}.go` | `yolo git-remote`, routed before the front door like `gh` |
| `internal/cli/approve.go` | every registered store; `w-` ids; `--revoke all --workspace` |
| `internal/loopholes/runtime.go` | `Set.FrontedHostNames` |
| `internal/cli/run/assemble.go`, `internal/cli/run/packservices.go`, `internal/cli/check/served.go` | serve the fronted host loopholes' names (ID-6) |
| `packs/git/` | new pack — `pack.json`, `loopholes/git-broker/manifest.jsonc`, `briefing/git.md` |
| `packs/embed.go` | embed `git/` |
| `integration/gitbroker_test.go` | new — fetch, refusal, and create-branch through a real jail against a fake host `ssh` |
| `userguide/guides/git.md` | new — the git source for users |
| `userguide/guides/loopholes.md`, `userguide/reference/configuration.md`, `userguide/reference/cli-reference.md`, `userguide/reference/settings-per-setup.md` | the new loophole, keys and verbs |

## Reuse before you write

- **`brokerscope.ReadRemotes` / `readRemotes`** (`internal/brokerscope/remotes.go:82`): reads a
  workspace's git config as text with no host git, no symlinks, no includes, worktrees handled.
  `ReadAllRemotes` (task 2) is the same walk, keeping every `url` and `pushurl` value rather than
  reducing GitHub ones to `OWNER/REPO`. Never run git on the host against the workspace
  ([GB-D3](git-broker.md#GB-D3)).
- **`ghbroker.placementRefusal`** (`internal/ghbroker/daemon.go:263`): the placement rule for a
  host program the broker runs. The git broker applies it to the host `ssh` and `git` it runs.
  Move it to `brokercore` in task 1 (exported as `PlacementRefusal(program string, workspaces ...string)`),
  keeping `ghbroker.placementRefusal` as a delegate.
- **`ghbroker.Runner.buildEnv`** (`internal/ghbroker/runner.go:200`): the env-from-nothing shape.
  The SSH and credential-helper envs (task 5, task 6) copy its shape, not its contents.
- **`frameproto` and `hostservice.Session`**: the request frame, then Stdout(0)/Stderr(1)/Exit(2)
  frames. **`internal/serialdaemon/serialdaemon.go:292`** is the precedent for reading
  client-to-host frames from `s.Conn()` after the request: mirror its inbound pump.
- **`ghbroker.Forward`** (`internal/ghbroker/forward.go:51`): the jail client's dial, its
  missing-endpoint message naming `yolo loopholes enable … --workspace` with `YOLO_HOST_DIR`, and
  its error mapping for `svcendpoint.ErrEndpointMissing` and `ErrAuthRejected`. The git helper's
  messages mirror it word for word, with `git-broker` substituted.
- **Test servers:** `hostservice.ServeEndpoint` with `svcendpoint.AdvertiseHostEnv=127.0.0.1`
  (`internal/serialdaemon/serialdaemon_test.go:40`), and `hostservice.ServeFrontedUnix` plus
  `svcendpoint.ServeFront` (`internal/ghbroker/daemon_test.go:310`) for the fronted shape.
- **Test isolation:** `t.Setenv("HOME", t.TempDir())` and `t.Setenv("YOLO_USER_LAYER", "")`, as
  `authorityFixture` does (`internal/ghbroker/authority_test.go:14`); every broker path hangs off
  `paths.GlobalStorage()`.
- **The integration fixture:** `githubBrokerFixture` (`integration/githubbroker_test.go`): a fake
  host program first on the launcher's PATH, `yolo loopholes enable`, then
  `yolo check --accept-config-changes`. The git fixture copies it, with a fake `ssh`.
- **The served set:** `loopholes.Set.JailBoundNames` (`internal/loopholes/runtime.go:455`) is the
  shape `FrontedHostNames` copies, gated by the same `admitsJailSideEffects`.

## House style that isn't obvious

- **Every refusal names the next step** (`docs/reference/happy-path-principle.md`): the command,
  the key, or who can act.
- **A broker's stderr log names no workspace, no repository, no host path and no ref**
  (`internal/ghbroker/daemon.go:197`): the file is shared by every jail and sits under a directory
  a `mounts` entry commonly exposes. Log request ids and counts only; the audit log holds the rest.
- **Anything crossing into a shell** goes through `shquote` (`internal/shquote`). The SSH remote
  command is the one place this plan builds a shell string (task 5).
- **Tests that read files outside their package** need `python3 scripts/completion-census.py`
  rerun and its output committed, per AGENTS.md's Workflow step 5.

## Traps

- **`ValidPattern` guards every scope write today.** `brokerscope.ValidateCurrent`
  (`file.go:193`), `brokerscope.ReplaceReposContext` (`live.go:88`) and
  `config.WorkspaceRead.Entry` (`gateread.go:64`) refuse anything that is not `OWNER/REPO`. A URL
  scope written before task 2 makes every git request fail as out of scope, and a `yolo config
  apply` fail closed. Symptom: "not a valid repository" on a URL.
- **`remote_host` is validated as a hostname** (`loopholedecl/brokered.go:69`). `"*"` fails the
  manifest load before task 2.
- **`ghbroker` tests name unexported symbols** that a move would break: `runRoot()`, `newBroker`,
  `.store`, `.authority`, `.authority.path`, `.authority.scope`, `.delivery`, `.audit`,
  `.endLifetimes`, `startOnRequest`, `notifierNote`, `.trackJail`, `waitStatus`, plus the exported
  `BindAuthority`, `ApprovalAuthority`, `StoreAudit`, `RequiredDelivery`. Each stays, as a field
  or a delegate.
- **`Broker.Handle` reads one byte from the connection to notice cancellation**
  (`daemon.go:332`). The git broker's connection carries data the other way, so it must not copy
  that pattern: its inbound pump reads frames.
- **git chooses `connect` over `stateless-connect`** when a helper offers both
  (gitremote-helpers(7)). The helper offers `connect` only.
- **git sets no `GIT_PROTOCOL` for a `connect`-only helper.** Measured on 2026-10-23 with git
  2.55 in the task 4 review: with `protocol.version=2` the helper's request carried an empty
  `protocol` on every fetch, ls-remote and push, and all of them ran protocol v0. So the
  `SendEnv=GIT_PROTOCOL` branch of ID-7 never fires through this helper, and ID-8's v2 relay
  rests on git accepting a v2 advertisement it did not ask for: task 6's stop-and-report guard
  is the expected outcome to check first, not an edge case.
- **git ignores the helper's exit code once the connected line is out.** A broker that sent
  `0000` and then dropped made the helper exit 69 while `git fetch` exited 0 and saw an empty
  remote. A failure after the first upstream bytes must surface inside git's own protocol
  (an `ERR` packet, or a `ng` line in the report), never only as an Exit frame.
- **The helper sends no `agent`.** Nothing in the jail names the running agent (the gh
  forwarder sends none either), so the field is left out and the audit records no agent.
- **receive-pack sends the server's advertisement first.** The broker must open the upstream
  before it can read the push's commands, and so can only refuse after connecting upstream.
  Refusing then means sending the upstream a flush (an empty command list, which receive-pack
  treats as nothing to do) and answering the client itself.
- **A push of only deletes carries no pack**, and a push with `push-options` negotiated sends a
  second pkt-line section before the pack. A drain that waits for a pack in either case hangs.
- **The upstream's report may be side-band multiplexed.** Recording a created branch reads
  `ok <ref>` out of band 1 when `side-band-64k` was negotiated.
- **The integration suite runs nested**, where podman forces `--net=host`. It proves nothing about
  reachability (AGENTS.md, Testing). Report `podman info --format '{{.Host.RootlessNetworkCmd}}'`
  with any reachability claim.
- **The jail exports `YOLO_ALLOW_MISSING_PROGRAMS=1`** and other variables that skew `go test`.
  Run the gates with them unset: `env -u YOLO_ALLOW_MISSING_PROGRAMS -u YOLO_VERSION just test-fast`.
- **`git add` before any nested-image build**: nix sees tracked files only, so a new package
  vanishes from the image while `go build ./...` stays green.
- **`packs/embed.go` names its embedded trees explicitly** (`internal/packload/embeddrift_test.go`, `TestEmbedMatchesTree`). A new
  `packs/git/` that is not listed there loads in tests run from the tree and is missing from the
  binary.
- **`TestNoOfficialPackDeclaresABinary`**: `packs/git` declares no `binaries`; its daemon is
  `yolo internal daemon git-broker`.
- **An exported `GIT_CONFIG_COUNT` in the agent's own shell replaces the pack's rewrite.** That is
  git's own precedence and is not defended; the briefing says it.

## Implementation decisions

Each is the planner's, decided within the design's behavior. Each is reversible; the
**Reverse by** line says what changing it costs.

1. **ID-1 — Package layout.** The core is a new package, `internal/brokercore`; the git source is
   `internal/gitbroker`, its jail client `internal/gitremote`, and the wire-format code
   `internal/gitproto`, shared by both. *Why:* the client runs in the jail and must not import the
   daemon's upstream code; the protocol code is the one thing both halves need. *Reverse by:*
   merging packages, no behavior change.
2. **ID-2 — The git scope records the workspace's remote URLs verbatim.** A manifest declaring
   `"remote_host": "*"` makes core read every remote of the workspace on any host, `url` and
   `pushurl` alike, and put each URL in front of the human at the config-change gate, verbatim. The
   scope file records them with `"form": "remote-url"`. Requests match the exact URL by default.
   The Git broker also admits only these bounded SSH equivalences: SSH schemes with identical
   parsed destination, explicit port and path, and (only for `git@github.com` without a port) the
   two-segment relative scp path versus the corresponding absolute SSH URL path. It preserves the
   request URL for its SSH argv and audit; it does not strip `.git`, trailing slashes, case, `~/`
   or encoding, nor equate usernames, hosts, ports or generic relative/absolute paths. HTTPS and
   all other callers remain exact. An `https://` URL whose user-info carries a password is left out
   of the scope, with a gate line saying why. `brokered.git.repos` is refused by `yolo check`: a
   git remote is added with `git remote add`, which reaches the gate as a new remote. *Why:* the
   design says the upstream comes from the scope ([§7](git-broker.md#7-security)), and this narrow
   matcher gives the jail no choice of upstream endpoint. *Reverse by:* a pattern form later; the
   `form` field is what makes that additive.
3. **ID-3 — Repository identity.** Grants and working-set entries name a repository as
   `host/path`: the host lower-cased, a port kept as `host:port`, the user-info dropped, a leading
   `/` or `~/` stripped from the path, a trailing `/` then a trailing `.git` stripped. So
   `git@github.com:o/r.git`, `ssh://git@github.com/o/r` and `https://github.com/o/r.git` are all
   `github.com/o/r`. *Why:* one branch must not be two working-set entries because it has two
   remotes. *Reverse by:* re-keying the store, before any release carries it.
4. **ID-4 — The jail leg is a git remote helper.** The jail's git rewrites
   `ssh://`, `git+ssh://`, `ssh+git://`, `https://` and scp-style `git@` URLs (including bracketed
   IPv6 scp hosts) to `yolo::<original URL>`, by `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_<n>`/
   `GIT_CONFIG_VALUE_<n>` from a pack `env` contribution. Git then runs `git-remote-yolo <remote>
   <url>`, placed by an `intercept` contribution forwarding to `yolo git-remote --`. The helper
   answers `capabilities` with `connect` and nothing else. Not rewritten, and documented:
   `http://`, `git://`, `file://`, local paths, and scp-style remotes that do not start with `git@`
   (such as `host:path` and `alice@host:path`). *Why:*
   the env keeps the jail's global git config, which yolo composes, untouched, and the `yolo::`
   prefix carries the original URL to the helper unchanged. *Reverse by:* moving the rewrite into
   the composed global config.
5. **ID-5 — The jail-to-host stream.** The helper sends one request frame
   `{"service":"git-upload-pack"|"git-receive-pack","url":"<original URL>","protocol":"<GIT_PROTOCOL or empty>","agent":"<agent name or empty>"}`,
   then git's bytes as Stdout(0) frames; a Stdout frame with an empty payload means git closed its
   side. The broker answers with Stdout frames (git's protocol), Stderr frames (shown to the user)
   and one Exit frame. The helper prints git's blank "connected" line immediately before relaying
   the first host Stdout bytes; on a refusal that arrives before any, it prints the Stderr text
   and exits with the Exit code without the blank line, so git reports the helper's failure.
   `git-upload-archive` is refused. *Why:* this is the serial bridge's shape, already served by
   `hostservice`. *Reverse by:* wire change in two packages, both new.
6. **ID-6 — The rewrite exists only where the broker runs.** The env contribution is
   `served_by: "git-broker"`, and a new `loopholes.Set.FrontedHostNames(from, runtime)` lists the
   enabled loopholes whose `host_daemon` serves the jail over `loopback-tls`, gated by
   `admitsJailSideEffects`, and none on Apple Container (`runtime == "container"`), which runs no
   host services. Its names join the served set at the launch (`assemble.go`'s `servedDaemons`,
   recorded beside `o.jailBound` in `packservices.go`) and at `yolo check`
   (`check/served.go`). So with the loophole off, the jail's git is untouched, and `yolo host --`
   withholds the env as unserved. It is a config-level answer: a daemon that then fails to start
   leaves the rewrite in place, and the helper says the endpoint is missing and names `yolo check`,
   as [§6](git-broker.md#6-failure-paths) requires. *Why:* without the gate, enabling the pack
   would break every remote fetch in a jail with the loophole off, and every host git at
   `yolo host --`. *Reverse by:* deleting the method and its three call sites.

   *Built differently (Task 7):* `FrontedHostNames` also leaves out a loophole that declares a
   `jail_daemon` beside its `host_daemon`. Such a loophole (aws-auth's shape) serves its pointers
   through the jail daemon, whose address they name, and `loopholes.ServedJailDaemonNames`
   already decides where that one runs; listing it here too would deliver its pointers on a
   runtime whose jail daemon does not run there, such as the macos-user guest's declines. A
   withheld rewrite names its own reason (`loopholes.Set.FrontedHostWhy`, passed through
   `WithNotServedWhy` at the launch, at `yolo check` and at `yolo host --`): the loophole off names
   `yolo loopholes enable git-broker`, Apple Container says it runs no host services, and the
   host says its agent's git already reaches remotes with the host's own credentials.
7. **ID-7 — SSH upstream.** The broker runs the host's `ssh` (placement-checked) as
   `ssh -x -T -o ClearAllForwardings=yes -o PermitLocalCommand=no -o ConnectTimeout=30 [-p <port>] [-o SendEnv=GIT_PROTOCOL] -- <[user@]host> "<git-upload-pack|git-receive-pack> '<path>'"`,
   the path single-quoted by `shquote`, as a stateful pipe. A host starting with `-` is refused.
   Its env is built from nothing: `PATH`, `HOME`, `USER`, `SSH_AUTH_SOCK`, and, when set,
   `DISPLAY`, `WAYLAND_DISPLAY`, `SSH_ASKPASS`, `SSH_ASKPASS_REQUIRE`, plus `GIT_PROTOCOL` when the
   request carried a valid one (`version=2` or `version=1`, nothing else). No `BatchMode`, so a key
   needing a touch or passphrase prompts on the host, as the design requires. *Why:* this is what
   `git` itself runs for an SSH remote, minus forwarding. *Reverse by:* local change in one file.
8. **ID-8 — HTTPS upstream, smart HTTP.** Credentials come from the host's `git credential fill`
   (`protocol`, `host`, `path` on stdin), run placement-checked, in an empty broker-owned
   directory, with `GIT_TERMINAL_PROMPT=0`, `GIT_CEILING_DIRECTORIES=<that dir>` and an env of
   `PATH`, `HOME`, `DBUS_SESSION_BUS_ADDRESS`. A fill that returns no password is the
   "host has no credential" refusal; `approve` or `reject` is reported back to the helper after
   the first authenticated response. Fetch always asks the server for protocol v2
   (`Git-Protocol: version=2`): the broker relays the capability advertisement, then POSTs each
   client request up to its flush and relays the response. A server that answers v0 is refused:
   "the HTTPS server does not speak protocol v2; use its SSH remote". Push uses
   `info/refs?service=git-receive-pack`, relays the advertisement without the `# service` line and
   its flush, reads the commands to the flush and the pack to the client's end (no pack when every
   command is a delete), POSTs them, and relays the report. TLS uses the system roots, with no
   option to skip verification. *Why:* v2 fetch is request-response, which a stateful client
   stream maps onto stateless POSTs without a negotiation state machine. *Reverse by:* adding v0
   stateless-rpc negotiation if a server needs it.
9. **ID-9 — Push policy reads the commands before forwarding anything** and is all-or-nothing
   ([GB-D4](git-broker.md#GB-D4)). A ref outside `refs/heads/` and `refs/tags/` is refused always.
   In this step every other ref no grant or working-set entry covers is refused with
   `needs approval, which this version cannot ask for` (no request is filed: requests turn on in
   step 3). Refusing answers the client with `unpack ok` and one `ng <ref> <reason>` per command,
   side-band wrapped if the client negotiated it. A creation (old id all zeros) under `refs/heads/`
   is covered by a `create-branch` grant on the repository identity or `*`; an update or delete is
   covered by a live working-set entry for that ref. No force detection in this step: a force
   push to a working-set ref is an update. *Why:* the design's [§4.2](git-broker.md#42-policy-read-from-the-protocol) and [§5](git-broker.md#5-the-git-sources-sets). *Reverse by:* none
   needed; step 3 replaces the refusal with filing.
10. **ID-10 — Grant configuration** ([BB-D96](boundary-broker.md#BB-D96)). The manifest's
    `brokered` block gains `grants`, the kinds the source defines (`["create-branch"]` for git;
    none for github in this plan). User config: `brokered.<source>.grant_kinds: ["create-branch"]`.
    Workspace config: `brokered.<source>.grants: [{"kind": "create-branch", "repo": "github.com/o/r"}]`,
    `"repo": "*"` being the written-out wildcard. `yolo check` refuses a workspace grant of a kind
    the source does not define, a kind the user config does not list (naming
    `brokered.<source>.grant_kinds` in the user config), a `repo` that is not a repository identity
    or `*`, `grant_kinds` at workspace scope and `grants` at user scope. `read` is implied by the
    loophole being on; writing it warns. Grants appear in the config-change gate as their own lines
    under the source's scope block, are recorded under the approval record key `<source>.grants`
    (a source name cannot contain `.`, so it collides with nothing), and are written to the scope
    file's `grants`. At every push the broker reads the live scope file's grants and re-reads the
    user config's `grant_kinds`, so removing the kind on the host takes effect at the next push.
    `yolo config apply` replaces a running jail's grants as it replaces its repositories. *Why:*
    the gate is the "config-change approval" BB-D96 names, and `config apply` is that gate run
    live. *Reverse by:* key renames before release; `yolo config-ref` is the authority once built.

    **ID-10a — A grant's `repo` needs a host with a `.` or a `:`.** *Implementation decision*,
    made in Task 8. A grant's `repo` is `*` or a repository identity (ID-3) whose host contains
    a `.` or a `:` (a port). So a scope entry on `git@gitserver:team/app.git`, whose identity is
    `gitserver/team/app`, is admittable, but a grant covering it must be `"*"`. *Why:* so the
    GitHub shorthand `o/r` written as a grant's `repo` is refused, rather than read as host `o`
    and path `r`. *Reverse by:* accepting a
    single-label host when the identity has two or more path segments after it.
11. **ID-11 — The working set's records.** A new record kind in the source's store,
    `working/w-<id>.json`: `ID`, `Workspace`, `Repo` (identity), `Kind` (`"branch"`), `Resource`
    (the ref), `Added`, `By` (`"create-branch"`, a grant id, or `"human"`), `Ended`, `EndReason`
    (`deleted`, `gone`, `revoked`). One live entry per `(Workspace, Repo, Resource)`; adding a
    duplicate is a no-op. A creation the upstream did not report `ok` records nothing. A creation
    on a ref that already has a live entry means the branch was deleted upstream: the entry ends
    `gone` and the push is judged as a creation. A delete reported `ok` ends the entry `deleted`.
    Writes go through the store's existing transaction lock. *Why:*
    [§5.1](git-broker.md#51-the-working-set-of-pushable-branches) and
    [BB-D85](boundary-broker.md#BB-D85). *Reverse by:* store migration, before any release
    carries it.
12. **ID-12 — The git source ships as its own pack**, `packs/git`, with loophole `git-broker`,
    `default_enabled: false`, enabled per workspace by `yolo loopholes enable git-broker`
    ([GB-D5](git-broker.md#GB-D5) left the pack to the implementer). *Why:* selecting the github
    pack must not include git ([OQ-BB16](boundary-broker.md#OQ-BB16)), and a pack selected without
    its loophole enabled changes nothing (ID-6). Its `credential_paths` fence `~/.ssh`,
    `$SSH_AUTH_SOCK`, `~/.git-credentials` and `~/.config/git/credentials`, so with the pack
    selected a workspace `mounts` entry reaching one is refused and a user-scope one disclosed
    ([BB-D26](boundary-broker.md#BB-D26)). *Reverse by:* moving the contributions into another
    pack; trimming the fence list.
13. **ID-13 — Audit.** Git events use `Service: "git"`, `Repo` the identity, `Remote` the URL,
    `Set` one of `read`, `create-branch`, `working-set`, `refused`, and `Refs`, one
    `{ref, old, new, covered_by}` per command, `covered_by` naming the grant kind or the `w-` id.
    `Argv` holds `[service]` alone. *Why:* `yolo audit --service git` must answer "what did it push
    with my access" ([§7](git-broker.md#7-security)). *Reverse by:* additive fields only.

## Tasks

Build order: **1, 2 and 3 are independent.** 4 after 3. 5 after 1, 2, 3 and 4. 6 after 5.
7 after 5. 8 after 2. 9 after 7 and 8. Task 1 unblocks every later source and gets more
expensive with every change to `internal/ghbroker`; land it first.

### Task 1 — factor the broker core (no behavior change)

- **Settled, do not re-open:** *"The factoring is a refactor with no behavior change"*; done is
  the github-broker's tests passing unchanged on the core plus a gh source, and a fake second
  source filing, granting and auditing through the same core
  ([§3](git-broker.md#3-the-broker-core-what-step-1-factors-out)).
- **Preconditions:** `go test -short ./internal/ghbroker/... ./internal/cli/...` is green on a
  clean tree. If not, stop and report.
- **Tests first:** `internal/brokercore/core_test.go`. Red because the package does not exist.

  ```go
  package brokercore

  import (
  	"context"
  	"os"
  	"path/filepath"
  	"strings"
  	"testing"
  	"time"

  	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
  	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
  	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
  	"github.com/mschulkind-oss/yolo-jail/internal/paths"
  )

  // A second source, named "fake", files, is granted, and audits through the same core the
  // github source uses, in its own store directory and with its own audit service.
  func TestFakeSourceFilesGrantsAndAuditsThroughTheCore(t *testing.T) {
  	t.Setenv("HOME", t.TempDir())
  	t.Setenv("YOLO_USER_LAYER", "")
  	const source = "fake"
  	sf := brokerscope.File{Source: source, LaunchID: "fixture", PID: os.Getpid(),
  		Container: "fixture-container", Workspace: "/fixture", Repos: []string{"o/r"}}
  	path, err := brokerscope.Write(sf)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if want := paths.BrokerScopeFile(source, "fixture"); path != want {
  		t.Fatalf("scope file at %s, want %s", path, want)
  	}
  	scope, lease, err := BindLease(source, path, sf, "fresh-start")
  	if err != nil {
  		t.Fatal(err)
  	}
  	t.Cleanup(func() { _ = CloseLease(source, lease) })
  	if _, err := Live(source, path, scope, "fresh-start"); err != nil {
  		t.Fatalf("a bound lease is not live: %v", err)
  	}
  	log := brokeraudit.Open(paths.BrokerAuditLog(), nil)
  	store := &brokerstore.Store{Dir: paths.BrokerSourceDir(source), Source: source,
  		Revalidate: func(brokerstore.Request) (brokerstore.Policy, error) {
  			return brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 15 * time.Minute, Uses: 1}, nil
  		},
  		Audit: StoreAudit(log, source)}
  	q, err := store.FileContext(context.Background(), brokerstore.Request{
  		Jail: paths.JailShortHash(sf.Container), Start: "fresh-start", Workspace: sf.Workspace,
  		LaunchID: sf.LaunchID, Argv: []string{"write"}, Repo: "o/r", Set: "fake-write",
  		Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 15 * time.Minute, Uses: 1}})
  	if err != nil {
  		t.Fatal(err)
  	}
  	if _, err := store.Decide(q.ID, "pending", brokerstore.Decision{}); err != nil {
  		t.Fatal(err)
  	}
  	if got := filepath.Dir(filepath.Dir(RunRoot(source))); got != paths.BrokerDir() {
  		t.Fatalf("run root %s is not under the broker dir", RunRoot(source))
  	}
  	data, err := os.ReadFile(paths.BrokerAuditLog())
  	if err != nil {
  		t.Fatal(err)
  	}
  	if !strings.Contains(string(data), `"service":"fake"`) {
  		t.Fatalf("audit log has no fake-source event:\n%s", data)
  	}
  }
  ```

  Red: `go test -short ./internal/brokercore/` fails to build (no package). If the `Decide`
  signature or the audit `service` key spelling differs at the stamped commit, follow the tree
  and say so; the assertion is that the event carries the fake source's name.
- **Change:** move into `brokercore`, parameterized by `source string` where the original reads
  `ghbroker.Source`: `lease.go` whole; `BindAuthority`'s lease-acquire closure and `Authority.live`
  as `BindLease`/`Live`, `Authority.Close` as `CloseLease`; `rundir.go`'s three functions;
  `StoreAudit` with a service argument; `Broker.trackJail`/`endLifetimes` as a `Lifetimes` value
  the broker embeds by pointer; `placementRefusal` as `PlacementRefusal(program, workspaces...)`
  (its message names the program instead of `gh`); `Main`'s flag parsing, fail-closed scope read,
  chdir, signal wiring, `startOnRequest` and `ServeFrontedUnix` as a `Daemon` struct the source
  fills with a name, a `NewHandler` and an `OnStop`. `ghbroker` keeps every name its tests use, as
  one-line delegates or unchanged struct fields. `Authority` keeps its `path`, `scope`, `start`,
  `lease` fields; `Authority.Classify` and `Revalidate` stay in `ghbroker`, since they are gh's.
- **Gates:** `go test -short ./internal/brokercore/ ./internal/ghbroker/... ./internal/cli/...`;
  `git diff --stat main -- 'internal/ghbroker/*_test.go'` prints nothing, or only import and
  identifier lines listed in the commit body; `just check-ci`; then
  `go test -run 'GitHubBroker' ./integration` (the launch path runs the daemon).
- **Fences:** no behavior change, so no assertion in any `_test.go` changes; an assertion that
  must change is a stop-and-report. Nothing outside `internal/brokercore/` and `internal/ghbroker/`.
- **Commit:** `refactor(broker): factor the source-neutral broker core out of ghbroker`
- **Report:** the red build, the green gates, the `_test.go` diff stat, and any delegate whose
  only caller is a test.

### Task 2 — the remote-URL scope form

- **Settled, do not re-open:** the git source reads every remote of the workspace, on any host
  ([§3](git-broker.md#3-the-broker-core-what-step-1-factors-out)); remotes join only at the
  human's y at the config-change gate ([OQ-BB18](boundary-broker.md#OQ-BB18)); the upstream comes
  from the scope, never the jail ([§7](git-broker.md#7-security)). ID-2.
- **Preconditions:** none beyond a clean tree.
- **Tests first:**
  - `internal/loopholedecl/brokered_test.go`, `TestBrokeredRemoteHostWildcard`: a manifest with
    `"brokered": {"source": "git", "remote_host": "*"}` parses with `RemoteHost == "*"`, and
    `"remote_host": "*.com"` is still refused.
  - `internal/brokerscope/brokerscope_test.go`, `TestReadAllRemotesKeepsEveryURL`: a workspace whose
    `.git/config` is

    ```ini
    [remote "origin"]
    	url = git@github.com:o/r.git
    	pushurl = ssh://git@github.com/o/r
    [remote "lab"]
    	url = https://gitlab.example.com/team/app.git
    [remote "leak"]
    	url = https://me:hunter2@git.example.com/x.git
    ```

    yields URLs `git@github.com:o/r.git`, `ssh://git@github.com/o/r`,
    `https://gitlab.example.com/team/app.git` in that order, and one problem line that names
    remote `leak` and contains neither `hunter2` nor `me:`.
  - `internal/brokerscope/brokerscope_test.go`, `TestValidateCurrentRemoteURLForm`: a file with
    `Form: "remote-url"`, `Repos: ["git@github.com:o/r.git"]` accepts that exact string and
    refuses `git@github.com:o/r` (no `.git`) and `git@evil.example:o/r.git`; a file with
    `Form: ""` still refuses a URL.
  - `internal/brokerscope/brokerscope_test.go`, `TestReplaceReposRemoteURLForm`: `ReplaceRepos` on a
    `remote-url` file accepts a URL list; on an owner-repo file it refuses one.
  - `internal/cli/run/brokeredscope_test.go`, `TestWriteScopeFilesRemoteURLForm`: through
    `writeScopeFiles`' own call site, a loophole whose `brokered.remote_host` is `*` writes a scope
    file with `"form": "remote-url"` and the gate's approved URLs. It must fail if the call site
    stops passing the form.
  - `internal/config/brokered_test.go`, `TestBrokeredReposRefusedForWildcardSource`: a workspace
    config with `brokered.git.repos` and a resolver declaring `git` with `remote_host: "*"` gets an
    error naming `git remote add`.

  Red: each fails (unknown host form, missing function or field, or no refusal).
- **Change:** `loopholedecl.parseBrokered` accepts `*` for `remote_host`. `brokerscope.File`
  gains `Form string \`json:"form,omitempty"\``; `ValidateCurrent` and `ReplaceReposContext`
  branch on the file's `Form`, never on the source name. `brokerscope.ReadAllRemotes(workspace)`
  reuses `readRemotes`' walk. `config.NewScopeCheck` and `ScopeSource` carry the form: for a `*`
  broker, `Read` lists URLs and `Entry` is empty. `writeScopeFiles` writes `Form`.
  `validateBrokeredWorkspace` refuses `repos` for a wildcard source, through the resolver.
- **Gates:** `go test -short ./internal/loopholedecl/ ./internal/brokerscope/ ./internal/config/ ./internal/cli/run/`;
  `just check-ci`; `go test -run 'GitHubBroker|Scope|ConfigApply' ./integration`, since the gate
  and `config apply` paths are shared.
- **Fences:** the github source's behavior: an owner-repo file reads, validates and replaces
  exactly as before.
- **Commit:** `feat(broker): a remote-URL scope form for a source on any host`
- **Report:** the red runs, the green gates, and the gate's text for the three-remote fixture.

### Task 3 — git's wire formats (`internal/gitproto`)

- **Settled, do not re-open:** a push's ref commands are read before anything is forwarded; a
  refusal is git's own `! [remote rejected] <ref> (<reason>)` line
  ([§4.2](git-broker.md#42-policy-read-from-the-protocol)). ID-3, ID-9.
- **Preconditions:** none.
- **Tests first:** `internal/gitproto/gitproto_test.go`.

  ```go
  package gitproto

  import (
  	"bytes"
  	"strings"
  	"testing"
  )

  func TestIdentity(t *testing.T) {
  	for in, want := range map[string]string{
  		"git@github.com:o/r.git":          "github.com/o/r",
  		"ssh://git@github.com/o/r":        "github.com/o/r",
  		"https://github.com/o/r.git":      "github.com/o/r",
  		"https://GitHub.com/o/r/":         "github.com/o/r",
  		"ssh://git@host:2222/~/repo.git":  "host:2222/repo",
  		"https://gitlab.example.com/a/b/c": "gitlab.example.com/a/b/c",
  	} {
  		got, err := Identity(in)
  		if err != nil || got != want {
  			t.Errorf("Identity(%q) = %q, %v; want %q", in, got, err, want)
  		}
  	}
  	for _, bad := range []string{"", "file:///x", "/srv/x.git", "ssh://-oProxyCommand=x/r", "git@:r"} {
  		if _, err := Identity(bad); err == nil {
  			t.Errorf("Identity(%q) accepted", bad)
  		}
  	}
  }

  const zero = "0000000000000000000000000000000000000000"

  func TestReadCommands(t *testing.T) {
  	var in bytes.Buffer
  	w := NewWriter(&in)
  	w.Line(zero + " 1111111111111111111111111111111111111111 refs/heads/agent/x\x00report-status side-band-64k push-options\n")
  	w.Line("2222222222222222222222222222222222222222 " + zero + " refs/heads/old\n")
  	w.Flush()
  	w.Line("ci.skip\n")
  	w.Flush()
  	cmds, caps, opts, err := ReadCommands(NewReader(&in))
  	if err != nil {
  		t.Fatal(err)
  	}
  	if len(cmds) != 2 || !cmds[0].Create() || !cmds[1].Delete() || cmds[0].Ref != "refs/heads/agent/x" {
  		t.Fatalf("commands %+v", cmds)
  	}
  	if !caps.Has("side-band-64k") || !caps.Has("push-options") || len(opts) != 1 {
  		t.Fatalf("caps %v opts %v", caps, opts)
  	}
  	if NeedsPack(cmds[1:]) {
  		t.Fatal("a delete-only push carries no pack")
  	}
  }

  func TestRefusalReportIsGitsOwnLine(t *testing.T) {
  	var out bytes.Buffer
  	cmds := []Command{{Old: zero, New: "1111111111111111111111111111111111111111", Ref: "refs/heads/main"}}
  	WriteRefusal(&out, Caps{"report-status": true, "side-band-64k": true}, cmds,
  		"needs approval, which this version cannot ask for")
  	report, err := ReadReport(NewReader(&out), true)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if report.Unpack != "ok" || report.Refs["refs/heads/main"] != "needs approval, which this version cannot ask for" {
  		t.Fatalf("report %+v", report)
  	}
  	if !strings.Contains(out.String(), "ng refs/heads/main ") {
  		t.Fatalf("no ng line in %q", out.String())
  	}
  }
  ```

  Red: `go test -short ./internal/gitproto/` fails to build.
- **Change:** new package: pkt-line `Reader`/`Writer` (flush `0000`, delim `0001`, response-end
  `0002`, length cap 65520), `Caps`, `Command` with `Create`/`Delete`, `ReadCommands`,
  `NeedsPack`, `WriteRefusal`, `ReadReport` (demultiplexing side-band when asked), `Identity`.
  Names are cheap and the implementer's; the test's are the contract.
- **Gates:** `go test -short ./internal/gitproto/`; add a fuzz target for `ReadCommands` beside the
  repo's other fuzz tests and run it for 30 seconds once.
- **Fences:** nothing outside the new package.
- **Commit:** `feat(gitproto): pkt-line, receive-pack commands and report-status`
- **Report:** red, green, and the fuzz run's line count.

### Task 4 — the jail's remote helper (`yolo git-remote`)

- **Settled, do not re-open:** the jail runs the real git, unchanged; the leg to the broker
  carries no git credential; the supported remote forms are rewritten to the broker
  ([§4.1](git-broker.md#41-the-jails-leg)). ID-4, ID-5.
- **Preconditions:** task 3 landed.
- **Tests first:** `internal/gitremote/helper_test.go`.
  - `TestHelperAdvertisesConnectOnly`: feeding `capabilities\n` yields exactly `connect\n\n`.
  - `TestHelperConnectRelaysBothWays`: against an in-test endpoint
    (`hostservice.ServeEndpoint`, `svcendpoint.AdvertiseHostEnv=127.0.0.1`) whose handler reads
    the request frame, asserts
    `{"service":"git-upload-pack","url":"ssh://git@github.com/o/r","protocol":"version=2"}`
    (with `GIT_PROTOCOL=version=2` in the helper's env), echoes each Stdout frame back with
    `echo:` prepended, and exits 0 after the empty frame. Input
    `capabilities\nconnect git-upload-pack\nhello` then EOF yields `connect\n\n`, then `\n`, then
    `echo:hello`.
  - `TestHelperRefusalHasNoConnectedLine`: a handler writing Stderr
    `not in this workspace's approved scope` and Exit 77 makes the helper print that to stderr,
    write nothing after `connect\n\n` to stdout, and return 77.
  - `TestHelperNoEndpointNamesTheSwitch`: with `YOLO_SERVICE_GIT_BROKER_ENDPOINT` unset and
    `YOLO_HOST_DIR=/home/u/app`, stderr contains
    `yolo loopholes enable git-broker --workspace /home/u/app` and the exit is 69.
  - `internal/cli/gitremote_test.go` (mirroring `gh_test.go`), `TestGitRemoteRoutedBeforeTheFrontDoor`: `yolo git-remote --`
    with stdin `capabilities\n` prints `connect\n\n` and nothing else on stdout (no banner),
    through `cli.Main`'s own routing.

  Red: the packages and verb do not exist.
- **Change:** `internal/gitremote` implements the helper loop (`capabilities`, `connect <service>`,
  and refusing anything else with the helper protocol's error form). `internal/cli/gitremote.go`
  is the verb; route it in `cli.go` next to `gh` (`cli.go:84`), register it in `dispatch.go`,
  `help.go` (`"In a jail: git's remote helper for the git-broker"`) and `subhelp.go`. The endpoint
  variable is derived exactly as `ghbroker.EndpointEnv` is, from the name `git-broker`.
- **Gates:** `go test -short ./internal/gitremote/ ./internal/cli/`; `just check-ci`.
- **Fences:** `internal/ghbroker/forward.go` is not edited; copy its messages, do not share code
  across the jail/host boundary through `ghbroker`.
- **Commit:** `feat(gitremote): the jail's git remote helper over the broker's endpoint`
- **Report:** red, green, and the exact no-endpoint message.

### Task 5 — the git-broker daemon: reads, SSH upstream, pushes refused per ref

- **Settled, do not re-open:** a fetch in scope is a `read`, forwarded and streamed back; the
  broker connects upstream the way the host does; a push's refs are judged before anything is
  forwarded; until writes turn on, a push nothing covers answers "needs approval, which this
  version cannot ask for"; the upstream receives nothing on a refused push; out-of-scope is
  refused naming the scope, audited, with no request ([§4](git-broker.md#4-how-git-crosses),
  [§6](git-broker.md#6-failure-paths), [§9](git-broker.md#9-the-first-slice)). ID-7, ID-9, ID-13.
- **Preconditions:** tasks 1 to 4 landed. `command -v git ssh` on the test host; the tests use
  only a fake `ssh`.
- **Tests first:** `internal/gitbroker/broker_test.go`, with a fixture that:
  1. creates a bare repository `up.git` with one commit on `main`, and a workspace clone whose
     `origin` is `git@fixture.test:o/r.git`;
  2. writes a fake `ssh` first on the broker's `PATH`, a shell script that records its argv to a
     file and execs `sh -c "$last_argument"` with `cd` into the directory holding `up.git`
     (so `git-upload-pack 'o/r.git'` runs against the fixture);
  3. writes a `remote-url` scope file approving `git@fixture.test:o/r.git`, binds the broker via
     `brokercore.BindLease`, and serves it with `hostservice.ServeEndpoint`;
  4. runs the real `git` in the workspace with `GIT_CONFIG_COUNT=1`,
     `GIT_CONFIG_KEY_0=url.yolo::git@.insteadOf`, `GIT_CONFIG_VALUE_0=git@`, a `PATH` whose first
     entry holds a `git-remote-yolo` script that re-runs the test binary as the helper
     (`exec "$TEST_BINARY" -test.run=^TestHelperProcess$ -- "$@"` with `GO_HELPER=1`), and the
     endpoint variable pointing at the fixture's endpoint.

  Cases:
  - `TestFetchThroughTheBroker`: `git fetch origin` exits 0, `refs/remotes/origin/main` equals
    the bare repository's `main`, the fake `ssh` argv ends with
    `fixture.test git-upload-pack 'o/r.git'` and contains `-o ClearAllForwardings=yes`, and the
    audit log has one event with `"service":"git"`, `"set":"read"`, `"repo":"fixture.test/o/r"`.
  - `TestPushRefusedNothingForwarded`: after a local commit, `git push origin HEAD:main` exits 1,
    its stderr contains `! [remote rejected]` and
    `needs approval, which this version cannot ask for`, the bare repository's `main` is
    unchanged, and the audit event has `"set":"refused"` and one `refs` entry for
    `refs/heads/main`.
  - `TestPushOutsideHeadsAndTagsRefused`: `git push origin HEAD:refs/notes/x` is refused with a
    reason naming `refs/heads/` and `refs/tags/`.
  - `TestOutOfScopeRefused`: a remote `git@other.test:o/r.git` not in the scope fails
    `git fetch` with stderr naming the approved scope and the config-change gate, and the fake
    `ssh` never ran.
  - `TestSSHHostStartingWithDashRefused`: a scope entry `ssh://-oProxyCommand=x/r` (written
    directly to the scope file) is refused before any `ssh` runs.
  - `TestOnlySSHAndHTTPSTransports`: scope entries `file:///srv/r.git`, `ext::sh -c x`,
    `fd::3` and a scp-style path starting with `-` (each written directly to the scope file,
    which accepts any transport string) are refused before any upstream starts; the broker
    allows only the supported SSH URL forms (`ssh://`, `git+ssh://`, `ssh+git://` and scp-style `user@host:path`, including bracketed IPv6 hosts), plus `https://`.
  - The receive-pack path forwards the bytes `gitproto.Reader.Tee` buffered, only after the
    commands are judged, and never re-encodes them with `WriteCommands` (which drops shallow
    lines and re-orders capabilities).
  - `TestSSHEnvBuiltFromNothing`: with `GH_TOKEN=leak` and `AWS_SECRET_ACCESS_KEY=leak` in the
    broker's environment, the fake `ssh` records an env containing `SSH_AUTH_SOCK` (set by the
    test) and neither leak.
  - `TestNoHostCredentialNamesTheFix`: a fake `ssh` that exits 255 printing
    `Permission denied (publickey).` makes `git fetch` fail with that line passed through and a
    broker line naming `ssh-add -l` on the host.
  - `TestBrokerLogNamesNoRepository`: the broker's stderr log after the fetch and refused push
    contains neither `fixture.test`, `o/r` nor the workspace path.

  Red: the package does not exist.
- **Change:** `internal/gitbroker`: a `Broker` on `brokercore.Daemon` with `Source = "git"`; the
  session handler reads the ID-5 request, checks the URL against the live scope
  (`brokercore.Live`, then `brokerscope.ValidateCurrent`), starts the upstream (SSH only in this
  task; an `https://` scope entry answers "HTTPS remotes arrive in the next build; use the SSH
  remote" until task 6), pumps upload-pack both ways, and for receive-pack reads the advertisement,
  relays it, reads the commands, applies ID-9 and either forwards or refuses. Add
  `case "git-broker": return gitbroker.Main(rest)` and the name in the usage line of
  `internal/internaldaemon/internaldaemon.go`. `brokeraudit.Event` gains `Remote` and `Refs`.
- **Gates:** `go test -short ./internal/gitbroker/ ./internal/brokeraudit/ ./internal/internaldaemon/`;
  `just check-ci`.
- **Fences:** no grants and no working set yet (every push is refused); no pack files; nothing
  under `internal/ghbroker/`.
- **Commit:** `feat(gitbroker): git fetch through the host's SSH, pushes refused per ref`
- **Report:** red, green, and the refused-push stderr verbatim. (Whether git passes
  `GIT_PROTOCOL` to the helper is already measured: it does not; see the pitfalls above.)

### Task 6 — the HTTPS upstream

- **Settled, do not re-open:** an HTTPS remote uses the host's `git credential fill`; the broker
  never starts a login and never prompts for a username or password; it speaks smart HTTP
  ([§4.3](git-broker.md#43-the-upstream-leg-the-hosts-own-credentials),
  [§6](git-broker.md#6-failure-paths)). ID-8.
- **Preconditions:** task 5 landed; `ls "$(git --exec-path)/git-http-backend"` succeeds. If it
  does not, stop and report: the tests need it.
- **Tests first:** `internal/gitbroker/https_test.go`, serving the fixture's bare repository with
  `git http-backend` behind `net/http/cgi` on an `httptest.NewTLSServer` with HTTP basic auth
  (`fixture`/`s3cret`), the broker trusting the test server's certificate through an unexported
  test seam, and a fake host `git` first on `PATH` that answers `credential fill` with
  `username=fixture` and `password=s3cret` (and records what it was asked, and its cwd).
  - `TestHTTPSFetchProtocolV2`: `git fetch origin` over `https://127.0.0.1:<port>/o/r.git`
    succeeds; every request the server saw carried `Git-Protocol: version=2`.
  - `TestHTTPSPushRefusedNothingForwarded`: `git push origin HEAD:main` exits 1 with
    `! [remote rejected]`, the server saw the `info/refs?service=git-receive-pack` GET and no
    POST, and the bare repository is unchanged. (A forwarded HTTPS push is task 9's
    `TestCreateBranchOverHTTPS`.)
  - `TestHTTPSV0ServerRefused`: a server that answers the v2 probe with a v0 advertisement fails
    the fetch with `does not speak protocol v2; use its SSH remote`.
  - `TestCredentialFillRunsInAnEmptyBrokerDir`: the fake `git` saw `protocol=https`,
    `host=127.0.0.1:<port>`, `path=o/r.git`, a cwd under `paths.BrokerSourceDir("git")` that is
    not the workspace, and `GIT_TERMINAL_PROMPT=0`.
  - `TestNoCredentialNamesTheFix`: a fill returning no password fails the fetch naming
    `git credential fill` on the host, and the server saw no request with credentials.

  Red: HTTPS answers "arrive in the next build".
- **Change:** an HTTPS upstream beside the SSH one, chosen by the approved URL's scheme. Write
  `TestHTTPSFetchProtocolV2` first and run it before anything else: git sends a `connect`
  helper no `GIT_PROTOCOL` (pitfalls above), so the client starts in v0. If real
  git rejects the v2 relay through a `connect` helper, or does not close its side after the pack,
  **stop and report**: ID-8 rests on both, and the fallback (v0 stateless negotiation) is a
  larger task that needs re-planning.
- **Gates:** `go test -short ./internal/gitbroker/`; `just check-ci`.
- **Fences:** no option to skip TLS verification, in config or env.
- **Commit:** `feat(gitbroker): smart-HTTP upstream through the host's credential helper`
- **Report:** red, green, and the request log of one v2 fetch.

### Task 7 — the `git` pack, the served rewrite, and the read path end to end

- **Settled, do not re-open:** the source is off until the user includes it, and nothing in a
  workspace's own config can include it ([GB-D5](git-broker.md#GB-D5)); a workspace the user did
  not include is refused naming the switch ([§6](git-broker.md#6-failure-paths)). ID-6, ID-12.
- **Preconditions:** task 5 landed.
- **Tests first:**
  - `internal/loopholes/runtime_test.go`, `TestFrontedHostNames`: an enabled loopback-TLS host
    daemon loophole is listed on `podman` and `macos-user`, not on `container`, and a disabled one
    is never listed.
  - `internal/cli/check/served_test.go`, `TestGitRewriteServedOnlyWithTheLoophole`: through
    `check/served.go`'s own call site, with packs `["git"]` and `git-broker` disabled, the
    composed env lacks `GIT_CONFIG_COUNT`; enabled, it carries
    `GIT_CONFIG_KEY_0=url.yolo::ssh://.insteadOf`. It must fail if the call site stops appending
    `FrontedHostNames`.
  - `internal/packload/served_test.go`, `TestGitRewriteWithheldWhereUnserved`, built like
    `TestAPointerIsDeliveredOnlyWhereItsDaemonIsServed` with `embeddedNamed(t, "git")`:
    `ScopeCredentials` with `Served: NothingServed()` (the host notch) folds no
    `GIT_CONFIG_COUNT`; with `ServedInJail([]string{"git-broker"})` it folds all eleven keys.
  - `internal/packload/embeddrift_test.go`'s `TestEmbedMatchesTree` goes red when `packs/git/` exists unlisted.
  - `integration/gitbroker_test.go`, `TestGitBrokerFetchAndRefusal`: copying
    `githubBrokerFixture`, a fake host `ssh` first on the launcher's PATH serving a bare
    repository; `yolo loopholes enable git-broker`; `yolo check --accept-config-changes`; then in
    the jail `git fetch origin` succeeds, `git push origin HEAD:main` prints
    `! [remote rejected]` and exits 1, the bare repository is unchanged, and `yolo audit --service git`
    on the host shows one `read` and one `refused` line. A second case with the loophole not
    enabled: `git fetch origin` in the jail fails as it would with no broker (the remote is not
    rewritten), and `env | grep GIT_CONFIG_COUNT` is empty.

  Red: no pack; no method.
- **Change:** `packs/git/pack.json` with three contributions: the loophole
  `loopholes/git-broker`, the briefing `briefing/git.md`, the intercept
  `{"bin": "git-remote-yolo", "forward": ["yolo", "git-remote", "--"]}`, and an `env`
  contribution `served_by: "git-broker"` with `GIT_CONFIG_COUNT=5` and rewrites for `ssh://`,
  `https://`, `git@`, `git+ssh://` and `ssh+git://`. Bracketed IPv6 scp remotes use the same
  broker transport. The manifest mirrors
  `packs/github/loopholes/github-broker/manifest.jsonc`: `default_enabled: false`,
  `transport: "loopback-tls"`, `lifecycle: "spawned"`,
  `brokered: {"source": "git", "remote_host": "*", "credential_paths": ["~/.ssh", "$SSH_AUTH_SOCK", "~/.git-credentials", "~/.config/git/credentials"]}`,
  `host_daemon.cmd: ["yolo","internal","daemon","git-broker","--socket","{socket}","--scope-file","{repository_scope}"]`,
  `publishes: "socket"`, and a `doctor_cmd` with `--self-check` that reports the host `ssh` and
  `git` it would run. Add `git/` to `packs/embed.go`. `loopholes.Set.FrontedHostNames` and its
  three call sites. The briefing tells the agent: fetch works; pushes need a grant; read
  `! [remote rejected]` reasons; never set `GIT_CONFIG_COUNT`.
- **Gates:** `go test -short ./internal/loopholes/ ./internal/cli/... ./internal/packload/`;
  `just check-ci`; `just build-dev-bundle`, then a nested jail from a throwaway workspace with the
  loophole enabled, running `git fetch` against a fake-ssh remote; `go test -run 'GitBroker|GitHubBroker' ./integration`.
- **Fences:** `packs/github/` is not edited.
- **Commit:** `feat(packs): the git pack and its git-broker loophole, reads only`
- **Report:** red, green, the nested run's transcript, and `podman info --format '{{.Host.RootlessNetworkCmd}}'`.

### Task 8 — grants in two layers

- **Settled, do not re-open:** *"the user's own config lists which kinds of grant may be held;
  the workspace config holds the specific grants, each naming its resource (a wildcard written out
  as one), and each joins only at the config-change approval and only within the kinds the user's
  config allows. A grant of a kind the user's config does not allow is refused by `yolo check`,
  naming the user-config key that would allow it"* ([BB-D96](boundary-broker.md#BB-D96)). ID-10.
- **Preconditions:** task 2 landed.
- **Tests first:**
  - `internal/loopholedecl/brokered_test.go`, `TestBrokeredGrants`: `"grants": ["create-branch"]`
    parses; `"grants": ["Create Branch"]` is refused.
  - `internal/config/brokered_test.go`:
    - `TestGrantKindNotAllowedNamesUserKey`: workspace
      `{"brokered": {"git": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`
      with a user config lacking `brokered.git.grant_kinds` errors with text containing
      `brokered.git.grant_kinds` and `create-branch`.
    - `TestGrantKindAllowedPasses`: the same with user `{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`
      passes.
    - `TestGrantShapeRefusals`: a kind git does not define (`create-pr`), a repo `o/r` (no host),
      `grant_kinds` at workspace scope, and `grants` at user scope are each refused with their own
      message; `{"kind": "read", ...}` warns.
  - `internal/config/scopeapproval_test.go`, `TestGrantJoinsOnlyAtTheGate`: a new grant shows as a
    line in the git scope block, is absent from `ApprovedScope`-equivalent grant reading until
    `RecordApproval`, and is recorded under key `git.grants`.
  - `internal/cli/run/brokeredscope_test.go`, `TestScopeFileCarriesApprovedGrants`: through
    `writeScopeFiles`, the file's `grants` holds the approved entries and not an unapproved one.
  - `internal/cli/run/liveapply_test.go`, `TestConfigApplyReplacesGrants`: `yolo config apply`'s
    seam rewrites a running launch's `grants`.

  Red: unknown keys.
- **Change:** `loopholedecl.Brokered.Grants`; `config` keys `brokered.<source>.grant_kinds` (user)
  and `brokered.<source>.grants` (workspace) in `knownBrokeredSourceKeys` with their scope rules;
  the resolver gives validation each source's defined kinds; the gate shows and records grants;
  `brokerscope.File.Grants []Grant{Kind, Repo}`; `writeScopeFiles` and the `config apply` seam
  (`replaceScopeReposSeam`, widened to carry grants) write it. `yolo config-ref` documents both keys.
- **Gates:** `go test -short ./internal/loopholedecl/ ./internal/config/ ./internal/cli/run/ ./internal/brokerscope/`;
  `just check-ci`; `go test -run 'Scope|ConfigApply|GitHubBroker' ./integration`.
- **Fences:** the broker does not read grants yet (task 9); github's behavior is unchanged.
- **Commit:** `feat(broker): user grants in two layers, approved at the config-change gate`
- **Report:** red, green, and the gate's text with one grant.

### Task 9 — `create-branch`, the working set, and `yolo approve`

- **Settled, do not re-open:** a branch created under `create-branch` joins the working set and
  is pushable again with no human, across launches, until deleted or revoked; only a creation
  through the broker counts; a creation the push lost records nothing; two jails of one workspace
  share the record; read-only pushes nothing
  ([§5.1](git-broker.md#51-the-working-set-of-pushable-branches),
  [GB-D7](git-broker.md#GB-D7), [OQ-GB2](git-broker.md#OQ-GB2)). `yolo approve` lists the
  working set and revokes `w-` entries and `all --workspace` ([BB-D86](boundary-broker.md#BB-D86)).
  ID-9, ID-11.
- **Preconditions:** tasks 7 and 8 landed.
- **Tests first:**
  - `internal/brokerstore/working_test.go`: `TestWorkingAddIsIdempotent`,
    `TestWorkingEndReasons` (`deleted`, `gone`, `revoked`), `TestRevokeWorkingByID`,
    `TestRevokeAllForWorkspace` (ends that workspace's entries and grants only).
  - `internal/gitbroker/broker_test.go` (task 5's fixture, with a grant in the scope file and
    `grant_kinds` in the user config):
    - `TestCreateBranchUnderGrantJoinsWorkingSet`: `git push origin HEAD:refs/heads/agent/x`
      succeeds; the store holds one live entry for `fixture.test/o/r` and `refs/heads/agent/x`
      with `By: "create-branch"`; a second push and a `--force` push succeed and audit
      `covered_by` the `w-` id.
    - `TestWorkingSetSurvivesANewBroker`: after the broker restarts with a new start id, a push
      to `agent/x` still succeeds.
    - `TestNoGrantPushesNothing`: without the grant, the same creation is refused and the store is
      empty.
    - `TestUserKindRemovedTakesEffectAtNextPush`: removing `grant_kinds` from the user config
      between two creations refuses the second.
    - `TestRejectedCreationRecordsNothing`: an upstream `pre-receive` hook that rejects the push
      leaves no entry.
    - `TestDeleteEndsEntry` and `TestGoneUpstreamEndsEntry` (the branch deleted directly in the
      bare repository, then pushed again: the old entry ends `gone`, a new one is recorded).
    - `TestMainStillRefused`: `git push origin HEAD:main` with the grant is refused, needing
      approval.
    - `TestCreateBranchOverHTTPS` (task 6's server): a creation under the grant lands in the bare
      repository and records an entry from the upstream's `ok` report.
  - `internal/cli/approve_test.go`: `TestApproveListsWorkingSet` (the listing shows the git
    store's entry with workspace, repository and ref), `TestApproveRevokeWorkingID`, and
    `TestApproveRevokeAllWorkspace`; existing `approveMain` tests pass unchanged.
  - `integration/gitbroker_test.go`, `TestGitBrokerCreateBranch`: with `grant_kinds` in the user
    config and a grant in the workspace config, accepted at `yolo check --accept-config-changes`,
    a push of a new branch lands in the bare repository, a second push lands, the jail restarts,
    and a third push lands; `yolo approve --revoke all --workspace <ws>` on the host makes the
    fourth push refused.

  Red: no working records; every push refused.
- **Change:** `internal/brokerstore/working.go` per ID-11, including `Revoke` accepting `w-` ids
  and a workspace filter for `all`. The git broker's policy reads the live scope file's grants,
  re-reads `grant_kinds`, consults the working set, forwards a covered push, tees the upstream
  report to record or end entries. `approve.go`: an `approveAll(ctx, args, inJail, stores, …)` that
  `approveMain` delegates to with its one store; `runApprove` passes the github and git stores;
  ids are looked up in each store; the usage text gains `--revoke <grant|w-entry|all> [--workspace <path>]`
  and drops its claim that only the GitHub broker exists.
- **Gates:** `go test -short ./internal/brokerstore/ ./internal/gitbroker/ ./internal/cli/`;
  `just check-ci`; the full integration suite once, on the combined tree:
  `go test -count=1 -timeout 0 ./integration`.
- **Fences:** no request filing (step 3); no change to how github writes answer.
- **Commit:** `feat(gitbroker): create-branch and the per-workspace working set`
- **Report:** red, green, the integration transcript, and the `yolo approve` listing with one
  working-set entry.

## Ships with

- **Unit tests**, by task above; the degenerate cases are named there: an empty scope (every
  request refused, naming the gate), an empty push (no commands: forwarded as a no-op), a
  delete-only push, a push with `push-options`, an over-long pkt-line, a remote with credentials in
  its URL, a duplicate working-set add.
- **Integration:** `integration/gitbroker_test.go`, joining the serial `integration` package
  (`requireJail(t)`, no `t.Parallel()`), honoring `YOLO_TEST_JAIL_TIMEOUT`. It covers
  [§10](git-broker.md#10-what-done-looks-like) items 1 (SSH half), 3, 4, 5, 6 and 8; the HTTPS half
  of item 1 is covered by task 6's package tests only, since no TLS server is reachable from a
  nested jail without a trust hatch, and none is added.
- **Tests that will break, and should:** none expected. If one does, it asserted the old
  behavior: rewrite it to the new behavior; never loosen an assertion until it passes.
- **User docs** (required by the roadmap's git broker entry, *"proper evergreen documents and a user guide
  section"*): `userguide/guides/git.md` (new: enabling, what fetch and push do, granting
  `create-branch` in both configs, the working set, `yolo approve --revoke`, what is not
  rewritten); `userguide/guides/loopholes.md` (the `git-broker` row);
  `userguide/reference/configuration.md` (`brokered.<source>.grant_kinds`,
  `brokered.<source>.grants`, `brokered.git.repos` refused); `userguide/reference/cli-reference.md`
  (`yolo git-remote`, the `approve` additions); `userguide/reference/settings-per-setup.md` (the
  loophole per backend: off on Apple Container). Each lands with the task whose behavior it
  describes (7 or 9).
- **Surfaces:** `yolo config-ref` (task 8's keys); `yolo help` and subhelp (task 4); the
  `approve` usage text (task 9); `packs/git/briefing/git.md` (task 7); the
  `internaldaemon` usage line (task 5); `doctor_cmd` output in `yolo check` (task 7).
- **Design docs:** when step 1 is built, set [`git-broker.md`](git-broker.md)'s ledger `Built`
  cells from the tree and its stage to `BUILT` with a measurement clause, then graduate it to a
  system doc under `docs/reference/`.
- **Not touched:** `vendor/` (no new dependency: `net/http/cgi` and `net/http/httptest` are the
  standard library); `flake.nix`'s `shippedBinaries` and `scripts/stage-source-bundle.sh` (no new
  `cmd/` binary); the `goSrc` fileset (every new package is under `internal/` or `packs/`).
- **Cheap, and the implementer's:** type and function names inside the new packages, beyond the
  ones the tests fix; how the pump goroutines are arranged; error wording other than the strings
  the tests and the design quote.

## Don't

- **Don't intercept `git`.** The jail runs the real git; only `git-remote-yolo` is placed
  ([OQ-GB1](git-broker.md#OQ-GB1)).
- **Don't run git on the host with the workspace as its repository**, not even to read a remote:
  the workspace's config can run code ([GB-D3](git-broker.md#GB-D3)).
- **Don't file requests or add a notifier path.** Requests turn on in step 3; with no notifier the
  request is only logged ([BB-D98](boundary-broker.md#BB-D98)).
- **Don't add `BatchMode=yes` or a credential prompt.** A key prompts on the host; the broker
  never asks for a username or password ([§4.3](git-broker.md#43-the-upstream-leg-the-hosts-own-credentials)).
- **Don't make `read` a grant entry**, and don't let a workspace config enable the loophole:
  both are the user's ([GB-D5](git-broker.md#GB-D5)).
- **Don't cap fetch size.** The broker streams and holds no copy ([§6](git-broker.md#6-failure-paths)).
- **Don't start the GitHub resource model here** (write kinds, `create-pr`, retiring `admit`):
  it is the rest of step 1 in [`boundary-broker.md` §11.1](boundary-broker.md#111-step-1-sources-read-and-write),
  and needs its own plan.
- **Don't add a TLS-skip or a test-only env hatch** to the shipped binary; tests use an unexported
  seam. A hatch would be a second path around the scope's own trust.

## Blockers

None. Every ruling the design rests on is in its [ledger](git-broker.md#12-decision-ledger) and
[`boundary-broker.md`'s](boundary-broker.md#15-decision-ledger). Task 6 carries the one
measured assumption, with its stop-and-report condition.

## Backlog

Found in the task 6 review; out of scope, not blocking.

- **A push POST over 1 MiB that gets a 401 is not replayed.** That body streams, so a server that
  asks for a credential only at the upload (and not at its `info/refs`) fails the push, naming the
  host. A push of at most 1 MiB is buffered and replayed with the host's credential (fixed after
  the task 9 review).
- **A stalled upstream can hang a call once the jail side has closed**, over HTTPS as over SSH:
  `Broker.Handle` (`internal/gitbroker/broker.go`) cancels only when the jail's connection errors.
- **A server whose CGI cannot take a chunked request body** (Go's `net/http/cgi` is one) refuses
  the broker's streamed push; check task 9's live run against real forges.

  *Measured in task 9, 2026-10-24,* against task 6's test server with its de-chunking turned off
  (`git http-backend` behind `net/http/cgi`): the broker's creation POST was answered HTTP 400 and
  git printed `! [remote rejected] HEAD -> agent/x (the git-broker's HTTPS upstream failed; its
  reason is above)`, with nothing recorded in the working set. Stock git's own push of the same
  commit to the same server succeeded: it sent `Content-Length: 444`, because git buffers a push
  body up to `http.postBuffer` (1 MiB by default) and streams chunked only above it. So the broker
  is less compatible than git for small pushes. A forge that git can push a large branch to
  already takes chunked bodies; no real forge was reached from the test environment.

  *Fixed after the task 9 review:* the broker reads up to 1 MiB + 1 byte of the push body; at or
  under 1 MiB it sends the body with a `Content-Length` (and replays it with a credential on a
  401), and above that it streams chunked as before. The test server now answers a chunked POST
  411 unless a test opts in, so every small HTTPS push test is a regression test, and
  `TestHTTPSSmallPushSentWithLength` and `TestHTTPSLargePushStreamsChunked`
  (`internal/gitbroker/https_test.go`) pin both sides. A server that refuses chunked bodies still
  refuses a push over 1 MiB, as it refuses git's own.

Found building task 9; out of scope, not blocking.

- **A creation on a ref with a live working-set entry ends the entry `gone` when the push is
  judged** (ID-11), before the upstream answers, and even when another ref then refuses the push
  whole. A jail that sends an all-zero old id for a branch that still exists ends its own entry;
  the upstream then rejects the creation. It only ever narrows what the workspace may push.
- **Revoking a working-set entry is not audited**, as revoking a grant is not: `yolo approve
  --revoke` writes the store only. `yolo audit --service git` shows the pushes, not the revocations.
- **`yolo approve --watch` reports no working-set events.** It opens with one state holding every
  store's live entries, but `brokerstore.Diff` compares requests and grants only, so an entry added,
  deleted, gone or revoked while watching shows up only in the next `yolo approve` listing.
- **ID-3's repository identity can merge two repositories.** It drops the user and strips a leading
  `~/`, so on a host where a path is relative to the login's home, `alice@host:r.git` and
  `bob@host:r.git` (two homes), or `ssh://host/~/r.git` and `ssh://host/r.git`, are one identity: a
  grant or working-set entry for one covers the other. On the forges ID-3 was written for, where
  the path names the repository whoever logs in, the forms are the same repository.
