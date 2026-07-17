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

## 2. Build the builder image (native aarch64-darwin→aarch64-linux)
On a Mac this needs a Linux builder itself (chicken-and-egg) UNLESS the closure
is cached. Two ways — try substitution first:
```
NIXFLAGS="--extra-experimental-features 'nix-command flakes' --impure"
# Streamer build. If this needs a Linux builder and you don't have one yet,
# that's expected — see the NOTE below.
STREAMER=$(nix $NIXFLAGS build --no-link --print-out-paths .#packages.aarch64-darwin.builderImage 2>&1 | tail -1)
echo "streamer: $STREAMER"
```
> **NOTE / expected snag:** building `builderImage` on a Mac is itself an
> aarch64-linux build — the very thing we need a builder for. For THIS test,
> get the image without a Mad build one of these ways, in order:
>   (a) if the closure substitutes from cache.nixos.org, the build above just
>       works (most deps are stock; only sshd_config/entrypoint are tiny local
>       derivations — may still need a builder).
>   (b) **build the streamer on this Linux dev jail / CI and copy the resulting
>       image tar to the Mac** (the intended production flow — CI publishes to
>       GHCR, Mac pulls). For the test, `scp` the tar over.
>   (c) if a QEMU `darwin.linux-builder` is already running, it'll build it.
> Report WHICH path worked — it tells us how much the chicken-and-egg bites
> before Cachix/GHCR is live.

## 3. Produce a plain image tar, then convert to OCI for AC
The flake output is a *streamer* script that writes a docker-archive tar to
stdout. Convert it to the OCI layout AC wants (this is exactly what
image.py `_convert_via_skopeo` does):
```
"$STREAMER" > "$WORK/builder.tar"
OCI="$WORK/oci"
skopeo copy "docker-archive:$WORK/builder.tar" "oci:$OCI:latest"
tar cf "$WORK/builder.oci.tar" -C "$OCI" .
```

## 4. Load into Apple Container
```
container image load -i "$WORK/builder.oci.tar"
container images | grep yolo-jail-builder     # confirm it's there
```

## 5. Run it — and CAPTURE THE ADDRESS/PORT (the AC-specific unknown)
podman used `--network=host`; AC has **no `--net=host`** and networks each
container in its own VM. So we must discover how the host reaches the
container's sshd. Try, in order, and REPORT which works:
```
# (a) AC gives each container an IP on its internal network:
CID=$(container run -d --rm -e YOLO_BUILDER_PUBKEY="$YOLO_BUILDER_PUBKEY" \
      yolo-jail-builder:latest)
container ls                                   # note the container's ADDR column
ADDR=$(container inspect "$CID" | grep -i '"address"\|ipv4\|gateway' | head)
echo "container network info: $ADDR"

# (b) does AC support publishing a port to the host? (may not — report the error)
#   container run -d --rm -p 127.0.0.1:31122:22 -e YOLO_BUILDER_PUBKEY=... yolo-jail-builder:latest
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
