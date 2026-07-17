# RUNBOOK — prove the Apple Container Linux builder (zero sudo)

**Who runs this:** a host agent (or you) on the Mac, in a checkout of yolo-jail.
**Privilege:** NONE. No sudo, no `/etc` writes, no daemon restart. Everything is
a throwaway container + local nix calls. Cleanup = `container rm`.

**What it proves (the gating unknown from the support matrix):** can Apple
Container (a) load our builder image (via OCI conversion) and (b) run it so its
sshd is reachable by the host's nix over `ssh-ng` and actually builds. podman
already passed this in-jail; AC is the open question.

**The make-or-break step is #6** (host nix → AC container sshd). Everything
before it is plumbing; if #6 works, AC becomes a supported build path. If it
fails, we fall back to QEMU `darwin.linux-builder` for AC — a known result,
not a failure of the session.

---

## Prereqs (report versions, don't fix)
```
container --version && container system status     # AC installed + running
nix --version                                      # host nix present
command -v skopeo || echo "NO skopeo — needed for AC OCI conversion (brew install skopeo)"
sw_vers -productVersion ; uname -m                 # macOS version + arm64
```
If `skopeo` is missing, `brew install skopeo` (no sudo) — it's how we convert
our streamed image to the OCI layout AC requires (mirrors image.py).

## 1. Generate a throwaway builder keypair (the host-daemon side)
```
WORK=$(mktemp -d); echo "WORK=$WORK"
ssh-keygen -t ed25519 -N "" -f "$WORK/builder_key" -q -C "yolo-ac-test"
export YOLO_BUILDER_PUBKEY="$(cat "$WORK/builder_key.pub")"
echo "pubkey: $YOLO_BUILDER_PUBKEY"
```

## 2. Get the builder image onto the Mac

The image is `aarch64-linux` — a Mac can't build it without a Linux builder
(the very thing it IS). So the real path is **pull the prebuilt one from GHCR**
(CI builds it natively on an arm-Linux runner and publishes it).

### 2a. Preferred — pull from GHCR (once the publish job has run on a release)
```
REPO=ghcr.io/mschulkind-oss/yolo-jail-builder
container image pull "$REPO:latest"       # AC pulls OCI straight from GHCR
container images | grep yolo-jail-builder
```
If the pull 404s / auth-fails, the publish job hasn't run yet (it's
release-gated) or the package is still private — see the fallback, and flip it
public in GHCR package settings.

> **AC can `pull` an OCI registry image directly** — no skopeo-convert or
> `image load` of a tar needed. That's the whole ergonomic win of GHCR over
> shipping a tarball. If your AC version can't pull, fall through to 2b.

### 2b. Fallback — build on a Linux box + copy the tar (no GHCR yet)
Build the streamer on this repo's Linux dev jail / CI (native aarch64-linux),
copy the tar, convert to OCI, and `image load` (mirrors image.py's skopeo path):
```
# ON A LINUX MACHINE (or arm CI):
NIXFLAGS="--extra-experimental-features 'nix-command flakes' --impure"
STREAMER=$(nix $NIXFLAGS build --no-link --print-out-paths .#packages.aarch64-linux.builderImage | tail -1)
"$STREAMER" > builder.tar        # then scp builder.tar to the Mac
# ON THE MAC:
skopeo copy "docker-archive:builder.tar" "oci:$WORK/oci:latest"
tar cf "$WORK/builder.oci.tar" -C "$WORK/oci" .
container image load -i "$WORK/builder.oci.tar"
container images | grep yolo-jail-builder
```
Report WHICH path (2a pull vs 2b tar) you used.

## 5. Run it — and CAPTURE THE ADDRESS/PORT (the AC-specific unknown)
podman used `--network=host`; AC has **no `--net=host`** and networks each
container in its own VM. So we must discover how the host reaches the
container's sshd. Try, in order, and REPORT which works:
```
# IMG = whatever §2 gave you: "$REPO:latest" (2a pull) or yolo-jail-builder:latest (2b load)
IMG="${REPO:-}:latest"; [ "$IMG" = ":latest" ] && IMG="yolo-jail-builder:latest"

# (a) AC gives each container an IP on its internal network:
CID=$(container run -d --rm -e YOLO_BUILDER_PUBKEY="$YOLO_BUILDER_PUBKEY" "$IMG")
container ls                                   # note the container's ADDR column
ADDR=$(container inspect "$CID" | grep -i '"address"\|ipv4\|gateway' | head)
echo "container network info: $ADDR"

# (b) does AC support publishing a port to the host? (may not — report the error)
#   container run -d --rm -p 127.0.0.1:31122:22 -e YOLO_BUILDER_PUBKEY=... "$IMG"
```
```
container logs "$CID" 2>&1 | tail          # expect: "Server listening on ... port 22"
```
**Report:** the container's reachable address (its VM IP, or a published
127.0.0.1 port if AC supports `-p`). Call it `<HOST>:<PORT>` below.

## 6. ⭐ THE GATING TEST — host nix builds THROUGH the AC container
```
export NIX_SSHOPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
STORE="ssh-ng://root@<HOST>?ssh-key=$WORK/builder_key"      # add :<PORT> / &port= if published

# 6a. protocol handshake — does nix see a trusted store?
nix $NIXFLAGS store info --store "$STORE"          # expect: "Trusted: 1"

# 6b. THE PROOF — build a Linux derivation inside the AC container, read it back
OUT=$(nix $NIXFLAGS build --no-link --print-out-paths --store "$STORE" \
  --expr 'derivation { name="ac-proof"; system=builtins.currentSystem; builder="/bin/sh"; args=["-c" "echo AC-CONTAINER-BUILDER-WORKS > $out"]; }' | tail -1)
nix $NIXFLAGS store cat --store "$STORE" "$OUT"    # expect: AC-CONTAINER-BUILDER-WORKS
```
If 6a says `Trusted: 1` and 6b prints `AC-CONTAINER-BUILDER-WORKS`, **the AC
container-builder cell goes ✅** — same result I got on podman in the Linux jail.

## 7. Cleanup (no residue)
```
container rm -f "$CID" 2>/dev/null
container image rm yolo-jail-builder:latest 2>/dev/null
rm -rf "$WORK"
```

---

## What to report back (any outcome is useful)
1. Prereq versions (AC, nix, skopeo, macOS).
2. **How you got the image** (§2 path a/b/c) — quantifies the chicken-and-egg.
3. **How the host reaches the container** (§5): AC container IP? published port?
   neither (→ the real AC blocker)?
4. **§6 result verbatim** — `Trusted: N` and whether `AC-CONTAINER-BUILDER-WORKS`
   printed. This is the answer.
5. Any AC error at load/run/connect — paste it; those are the exact gaps I'd fix.

## Interpreting it
- **§6 works** → AC container-builder is proven; I wire the CLI orchestration
  (start container + point nix at `<HOST>:<PORT>`) and AC joins podman as a
  supported build path.
- **§5 has no reachable path** (AC won't expose the sshd to the host nix daemon)
  → that's the AC networking limit we suspected; AC falls back to QEMU
  `darwin.linux-builder`, and we document it. Still a clean, useful result.
- **§2 needed a builder you didn't have** → underscores turning on Cachix/GHCR so
  the Mac pulls the image instead of building it.
