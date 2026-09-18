# `aws-auth` — Bedrock from a host `aws sso login`

One host-wide service turns a live SSO session into a **short-lived, narrowed** AWS
credential and answers for it over the protocol every AWS SDK already speaks. The jail
holds a loopback URL and nothing else: no access key, no session token, no refresh
token, no `~/.aws`.

Design: [`sso-backed-bedrock.md`](../../docs/design/sso-backed-bedrock.md).

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
  "packs": ["claude", "aws-auth"],
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

Then `yolo -p bedrock -- claude`. Selecting the pack on its own changes nothing
observable: the credential pointer is gated on the `bedrock` profile, and the loophole
is off until you enable it.

| Setting | What it does |
|---|---|
| `profile` | the AWS profile the service resolves — the one you `aws sso login --profile`. Absent: the daemon refuses at spawn and names this key |
| `role_arn` | a role to assume before serving, so the jail holds that role's permissions rather than your whole permission set |
| `session_policy` | an inline IAM session policy attached to that AssumeRole, narrowing **inside** Bedrock. Needs `role_arn` |
| `unnarrowed` | serve the permission set as-is. The one widening, and it has to be asked for by name |

**A narrowing is required, and absence is never un-narrowed.** With neither `role_arn`
nor `unnarrowed` set the daemon refuses at spawn and names both. That is deliberate and
it is the whole security argument: a credential this service mints is readable by
**every process in the jail**, so the narrowing is not defence in depth — it is the only
defence. If you genuinely have nothing to narrow with, `"unnarrowed": true` is supported
and is disclosed everywhere this service reports.

`yolo check` mints once and tells you what it resolved, including which SSO config form
your profile uses and how much session lifetime is left.

## What is NOT here, and must not be

**Never grant `~/.aws` alongside this.** A `host_files` entry for `~/.aws` does not
duplicate this pack — it **disables** it while looking like it works. `fromIni` sits
*ahead* of the container-credentials provider in every SDK's chain, so the jail
silently goes back to using the whole permission set and every narrowing you configured
is inert.

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

**No authorization token, deliberately.** An SDK sends `Authorization` only when
`AWS_CONTAINER_AUTHORIZATION_TOKEN` is set, and setting it here would buy nothing:
an environment variable is inherited by every process the agent spawns, so everything
that could read the token can already reach the port. The boundary is the `0600`
endpoint file on the hop the adapter makes, not a header on the hop it serves.

## Properties worth knowing before you are surprised by them

**⚠ The pointer is jail-wide, not claude-only, and that is a known open defect.**
An `env` contribution's `profile` gate keys on a BIN: the profile active for a bin the
pack installs, else the profile active for **any** bin. `aws-auth` installs no CLI — it
is CLI-less, which is exactly what that second pass exists for — so the gate resolves
through it, and `bedrock` is a profile `packs/claude` ships. The consequence is that
`yolo -p bedrock -- claude` sets `AWS_CONTAINER_CREDENTIALS_FULL_URI` for **everything
in the jail**: codex, pi, opencode and any script the agent runs will each pick up
SSO-minted credentials from this adapter, whether or not you chose Bedrock for them.

That is [`OQ-BR4`](../../docs/design/bedrock-plumbing.md#OQ-BR4) and this pack is the first
instance where both of its horns bite. Narrowing the gate to the pack's own bins —
the obvious fix — would break this pack outright, because CLI-less is the case the wide
pass exists for. Keeping it wide is what leaks the URI. Until now the worked example was
a flag (`CLAUDE_CODE_USE_BEDROCK=1` reaching another CLI); a credential endpoint is a
different severity, which is why it is written down here rather than left to be
discovered. **Do not "fix" it by narrowing the gate**: the resolution is a ruling on how
env is scoped by agent, and it is the maintainer's.

What limits the blast radius in the meantime is the narrowing you configure above: the
credentials every one of those processes can reach are exactly the ones `role_arn` and
`session_policy` allow. That is the same argument as the netns one below, one layer up.

**A nested jail shares this endpoint.** Podman-in-podman forces `--net=host`, so a
nested jail's loopback *is* this jail's loopback and its SDKs will resolve these
credentials. That is a property, not a defect — a nested jail already shares this jail's
home, and the netns is not the widest thing it shares. What follows from it is that
anything reasoning about who may mint has to reason at the **network namespace**, not at
the process.

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

The jail's next credential fetch gets a 4xx whose message contains, verbatim:

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

**A podman backend.** Two others are inert and both say so at launch. `macos-user` runs
no jail-side daemon at all, so the adapter never starts. Apple Container
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
