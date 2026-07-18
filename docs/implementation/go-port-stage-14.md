# Go-port Stage 14 — Command-slice pure cores (handoff)

**Status:** pure cores landed for the runtime / storage / image / builder /
network / darwin-packages / container-builder slices (prune landed earlier).
The command *wiring* (front-door native dispatch, ok/warn/fail plumbing, the
loopholes registry, subprocess orchestration) is still delegated to Python —
this stage delivers the byte-exact engines Stages 15 (`check`) and 16 (`run`)
consume, exactly as Stage 13 delivered the config library.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 14.
**Built on:** `internal/jsonx`, `internal/paths`, `internal/agents`,
`internal/tomlx`, `internal/pytext`.

## What landed

Each slice is a pure/file-testable library with a `TestParityVsLivePython`
(or equivalent) that byte-diffs against the LIVE Python and SKIPs when
python3/uv is absent. Commits (main), newest first:

- `2a72352` feat(go): runtime BakedYoloVersionFromInspectEnv (version.py parity)
- `b0f24dd` feat(go): prune FmtBytes — human-readable byte formatter
- `f83570c` feat(go): macos container builder — internal/containerbuilder
- `452959c` feat(go): macos-user darwin packages — internal/darwinpkg
- `6091b9e` feat(go): Stage 14/16 socat port-forward argv — internal/network
- `c8509f9` feat(go): Stage 14 image pipeline core — internal/image
- `326abbe` feat(go): Stage 14 storage slice — internal/storage
- `b9334db` feat(go): Stage 14 runtime probe layer — internal/runtime
- `be14acd` feat(go): Stage 14 builder slice — internal/builder
- `bd0de13` feat(go): Stage 14 prune slice — internal/prune (earlier session)

## Slices

- **internal/runtime** — `podman ps` / Apple-Container `container ls` table
  parsers, the None-vs-empty **live-set tri-state** (`LiveSet{Known,Names}` —
  liveness-unknown must never read as "nothing live", same polarity as
  prune.ReferencedSet), the `ps` display table (rune-width padded), the
  stuck-container `top` analyzer, `BakedYoloVersionFromInspectEnv` /
  `WorkspaceFromInspectEnv` (note: version STRIPS + empty→absent; workspace is
  verbatim), and the podman-machine resize hint.
- **internal/storage** — host probes (`LinuxMultilib`, `NixCustomConfIncluded`,
  `DetectNixDaemonLabel`, `DetectHostTimezone`), the **claude.json two-way
  login-state sync** (order-preserving via jsonx OrderedMap; forward fill +
  allowlisted reverse), and `EnsureGlobalStorage` / `EnsureSymlink` /
  `MigrateStorageLayout` (tri-state liveness-gated, marker-stamped).
- **internal/image** — command builders, the nix-stderr summarizer, the
  byte-progress formatter, the per-runtime load **sentinel LRU** (10-entry
  move-to-end), the sha256-keyed cache path, the `/etc/nix/machines` builder
  selection, and the **preserved size-file quirk** (the reader path
  `last-load-size-size` never meets the writer's `last-load-size` — faithfully
  reproduced, documented on `SizeFileForSentinel`, NOT fixed).
- **internal/builder** — the ssh_config block, nix builders line, trusted-users
  merge, and the single-sudo root script (Mac-builder-runbook-critical strings).
- **internal/network** — `_parse_port_forwards` (int / "n" / "n:m" + the
  invalid-entry warning; non-numeric aborts like Python's ValueError) and the
  **byte-identical socat `UNIX-LISTEN→TCP` argv** (Stage 16 requires Go spawn
  the same argv).
- **internal/darwinpkg** — the macos-user firm requirement: nix buildEnv argv,
  the `YOLO_EXTRA_PACKAGES` env contract (compact JSON), `profile_paths`
  (bin + optional PKG_CONFIG_PATH), flake.lock rev read, skip-list parse.
- **internal/containerbuilder** — the on-demand Linux builder pull/run argv
  (podman `-p` vs Apple Container omit), ssh-ng URI, the nix `--builders` line,
  and the `container ls` VM-IP address parse.

## Stage 15 / 16 down payments (landed alongside)

- **internal/checkdiag** (Stage 15) — nix build-failure classifier
  (`_diagnose_nix_build_failure`, isMacOS injected), the dry-run **will-build
  tri-state** parse + offending drv basenames, the `/etc/nix` builders-config
  parse, the creds-freshness duration formatter, the Linux-builder remedy
  template, and the self-check `FAIL:`-block problem parser — all byte-goldened.
- **internal/agentsmd** (Stage 16 briefing gate) — the byte-exact AGENTS.md /
  CLAUDE.md briefing generator across net-mode / forwarded-port / loophole /
  resource / blocked-tool / mount permutations (verified vs live
  `generate_agents_md`), the **hardlink-breaking inode-preserving** briefing
  write (`st_nlink > 1` → fresh inode), and the yolo-source-tree TOML probe.

## What remains for Stage 14 proper

- **internal/loopholes** — the registry (discover / manifest parse / enable-
  disable rewrite incl. the comments-lost degradation / `_expand_env`
  empty-collapse / `runtime_args_for` bind-mount argv). Delegated in-flight.
  It must supply the `LoopholeResolver` interface `internal/config.ValidateConfig`
  already consumes (name → has-host-daemon).
- Front-door native dispatch for `ps` / `prune` / `builder` / `broker` — these
  stay delegated until byte-goldened at the command level (per the plan's
  "native slices delegate until byte-goldened" rule); the engines are ready.
- The command bodies' ok/warn/fail + rich output (Stage 15 `check`, the run
  lifecycle in Stage 16) — the orchestration layer.

## Constraints honored

Byte-exact vs LIVE Python; surprising behavior preserved (image size-file
quirk, network ValueError-on-bad-port, storage claude.json reverse-sync
allowlist); `GOOS=darwin GOARCH=arm64 go build ./...` clean; offline vendored
build (`GOPROXY=off -mod=vendor`) clean — no new external deps; every slice
gofmt/vet/staticcheck clean; conventional commits, no AI trailers, no amends.

---

## Re-audit 2026-07-18

This stage has confirmed blocker/major findings in the consolidated re-audit: [`go-port-audit-2026-07-18.md`](go-port-audit-2026-07-18.md). Fix or ledger the items attributed to this stage before its `YOLO_IMPL=go` gate is recommended for dogfood.
