---
status: draft
stage: SKETCH
next: "A planner completes this against the tree: the map, reuse, traps and tasks for build steps 1 to 5 of keychain-from-a-jail.md §6"
depends-on: [keychain-from-a-jail.md]
tags: [plan, copilot, credentials, keychain, secret-service, d-bus, loopholes]
---

# Plan seed: Copilot's keychain, from a jail

**Status:** 2026-11-12 — a sketch, opened from what the accepted design surfaced; not a hand-off.
The design is accepted with no question open, so the planner's gate is open; nothing may be built
from this file until a planner has read the tree and stamped it `DECIDED`.

**Design:** [`keychain-from-a-jail.md`](keychain-from-a-jail.md). The design wins on behavior; the
tree wins on fact; this file is the first thing to be wrong.

## Where the build stands

- **Built:** only build step 6, macos-user's route ([OQ-KC5](keychain-from-a-jail.md#OQ-KC5)):
  `host_keychain_env` in `packs/copilot/pack.toml`, read by `internal/cli/run/hostkeychainenv.go`.
  Whether Copilot accepts the token is [W-KC5](keychain-from-a-jail.md#W-KC5)'s, waiting on account
  mode making HTTPS requests.
- **The service name.** W-KC5 step 1 found Copilot 1.0.105's login under `copilot-cli` on a Mac
  host (2026-11-04), the provisional machine list of
  [KC-D12](keychain-from-a-jail.md#KC-D12). Linux Copilot's name is still owed by build step 4's
  human login; a mismatch fails safe (the login stays per workspace).
- **Everything else** — steps 1 to 5 — is unbuilt.

## Notes the design surfaced

**Order.** The design's [§6](keychain-from-a-jail.md#6-build-order) order holds: the protocol core
with no host (step 1) is independent of every core change and can start first; the host half
(step 2) after it; wiring (step 3) needs the core changes below; the login copy (step 5) is
independent of steps 1 and 2.

**Core changes** ([§3.14](keychain-from-a-jail.md#314-what-yolo-itself-must-change)), each a
search for the planner, not an entry:

- Readiness budget and `waiting`/`failed` lines for a loophole host daemon ([KC-D13](keychain-from-a-jail.md#KC-D13)).
  Check what the [host-service startup diagnostics](../reference/host-service-startup-diagnostics.md)
  already built: a failure-only reason channel exists, `ready`/`waiting` and a longer budget do not.
  Reuse that channel; do not add a second one.
- A launcher-side value in the jail's whole environment, set only when the route is on
  ([KC-D18](keychain-from-a-jail.md#KC-D18)). Check how `CLAUDE_SECURESTORAGE_CONFIG_DIR` reaches
  each backend (`internal/cli/run/claudesecurestorage.go`) and copy that vehicle split.
- A startup input naming the container and the selected packs' machine lists. Check the host
  daemon's argv builder for the current inputs ([E25](keychain-from-a-jail.md#E25)).
- A machine-list field in the pack manifest (`internal/packdecl/packdecl.go`; its doc comments are
  the reference).
- A written-out footprint claim for the keychain crossing (`yolo pack footprint`).
- The attach rule ([KC-D14](keychain-from-a-jail.md#KC-D14)): an attach returns before loophole
  daemons start (`internal/cli/run/run.go`); the probe goes on that path.
- A host-side step when a jail ends, for the login copy's harvest. Check whether the keeper's
  end-of-jail hook that [mcp-auth-sharing-plan ID-19](mcp-auth-sharing-plan.md#id-19) added
  (`endJail`) can carry it, before writing another.

**Dependencies.** `CGO_ENABLED=0` and no new vendored module
([E21](keychain-from-a-jail.md#E21)): `dh` from `math/big`, `crypto/hkdf`, AES-CBC by hand; sealed
files with `crypto/aes` + `crypto/cipher` GCM. The Linux host adapter may reuse the in-tree D-Bus
code from step 1 or BB-D11's `godbus` — check whether `godbus` is vendored yet before choosing.

**Test fixtures.** `secret-tool` comes from nixpkgs for step 1's tests; check it is in the dev
shell or the image before relying on it. A go-keyring replay client is the implementer's choice
(vendoring go-keyring as a test dependency is allowed by the design).

**Traps the design already names, to carry into the plan's Traps section:**

- The `dh` shared secret is left-padded to 128 bytes before HKDF; one test forces a leading zero
  byte ([§3.2](keychain-from-a-jail.md#32-the-jail-side-a-single-tenant-bus-that-is-also-the-secret-service)).
- The sealed-file directory is never under `cache/`, which every jail mounts read-write
  ([§3.3](keychain-from-a-jail.md#33-the-host-side-the-doorway-into-the-real-keychain)).
- A nested jail verifies none of the jail-to-host hop: build step 4 is a real jail on a rootless
  host ([AGENTS.md, Testing](../../AGENTS.md#testing)).
- The workspace key is the container name, never the preamble's 32-bit jail identity
  ([KC-D7](keychain-from-a-jail.md#KC-D7)).
