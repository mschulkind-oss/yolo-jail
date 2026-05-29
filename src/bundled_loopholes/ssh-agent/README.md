# ssh-agent loophole (bundled)

Pass-through of the host's ssh-agent socket. Lets jailed `git`, `ssh`, `scp`, etc. sign against keys held by the host's agent without copying private keys into the jail.

**Disabled by default** — most jails don't need agent access, and granting it on by default would silently expand every jail's blast radius.

## Trust model

This is **raw passthrough**. Semantically identical to `ssh -A` agent forwarding: anything in the jail that can read `SSH_AUTH_SOCK` can ask the host agent to sign with any key the agent holds. Keys never leave the agent (the SSH agent protocol exposes signing, not key extraction), but signing-as-you is the whole capability we're granting.

Don't enable this in jails running untrusted workloads. A future version may grow a filtering broker (host-side daemon, per-key allowlist) — for v1 the trust posture matches `ssh -A`, no new code paths.

## Enabling

Per workspace, in `yolo-jail.jsonc`:

```jsonc
"loopholes": {
  "ssh-agent": { "enabled": true }
}
```

Or globally:

```sh
yolo loopholes enable ssh-agent
```

Either way the change takes effect on the **next `yolo run`** — loopholes are wired at container-create time, not hot-attached. Restart any in-flight jails after enabling.

## Activation gate

Even when enabled, the loophole stays inactive when the host has no agent — `requires.file_exists: "${SSH_AUTH_SOCK}"` collapses an unset env var to the empty string and fails the existence check, so the loophole stays silently inactive instead of crashing the jail. `yolo loopholes list` will show it as inactive with a reason.

## Runtimes

Works on **Linux Podman**, **macOS Podman**, and **macOS Apple Container**.

Unix sockets can't traverse virtiofs, so on Apple Container the loader detects when the bind-mount source is the live `$SSH_AUTH_SOCK` and emits AC's purpose-built `--ssh` flag — which forwards the host agent to a fixed in-jail path (`/var/host-services/ssh-auth.sock`) — instead of `-v`. The loader also rewrites the manifest's `SSH_AUTH_SOCK` jail_env to AC's fixed path so apps in the jail point at the socket AC actually populates. Manifests stay runtime-agnostic — same declaration, the right flag is chosen at run time. (Note: `--publish-socket` is the *opposite* direction — container→host — so it can't be used for this.)

Verify with `yolo loopholes list` from inside the jail. If the loader is older than the socket-aware version, the loophole shows "host-side wiring not visible in this jail" on AC and the fix is a yolo-jail upgrade — not a config change.

## Inside the jail

After a jail restart with the loophole active:

```sh
ssh-add -l                  # lists keys held by the host agent
ssh -T git@github.com       # authenticates with the user's host SSH identity
git clone git@github.com:…  # works without keys-in-jail
```
