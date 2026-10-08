---
status: accepted
stage: GRADUATED
date: 2026-10-07
next: "Follow the accepted host-service startup diagnostics design and incomplete implementation checklist; runtime acceptance remains open"
tags: [diagnostics, host-services, aws-auth, launch, happy-path]
summary: "A missing AWS narrowing produces the right daemon error but the launch shows generic socket and reachability failures instead. Preserve disclosures while promoting the original cause and the user-scope remedy; do not turn the service bypass into the normal fix."
---

# Host-service startup failures need their original cause and remedy

Yolo manages an agent's environment, including services that provide credentials. When one
service refuses its configuration, the operator should see that refusal and the safest next
step—not infer it from the service's missing socket inside a jail.

**Source checked:** 2026-10-07 at `10f90ba3e`. This records an observed reporting shape and
requirements for a repair. The [design](../design/host-service-startup-diagnostics.md) now owns
that repair, with its [implementation plan](../plans/host-service-startup-diagnostics.md).
Candidate work is stopped outside main; it has not passed whole-feature acceptance.
The AWS policy remains the pack's; core must not learn AWS setting names to special-case it.

## The observed failure

A launch selects AWS authentication with a profile but neither a role to assume nor explicit
permission to serve that profile's permission set unchanged. The host daemon's log contains:

```text
yolo-aws-auth: refusing to serve — no narrowing is configured for AWS profile "<test-profile>":
set loopholes.aws-auth.settings.role_arn ... or set
loopholes.aws-auth.settings.unnarrowed to true to serve that permission set as-is.
```

The terminal instead shows a chain:

1. The host-wide daemon's settings changed, so it is restarting.
2. The daemon exited without binding its socket; consult its log.
3. The socket is not accepting connections.
4. The jail cannot find the published endpoint.
5. Host-loopback forwarding was requested but the service is unusable; the jail refuses entry.
6. A bypass variable and a second failed-state-preservation variable are offered.

The first useful explanation—the daemon's configuration refusal—is absent from that chain.
Both trust disclosures and legitimate downstream checks obscure the action the operator needs.
A generic “relaunch” cannot repair unchanged invalid settings.

The inspected host log also contains later valid role-narrowed startup entries. A historical
refusal in a reused log is evidence of that refusal, **not proof that the daemon is still down**
or that a different launch failed for the same reason. This is why an automatic diagnostic
must be tied to the startup attempt, not scraped from the log's last interesting line.

## The existing policy and current remedy

**Narrowing** means limiting the AWS permissions supplied to a jail, through an assumed role
and optionally its session policy. The [AWS authentication pack](../../packs/aws-auth/README.md)
defines this policy and why absence must never grant unchanged permissions. AWS's
[session-policy reference](https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies.html#policies_session)
explains how an assumed role can be restricted further.

The supported configuration lives in the host user's configuration, not in agent-writable
workspace settings. If the operator deliberately wants to test a profile without further
narrowing, merge the following into their existing user configuration:

```json
{
  "loopholes": {
    "aws-auth": {
      "settings": {
        "profile": "<test-profile>",
        "unnarrowed": true
      }
    }
  }
}
```

This is a fragment to merge, not a replacement for the entire file. If `role_arn` or
`session_policy` is configured, resolve that conflict deliberately: requesting unchanged
permissions alongside a role is refused, and a session policy without a role is refused.
The alternative is to set a suitable `role_arn`, adding `session_policy` if needed.

After editing the host user's configuration, run the host's `yolo check --no-build`, then
retry the intended launch. The explicit setting selects the configured permission-set/profile
policy as-is; it does not mean unrestricted AWS access. The 2026-10-07 ruling removes routine
`unnarrowed` notices entirely, not merely their warning color. That runtime change landed
2026-10-08; required pack read/exec disclosures and real refusals/errors must remain.
Nothing in this investigation changed configuration or chose this route for the operator.

`YOLO_ALLOW_UNREACHABLE_SERVICES=1` is for an intentionally service-less debugging shell.
It neither changes the selected AWS permission mode nor repairs a daemon. It must not become
the headline remedy for this configuration error.

## Where the cause is lost

- [The settings resolver](../../internal/awsauth/settings.go) rejects missing narrowing and
  names the relevant full configuration paths.
- [The daemon startup preparation](../../internal/awsauthdaemon/main.go) reports that refusal
  before probing the AWS CLI or binding sockets.
- [The generic singleton lifecycle](../../internal/broker/brokerlifecycle.go) detects exit or
  timeout but reports only the socket failure and log path. It does not carry this daemon's
  configuration reason back to the launch.
- [The launch service path](../../internal/cli/run/loopholesruntime.go) and
  [the jail reachability check](../../internal/entrypoint/reachability.go) report subsequent
  consequences. Those checks are not the original configuration validator.

The missing channel was first identified by [the keychain design's host-daemon readiness work](../design/keychain-from-a-jail.md#314-what-yolo-itself-must-change). The bounded failure-only channel and early pure-settings validation for this incident are now specified in [`host-service-startup-diagnostics.md`](../design/host-service-startup-diagnostics.md); do not add an AWS-only error parser.

## What better output should say

The following is **proposed**, not current CLI output:

```text
Cannot start aws-auth: this profile has no configured credential narrowing.

For this deliberate test, set loopholes.aws-auth.settings.unnarrowed to true in your USER config
(~/.config/yolo-jail/config.jsonc). This lets every process in the jail use that profile's
permission set. Otherwise configure loopholes.aws-auth.settings.role_arn.

Then run yolo check --no-build on the host and retry your original command.
Details: the host-service log. The missing socket and jail endpoint follow from this refusal.
```

A useful final failure block belongs near the point of refusal, after any required disclosures.
It should repeat the original reason and remedy even when earlier output has scrolled away.
The [happy-path principle](../reference/happy-path-principle.md) already requires a next step;
[report tiers](../reference/report-tiers.md) owns disclosure density and the launch's no-quiet
rule. This repair must honor both. Removing only the expected `unnarrowed` notice does not
remove pack trust disclosures, silently opt a user in, or alter the profile's permission policy.

## Repair requirements

- Distinguish a settings refusal, missing executable, authentication failure, process crash,
  timeout and transport failure; do not label them all as networking.
- Bind the reason to the exact attempted daemon startup. Bound and sanitize its size, and
  never include tokens, credential responses or arbitrary old log content.
- Check invalid desired settings before costly provisioning where the pack can expose a cheap
  check. Investigate validation before replacing a working shared daemon; a refused new launch
  must never silently use credentials from the previous profile.
- Preserve the disclosure that a host-wide settings change affects other jails.
- Keep the causal refusal prominent and place derivative endpoint/socket messages under it.
- Keep config scope explicit. A workspace must not authorize a broader host credential.
- Preserve the reachability guard and its existing host-capability distinctions. Earlier
  configuration diagnosis is not a waiver of that guard.

## Verification needed

Use permanent tests that call the actual launch path, not only the daemon's settings parser:

1. A fake daemon refuses its settings before binding; the terminal contains its reason and
   actionable remedy, and no networking remedy is presented as the configuration fix.
2. The host-wide log contains an older, different failure; the new launch does not reuse it.
3. A valid previous daemon exists and new settings are invalid; document and test the handling
   of existing clients, without serving the old profile to the new launch.
4. Crash, timeout, absent executable and actual transport failure remain distinct.
5. Required disclosures remain visible and no secret appears in terminal output or saved reports.
6. Deleting the production caller's diagnostic forwarding makes a regression test fail.

Linux fixtures can verify the reporting contract. Nested containers do not verify real
rootless host-loopback forwarding; the [native testing limits](../../AGENTS.md#testing) still
apply. No AWS API call or credential-widening action was performed in this investigation.
