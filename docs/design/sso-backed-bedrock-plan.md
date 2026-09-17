---
title: "SSO-backed Bedrock — implementation sketch"
date: 2026-09-17
status: draft
tags: [aws, bedrock, sso, credentials, loopholes, packs, sketch]
summary: "The parking lot for implementation material from sso-backed-bedrock.md: package layout, the presign algorithm, the manifest shape, vendoring, and the tests. Not a hand-off artifact — nothing here is buildable while the design's questions are open."
---

# SSO-backed Bedrock — implementation sketch

**Status:** SKETCH, 2026-09-17 — incomplete, and unstable while questions are open.

**This is not a hand-off artifact.** It is the parking lot that keeps
[`sso-backed-bedrock.md`](sso-backed-bedrock.md) at altitude. An agent must not build from
it: a real plan's product is codebase knowledge written by someone who has just read the
tree, and that is `implementation-plan`'s job, not this file's.

**The design wins on behaviour.** Where this file and
[`sso-backed-bedrock.md`](sso-backed-bedrock.md) disagree about what the system does, the
design is right and this is a stale note.

---

## Package and command layout

Mirrors `openai-auth`'s split, which is the closest built thing.

| Piece | Probable home | Mirrors |
| :--- | :--- | :--- |
| host daemon | `internal/awsauthdaemon/` | `internal/openaiauthdaemon/` |
| canonical state + mint logic | `internal/awsauth/` | `internal/openaiauth/` |
| jail adapter (container-credentials HTTP) | `internal/awscredadapter/` | `internal/openaiauthadapter/` |
| jail-side client over the front | reuse `internal/openauthclient`'s shape | `internal/openauthclient/` |
| host subcommand | `yolo internal daemon aws-auth` | `yolo internal daemon openai-auth-broker` |
| jail subcommand | `yolo-jaild aws-credential-adapter` | `yolo-jaild openai-auth-adapter` |

**No new `cmd/` binary.** Both halves are subcommands of existing binaries, dispatched on
plain `args[0]` (`cmd/yolo-jaild/main.go:32-40`). That sidesteps `flake.nix`'s
`shippedBinaries` and `scripts/stage-source-bundle.sh`'s `SHIPPED_BINARIES` entirely — the
trap AGENTS.md names, avoided by not triggering it.

## Manifest shape

Starting point, to be checked against `internal/loopholedecl` at build time rather than
trusted from here.

```jsonc
// packs/aws-auth/loopholes/aws-auth/manifest.jsonc
{
  "name": "aws-auth",
  "description": "Derives short-lived, Bedrock-scoped credentials from the host's AWS SSO session.",
  "version": 1,
  "default_enabled": true,
  "serves": ["aws-bedrock-credentials"],
  "transport": "loopback-tls",
  "lifecycle": "spawned",
  "host_daemon": {
    "cmd": ["yolo", "internal", "daemon", "aws-auth", "--socket", "{socket}", "--state-dir", "{state}"],
    "publishes": "socket",      // mandatory for a pack-shipped loophole (packshipped.go:471-480)
    "scope": "host"             // blocked on OQ-SSO2
  },
  "jail_daemon": {
    "cmd": ["yolo-jaild", "aws-credential-adapter", "--listen", "127.0.0.1:<port>"],
    "restart": "on-failure"
  },
  "state_files": [".mount-sentinel"],   // never empty — an empty list mounts the whole state dir
  "doctor_cmd": ["yolo", "internal", "daemon", "aws-auth", "--self-check", "--state-dir", "{state}"]
}
```

Pack manifest beside it: a `kind: "loophole"` with `from`, plus the `kind: "env"` carrying the
pointer variables — `jail_env` is refused for a pack-shipped loophole and the refusal names
this substitution (`internal/loopholedecl/packshipped.go:126-134`). `packs/audio/pack.json` is
the two-contribution precedent.

**Port number.** Delegated: any fixed, documented, unprivileged port that does not collide
with `1460` (the OpenAI adapter) or the wire-bridge range. It has to be fixed rather than
allocated, because the `kind: "env"` contribution is static data.

**`serves` name.** `aws-bedrock-credentials` is a guess at the capability vocabulary; check
what an existing `supersedes` would want to say before fixing it.

## The N1 presign — reimplementable, but verify

Blocked on [OQ-SSO5](sso-backed-bedrock.md#OQ-SSO5) — the arm may not ship.

Shape recovered from the `aws-bedrock-token-generator` packages and third-party teardowns,
**not** from an AWS specification. Treat as a starting point and diff against the official
generator's output before shipping:

```
presign GET https://bedrock.amazonaws.com/?Action=CallWithBearerToken
  service = "bedrock",  region = <the one region this key will work in>
  X-Amz-Expires = 43200      (12h maximum)
  signed headers = host
token = "bedrock-api-key-" + base64(<query string without scheme/host>) + <version suffix>
```

Pure `crypto/hmac` + `crypto/sha256`; no AWS SDK needed for this half. The version suffix is
the part most likely to be wrong from a teardown — get it from the generator.

## Measure this before anything else

**The access-token lifetime, on the real machine.** It is one observation and it rules
[OQ-SSO3](sso-backed-bedrock.md#OQ-SSO3), which in turn decides whether the daemon can be
read-only. **Check which config form is in use before measuring anything**: no `[sso-session]`
block in `~/.aws/config` means the legacy fixed-8h non-refreshable form, and the question is
already answered — nothing refreshes, so read-only costs nothing. Log in, then watch `~/.aws/sso/cache/*.json`'s `expiresAt` against the portal
session's own expiry, touching nothing in between — the question is whether a cached token
nobody refreshes survives as long as the session does. Do it before writing the resolver:
the two implementations below are not a refactor apart.

## Resolving the host session

Two implementations, and the choice is
[OQ-SSO3](sso-backed-bedrock.md#OQ-SSO3)'s to make:

- **Shell out** to `aws configure export-credentials --profile X --format process`, then
  `aws sts assume-role --policy file://…` for the narrowing. Simplest, agrees with the user's
  own config by construction, needs AWS CLI v2 on the host, and *writes* the SSO cache.
- **Read the cache** at `~/.aws/sso/cache/` directly and call `sso:GetRoleCredentials` and
  `sts:AssumeRole` over plain HTTPS with a hand-rolled SigV4. No CLI dependency, no write, and
  yolo now owns a small amount of AWS protocol.

**Vendoring note either way:** `aws-sdk-go-v2`'s config loader would pull in `config`, `sso`,
`ssooidc`, `sts` and their transitive tree into a committed `vendor/` that the nix build reads
hermetically. Weigh that before reaching for it; both options above avoid it.

## Config surface

Blocked on [OQ-SSO1](sso-backed-bedrock.md#OQ-SSO1) and
[OQ-SSO4](sso-backed-bedrock.md#OQ-SSO4) — the keys below assume refuse-by-default and
user-scope-only, and both may move.

```jsonc
// ~/.config/yolo-jail/config.jsonc — user scope only
"aws_auth": {
  "profile": "my-bedrock-profile",            // N4: a profile naming a Bedrock-only permission set
  "region": "us-east-1",
  // N2/N3 only — omit both under N4, where the permission set IS the narrowing
  "role_arn": "arn:aws:iam::123456789012:role/yolo-bedrock",
  "session_policy": "bedrock-invoke-only"     // a named built-in, or an inline policy document
}
```

**Two shapes, and N4 is the smaller one.** Under N4 the daemon makes no `AssumeRole` call at
all — it resolves a profile and serves what comes back — so `role_arn`/`session_policy` are
the N2/N3 path only. Whether the schema should make that an explicit mode rather than three
optional keys is a real question; do not invent an answer here, it is
[OQ-SSO1](sso-backed-bedrock.md#OQ-SSO1)-adjacent.

A shipped `bedrock-invoke-only` session policy would be `bedrock:InvokeModel`,
`bedrock:InvokeModelWithResponseStream`, `bedrock:ListInferenceProfiles`,
`bedrock:GetInferenceProfile` on inference-profile, application-inference-profile and
foundation-model ARNs — the set Claude Code's own IAM page asks for. Check the other three
agents' needs before calling it shared; opencode and pi drive Converse and may want
`bedrock:Converse*`.

Also unresolved and cheap: whether `yolo check` grows a section that reports the SSO session's
remaining lifetime. It would be the natural place, and it is the one surface where "you have
90 minutes left" is useful before a long run.

## Tests worth writing

- **The exclusivity refusal fails when its call site is deleted.** The class AGENTS.md names —
  a test that pins the callee while the call site is unpinned is not a test. Blocked on
  [OQ-SSO5](sso-backed-bedrock.md#OQ-SSO5).
- **A golden response shape** asserted against the four required keys and an RFC3339
  `Expiration`, because the SDK rejects anything else with a message that does not name the
  missing field.
- **A served-latency assertion** under the 1000 ms SDK budget, with a cold cache — the
  measurement R1 rests on.
- **No credential in any rendered artifact**: grep the composed env file, the agent settings
  and the render sidecar for the minted secret, in the shape of
  `TestNoBrokerTokenEnvEmitted`.
- **No agent is started.** `--version` probes only, per the repo rule.

## Verification traps specific to this work

- **A nested jail cannot verify the reachability half at all.** Podman-in-podman forces
  `--net=host`, so the jail's loopback and the launcher's are the same loopback and the
  forwarding class of bug gets a free green. AGENTS.md's carve-out applies verbatim; the bare
  `podman run --network=pasta` reproduction in it is the instrument.
- **`git add` before rebuilding.** The nested image build sees tracked files only, so a new
  untracked `packs/aws-auth/` vanishes from the image while everything reports success.
- **`just install` is refused in-jail**, and rebuilding the image is the move.
