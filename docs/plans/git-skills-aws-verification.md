---
title: "Git sourced skills and AWS: follow-up verification"
status: accepted
stage: DECIDED
next: "Run W-GSS1–3 on main using the owner's Mac agent; measure N7 separately without upstream or agent execution."
tags: [verification, macos, skills, aws]
---

# Git sourced skills and AWS: follow-up verification

**Status:** 2026-11-17. Linux synthetic production paths are observed; native delivery,
agent discovery and real provider compatibility are separate, unmeasured work. These
witnesses run on main at `bde502e4a` or later after Linux landing, never on a branch.
This page owns remaining work, not permission to change credentials or call real AWS.

The implemented contracts are [Git sourced skills](../reference/git-sourced-skills.md)
and [AWS operations](../reference/aws-operation-broker/README.md).

## Mac checks

The owner's Mac agent builds `just build-dev-bundle` from main and uses its printed
**absolute** launcher. No install/deploy or interactive coding agent runs. Use a fresh
owned workspace and sibling HOME under `/Users/Shared/yolo/git-skills-verification`;
if that name already holds another run, choose a new owned sibling rather than deleting it.
Record OS/runtime/filesystem versions, HEAD, launcher full version and bundle stamp.
Both suppressions below are mandatory; automatic capture is independent of readiness.

### Synthetic fixture setup

Create only these owned fixture bytes, using a real Git executable rather than an
ambient wrapper. No upstream script or repository is read or executed.

```bash
mkdir -p /Users/Shared/yolo/git-skills-verification
cd /Users/Shared/yolo/git-skills-verification
mkdir home workspace origin selected
mkdir -p origin/skills/chosen/scripts origin/skills/hidden origin/shared
printf 'payload-v1\n' > origin/VERSION
printf 'sibling-v1\n' > origin/shared/data
printf '%s\n' '---' 'name: chosen' '---' 'Synthetic chosen skill.' > origin/skills/chosen/SKILL.md
printf 'Hidden marker.\n' > origin/skills/hidden/SKILL.md
printf 'Fixture license.\n' > origin/LICENSE
cat > origin/skills/chosen/scripts/probe.sh <<'SH'
#!/bin/sh
set -eu
here=$(dirname "$0")
cat "$here/../../../VERSION" "$here/../../../shared/data"
SH
chmod +x origin/skills/chosen/scripts/probe.sh
git -C origin init -q -b main
git -C origin -c user.name=Fixture -c user.email=fixture@example.invalid add .
git -C origin -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm fixture
cat > selected/pack.toml <<'TOML'
[[skills]]
source = "git+file:///Users/Shared/yolo/git-skills-verification/origin//skills/chosen?ref=main"
as = "probe"
[[skills]]
agent = "fixture"
into = ".fixture/skills"
TOML
mkdir -p home/.config/yolo-jail
cat > home/.config/yolo-jail/config.toml <<'TOML'
packs = ["fixture/selected"]
update_check = false
[pack_sources]
fixture = {path = "/Users/Shared/yolo/git-skills-verification", bare = true}
[agent_updates]
"*" = false
TOML
export HOME=/Users/Shared/yolo/git-skills-verification/home
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_DATA_HOME="$HOME/.local/share"
export YOLO_NO_PROGRAM_READINESS=1 YOLO_NO_AUTO_CAPTURE=1 YOLO_NO_UPDATE_CHECK=1
export YOLO_NO_AUTO_IMAGE_REAP=1
cd /Users/Shared/yolo/git-skills-verification/workspace
```

Use each numbered setup in a separate fresh fixture. Substitute its fresh absolute
base in the manifest/config as well as shell variables. Never reuse a failed full log.

### W-GSS1 — Apple Container reading root and copy

1. Set `YOLO_SETUP=apple-container-self`. Validate using the emitted launcher:

   ```bash
   /absolute/emitted/bin/yolo config check
   /absolute/emitted/bin/yolo -- sh -c 'set -eu; "$HOME/.fixture/skills/probe/scripts/probe.sh"; test -f "$HOME/.fixture/skills/probe/SKILL.md"; test ! -L "$HOME/.fixture/skills/probe/SKILL.md"; test ! -e "$HOME/.fixture/skills/hidden"; readlink "$HOME/.fixture/skills/probe/scripts"'
   ```

   Expect `payload-v1` then `sibling-v1`, a regular private marker, no hidden
   registration and a target below `/home/agent/.yolo-packs/_skill-payloads`.
2. Keep a session alive with readiness/release FIFOs in the owned workspace. After
   the readiness event, remove **only** the owned HOME's mirrors/payload cache and
   rename the synthetic origin offline. Run the same actual attach command; expect
   identical output and no cache recreation. Observe producer termination as well
   as readiness; a generous timeout is a failure guard, not a timed pass condition.
3. Release/wait for the held session, stop the owned jail and verify its runtime
   liveness is gone before deleting the fixture. Confirm zero capture/install
   markers. Report marker/link shape, target text, outputs, cache purity and cleanup.

This settles native mapping/copy, not agent scanning or read-only integrity: Apple
Container's guest copy is not a read-only mount and a guest can alter that copy.

### W-GSS2 — macos-user, both modes

Repeat the same fixture/witness separately with `YOLO_SETUP=seatbelt-self` and
`YOLO_SETUP=seatbelt-yolo-account` where those setups are already provisioned.
Do not provision an account or change credentials as part of this witness.
Print `YOLO_PACK_ROOT` in the jail; the registration target must be under the same
`/Users/Shared/yolo-staged/packs/<container>.<tree-leaf>` root. Execute through
registration links, not directly from the payload. Report outputs, preserved safe
supporting links, sealed modes, unchanged owner paths and actual cleanup. Neither
mode is inferred from Podman or from reading `cp -R` argv.

### W-GSS3 — filesystem and owned-copy guards

On main, from the normal checkout environment (not the fixture HOME), run:

```bash
go test -short -count=1 ./internal/packsrc -run 'SkillPayloadSelectedRefusalsAndSupportingOmissions|SkillPayloadOmittedIntermediateLink|RemoveSealedSkillTree'
go test -short -count=1 ./internal/hostskills ./internal/treesync -run 'SourcedRegistrationArchiveFallback|OwnedLinks'
go test -short -count=1 ./internal/cli/run -run 'SourcedAppleContainerReadingRoot|DiscardRemoverDeletesSealed|RemoveSealedSkillTreeOrdinaryCompatibility'
```

Expect `ok`; distinguish Linux-only UID controls skipped on darwin from native
filesystem evidence. Report case-insensitive strict selected refusals versus
supporting omission groups, no-follow archive/sync and no leaked scratch.
File a lab report using the [lab convention](../lab-reports/README.md), one observed
row per witness. Failure gets a permanent regression and a fix-forward main commit.

## Separate measurements and nonblocking review notes

- **N7:** measure whole-payload reads/peak memory on fresh launch, attach and host
  observe using owned synthetic payloads near the bound. Do not infer cost from
  code inspection; begin by counting reader invocations and retained bytes.
- Reproduce declaration-certain built-in collision versus unresolved-layer notch
  suppression before changing policy. Also reproduce a deeply nested staged pack's
  record-root lookup before changing record resolution. Both were source-only P2
  notes, not established runtime defects.
- Test case-insensitive recognition of a skill's own license before proposing an
  ergonomics change. Canonical automatic-license refusal remains the contract.
- Native agent discovery remains unmeasured. No automated interactive agent/model
  execution is authorized; repository bytes/link shape do not prove scanner behavior.
- Real rootless-host reachability/ID mapping needs a real rootless host or CI, with
  Podman rootless/network-command observations. Nested host namespaces cannot prove it.
- Real AWS CLI v1/v2 compatibility, account/IAM visibility, retries and billing need
  a separately authorized account-free or provider check. No real AWS/STS/SSO call,
  credential/profile edit or install is authorized here. The Linux fake proves
  canonical broker invocation counts only, not request/cost ceilings.
