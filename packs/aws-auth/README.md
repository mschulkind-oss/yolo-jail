# `aws-auth` — Bedrock from a host `aws sso login`

One host-wide service turns a live SSO session into a **short-lived, narrowed** AWS
credential and answers for it over the protocol every AWS SDK already speaks. The jail
holds a loopback URL and nothing else: no access key, no session token, no refresh
token, no `~/.aws`.

How it behaves:
[`agent-credentials.md`'s SSO-backed Bedrock section](../../docs/reference/agent-credentials.md#sso-backed-bedrock-credentials-aws-auth).
Why: [`sso-backed-bedrock.md`](../../docs/design/sso-backed-bedrock.md), the graduated design
and its decision ledger.

```
host                                          jail
  aws sso login  ──▶ ~/.aws/sso/cache
                        │
                        ▼
              aws-auth service ──── 0600 endpoint file, loopback-TLS ────▶ adapter
              (one per machine)                                           127.0.0.1:1461
                        │                                                     ▲
                        └── AssumeRole + session policy ──▶ STS                │
                                                             claude / codex ───┘
                                                             (AWS_CONTAINER_CREDENTIALS_FULL_URI)
```

## Enabling it

Both halves go in your **user** config (`~/.config/yolo-jail/config.jsonc`), never in a
workspace `yolo-jail.jsonc` — see [Why every key is user-scope](#why-every-key-is-user-scope).

```jsonc
{
  "packs": ["claude"],
  "loopholes": {
    "aws-auth": {
      "enabled": true,
      "settings": {
        "profile": "my-sso-profile",
        "role_arn": "arn:aws:iam::111122223333:role/yolo-jail-bedrock",
        "session_policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":[\"bedrock:InvokeModel\",\"bedrock:InvokeModelWithResponseStream\"],\"Resource\":\"*\"}]}"
      }
    }
  }
}
```

Then `yolo -p bedrock -- claude`. `claude` brings this pack with it: the claude pack
`needs` the [`bedrock`](../bedrock/README.md) pack, which ships the Bedrock provider and
`needs` `aws-auth`, and the launch prints `+ bedrock (needed by claude)` and
`+ aws-auth (needed by bedrock)`. codex, opencode and pi need `bedrock` the same way, so you
never list either pack yourself. Having it selected changes nothing observable on its own: the
credential pointer is gated on the selected provider being Bedrock (its `platform` is
`aws-bedrock`), and the loophole is off until you enable it. Enabling it starts nothing either:
the in-jail adapter starts only on a launch where some agent's selected provider is Bedrock, and
a launch that leaves it out says so:

```
Not started: the aws-auth jail daemon, because no agent's selected provider is on platform "aws-bedrock", which it serves; select one (`-p <agent>=bedrock`) to start it …
```

The gate is the provider, not the profile's name: a profile of your own over `bedrock`, and a
provider of your own that declares `"platform": "aws-bedrock"`, get the pointer as `-p bedrock`
does ([`OQ-BR8`](../../docs/design/providers-and-profiles-redesign.md#OQ-BR8)).

**The same config serves `yolo host`.** With `"profile": {"pi": "bedrock"}` added (or
`yolo host -p bedrock -- pi`), `yolo host -- pi` starts the host service if it is not running,
opens the adapter for pi alone on a port it picks, hands pi the pointer and its token, and stops
the adapter when pi exits; it says so on stderr:

```
yolo host: opened the "aws-auth" doorway (pack "aws-auth", pid …) on 127.0.0.1:… for pi: it answers only this launch's caller token, forwards to the host's "aws-auth" service, and stops when pi exits. …
```

With the loophole off, the launch withholds the pointer and says how to turn it on. pi counts
the pointer as a credential; so do opencode and, by its AWS SDK's chain, codex and claude
([`host-notch-services.md` §4.8](../../docs/design/host-notch-services.md#48-yolo-host)).

| Setting | What it does |
|---|---|
| `profile` | the AWS profile the service resolves — the one you `aws sso login --profile`. Absent: the daemon refuses at spawn and names this key. Its `region` in `~/.aws/config` is also the region an agent this service serves is given when nothing else names one ([the bedrock pack](../bedrock/README.md#what-the-provider-declares)) |
| `role_arn` | a role to assume before serving, so the jail holds that role's permissions rather than your whole permission set |
| `session_policy` | an inline IAM session policy attached to that AssumeRole, narrowing **inside** Bedrock. Needs `role_arn` |
| `unnarrowed` | explicitly use the configured profile's permission set as-is, without an extra AssumeRole or session policy. This is a valid choice when that assigned permission set is the intended policy boundary |

**A permission mode must be selected, and absence never selects the profile-as-configured route.** With neither `role_arn` nor `unnarrowed` set the daemon refuses at spawn and names both; the explicit user-only setting defaults to false. An assigned Identity Center permission set used as configured is a supported, expected mode, and the role/session-policy arms remain optional additional restrictions.

The credential is available to processes in the jail, and AWS enforces the permissions attached to the configured profile/session. In organizations that activate IAM-principal cost allocation, AWS supports using federated session attributes to distinguish users who share a role. Using the profile's current session without an extra role hop can preserve that existing attribution context; verify role/tag propagation for any assumed-role route rather than assuming it is lost or retained. See AWS's [Bedrock IAM-principal tracking guidance](https://docs.aws.amazon.com/bedrock/latest/userguide/cost-mgmt-iam-principal-tracking.html) and [IAM-principal cost-allocation dimensions](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/iam-principal-cost-allocation.html).

`yolo check` explicitly verifies the settings and credential path. It reports a valid profile-permissions-as-configured selection as healthy; launches do not print a routine warning for this expected choice. Ordinary selected-pack trust banners and actionable configuration/runtime failures remain visible.

## What is NOT here, and must not be

**Never grant `~/.aws` alongside this.** A `host_files` entry for `~/.aws` does not
duplicate this pack. When it holds credentials for the profile the SDK resolves, it
**disables** the pack while looking like it works. `fromIni` sits *ahead* of the
container-credentials provider in every SDK's chain, so the jail silently goes back to
using the whole permission set and every narrowing you configured is inert.

That is the tempting option and it deserves naming rather than discovering. The design
calls it [**option A, "mount the wallet"**](../../docs/design/sso-backed-bedrock.md#4-five-options),
and refuses it: the jail gets every
permission the permission set grants, plus the refresh token, plus every other profile
in the file. It is also the only option that satisfies the refresh requirement for
free, which is exactly why people reach for it.

**Never configure `AWS_BEARER_TOKEN_BEDROCK` in the same jail.** In every client
measured the bearer **beats** the credential chain, so a jail with both configured uses
the frozen bearer and this service never runs — a silent wrong answer rather than a
visible conflict.

**Nor a static key pair.** `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` together are
what the chain's environment provider answers with, and it runs *before* the
container-credentials provider in claude, codex, opencode and pi alike, so the jail signs
with the long-lived key and this service is never asked. One half alone answers nothing
and is left alone. The pair beside an `AWS_PROFILE` delivered into the same jail is left
alone too, because the JavaScript SDKs then skip the environment provider — but codex's
Rust SDK does not, so do not lean on that exception to keep both.

**yolo refuses the bearer and the pair at launch, and warns about `~/.aws`.** The pointer's
`env` contribution in [`pack.json`](pack.json) declares all three under `overridden_by`.
Whenever the pointer is delivered (some agent's selected provider is Bedrock), a launch that also
delivers the bearer or the pair stops before the jail starts, names both sides, and says to
drop one. There is no escape hatch: proceeding would be proceeding into the wrong credential.
A `host_files` entry that renders anything under `~/.aws` gets a warning instead, and the
launch continues. It overrides the pointer only when it holds credentials for the profile the
SDK resolves, and a region-only config does not, so the entry is declared `certain: false`.
The warning is printed on every such launch and cannot be switched off. `yolo check` predicts
all three, the two refusals as FAIL and the grant as WARN, and `yolo pack footprint aws-auth`
lists the three lines. Core names none of these variables; the rule is this pack's
declaration ([`OQ-SSO8`](../../docs/design/sso-backed-bedrock.md#OQ-SSO8)).

"Delivers" means into the jail, through `env_sources` or a pack. A variable exported only in
the shell you run `yolo` from never reaches the jail, so it is not refused, and an
`AWS_PROFILE` exported there does not excuse a pair that `env_sources` delivers. At
`yolo host`, which opens the adapter too, the agent inherits that shell, so a bearer or a pair
exported there is delivered and refuses a launch that hands the agent the pointer. A directory
grant such as `~/.aws/` counts only on a backend that delivers it: podman, and Apple
Container from 1.1.0. macos-user never copies one.

**Every request carries this launch's caller token, and only the selecting agent has it.**
Loopback is not the jail: a jail on `network.mode: "host"` puts the adapter's port on your
host's loopback, and a nested jail shares its parent's, so without a check any local process
could `GET` the credential. Each launch mints a new token for the adapter. This pack sets
`AWS_CONTAINER_AUTHORIZATION_TOKEN` to it beside the credentials URI, in the env file of each
agent whose selected provider is Bedrock and in no other process's environment, a bare shell's
included ([`OQ-CN7`](../../docs/reference/providers.md#oq-cn7)). Your agent's AWS
SDK sends the value as `Authorization`, as it does whenever
`AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE` is unset, so do not set that variable yourself: the
SDK would read the file instead. A request without the token gets `401` with a message naming
yolo
([notch convergence §2.3](../../docs/plans/notch-convergence.md#23-the-fix-every-service-authenticates-its-caller-at-every-notch)).
The token is still a same-uid file read away inside the jail, in that agent's env file and in
an unexported record the adapter reads its token from. It authenticates the adapter hop but
does not isolate jail processes; AWS permissions attached to the selected profile or role govern
the actions available through the credential.

## Properties worth knowing before you are surprised by them

**The pointer reaches only the agents on a Bedrock provider.** An `env` contribution's
`platform` gate is answered per agent (the credential gate,
[`OQ-BR4`](../../docs/reference/providers.md#oq-br4), built): `aws-auth`
installs no CLI, so its gated pointer goes to every agent whose selected provider's platform is
`aws-bedrock` — `yolo -p codex=bedrock` gives codex `AWS_CONTAINER_CREDENTIALS_FULL_URI` — and to
no other process, a bare shell included. In a container jail it crosses in that agent's own
env file, sourced by its launcher. The processes an agent spawns inherit it, as they inherit
anything in the agent's environment. **It crosses only where the adapter runs.** The pointer
names this loophole's jail daemon (`served_by: "aws-auth"`), so a launch that does not run it
leaves the pointer out and says why: a jail launch that has not enabled the loophole, and
`yolo host env`, which runs no process. macos-user opens the adapter outside its sandbox, on the
Mac's loopback the agent shares, as a listener that launch owns, and `yolo host -- <agent>` does
the same for the one agent it runs when that agent is on a Bedrock provider, stopping it when the
agent exits; both deliver the pointer at a port the launch picks
([`host-notch-services.md` HS-D15](../../docs/design/host-notch-services.md#HS-D15),
[§4.8](../../docs/design/host-notch-services.md#48-yolo-host);
[notch convergence §2.4](../../docs/plans/notch-convergence.md#24-the-addresses-those-secrets-protect-are-composed-not-literal)).
A profile of your own in `~/.aws` still wins at the host: every shipped agent's AWS client asks
the profile it resolves before the pointer, so an agent you already point at a profile keeps
signing with it. With no `AWS_PROFILE` that profile is `default`, so a `[default]` holding
credentials or an SSO session is used instead of the adapter, and a lapsed one fails with an SSO
error rather than falling back to it. The launch cannot tell which of these happens, and still
says it opened the adapter. For the adapter to serve, leave `[default]` without credentials, or
point `AWS_PROFILE` at a region-only profile. **Do not "fix" the gate by narrowing it to the pack's own bins**: that
would break this pack outright, because CLI-less is the case the gate's CLI-less arm exists
for.

**The adapter follows the pointer.** It listens on `127.0.0.1:1461` (on a jail that shares its
launcher's loopback, `network.mode: "host"` or nested, a port the launch picks instead, which
the pointer follows) only in a jail whose launch had some agent on `bedrock`, and it answers
only a request carrying the token that agent's environment holds. So a bare shell, or claude
under `-p codex=bedrock`, is refused `401`
([`OQ-CN7`](../../docs/reference/providers.md#oq-cn7), built 2026-09-28). An
attach cannot start it: attaching with `-p codex=bedrock` to a jail whose launch selected no
`bedrock` stops and asks you to restart the jail (or refuses off a terminal, naming
`yolo stop`), because the pointer it would deliver would point at nothing.

What limits the blast radius is still the narrowing you configure above: the credentials any
process in the jail that reads the token can reach through the adapter are exactly the ones
`role_arn` and `session_policy` allow. That is the same argument as the netns one below, one layer up.

**A nested jail shares this endpoint.** Podman-in-podman forces `--net=host`, so a
nested jail's loopback *is* this jail's loopback and its SDKs will resolve these
credentials. That is a property, not a defect — a nested jail already shares this jail's
home, and the netns is not the widest thing it shares. What follows from it is that
anything reasoning about who may mint has to reason at the **network namespace**, not at
the process. It is also how a nested jail reaches Bedrock at all: a podman jail launched from
inside a jail whose environment holds this pointer starts no aws-auth service of its own and
hands its Bedrock agents this jail's pointer and region, never more than this jail can reach
([`agent-credentials.md`](../../docs/reference/agent-credentials.md#a-nested-jail-uses-its-launching-jails-pointer)).

**Logging in less often is an org-wide change, not a setting.** The one dial that
extends how long a portal session lasts is **instance-wide**: it changes the session
length for every user and every application of that IAM Identity Center instance. So
"I re-authenticate too much" escalates into an org-wide security change rather than a
local fix. The per-role duration dial helps only the bearer arm, and the honest
alternative for a genuinely long-lived non-interactive credential is not an SSO dial at
all — it is not using SSO as the source, which is a different threat model and is not
what this pack is.

**Which SSO config form you use decides your login cadence, not the jail's lifetime.**
A legacy profile (no `[sso-session]` block) has no refresh token, so its access token is
fixed at eight hours and cannot be refreshed automatically; the `sso-session` form
refreshes itself and the portal session lasts up to 90 days. Both are supported and the
jail behaves identically under each — the service re-reads your SSO cache on every mint,
so a re-login on the host is picked up by an **already-running** jail with no restart.
AWS's own guidance is worth repeating: *"If you have long-running processes or
automation, use the SSO token provider configuration, which supports automatic token
refresh."* The service says which form it resolved at startup and in `yolo check`.

## When the session lapses

**The launch tells you first.** A launch where some agent's provider is Bedrock asks the
service whether that agent's first credential fetch would be served. When it would not, the
launch prints one line before the jail starts, and then starts it anyway:

```
loophole aws-auth: cannot mint a Bedrock credential for this launch: the AWS SSO session for profile "<your profile>" has expired or was never established — on the HOST run: aws sso login --profile <your profile> (aws said: …). The launch continues; Bedrock requests fail until that is fixed, and then work with no relaunch
```

A profile your host's `~/.aws/config` does not have gets the same line saying how to add it or
which setting to change, and anything else AWS refuses gets AWS's own words. Where the fix can be
a setting, the line ends differently: a fix outside yolo is still picked up with no relaunch, but
the service reads `loopholes.aws-auth.settings` only when it starts, so a changed setting takes
effect when the next launch of a new jail restarts it. On a warm cache
the question costs no `aws` call; on a cold one the service makes the mint the agent's first
request would have made, and the launch waits for it only briefly. No flag hides the line. A
session that ends after the cached credential was minted shows at the next mint instead,
within the hour, since that credential keeps working until then.

Inside the jail, the next credential fetch after a lapse gets a 4xx whose message contains,
verbatim:

```
aws sso login --profile <your profile>
```

It reaches you as the agent's own error text. Run it on the host; the next turn
succeeds with no restart and no relaunch.

**That is a message, not a request.** Nothing in this pack files an approval, prompts a
human, or waits inside the credential path — and nothing may be added that does. The
request-shaped version of this ("the jail asks the host to log in, a human approves")
is [`boundary-broker.md`](../../docs/design/boundary-broker.md)'s to build, and this
design is a good first consumer of that queue and a bad place to invent it: half an
approval mechanism living in a credential pack is exactly the second front door that
doc exists to prevent.

## Trying it on a real host

**Tried on 2026-09-29.** The maintainer uses this pack every day, in many of their jails,
against a live `aws sso login` on a Linux host with rootless podman, serving their own SSO
profile narrowed to a Bedrock-only role. That covers step 3 below: claude works on Bedrock
through the pointer, and the jail holds no AWS secret and no `~/.aws`. It also covers the
real `aws` calls step 1 exercises, and the `sso-session` config form step 2 asks about. Step 4,
the narrowing shown by a denied call, was run the same day: S3 and EC2 calls were refused and
Bedrock answered. Step 5 is covered too: the
maintainer's host logs the SSO profile out and back in every four hours, and running jails
kept working across it with no relaunch. A turn during a longer lapse has not been watched. What was and was not observed is in
[the design's evidence](../../docs/design/sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it).
The steps stay here for anyone trying it on another host.

About 20–30 minutes on a Linux host with rootless podman, AWS CLI v2, a working
`aws sso login --profile <p>`, and a current host yolo (`just install`). Nothing below
prints a secret; paste each step's output back. `<p>` is your profile and `<arn>` a
Bedrock-only role your permission set can assume. Without one, use
`"unnarrowed": true` and skip step 4.

1. **The real `aws` calls and output shapes:**
   ```console
   $ printf '%s' '{"profile":"<p>","role_arn":"<arn>","session_policy":"","unnarrowed":false}' > /tmp/aws-auth-settings.json
   $ yolo internal daemon aws-auth --self-check --settings /tmp/aws-auth-settings.json
   ```
2. **Your config's shape, no values:**
   ```console
   $ grep -oE '^\[[^]]+\]|^[a-z_]+ *=' ~/.aws/config | sed 's/=.*//' | sort | uniq -c
   ```
3. **End to end:** add [the block above](#enabling-it) to `~/.config/yolo-jail/config.jsonc`,
   run `yolo -p bedrock -- claude`, and send one short prompt. Paste the launch lines naming
   aws-auth, whether the turn worked, and
   `podman info --format '{{.Host.RootlessNetworkCmd}}'`.
4. **The narrowing, demonstrated:** with `"packages": ["awscli2"]` in that workspace's
   `yolo-jail.jsonc`, run `aws s3 ls` inside the jail. It should be denied.
5. **Lapse and recovery**, with the jail from step 3 still running: `aws sso logout` on the
   host, then send a prompt and paste the error, which should name
   `aws sso login --profile <p>`. Then run that login and send one more prompt; it should
   work without relaunching.

## Why every key is user-scope

A workspace `yolo-jail.jsonc` is a file the jail's own agent can rewrite. If `profile`
were settable there, an agent could point this service at your `admin` profile; if
`role_arn` or `session_policy` were, it could swap your narrowing for one of its own.
So all four keys are declared `scope: "user"` in the manifest and core refuses a
workspace value for any of them.

## Requirements

**AWS CLI v2 on the host.** The service shells out to it — no AWS SDK is vendored, and
`aws configure export-credentials` is what refreshes the SSO access token, which is why
a re-login is transparent. The manifest deliberately does **not** declare a
`requires.command_on_path` probe for it: a loophole whose program is missing must fail
loudly at spawn, not disappear from `yolo loopholes list`. If `aws` is absent the daemon
says so and exits, and the launch reports it.

> [!NOTE]
> **Reachable since 2026-09-20.** Earlier builds never told the jail where the
> credential service was, so the adapter answered every request with a
> `ServiceUnreachable` 4xx. If you see that error, update yolo. A podman launch now
> sets `YOLO_SERVICE_AWS_AUTH_ENDPOINT` whenever this loophole is enabled. The pack
> is in daily use against a live `aws sso login`
> ([the try-out](#trying-it-on-a-real-host), 2026-09-29).

**A podman backend, macos-user, or `yolo host`.** On `macos-user` the adapter runs outside the
sandbox, on the Mac, as a listener the launch owns: started only when some agent's selected
provider is Bedrock, answering only this launch's caller token, and stopped when the sandboxed
command exits; the sandbox runs no copy of it
([`host-notch-services.md` HS-D15](../../docs/design/host-notch-services.md#HS-D15); not yet
run on a Mac). `yolo host -- <agent>` opens it the same way for an agent on a Bedrock provider.
Apple Container
(`runtime: "container"`) carries no container→host connection — measured on 1.1.0: the
handshake completes and nothing crosses — so no loopback-TLS loophole is reachable
there. That skip is expected to expire with an upstream release rather than stand
forever.

## Where things live

| Part | Path |
|---|---|
| mint, cache, host-wide lock, narrowing | `internal/awsauth` |
| the host daemon and its `--self-check` | `internal/awsauthdaemon` |
| the jail-side container-credentials endpoint | `internal/awscredadapter` |
| the loophole manifest | [`loopholes/aws-auth/manifest.jsonc`](loopholes/aws-auth/manifest.jsonc) |
