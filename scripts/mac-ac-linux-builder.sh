#!/bin/bash
# Ensure an aarch64-linux nix builder is running in Apple Container, and print the
# `--builders` spec that points nix at it.
#
# Usage:
#   nix build .#ociImage --builders "$(scripts/mac-ac-linux-builder.sh)"
#
# stdout is ONLY the spec, so it can be substituted directly. Every diagnostic goes
# to stderr.
#
# ─── WHY THIS EXISTS ───
#
# An ARM Mac cannot build the jail image and cannot substitute it either, and the
# second half is the one that surprises people. Measured on run 34868855087
# (2026-09-14): substitution fetched hundreds of paths from cache.nixos.org and
# then stopped at five derivations that must be BUILT — nodejs, nix-ld,
# bin-path-links, yolo-jail-prefix-links, yolo-jail-root.
#
# ⚠ Cachix cannot close that gap on this architecture, and pushing harder will not
# help. `imageIdentity` is a hash of flake.nix + flake.lock, so a darwin eval and a
# Linux eval agree on the .drv paths — but only WITHIN ONE ARCHITECTURE. The
# nightly's `build-image` job runs on `ubuntu-latest`, which is x86_64, so what it
# pushes is x86_64-linux. An arm64 Mac needs aarch64-linux: a different closure, not
# a stale one.
#
# So the Mac needs a real aarch64-linux builder, and this repo already proved one:
# docs/plans/runbooks/mac-ac-container-builder.md, verified 2026-07-17 on macOS 26.5
# arm64 and again 2026-09-14 on Apple Container 1.1.0 (`Trusted: 1`, proof build
# returned AC-CONTAINER-BUILDER-WORKS). This script is that runbook's procedure made
# repeatable and non-interactive; the runbook stays the explanation.
#
# ⚠ THERE IS A DEAD BUILDER ON THIS MACHINE THAT WILL MISLEAD YOU. If
# /etc/nix/nix.custom.conf carries `builders = ssh-ng://builder@linux-builder …`
# with /etc/ssh/ssh_config.d/100-linux-builder.conf pointing at localhost:31022,
# that is the nix-darwin convention — and nix-darwin may not be installed
# (`/run/current-system` absent), in which case the LaunchDaemon that would start
# the VM execs a `create-builder` that does not exist. nix then reports a cache-shaped
# problem as `failed to start SSH connection to 'linux-builder'`. Passing `--builders`
# OVERRIDES that setting, which is why this script prints a spec rather than editing
# any file — no sudo, and no dependence on which stubs a machine happens to carry.

set -uo pipefail

IMAGE=${YOLO_AC_BUILDER_IMAGE:-ghcr.io/mschulkind-oss/yolo-jail-builder:latest}
NAME=${YOLO_AC_BUILDER_NAME:-yolo-ac-builder}
CPUS=${YOLO_AC_BUILDER_CPUS:-8}
MEMORY=${YOLO_AC_BUILDER_MEMORY:-12g}
KEYDIR=${YOLO_AC_BUILDER_KEYDIR:-$HOME/.local/share/yolo-jail/ac-builder}
KEY=$KEYDIR/builder_key

log() { printf '%s\n' "$*" >&2; }
die() { printf 'mac-ac-linux-builder: %s\n' "$*" >&2; exit 1; }

command -v container >/dev/null 2>&1 || die "the \`container\` CLI is not on PATH. \
This needs Apple Container (userguide/guides/macos.md)."
container system status >/dev/null 2>&1 || die "the Apple Container apiserver is not \
running. Start it with: container system start"

# ─── THE KEY IS DURABLE, NOT A mktemp ───
#
# The runbook generates a throwaway pair because it is proving a capability once.
# A CI runner needs the same key across runs: the container authorizes it at START,
# so a fresh key per invocation would mean restarting the container every time —
# and it would leave the nix daemon's known-hosts entries pointing at keys nothing
# uses. 0600, in the user's own data dir.
if [ ! -f "$KEY" ]; then
  log "generating a builder keypair at $KEY"
  mkdir -p "$KEYDIR" && chmod 700 "$KEYDIR"
  ssh-keygen -t ed25519 -N "" -f "$KEY" -q -C "yolo-ac-linux-builder" \
    || die "ssh-keygen failed"
  chmod 600 "$KEY"
fi

if ! container image ls 2>/dev/null | grep -q "yolo-jail-builder"; then
  log "pulling $IMAGE (~400 MB, once per machine)"
  container image pull "$IMAGE" >&2 || die "could not pull $IMAGE. It is the PREBUILT \
aarch64-linux builder — a Mac cannot build it, which is the chicken-and-egg this \
image exists to break."
fi

# ─── START IF NEEDED, AND RE-READ THE ADDRESS EVERY TIME ───
#
# The IP is NOT stable. Observed 2026-09-14: 192.168.64.2 on one start and
# 192.168.64.3 on the next, within minutes. Apple Container networks each container
# in its own VM and assigns from its internal range, so anything that caches the
# address is wrong by the next restart — which is the reason this is a script that
# prints a spec rather than a line in nix.conf.
if ! container ls 2>/dev/null | grep -q "^$NAME "; then
  log "starting $NAME ($CPUS cpus, $MEMORY)"
  # RESOURCES MATTER HERE, unlike in the runbook's proof. The default 1 GB is enough
  # to echo a string into $out; it is not enough to compile nodejs, which is one of
  # the five derivations this builder exists to produce.
  container run -d --rm --name "$NAME" --cpus "$CPUS" --memory "$MEMORY" \
    -e YOLO_BUILDER_PUBKEY="$(cat "$KEY.pub")" "$IMAGE" >/dev/null 2>&1 \
    || die "could not start $NAME"
  # sshd needs a moment; the address appears with the container.
  for _ in $(seq 1 30); do
    container ls 2>/dev/null | grep -q "^$NAME " && break
    sleep 1
  done
fi

ADDR=$(container ls 2>/dev/null | awk -v n="$NAME" '$1==n {print $6}' | cut -d/ -f1)
[ -n "${ADDR:-}" ] || die "$NAME is running but has no address in \`container ls\`. \
The column layout may have changed; run it by hand and read the IP column."
log "builder at $ADDR"

# ─── THE HOST KEY IS PINNED IN THE SPEC, AND THAT IS LOAD-BEARING ───
#
# A remote build is performed by the nix DAEMON, as root — not by this shell. So
# NIX_SSHOPTS, this user's ~/.ssh/config and this user's known_hosts are all
# invisible to it, and the first connection would fail host-key verification with
# no way for a non-root caller to fix it.
#
# The builders spec's 8th field is the base64-encoded host public key. Supplying it
# means the daemon verifies against a key we just read, needs no known_hosts entry,
# and needs no sudo — which is what keeps this whole path privilege-free.
for _ in $(seq 1 30); do
  HOSTKEY=$(ssh-keyscan -t ed25519 "$ADDR" 2>/dev/null | grep -v '^#' \
    | awk '{print $2" "$3}' | head -1)
  [ -n "${HOSTKEY:-}" ] && break
  sleep 1
done
[ -n "${HOSTKEY:-}" ] || die "sshd on $ADDR never answered a keyscan. Check: \
container logs $NAME (expect \"Server listening on 0.0.0.0 port 22\")."
HOSTKEY_B64=$(printf '%s' "$HOSTKEY" | base64 | tr -d '\n')

# uri system key maxjobs speedfactor supportedFeatures mandatoryFeatures hostPubKey
printf 'ssh-ng://root@%s aarch64-linux %s %s 1 big-parallel,kvm - %s\n' \
  "$ADDR" "$KEY" "$CPUS" "$HOSTKEY_B64"
