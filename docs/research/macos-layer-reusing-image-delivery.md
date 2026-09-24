---
title: "Layer-reusing image delivery on the two Mac backends"
author: "Agent"
date: 2026-09-24
status: in-review
tags: [research, macos, image-delivery, podman, apple-container]
summary: "Can podman-on-macOS and Apple Container get the per-layer reuse skopeo gives podman on Linux? Measured on Linux, sourced for the Mac side. A delta archive gets it with no new listener, but Apple Container still rebuilds its ext4 snapshot for every new image."
vantage:
  status-chip: true
---

# Layer-reusing image delivery on the two Mac backends

**Status:** RESEARCH, 2026-09-24. Everything on Linux here is **MEASURED** in this jail. Everything
about the Mac is **SOURCED** (read in upstream source at a named commit) or **INFERRED**. No Mac
was used. [What only a Mac can confirm](#what-only-a-mac-can-confirm) lists the commands that
settle each Mac claim.

**The question** (the maintainer, 2026-09-24): on podman-on-macOS and on Apple Container, can
image delivery get the layer reuse that `skopeo copy nix: → containers-storage:` gives podman on
Linux? That would "basically eliminate all image loading". Today both Mac backends go through
`deliverViaArchive`: they write a full archive and load it, and "neither backend gets the layer
reuse" ([Archive destinations](../reference/image-staging-vs-baking.md#archive-destinations)).

## The answer

1. **Yes for the transfer, on both backends, with no new listener and no new binary.** The two
   loaders we already call (`podman load -i` and `container image load -i`) take an OCI archive
   that leaves out every layer the destination already holds, as long as the manifest still
   names those layers. Measured on Linux for podman: after a `packages:` change, a
   **27.8 MB delta archive** replaces the 3.45 GB one, and the incremental delivery takes
   **1.8–2.0 s instead of 9.2–11.7 s**. For Apple Container this is SOURCED from its import code,
   not measured. See [the delta archive](#option-3--a-delta-archive-through-the-existing-loaders).
2. **For podman on macOS that is plausibly most of the cost.** A full `podman load -i` from the
   remote client uploaded **3,448 MB** over the API socket even when every layer was already in
   the store (MEASURED). On a Mac that socket is SSH-forwarded by gvproxy into the VM (SOURCED).
   Whether this upload is really the 10–33 min seen on CI can only be answered on a Mac.
3. **Not "all image loading" on Apple Container.** Its images service builds a **fresh ext4
   snapshot from every layer of each new image**. Nothing is reused across images at that step
   (SOURCED). Reuse removes the archive write, the tar extraction and the blob ingest, but the
   unpack of about 3.45 GB still happens on every image change. How much of the 22 s that
   unpack takes has not been measured.
4. **The first load is not helped by any option.** CI's macOS runners start with an empty VM
   store every job, so CI's 10–33 min `podman load` is a first-load cost that layer reuse
   cannot touch. Reuse pays off on a machine that keeps its runtime store between launches,
   such as the maintainer's Mac.

The two routes the question proposed were both measured too.

- `podman pull oci:<dir>` is **refused by the remote client** that macOS podman always is.
- A read-only registry **works**, over a non-loopback address as well, but it adds a listener.

[Recommendation](#recommendation-and-its-cost) has the verdicts.

---

## Terms

- **Layer reuse / blob negotiation.** Before copying a blob, the copy asks the destination
  whether it already holds that digest, and skips the blob if it does. In containers/image, the
  library under both skopeo and podman, this is each destination's `TryReusingBlob`. It is what
  makes the Linux path cheap ([Delivering into the
  runtime](../reference/image-staging-vs-baking.md#delivering-into-the-runtime)). It is not
  compression, and it is not deduplication inside one archive.
- **OCI image layout.** A directory holding `oci-layout`, `index.json` and `blobs/sha256/<hex>`,
  one file per blob, named by its digest. Defined by the [OCI Image Layout
  spec](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) (read
  2026-09-24). An **`oci-archive`** is that directory as a tar. A **`docker-archive`** is the
  older `docker save` tar: `manifest.json` plus one tarball per layer.
- **OCI Distribution API.** The HTTP protocol registries speak: `GET /v2/`,
  `GET|HEAD /v2/<name>/manifests/<ref>` and `GET|HEAD /v2/<name>/blobs/<digest>` for pulls. Defined
  by the [OCI Distribution spec](https://github.com/opencontainers/distribution-spec/blob/main/spec.md)
  (read 2026-09-24). A pull-only server needs just those three routes.
- **Podman Machine.** The Linux VM that podman on macOS runs containers in. The `podman` on the
  Mac is a **remote client** of the podman API service inside that VM
  ([podman-machine](https://docs.podman.io/en/latest/markdown/podman-machine.1.html), read
  2026-09-24).
- **gvproxy.** The user-space network stack from
  [gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock). Podman Machine's `applehv`
  and `libkrun` providers both use it for the VM's network and to forward the API socket
  (SOURCED, podman v5.8.6 `pkg/machine/{applehv,libkrun}/stubber.go`,
  `pkg/machine/shim/networking.go`).
- **Content store / snapshot (Apple Container).** Apple Container keeps image blobs by digest in a
  content store on the Mac. Before an image can run, it unpacks all of its layers into one ext4
  block file, the **snapshot**, kept per image manifest (SOURCED, apple/container `eafe6b8`
  `SnapshotStore.swift`).
- **Present set** *(coined here)*. The layer digests the destination runtime already holds. Today
  yolo reads it only to print the copy report (`PresentLayerDigests` in
  [`layercopy.go`](../../internal/image/layercopy.go)). The delta archive would use it to
  decide what to send.
- **Delta archive** *(coined here)*. An OCI archive whose manifest names every layer of the image
  but whose `blobs/` holds only the blobs outside the present set. It is not a
  "diff" or a "thin" image in any standard sense. The manifest is byte-identical to the full
  image's.
- **Placeholder seeding** *(coined here)*. A way to build a delta archive with the existing
  copier. Before `skopeo copy nix: → oci:<dir>`, create a zero-byte file at
  `blobs/sha256/<hex>` for each digest in the present set. The OCI layout destination treats a
  blob as present when that file exists, so skopeo writes only the other blobs. The placeholders
  are deleted before the tar is made.

---

## What the measurements ran on

**Environment (MEASURED 2026-09-24):**

- This jail: 32 cores; `/tmp` is btrfs with zstd.
- Nested **rootful** podman 5.8.6 (overlay driver through fuse-overlayfs), in a fresh
  `--root` store per run.
- To model the Mac's remote client, every podman step except those marked "local" went through
  `podman --remote` to a `podman system service` on a unix socket. There is no VM hop here.
  Linux cannot measure the cost of that hop, only the bytes that cross it.

**Copier:** `nix build --impure .#imageCopier`, which gives skopeo 1.24.0 with the `nix:`
transport.

**The image pair:** the real jail image, `.#ociImage`, built twice.

- **A:** `YOLO_EXTRA_PACKAGES=` (empty). 91 layers, 3,446,701,056 bytes of layers.
- **B:** `YOLO_EXTRA_PACKAGES='["hello"]'`. 92 layers.

B has exactly **two layers A lacks**: the `packages:` tier (343,552 B) and the top layer
(27,436,544 B), 27.78 MB together. This is exactly the recurring case in the question: a
`packages:` change makes a new image. The layer tiers are described in [The layer
plan](../reference/image-staging-vs-baking.md#the-layer-plan).

**Registry binaries:**

- `nixpkgs#distribution` 3.1.1, the reference registry.
- A 113-line pull-only Distribution handler written for this research, using only the Go
  standard library (`/tmp/lr/ocireg/main.go`, scratch, not committed). It serves an OCI layout
  read-only: `GET`/`HEAD` only, `405` for anything else, and it logs the bytes it serves.

Each option ran twice (run 1 / run 2). Times are wall-clock.

---

## Measured results

### Baselines

| Path | Load of A into an empty store | Load of B into a store holding A |
| :--- | :--- | :--- |
| **Linux today:** `skopeo copy nix: → containers-storage:` | 27.5 / 22.8 s | **1.50 / 1.50 s** |
| **macOS-podman today:** `nix: → docker-archive`, then `podman --remote load -i` | write 6.6 / 7.1 s, load 56.1 / 35.2 s | write 6.7 / 8.1 s, load 2.8 / 2.75 s; **total 9.2–11.7 s** |
| **Apple-Container today:** `nix: → oci-archive` (skopeo compresses by default) | write 7.0 / 13.4 / 10.8 s, **1,223,953,408 B** (gzip) | the same full write again |

**Bytes crossing the API socket (MEASURED, `strace` of the remote client).** `podman --remote
load -i b.tar` into a store already holding A wrote **3,448.2 MB**, the whole 3,447,376,384-byte
archive. The load is quick here (2.8 s) because the service skips writing layers it has, but only
after the entire file has been uploaded and spooled to `/var/tmp` on the service side. On a Mac,
that upload is what crosses into the VM.

**For comparison, a full gzip `oci-archive` of A loaded through `podman --remote load` into an
empty store took 37.2 s** (MEASURED). It is 2.8× fewer bytes to upload, paid for with CPU time.

### Option 1 — OCI layout on a shared path, then `podman pull oci:`

**Writing the layout: does it dedupe across copies? Only when uncompressed (MEASURED).**

| Layout write | A (empty layout) | B (layout holding A) | Layout growth for B |
| :--- | :--- | :--- | :--- |
| `skopeo copy nix: oci:<dir>:<ref>` (default, gzip) | 5.1 s, 1.22 GB | **12.75 s, all 92 blobs recompressed, 0 reused** | 0 (deterministic gzip rewrote identical files) |
| the same with `--dest-oci-accept-uncompressed-layers` | 6.1 / 7.6 s, 3,446,725,782 B | **0.79 / 0.87 s** | 27,804,962 B (the two new layers, a manifest and a config) |

Why the gzip layout reuses nothing (SOURCED, containers/image as vendored in podman v5.8.6,
`oci/layout/oci_dest.go`, `TryReusingBlobWithOptions`): the OCI layout destination reuses a blob
only if a file with **that exact digest** exists, and it checks with a bare `os.Stat`. The
source digest is the uncompressed one, and the layout holds the gzip one, so nothing matches.
Compare the registry push [below](#option-2--a-local-read-only-registry), which does find the
gzip copies through skopeo's blob-info cache.

**`podman pull oci:` from the remote client is refused (MEASURED).** The error was:

```text
Error: unsupported transport oci in "oci:/tmp/lr/L1:a": only docker transport is supported
```

The refusal is on the **server** side (SOURCED, podman v5.8.6
`pkg/api/handlers/utils/images.go`, `IsRegistryReference`). The API's pull endpoint accepts only
`docker://` references. So the question of which side resolves the path never comes up: the
Mac's `podman pull oci:…` fails whatever the path is.

**Run inside the VM instead, podman is local, and the pull reuses layers (MEASURED as a local
pull).**

| `podman pull oci:<layout>:<ref>` (local, not `--remote`) | A (empty store) | B (store holding A) |
| :--- | :--- | :--- |
| time | 25.9 / 17.5 s | **0.80 / 0.70 s** |
| bytes read, all file descriptors (`strace`) | — | 83.8 MB |

On a Mac that means `podman machine ssh -- podman pull oci:$HOME/…`. The path resolves inside the
VM, and Podman Machine mounts `/Users`, `/private` and `/var/folders` at the same paths by
default (SOURCED, podman v5.8.6 vendored `go.podman.io/common/pkg/config/default_darwin.go`,
`getDefaultMachineVolumes`). `podman machine ssh` logs in as `root` on a rootful machine and as the
machine's remote user otherwise (SOURCED, `cmd/podman/machine/ssh.go`), so it reaches the same
store the default connection uses. That only holds if yolo's connection is the default machine.

**Verdict: rejected as the primary route.** It works only through an SSH hop into one machine
chosen by name, and it is podman-only. What it does offer is a *first* load read over virtiofs
rather than uploaded over the API socket, which may be faster. That is only measurable on a Mac,
so it stays [open](#OQ-LR2).

### Option 2 — a local read-only registry

**yolo's pull-only handler over the uncompressed layout above, bound to this host's LAN address
`192.168.1.145` (non-loopback), then `podman --remote pull --tls-verify=false` (MEASURED):**

| Pull | Time | Bytes the registry served |
| :--- | :--- | :--- |
| A into an empty store | 29.3 / 22.2 s | 3,446,725,454 B in 93 responses |
| B into a store holding A | **1.90 / 0.70 s** | **27,804,751 B in 4 responses**: the manifest, the config and the two new layers |

The reuse is visible only on the server side. In non-TTY mode podman prints `Copying blob …` for
every layer and never `skipped: already exists`. So the byte count from the registry is the
evidence here, not podman's output.

**Plain HTTP needs the flag even on loopback (MEASURED).** Without `--tls-verify=false`,
`127.0.0.1:5057`, `localhost:5057` and `192.168.1.145:5057` all fail the same way:

```text
pinging container registry …: Get "https://…/v2/": http: server gave HTTP response to HTTPS client
```

containers/image does not treat `localhost` as insecure. The flag applies to that one pull. It
changes nothing in `registries.conf`.

**Pulling by digest works, and the name has to be fixed up afterwards (MEASURED).**

- `pull …/yolo-jail@sha256:c7382a44…` took 24.3 s cold.
- The image arrives named `127.0.0.1:5063/yolo-jail`, with no tag. A
  `podman tag <id> localhost/yolo-jail:<key>` took 0.03 s.
- The image ID is the config digest, `41685f1d…`, the same as through every other transport.
  So the content-addressed ref yolo already uses ([The content-addressed image
  ref](../reference/image-staging-vs-baking.md#the-content-addressed-image-ref)) still names the
  right image.

**`nixpkgs#distribution` 3.1.1 on loopback (MEASURED):**

| Step | A | B (registry and store holding A) |
| :--- | :--- | :--- |
| `skopeo copy nix: docker://127.0.0.1:5058/…` (the push compresses) | 5.7 s, 1.22 GB stored | **1.0 s**: reused through skopeo's blob-info cache, and the registry grew by 1.0 MB |
| `podman --remote pull --tls-verify=false` | 26.9 s | **0.69 s**, 3 blob GETs |

**How the VM and Apple Container would reach a registry on the Mac's loopback (SOURCED):**

- **Podman Machine.** gvproxy NATs its host address, `192.168.127.254` on the default subnet, to
  the Mac's `127.0.0.1` (gvisor-tap-vsock `fc319b8`, `cmd/gvproxy/config.go`: `config.Stack.NAT =
  {HostIP: "127.0.0.1"}`). Its own tests reach a host service from inside the VM as
  `http://host.containers.internal:9090` (`test-qemu/port_forwarding_test.go`). So a registry
  bound to `127.0.0.1` on the Mac is reachable from the VM. It never needs a LAN address.
- **Apple Container.** `container image pull` sends the request over XPC to the images service,
  which fetches blobs itself. That service runs on the Mac, not in a VM (SOURCED, apple/container
  `eafe6b8`, `ClientImage.pull`, and the images service's `ImageStore`). So a registry on the
  Mac's loopback is local to the process doing the pull.
  - Plain HTTP is chosen per pull with `--scheme http`. At `eafe6b8` the flag accepts only `http`
    or `https`, and `RequestScheme.schemeFor` returns what was asked for.
  - The pull skips every blob already in the content store: `ImportOperation.fetch` calls
    `contentStore.get(digest)` first and copies the stored file (containerization `bc994b8`,
    `ImageStore+Import.swift`).
  - Uncompressed layers (`application/vnd.oci.image.layer.v1.tar`) unpack fine
    (`EXT4Unpacker.compressionFilter`).

**Verdict: works, and shortlisted as the fallback.** The listener it adds is the cost.
`distribution` would also be a second binary to ship, keep a second persistent store and push
twice. The in-process handler needs neither and is small. But both are beaten by the delta
archive below, which needs no listener at all.

### Option 3 — a delta archive through the existing loaders

This option came out of reading the loaders' source, and it is the finding of this round.

**Podman.** `podman load` of an **oci-archive** extracts it and copies from the resulting layout.
The copy checks with the destination before it reads each layer, so **a layer the store already
has is never read from the archive**. The archive does not need to contain it (MEASURED below).

A **docker-archive** cannot do this. Its reader rejects a tarball that is missing a layer file
before any copy starts: `"Some layer tarfiles are missing in the tarball"` (SOURCED,
`docker/internal/tarfile/src.go`, `prepareLayerData`).

**The pipeline measured, against a remote store holding A, three iterations (MEASURED):**

1. **Probe the present set.** `podman images -q` followed by `podman image inspect --format
   '{{range .RootFS.Layers}}…'`. These are the two argv lines `PresentLayerDigests` already runs.
   nix2container layers are uncompressed, so a layer's diffID *is* its blob digest.
2. **Seed placeholders**, zero-byte files, for those 91 digests. Then run
   `skopeo copy --dest-oci-accept-uncompressed-layers nix:<B> oci:<dir>:yolo-jail:<key>`. Delete
   the placeholders, then `tar -C <dir> -cf delta.tar .`.
3. **Load it** with `podman --remote load -i delta.tar`.

| Iteration | Probe | Build the delta | Load | Total | Delta size |
| :--- | :--- | :--- | :--- | :--- | :--- |
| 1 | 0.03 s | 1.17 s | 0.78 s | **1.99 s** | 27,811,840 B |
| 2 | 0.04 s | 0.87 s | 0.93 s | **1.83 s** | 27,811,840 B |
| 3 | 0.03 s | 0.89 s | 0.91 s | **1.84 s** | 27,811,840 B |
| today's docker-archive, same store state | — | 8.89 / 6.47 s | 2.84 / 2.72 s | **11.72 / 9.19 s** | 3,447,376,384 B |

The loaded image is correct, and the result is well-defined in every case (all MEASURED):

- The image loads as `localhost/yolo-jail:<key>`, named from the layout's ref annotation. So
  the image still gets its name on the way in, as the content-addressed ref requires.
- It has the same image ID, and `podman run … hello` printed `Hello, world!`.
- The seeded layout's manifest digest (`c7382a44…`) is **identical** to the full layout's: skopeo
  records the source's layer sizes, not the placeholders' zero.
- **Fail-closed:**
  - A delta archive loaded into an **empty** store fails with `reading blob sha256:2a72b533…: …
    no such file or directory`, and **no image is written**.
  - Pulling the seeded layout into an empty store over the registry, with the zero-byte
    placeholders left in, fails with `Digest did not match, expected sha256:2a72b533…, got
    sha256:e3b0c442…` (the empty file's digest). The bytes are checked against the digest.

**Apple Container (SOURCED, not measured).** `container image load -i` extracts the tar and then
imports the directory as a layout (apple/container `eafe6b8`, `ImagesService.load`, then
`ImageStore.load(from:)`). The import goes through the same `ImportOperation.fetch`, which takes
a blob from the content store before it touches the layout (containerization `bc994b8`). So an
archive missing blobs the store already holds should import. Two things are Mac-only:

- Whether `LocalOCILayoutClient` complains about the missing files before `fetch` is reached.
- How to probe AC's present set. See [OQ-LR3](#OQ-LR3).

**Verdict: recommended.** See [below](#recommendation-and-its-cost).

### Other routes considered

| Route | Disposition |
| :--- | :--- |
| **A lazy registry inside yolo that tars layers straight from the nix store**, with no layout on disk | **Deferred.** It is the minimum in bytes, but it needs nix2container's layer writer as a Go dependency, which [`layercopy.go`](../../internal/image/layercopy.go) deliberately avoids. It also has to reproduce, byte for byte, the tar whose digest was computed at build time. The delta archive gets the same bytes-on-the-wire from the existing copier. |
| **A persistent uncompressed layout on the Mac** (option 1's layout kept between loads) | **Not needed.** It costs about 3.45 GB of Mac disk and needs its own GC. Placeholder seeding makes the layout transient and only 28 MB. |
| **The full layout written each time, with the present blobs deleted before tarring** | **The robust variant.** It gives the same delta archive without relying on the destination's `os.Stat`-only reuse check, at the cost of a full local write (6.1–7.6 s here, measured as the A write). Take it if pinning the placeholder behavior with a test proves too fragile. |
| **`podman machine ssh` running a Linux `skopeo copy nix: → containers-storage:` inside the VM** | **Only where `/nix` is shared with the VM**: `podman machine init -v /nix:/nix`, a fresh machine, which the macOS nightly already does ([macOS and the runtime VM](../reference/image-staging-vs-baking.md#macos-and-the-runtime-vm)). It is exactly the Linux path, reading the store over virtiofs with no archive at all. It needs a Linux copier binary visible to the VM, and default machines don't have one. A candidate for CI's first load; [OQ-LR2](#OQ-LR2). |
| **A containers-storage store on a shared mount** (`additionalimagestores`) | **Rejected (INFERRED).** The host copy would have to write an overlay store from macOS, and c/storage's overlay driver and its ownership mapping are Linux-only. |
| **Writing blobs directly into Apple Container's content store** | **Rejected.** It is an internal on-disk layout with no stability promise, and `load` and `pull` already skip what it holds. |
| **Compressing the podman archive** (gzip `oci-archive` instead of `docker-archive`) | **A first-load lever, and orthogonal to reuse.** 2.8× fewer bytes to upload for about 7–13 s of gzip here (MEASURED above). Worth measuring on the Intel CI Mac, where first load is the only load. |

---

## Apple Container: the unpack floor

**Reuse cannot remove this step (SOURCED).** After a load or a pull, the CLI calls
`image.unpack`. `SnapshotStore.unpack` skips a manifest whose snapshot directory already exists.
Otherwise it runs `EXT4Unpacker`, which creates a new `EXT4.Formatter` and unpacks **every layer
in `manifest.layers`**, bottom to top, into one block file (apple/container `eafe6b8`
`SnapshotStore.swift`; containerization `bc994b8` `EXT4Unpacker.swift`). A `packages:` change
makes a new manifest, so it gets a new snapshot built from all of its layers, about 3.45 GB here.
No step starts from the previous image's snapshot.

So on Apple Container, the most layer reuse can save is:

- skopeo's full archive write, gzip by default (7–13 s here for 1.22 GB);
- the service's full extraction of that tar into a temp directory;
- ingesting the blobs it already has.

The ext4 build stays. Whether that turns 22 s into 5 s or into 18 s is the first thing to
measure on the Mac ([commands](#apple-container)).

> [!NOTE]
> The [`Justfile`](../../Justfile) `load` recipe says Apple Container's "VM owns its store". Per
> the source above, the content store and the snapshots are on the Mac, managed by the images
> service. What runs in a VM is the container, which mounts the snapshot. This makes no
> difference to today's archive path, but it is why a loopback registry or a delta archive
> needs no network hop there.

---

## Security notes

- **A loopback registry is reachable by everything local for as long as it is up.**
  - On the Mac that means every process of every user.
  - On Podman Machine, it also means every container in the VM, **running jails included**,
    through `host.containers.internal`. gvproxy maps that name to the Mac's loopback, and that
    mapping is exactly what makes the registry reachable from the VM in the first place.
  - What a reader gets is the jail image, which comes from `/nix/store`, a world-readable tree
    on the Mac. So confidentiality loses almost nothing. The one new reader is a jail on Podman
    Machine, which does not mount `/nix`. For read-only content, the lifetime is the only thing
    to keep short: bind port `0`, serve for the one pull, then close.
  - The LAN-address test above ran for a few seconds on this host's LAN interface. A real
    implementation binds `127.0.0.1` only.
- **Integrity comes from digests, and only if the pull names one.** Every blob is checked
  against its digest on arrival (MEASURED above: `Digest did not match`). But a pull **by tag**
  trusts whatever manifest the port returns. A local process that bound the port first, or took
  it over between launches, could serve a different manifest under the tag. **Pull by digest**
  closes that gap. yolo computes the manifest digest locally when it writes the layout (it is in
  `index.json`), so the whole tree is then checked back to a value yolo produced itself.
  Measured working above (`…/yolo-jail@sha256:c7382a44…`).
- **The delta archive opens no listener.** It adds no exposure beyond today's archive: a
  temporary file in the image cache directory, handed to the same loader.
  - **An over-claimed present set fails closed.** That happens when a layer is pruned between
    the probe and the load. The loader then reports a missing blob and **writes no image**
    (MEASURED).
  - **An under-claimed present set costs only bytes.**
  - A zero-byte placeholder can never reach a store. Even if one were left in, it could not
    pass a digest check (MEASURED: `got sha256:e3b0c442…`).
- **The layout directory is user-writable**, like today's archive. A same-user process could
  swap a blob, and the digest check turns that into a failed load, not a substituted image.

---

## Recommendation and its cost

**Adopt the delta archive on both Mac backends, as a parameter of the mechanism that exists, not
as a second mechanism.** `deliverViaArchive` would:

1. read the present set (for podman, `PresentLayerDigests` as it stands, moved from report-only
   to load-bearing);
2. seed placeholders into a temporary OCI layout;
3. run `skopeo copy --dest-oci-accept-uncompressed-layers nix:<image.json> oci:<dir>:<ref>`;
4. delete the placeholders and tar the layout;
5. call the loader it calls today.

**"Nothing is present" produces exactly today's full archive.** So the existing "retry once"
([`copyImageWithRetry`](../../internal/image/layercopy.go)) becomes "retry once with an empty
present set" when the loader reports a missing blob. That is the same argv with the same loader,
the shape [One mechanism, no way back](../reference/image-staging-vs-baking.md#one-mechanism-no-way-back)
allows, and not a fallback transport.

**What it buys:**

- MEASURED on Linux: incremental delivery **9.2–11.7 s → 1.8–2.0 s**, and the archive
  **3.45 GB → 27.8 MB**.
- INFERRED for podman-on-macOS: the 3.45 GB upload into the VM, and the VM's spool of it to
  `/var/tmp`, both go away on every load after the first.
- INFERRED for Apple Container: the archive write, the extraction and the ingest go away, and
  the ext4 unpack stays.

**What it costs (INFERRED estimate):**

- A few hundred lines in `internal/image`: layout seeding, `archive/tar` from the standard
  library, and recognizing the loader's missing-blob error.
- Unit tests, including **one that fails if containers/image stops treating a bare file as a
  present blob**. The placeholder trick depends on that `os.Stat` check. If it breaks, the
  robust variant from the table above replaces it.
- An Apple Container present-set probe ([OQ-LR3](#OQ-LR3)).

**No new binary, no listener, and no change to the image or the flake.** It is testable end to
end on Linux against `podman --remote`, as done here, because that is where the podman half
lives. The Apple Container half needs the Mac, like every AC change.

**What it does not buy:** anything on the first load, so nothing for CI's ephemeral macOS
runners, and the ext4 unpack on Apple Container. The registry (option 2) stays shortlisted, in
case the Mac shows that `container image load` refuses a layout with missing blobs.

---

## What only a Mac can confirm

The scripts below build the same A/B pair, then time today's path and the delta path. Run them
from the checkout, with no jail running on the backend being measured. Every path is under
`$HOME`, which Podman Machine shares with the VM.

### Setup (both backends)

```bash
cd ~/code/yolo-jail
W=$HOME/yolo-lr && rm -rf "$W" && mkdir -p "$W"
nix build --impure .#imageCopier -o "$W/copier"; SK="$W/copier/bin/skopeo"
YOLO_EXTRA_PACKAGES=            nix build --impure .#ociImage -o "$W/imgA"
YOLO_EXTRA_PACKAGES='["hello"]' nix build --impure .#ociImage -o "$W/imgB"
A=$(readlink "$W/imgA"); B=$(readlink "$W/imgB")
# delta <image.json> <present-digests-file> <ref> <out.tar>
delta() { rm -rf "$W/D"; mkdir -p "$W/D/blobs/sha256"
  sed 's/^sha256://' "$2" | while read -r h; do : > "$W/D/blobs/sha256/$h"; done
  "$SK" copy -q --insecure-policy --dest-oci-accept-uncompressed-layers "nix:$1" "oci:$W/D:$3"
  find "$W/D/blobs/sha256" -size 0 -delete; tar -C "$W/D" -cf "$4" .; ls -l "$4"; }
```

### Podman Machine

```bash
podman machine inspect --format '{{.Name}} {{.VMType}} rootful={{.Rootful}}'
podman info --format 'rootless={{.Host.Security.Rootless}}'
podman rmi -f yolo-jail:lr-a yolo-jail:lr-b 2>/dev/null
# 1. today's path, first and incremental
time "$SK" copy -q --insecure-policy "nix:$A" "docker-archive:$W/a.tar:yolo-jail:lr-a"
time podman load -q -i "$W/a.tar"; rm "$W/a.tar"
time "$SK" copy -q --insecure-policy "nix:$B" "docker-archive:$W/b.tar:yolo-jail:lr-b"
time podman load -q -i "$W/b.tar"; rm "$W/b.tar"; podman rmi yolo-jail:lr-b
# 2. the delta archive (store holds A)
podman image inspect --format '{{range .RootFS.Layers}}{{println .}}{{end}}' yolo-jail:lr-a | grep '^sha256:' > "$W/present.txt"
time delta "$B" "$W/present.txt" yolo-jail:lr-b "$W/d.tar"
time podman load -q -i "$W/d.tar"
podman run --rm yolo-jail:lr-b hello; podman rmi yolo-jail:lr-b
# 3. registry on the Mac loopback, pulled from the VM through gvproxy
printf 'version: 0.1\nstorage: {filesystem: {rootdirectory: %s/reg}}\nhttp: {addr: 127.0.0.1:5000}\n' "$W" > "$W/reg.yml"
nix shell nixpkgs#distribution -c registry serve "$W/reg.yml" & RP=$!; sleep 2
"$SK" copy -q --insecure-policy --dest-tls-verify=false "nix:$A" docker://127.0.0.1:5000/yolo-jail:lr-a
"$SK" copy -q --insecure-policy --dest-tls-verify=false "nix:$B" docker://127.0.0.1:5000/yolo-jail:lr-b
time podman pull -q --tls-verify=false host.containers.internal:5000/yolo-jail:lr-b   # store holds A
kill $RP
# 4. first load read over virtiofs instead of uploaded (option 1 from inside the VM)
podman rmi -f yolo-jail:lr-a yolo-jail:lr-b host.containers.internal:5000/yolo-jail:lr-b
"$SK" copy -q --insecure-policy --dest-oci-accept-uncompressed-layers "nix:$A" "oci:$W/L:lr-a"
time podman machine ssh -- podman pull -q "oci:$W/L:lr-a"      # machine ssh logs in as root on a rootful machine, so this hits the default connection's store
```

What each step settles:

- **Step 1 against step 2** shows how much of today's incremental cost the upload is: the
  headline number.
- **Step 3** confirms that `host.containers.internal` from the VM reaches the Mac's loopback.
- **Step 4 against step 1's first load** compares virtiofs with the API upload for a cold
  store ([OQ-LR2](#OQ-LR2)).

On the Intel CI runner, step 1's first load is the 10–33 min figure. Timing the gzip variant
there as well (`oci-archive:` instead of `docker-archive:`) answers whether compression helps
the first load.

### Apple Container

```bash
container --version; container image pull --help | grep -A1 -i scheme
container image rm yolo-jail:lr-a yolo-jail:lr-b 2>/dev/null   # the CLI's rm also drops orphaned blobs and snapshots (eafe6b8), so each step starts from what it says
# 1. today's path: skopeo write, then load (the load includes the ext4 unpack)
time "$SK" copy -q --insecure-policy "nix:$A" "oci-archive:$W/a.oci:yolo-jail:lr-a"
time container image load -i "$W/a.oci"; rm "$W/a.oci"
time "$SK" copy -q --insecure-policy "nix:$B" "oci-archive:$W/b.oci:yolo-jail:lr-b"
time container image load -i "$W/b.oci"; rm "$W/b.oci"; container image rm yolo-jail:lr-b
# 2. the delta archive (content store holds A). Present set = A's layers, known from its image.json.
python3 -c "import json,sys;[print(l['digest']) for l in json.load(open(sys.argv[1]))['layers']]" "$A" > "$W/present.txt"
time delta "$B" "$W/present.txt" yolo-jail:lr-b "$W/d.tar"
time container image load -i "$W/d.tar"      # does it import? how long is "Unpacking image"?
container run --rm yolo-jail:lr-b hello; container image rm yolo-jail:lr-b
# 3. what a present-set probe could read
container image inspect yolo-jail:lr-a | head -60
```

What each step settles:

- **Step 2 answers the open import question**: whether a layout with missing blobs imports at
  all.
- Its load time is roughly the unpack floor, because the ingest is only 28 MB. That splits the
  22 s.
- If step 2 is refused, run step 3 of the podman list with `container image pull --scheme http
  127.0.0.1:5000/yolo-jail:lr-b` in place of the `podman pull`. That tests the registry
  fallback, which reuses blobs by the same `contentStore.get` check.

---

## Open questions

1. 💬 **OQ-LR1: Build the delta archive for the two Mac backends?** It is the only option that
   reuses layers with no listener and no new binary. It helps the maintainer's day-to-day Mac and
   does nothing for CI's ephemeral runners.

   <!-- vantage: oq id=OQ-LR1 leaning="Yes, after the Mac run: build it in deliverViaArchive as a present-set parameter (empty set = today's archive, retry once with it), if the podman step-1 vs step-2 run shows the upload dominates and Apple Container imports a layout with missing blobs." -->

   _Leaning:_ Yes, once the Mac commands above confirm two things. First, that on Podman Machine
   the upload is most of today's incremental cost. Second, that `container image load` imports a
   layout with missing blobs. If Apple Container refuses, ship the podman half and take the
   registry route for Apple Container.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-LR2: Is the first load worth its own work?** On CI's ephemeral Intel runners, reuse
   cannot help. The candidates are:
   - a gzip archive (2.8× fewer bytes, CPU paid);
   - `podman machine ssh` reading over virtiofs;
   - on machines with `/nix` shared, a Linux copier running inside the VM, which is exactly the
     Linux path.

   <!-- vantage: oq id=OQ-LR2 leaning="Measure first: time the gzip archive and the machine-ssh virtiofs pull on the Intel CI runner; only the /nix-shared in-VM copier is worth building, and only if it is several times faster." -->

   _Leaning:_ Measure before building. The in-VM copier is the only candidate that removes the
   archive entirely, and CI already shares `/nix`. Every other candidate is a constant-factor
   change to a cost CI pays once per job.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-LR3: What is Apple Container's present-set probe?** Podman has one:
   `PresentLayerDigests`. For Apple Container the choice is `container image inspect`, if it
   lists layer digests, or yolo's own record of which image.json it last delivered, confirmed by
   `container image list`.

   <!-- vantage: oq id=OQ-LR3 leaning="Use yolo's own record of delivered image.json files, gated on the ref still being listed, because it needs no AC output format and an over-claim fails closed anyway." -->

   _Leaning:_ yolo's own record. It depends on no Apple Container output format, and a wrong
   answer only costs one retry with an empty set.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Fast-moving — verify before building

Re-check these; each is pinned to a version or a commit that will move:

- **podman 5.8.6:**
  - the remote pull endpoint's refusal of non-`docker` transports;
  - the default darwin machine volumes (`/Users`, `/private`, `/var/folders`);
  - `podman machine ssh`'s user choice;
  - gvproxy forwarding the API socket over SSH.
- **containers/image as vendored in podman 5.8.6 and in skopeo 1.24.0:**
  - `oci_dest.go`'s `os.Stat`-only reuse check, which the placeholder seeding depends on;
  - the oci-archive source reading layers lazily;
  - the docker-archive reader requiring every layer file;
  - `--dest-oci-accept-uncompressed-layers`.
- **gvisor-tap-vsock `fc319b8`:** the default NAT of the host IP to `127.0.0.1`.
- **apple/container `eafe6b8` and containerization `bc994b8`:**
  - `--scheme http|https` on `image pull`;
  - both import paths skipping blobs held in the content store;
  - `load` extracting the full tar first;
  - a fresh ext4 snapshot per image from all layers;
  - uncompressed layers accepted.

## Sources

- [OCI Image Layout spec](https://github.com/opencontainers/image-spec/blob/main/image-layout.md): the `oci:` directory the delta archive is made of (read 2026-09-24).
- [OCI Distribution spec](https://github.com/opencontainers/distribution-spec/blob/main/spec.md): the three pull routes a read-only registry needs (read 2026-09-24).
- [containers/podman v5.8.6](https://github.com/containers/podman/tree/v5.8.6): the remote pull refusal, machine volumes and SSH user, the gvproxy API forwarding, and the vendored containers/image destinations and sources quoted above (read from source 2026-09-24).
- [containers/gvisor-tap-vsock](https://github.com/containers/gvisor-tap-vsock): the default host-IP-to-loopback NAT and the `host.containers.internal` test (read from source at `fc319b8`, 2026-09-24).
- [apple/container](https://github.com/apple/container): `image pull --scheme`, `image load`'s extract-then-import, and the per-image ext4 snapshot (read from source at `eafe6b8`, 2026-09-24).
- [apple/containerization](https://github.com/apple/containerization): `ImportOperation.fetch`'s content-store check, and `EXT4Unpacker` unpacking every layer (read from source at `bc994b8`, 2026-09-24).
- [distribution/distribution v3.1.1](https://github.com/distribution/distribution/tree/v3.1.1): the reference registry measured as option 2's packaged variant (`nixpkgs#distribution`).
- [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md): today's delivery design, the archive destinations this doc proposes to change, and the one-mechanism rule the proposal is shaped to.
- [`layercopy.go`](../../internal/image/layercopy.go): `PresentLayerDigests`, the copy report, and the retry the delta would reuse.
