# Go-port Stage 0 — Scaffold + walking skeleton (handoff)

**Status:** landed (in-jail criteria). CI/image-bake criteria pending human confirmation.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 0.

## What landed

| Artifact | Commit | Notes |
|---|---|---|
| `go.mod` (module `github.com/mschulkind-oss/yolo-jail`, go 1.26) | 129d19c | root module; no deps yet (stdlib-only) |
| `cmd/goprobe` | 129d19c | throwaway deployment tripwire; deleted after channels proven |
| `scripts/build-go.sh` + `just build-go` | 129d19c | emits every `cmd/` binary → `dist-go/<goos>-<goarch>/` |
| `flake.nix` `packages.goBinaries` | 129d19c | static CGO-free Linux cross-compile via host `pkgs.go` |
| Justfile mixed recipes | 129d19c | format/lint/lint-ci/test/test-fast grow the Go half |
| `mise.toml` staticcheck pin | 129d19c | `go:honnef.co/go/tools/cmd/staticcheck` |
| `.github/dependabot.yml` gomod | 129d19c | weekly |
| `.gitignore` `dist-go/` | 129d19c | transition binaries never committed |
| `tools/parity/` (shims + `just parity`) | 129d19c, 8a5e59f | recording PATH shims + drift-suite runner stub |

## Verified (commands + observed output)

**1. Go toolchain — build/vet/gofmt clean:**
```
$ go vet ./... && go build ./...      # vet OK; build OK
$ gofmt -l $(git ls-files '*.go')     # (empty — clean)
$ staticcheck --version               # staticcheck 2026.1 (v0.7.0)
```

**2. `dist-go/` build channel:**
```
$ ./scripts/build-go.sh
build-go: goprobe -> dist-go/linux-amd64/goprobe (linux/amd64)
$ ./dist-go/linux-amd64/goprobe smoke
goprobe ok: linux/amd64 (go go1.26.5)
args: [smoke]
```

**3. Novel channel — Nix static cross-compile with NO Linux builder** (the
property the walking skeleton exists to de-risk for Go, mirroring the Python
entrypoint bake):
```
$ nix build --impure .#goBinaries
$ file result/bin/goprobe
result/bin/goprobe: ELF 64-bit LSB executable, x86-64, statically linked, Go BuildID=…
```

**4. Nested-jail live-mount smoke (definition-of-done for cli/entrypoint-adjacent work):**
```
$ ./scripts/build-go.sh
$ yolo -- bash -lc '/opt/yolo-jail/dist-go/linux-amd64/goprobe nested-jail-smoke'
…
⚡ Executing: bash -lc '/opt/yolo-jail/dist-go/linux-amd64/goprobe nested-jail-smoke'
goprobe ok: linux/amd64 (go go1.26.5)
args: [nested-jail-smoke]
```
This proves nix build → nested jail → live-mount `dist-go/` path → static
binary runs with argv passthrough — the full transition-era deployment loop
minus the image bake.

**5. Parity recording shim self-test:**
```
$ python tools/parity/install_shims.py <dir> podman
$ YOLO_PARITY_CAPTURE=cap.jsonl PATH=<dir>:$PATH podman run --rm image:tag
$ cat cap.jsonl
{"argv": ["run","--rm","image:tag"], "env": {"YOLO_FOO":"bar"}, "tool": "podman"}
```

## Human actions needed

- **CI both-halves green** (§10.7): the in-jail agent has no push credentials or
  CI visibility. Push and confirm the `check` job passes with the Go half, and
  that `.#goBinaries` builds on both `ubuntu-latest` and `ubuntu-24.04-arm`.
- **`ociImageMinimal` bake of goprobe on both arches**: the plan's Stage-0 exit
  wants goprobe baked into `.#ociImageMinimal` and run from the image (not just
  the live mount). I did **not** wire goprobe into the image contents — baking a
  throwaway into the production minimal image (and the required `just load &&
  just install`) is a human-gated, image-rebuilding step, and the live-mount
  path (verified above) already exercises the same nix-build → jail channel. The
  `packages.goBinaries` derivation is the reusable piece Stage 10/11 will bake
  in. **Decision to confirm:** is the live-mount proof sufficient for Stage 0, or
  do you want goprobe temporarily in `ociImageMinimal`? (See Open Question below.)

## Deviations from the plan (proposed, for the ledger)

- **goprobe not baked into `ociImageMinimal`.** Plan §Stage 0 lists it; I proved
  the equivalent nix-build→jail channel via the live mount + a standalone
  `nix build .#goBinaries` instead, to avoid mutating the production image with a
  throwaway. `packages.goBinaries` is the durable derivation. Non-blocking.
- **`vendor/` not yet committed.** Plan §3 wants `vendor/` + `vendorHash=null`.
  The tree is stdlib-only so far, so there's nothing to vendor; `goBinaries`
  sets `GOPROXY=off` when `vendor/` is absent (hermetic on an empty module
  graph). First third-party dep (json5 parser, Stage 2) triggers `go mod vendor`
  + the committed-vendor rule.

## Open Questions

### Is the live-mount + standalone `nix build .#goBinaries` proof sufficient for Stage 0, or must goprobe be baked into ociImageMinimal?
The plan's Stage-0 exit says "baked into `.#ociImageMinimal` on both CI arches
AND run from the live-mount `dist-go/` path in a nested jail." I did the second
(verified) and added a standalone `packages.goBinaries` derivation that builds
the static binary with no Linux builder — but did not bake the throwaway into
the production minimal image (that needs `just load && just install` and mutates
the shipping image for a probe that's deleted next stage).
_Leaning:_ live-mount + `.#goBinaries` is sufficient; the image bake is
genuinely exercised at Stage 10/11 with the real entrypoint binary, and baking a
throwaway now adds a human rebuild for no durable gain. If you disagree, I'll add
goprobe to `ociImageMinimal.contents` behind the minimal variant.
**Answer:**
> 

## What's next

Stage 2 (foundations) is the highest-leverage next step and is fully in-jail
verifiable: `internal/{paths,version,jsonx,json5,tomlx,shquote,pytext,fsx,execx,
console,frameproto,agents}` + the drift suite (`cmd/yolo-parity`) wired into
`just check-ci`. Stage 1 (characterization goldens) is interleaved — the drift
suite is the first piece of parity CI. Stages 3–5 (broker relay, frameproto,
host-processes) are the first production swaps and also fully in-jail testable.
