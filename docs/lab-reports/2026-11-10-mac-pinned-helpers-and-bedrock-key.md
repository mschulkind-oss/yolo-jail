---
title: "Mac run of the pinned-host-helpers and bedrock-key checks: all pass as written; a real static key pair blocks every bedrock launch"
date: 2026-11-10
tags: [lab-report, macos, macos-user, apple-container, podman, pinned-host-helpers, bedrock-key, providers]
summary: "At main 5a69dd0b4, installed, W-PH1 to W-PH5 and W-BK1 to W-BK3 pass, with W-PH2/W-PH3 run on podman because git-broker is inert on Apple Container and W-PH4's dry run needing --setup seatbelt-self. With the owner's real static key pair in the private env set, every launch that puts an agent on bedrock is refused, because bedrock claims the pair too."
---

# Mac run of the pinned-host-helpers and bedrock-key checks

**Run 2026-11-10 on the owner's Mac** (darwin/arm64, Apple Container CLI 1.1.0, podman machine
`applehv`). `just install` at main `5a69dd0b4` replaced the installed `yolo`
`0.16.1+35.g7f1210e630c9` with `0.17.1+197.g5a69dd0b4075` and published the flake-bundle generation
`20261110T215845Z-25954`. The owner approved the install. Every launch set
`YOLO_ALLOW_MISSING_PROGRAMS=1`, because the owner's `pi` fork build is pending. Nothing in yolo
was fixed.

The checks are the [pinned-host-helpers Mac checks](../design/pinned-host-helpers.md#mac-checks)
and the [bedrock-key Mac checks](../design/bedrock-key-provider-plan.md#mac-checks).

## Observed

| Witness | Result |
| :--- | :--- |
| W-PH1, the darwin bundle carries the host yolo | **Passes** |
| W-PH2, a container jail's helpers run from the pin | **Passes on `podman-self`**. On Apple Container git-broker does not run, see [§1](#1-w-ph2-and-w-ph3-ran-on-podman) |
| W-PH3, a restart after the pin changed gives up | **Passes** on `podman-self` |
| W-PH4, macos-user helpers run from the pin | **Passes**. The dry run needs `--setup seatbelt-self` in a workspace whose default setup is another one, see [§2](#2-w-ph4s-dry-run-and-the-workspaces-default-setup) |
| W-PH5, the reaper's process reader on macOS | **Passes** |
| W-BK1, `bedrock-key` opens no aws-auth doorway | **Passes**, both with the witness's placeholder and with the owner's real key pair |
| W-BK2, `bedrock` still opens it | **Passes with the witness's placeholder. Refused with the owner's real key pair**, see [§3](#3-a-static-key-pair-in-the-private-set-blocks-every-bedrock-launch) |
| W-BK3, one launch cannot mix them | **Passes with the placeholder.** With the real pair, the launch is refused by the override check, and the "cannot mix" remedy never shows |
| Pinned-helpers W-BX1 step 2 (CircleCI on a nightly) | **Cannot read yet**: nightly 42833734953 at `4fe6d30d9` is still running |

### 1. W-PH2 and W-PH3 ran on podman

**W-PH1.** `ls ~/.local/share/yolo-jail/flake-bundle/bin/darwin-arm64/` listed `yolo yolo-jaild
yolo-log yolo-ps yolo-serial`. `cmp "$(command -v yolo)" …/darwin-arm64/yolo && echo same`
printed `same`.

**W-PH2 on Apple Container.** In `/Users/Shared/yolo/ph2`, a fresh `git init` workspace, the jail
printed `VERSION=0.17.1+197.g5a69dd0b4075`. Two helpers ran, `jail-keeper` and
`openai-auth-broker`, both from
`~/.local/share/yolo-jail/flake-bundles/20261110T215845Z-25954/bin/darwin-arm64/yolo`. The keeper
log's first line was `helper start: name=jail-keeper build=0.17.1+197.g5a69dd0b4075 …
jail=yolo-ph2-24134ed3-apple-container-self exe=<that path>`.

git-broker did not start, for two reasons in turn:

- With no remote, git-broker has nothing to serve.
- After `git remote add origin https://github.com/mschulkind-oss/yolo-jail.git`, the launch said
  *"git: loophole git-broker is inert on this backend — Apple Container carries no
  container→host connection …"*.

So W-PH2's git-broker steps cannot pass on Apple Container. The witness allows either container
setup, so the rest ran on `podman-self`.

**W-PH2 on `podman-self`** (the same workspace, with the remote added):

- **Step 1:** the jail printed `VERSION=0.17.1+197.g5a69dd0b4075`.
- **Step 2:** every helper ran from the bundle path above: `jail-keeper`, `host-processes`,
  `serial`, `git-broker`, `github-broker`, `aws-auth`, `openai-auth-broker` and
  `claude-oauth-broker`. None ran from `$GOBIN` or Homebrew.
- **Step 3:** the start line was `helper start: name=git-broker build=0.17.1+197.g5a69dd0b4075
  commit=5a69dd0b4075 pid=41127 jail=yolo-ph2-24134ed3-podman-self exe=<that path>`.
- **Step 4:** the first line of `jail-keeper-yolo-ph2-24134ed3-podman-self.log` contains `helper
  start: name=jail-keeper`, with the same build and path.

**W-PH3.** With that jail running, I ran `touch` on the bundle's `yolo`, then `pkill -f 'internal
daemon git-broker'`. The broker log then ended:

```text
host service 'git-broker' died (its process ended (exit status 0)); not restarting it: the yolo this jail was launched with is no longer at /Users/matt/.local/share/yolo-jail/flake-bundles/20261110T215845Z-25954/bin/darwin-arm64/yolo (an upgrade removed or replaced it). It stays down until the jail is launched again: 'yolo stop' from this workspace. Its log: …/host-service-git-broker.log
```

`ps` found no git-broker, and the log holds a single start line for this jail. The jail was then
stopped with `yolo stop`.

### 2. W-PH4's dry run and the workspace's default setup

In a fresh `/Users/Shared/yolo/ph4`, `yolo --setup seatbelt-self -- sh -c 'sleep 300'` started a
session. The last five `helper start:` lines were `github-broker`, `aws-auth`,
`openai-auth-broker`, `claude-oauth-broker` and `openai-auth-adapter`. Each had `jail=yolo-ph4-e5a13b8e-seatbelt-self` and `exe=` the bundle's
`bin/darwin-arm64/yolo`.

Step 2 as written, `yolo run --dry-run` in that workspace, exited 1 with *"--dry-run is only
supported for the macos-user runtime. Choose a Seatbelt setup (`--setup seatbelt-yolo-account` or
`--setup seatbelt-self`) to use it."*: the workspace's default is this Mac's,
`apple-container-self`. `yolo run --setup seatbelt-self --dry-run` printed `macos-user run plan
(dry-run — nothing executed)` and `✓ all plan invariants hold`, and exited 0. The witness's step 2
needs that `--setup`, or a workspace that selects `seatbelt-self`.

### 3. A static key pair in the private set blocks every bedrock launch

The owner's config already holds the shipped `bedrock`, `aws-auth`, `claude` and `codex` packs, the
`bedrock` provider, and `[allow.aws-auth]` with a live profile and `unnarrowed = true`. At the
owner's request, I moved their static IAM key pair out of `~/.config/yolo-jail/local.env`, where
both lines were commented out, into the private set:

- the pair now sits in `~/.config/yolo-jail/bedrock-key.env` (mode 0600);
- a new machine-local `~/.config/yolo-jail/config.d/overrides.toml` lists that file under
  `[env.secrets] files`.

The pair's values appear in no launch output.

Then the three witnesses, with `--setup seatbelt-self` in `/Users/Shared/yolo/bedrock-key`:

- **`-p claude=bedrock-key`** exits 0 with *"Not started: the aws-auth jail daemon, because no
  agent's selected provider is on platform "aws-bedrock" and claims a variable its pointer sets,
  which it serves; select one (`-p <agent>=bedrock`) to start it `(providers.md OQ-CN7)`."*, and with
  no `Opened the "aws-auth" doorway` line.
- **`-p claude=bedrock`** opens the doorway, then exits 1:

  ```text
  Refusing to launch: pack aws-auth's `kind: "env"` contribution sets AWS_CONTAINER_AUTHORIZATION_TOKEN, AWS_CONTAINER_CREDENTIALS_FULL_URI, and this launch also delivers what the pack declares OVERRIDES it — the agent would silently use that instead.
    AWS_ACCESS_KEY_ID is delivered by env_sources (the secret channel).
    AWS_SECRET_ACCESS_KEY is delivered by env_sources (the secret channel).
    The pack says: the AWS credential chain reads a static AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY pair (its environment provider) before the container-credentials pointer …
    Drop one. Remove any one of: AWS_ACCESS_KEY_ID from env_sources (the secret channel); AWS_SECRET_ACCESS_KEY from env_sources (the secret channel), or stop delivering pack aws-auth's contribution … or, to keep that credential, set aws-auth's profile to "" in the config layer that should not use it …
  ```

- **`-p claude=bedrock-key,codex=bedrock`** gives the same refusal, with no "One launch cannot mix
  them" line.

**Cause, from the design.** `bedrock` claims the static pair as well as `bedrock-key` does
([BK-D5](../design/bedrock-key-provider.md#BK-D5): *"`bedrock` keeps its claim on the pair
too"*). So a pair in the private set reaches an agent on `bedrock`, beside aws-auth's pointer, and
the override check refuses. A user who keeps a pair for `-p bedrock-key` therefore cannot launch
`-p bedrock` from the same config, unless they remove the pair or empty aws-auth's profile. The
witnesses' own config uses the bearer `AWS_BEARER_TOKEN_BEDROCK`, which `bedrock` does not claim,
so it never meets this.

**With the witness's placeholder** (`[env.secrets] set = { AWS_BEARER_TOKEN_BEDROCK =
"not-a-real-key" }` in place of the file, restored afterwards), all three pass as written:

- W-BK1 exits 0 with the "Not started" line.
- W-BK2 exits 0 with `Opened the "aws-auth" doorway (pack "aws-auth", pid …)` and no "Not started"
  line.
- W-BK3 exits 1 with `Refusing to launch` and *"One launch cannot mix them: codex on `bedrock`
  receives pack aws-auth's AWS_CONTAINER_AUTHORIZATION_TOKEN, AWS_CONTAINER_CREDENTIALS_FULL_URI,
  and claude on `bedrock-key` receives AWS_BEARER_TOKEN_BEDROCK. Launch on one provider alone,
  `yolo -p bedrock-key` or `yolo -p bedrock`; …"*.

No output held `not-a-real-key` or `profile = ""`. The W-BK3 refusal came after the doorway opened
but before any sandbox session started.

**Two smaller points in the refusal text.** It names the private set as `env_sources (the secret
channel)`. `env_sources` is the legacy JSONC key, and this config spells it `[env.secrets] files`.
The refusal also names no `yolo config` verb. Its last remedy spells the JSONC form first.

### 4. Nightlies

Nightly 42833734953, at `4fe6d30d9`, is the first nightly to contain `400d1eb28`. When this report
was written it was still running: 16 jobs had passed, and shard 5 had failed in its integration
step at 17:02Z. Its logs cannot be read until the run ends, so W-BX1 step 2 and shard 5's failure
are still to read.

## Fixed and committed

Nothing in yolo. The owner's machine-local config changed as described in
[§3](#3-a-static-key-pair-in-the-private-set-blocks-every-bedrock-launch); the dotfiles did not.

## Open

1. [§3](#3-a-static-key-pair-in-the-private-set-blocks-every-bedrock-launch): with a static pair in
   the private set, `-p bedrock` is refused. Decide whether `bedrock` should keep its claim on the
   pair (BK-D5) now that `bedrock-key` exists. (Owner.)
2. The override refusal names `env_sources` and no verb.
3. W-PH2's text should name podman, since git-broker is inert on Apple Container. W-PH4's step 2
   needs `--setup seatbelt-self`.
4. W-BX1 step 2 and shard 5 of nightly 42833734953.
5. `docs/design/bedrock-key-provider.md` has two ledger rows anchored `BK-D5` (the 11-10 key-pair row and the 11-07 layer-recipe row), so a `#BK-D5` link lands on whichever comes first.

## Evidence

The logs are in `/Users/Shared/yolo/evidence/`, beside the repository: `2026-11-10-just-install.log`,
`ph2-jail*.log`, `ph4-*.log`, `bk1.log` to `bk3.log` (the real pair) and `bk*-placeholder.log`. The
workspaces `ph2`, `ph4` and `bedrock-key` under `/Users/Shared/yolo` are left in place.
