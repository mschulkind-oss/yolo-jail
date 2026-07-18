# Handoff: reviving the macos-user backend as one composed product

**Status:** implementation plan (2026-07-17). Synthesized from 6 parallel
readers over HEAD, git tag `macos-user-experiment`, and excision commit
`b28a9a9`. No code was modified producing this plan.

**Reads with:** [macos-no-vm-direction.md](../design/macos-no-vm-direction.md) (`## Decision`),
[macos-nix-shell-backend-proposal.md](../plans/macos-nix-shell-backend-proposal.md)
(`## Decisions (settled)`).

---

## 1. Goal

Ship **one composed macOS product**, not two competing backends:

- **macos-user** — native macOS user + Apple Seatbelt, **NO VM, no Linux
  image**, packages materialized via **native aarch64-darwin nix** — is the
  **fast default**.
- **Apple Container** (Linux container in a VM, runs the aarch64-linux nix
  image) is the **fallback cell** for what native darwin can't cover (a
  `packages:` entry with no darwin build, or when VM-grade isolation is wanted).

**Acceptance bar (non-negotiable, from day one):** macos-user MUST honor
`packages:` via native **aarch64-darwin** nix. The first attempt was excised
precisely because it dropped yolo's nix layer and read as a SandVault clone.
Wiring dispatch alone does NOT clear the bar — the nix layer (Change Units
U8–U11) is the gating work; the wiring (U1–U7) only routes control into it.

**Settled mechanism (proposal doc, `## Decisions (settled)` #1):**
materialization is a **generated darwin devShell realized with
`nix print-dev-env`**, NOT `nix profile` (imperative/drift-prone) and NOT a
`buildEnv`/`darwinProfile` derivation. Two readers proposed those alternates;
the doc overrides them. NOTE: `macos-no-vm-direction.md` axis-3 wording still
says "`nix profile`" and is stale — U14 fixes it.

The restored `src/cli/macos_user.py` (+ `tests/test_macos_user.py`, 68 passing)
is currently INERT: nothing dispatches to it and `macos-user` is not a valid
runtime value. It also has **zero** packages/nix handling — confirmed across
all readers — so the acceptance bar is unmet until U8–U11 land.

---

## 2. Change units (sequenced)

### Kinds
- **new-file-parallel-safe** — isolated new module/test, no conflict with anyone.
- **shared-file-sequential** — edits a shared source file
  (`paths.py` / `config.py` / `runtime.py` / `config_ref_cmd.py` / `__init__.py`
  / `run_cmd.py` / `check_cmd.py` / `flake.nix` / `macos_user.py`); must be
  ordered so it does not collide with the on-demand-builder + ROCm work already
  in `check_cmd.py`.
- **docs** — documentation only.

### Global ordering rule
`paths.py` (U1) is the keystone: `NATIVE_RUNTIMES` / `ALL_RUNTIMES` are imported
by `config.py`, `runtime.py`, and `check_cmd.py`; adding those imports before U1
lands would `ImportError`. Land **U1 first**, then U2/U3 (independent of each
other), then U4/U5/U6, then **U7 last** among the wiring set (it is the only
file that diverged and must be hand-woven, not patch-applied).

The nix-layer track (U8→U9→U10→U11→U12) is largely independent of the wiring
track and can proceed in parallel, but **U6's dispatch does not clear the
acceptance bar until U11 is merged.**

---

### U1 — paths.py: restore `NATIVE_RUNTIMES` + widen `ALL_RUNTIMES`
- **kind:** shared-file-sequential
- **files:** `src/cli/paths.py`
- **anchors:** `SUPPORTED_RUNTIMES = ("podman", "container")` at line 24;
  `ALL_RUNTIMES = SUPPORTED_RUNTIMES` at line 27.
- **change:** after line 24 add `NATIVE_RUNTIMES = ("macos-user",)` (with the
  doc comment: native runtimes are opt-in, never auto-detected, and do NOT build
  a container argv — iterate `SUPPORTED_RUNTIMES`, not `ALL_RUNTIMES`, for
  container work). Change line 27 to
  `ALL_RUNTIMES = SUPPORTED_RUNTIMES + NATIVE_RUNTIMES`. Authoritative old text:
  `git show b28a9a9 -- src/cli/paths.py` (restore the `-` lines).
- **depends_on:** none.

### U2 — config.py: accept `macos-user` + (optional) macos config keys
- **kind:** shared-file-sequential
- **files:** `src/cli/config.py`
- **anchors:** validation gate `elif runtime is not None and runtime not in ALL_RUNTIMES:`
  at line 662; error string at line 663; `KNOWN_TOP_LEVEL_CONFIG_KEYS` (after
  `"journal",` ~line 98); `JOURNAL_MODES` (~line 105); `journal` validation block
  ending ~line 923 then `kvm = config.get("kvm")` at line 925.
- **change:** (required) line 663 → `errors.append("config.runtime: expected 'podman', 'container', or 'macos-user'")`.
  Once U1 widens `ALL_RUNTIMES`, line 662 accepts `macos-user` automatically.
  (optional, keep in lockstep with U4) restore `macos_log` + `macos_shared_root`
  to `KNOWN_TOP_LEVEL_CONFIG_KEYS`, restore `MACOS_LOG_MODES = ("off","user","full")`
  after `JOURNAL_MODES`, and restore their two validation blocks between the
  `journal` block and the `kvm` line — the `macos_shared_root` validator imports
  `home_containing` from `.macos_user` (present at `macos_user.py:269`).
- **depends_on:** U1.

### U3 — runtime.py: `_native_runtime_check` + selection short-circuits
- **kind:** shared-file-sequential
- **files:** `src/cli/runtime.py`
- **anchors:** the `from .paths import (` block (insert `NATIVE_RUNTIMES,`
  after `IS_MACOS`, ~line 33–35); `def _runtime(` at line 78; env branch
  `if env and env in ALL_RUNTIMES:` at line 124 with `if shutil.which(env):` at
  125; cfg branch `if cfg and cfg in ALL_RUNTIMES:` at line 135 with
  `if shutil.which(cfg):` at 136. **Do NOT touch** the HEAD-new
  `list_running_jail_names()` (~line 220).
- **change:** add the `NATIVE_RUNTIMES` import. Restore
  `_native_runtime_check(rt, source)` just before `def _runtime(` — returns
  `None` for non-native `rt`, `(rt, None)` for a valid native runtime on macOS,
  `(None, <error>)` when `macos-user` is selected off-macOS. In `_runtime_for_check`,
  immediately after line 124 insert:
  `native_err = _native_runtime_check(env, "YOLO_RUNTIME"); if native_err is not None: return native_err; if env in NATIVE_RUNTIMES: return env, None` (BEFORE the
  `which(env)` probe). Mirror after line 135 with source `"yolo-jail.jsonc"`
  before the `which(cfg)` probe. `_runtime()` itself needs no change (macos-user
  stays out of the auto-detect candidates — opt-in only).
- **depends_on:** U1.

### U4 — config_ref_cmd.py: runtime docs
- **kind:** shared-file-sequential
- **files:** `src/cli/config_ref_cmd.py`
- **anchors:** `runtime` field ~line 34; `Values:` line 35; `On Apple Silicon…`
  line 38; `agents` field line 40.
- **change:** line 35 → `Values: "podman", "container", or "macos-user".`.
  Replace the `On Apple Silicon…` line with the macos-user paragraph (macOS only,
  EXPLICIT opt-in, never auto-detected; native user + Seatbelt; WEAKER boundary
  than a VM). If (and only if) U2 restored the config keys, also restore the
  `macos_log` (default `"off"`) and `macos_shared_root`
  (default `"/Users/Shared/yolo"`) field docs before the `agents` field.
- **depends_on:** U2 (lockstep on the optional keys).

### U5 — __init__.py: `--dry-run` callback + macos-* command registration
- **kind:** shared-file-sequential
- **files:** `src/cli/__init__.py`
- **anchors:** `profile` option ending line 229; `version:` option line 230;
  `ctx.invoke(run, ctx=ctx, network=network, new=new, profile=profile)` at line
  326; `app.command()(doctor)` at line 371; the HEAD-new
  `from .builder_cmd import builder_app` / `app.add_typer(builder_app, ...)`
  block (~line 370–392) which now occupies the slot the macos block used to sit in.
- **change:** (1) add
  `dry_run: bool = typer.Option(False, "--dry-run", help="macos-user only: print the full run plan without executing it.")`
  between lines 229 and 230. (2) line 326 →
  `ctx.invoke(run, ctx=ctx, network=network, new=new, profile=profile, dry_run=dry_run)`
  (restore the old comment warning that omitting `dry_run` makes bare `yolo`
  inherit typer's truthy `OptionInfo` default and silently run in dry-run mode).
  (3) after `app.command()(doctor)` (line 371) add
  `from .macos_user import (macos_fix_permissions, macos_setup, macos_teardown, macos_unshare)  # noqa: E402`
  then register `macos-setup`/`macos-teardown`/`macos-unshare`/`macos-fix-permissions`
  (functions at `macos_user.py:1225/1340/1358/1384`). **Leave the builder/broker
  additions untouched** — insert the macos block without disturbing them.
- **depends_on:** U6 (the `dry_run` forward requires `run()` to accept the param).

### U6 — run_cmd.py: `--dry-run` param + macos-user dispatch
- **kind:** shared-file-sequential
- **files:** `src/cli/run_cmd.py`
- **anchors:** `profile` option ending ~line 1194 and the closing `):` ~line
  1195; `runtime = _runtime(config)` at line 1232; `agents = selected_agents(config)`
  at 1236; `full_command = list(ctx.args)` at 1240; `target_cmd = shlex.join(full_command)`
  at 1245; `# Collect identity env vars early` at 1247;
  `_reap_orphaned_jails` (~line 1300); `auto_load_image(...)` bool guard (~line 1449);
  `extra_packages = _effective_packages(config)` at 1440.
- **change:** (1) add `dry_run: bool = typer.Option(False, "--dry-run", help="macos-user only: …")`
  to `run()`'s signature (after `profile`, before the closing paren). (2) between
  line 1245 and 1247 insert the dispatch:
  `if runtime == "macos-user": from .macos_user import run_macos_user; agent_argv = full_command or ["/bin/zsh", "-l"]; sys.exit(run_macos_user(workspace, config, agents, agent_argv, repo_src=repo_root/"src", dry_run=dry_run))`
  followed by the guard `if dry_run: <error that --dry-run is macos-user only>`.
  Placement is deliberate: all of `runtime`, `config`, `agents`, `full_command`,
  `repo_root` are resolved by line 1245, and this sits BEFORE `_reap_orphaned_jails`,
  BEFORE the `auto_load_image` bool guard, and BEFORE all container machinery —
  so the native path never triggers a Linux image build.
- **depends_on:** U1, U3. (Acceptance bar additionally requires U11 for real
  package materialization; dispatch works without it but drops `packages:`.)

### U7 — check_cmd.py: HAZARD — hand-weave 4 gates around builder/ROCm code
- **kind:** shared-file-sequential
- **files:** `src/cli/check_cmd.py`
- **anchors (HEAD line numbers):** `from .paths import (` block (add
  `NATIVE_RUNTIMES,` after `IS_MACOS`, ~line 58); `_check_podman_machine_resources`
  (~line 896, insert helper before it); `ok(f"Runtime available: {runtime}")` at
  line 1372; end of the Entrypoint Dry-Run block `console.print()` at line 1414;
  `# --- GPU Checks ---` at line 1416; the FLATTENED Image Build region lines
  1736–1786; `if detected_runtime:` at line 1788; the Per-jail host-service
  liveness block lines 1932–1934; the "No container runtime installed" fail text
  ending ~line 1091; the early `selected_runtime` filter at lines 1026–1028.
  **Do NOT clobber** the HEAD-new `_preflight_builder_needs` (~735),
  `_linux_builder_remedy` / `_LINUX_BUILDER_REMEDY_TEMPLATE` (~696–733), or
  `_check_rocm_enumeration` (~830).
- **change (do NOT patch-apply the old diff here — this region diverged):**
  1. Add `NATIVE_RUNTIMES` import.
  2. Restore `_check_macos_user_backend(ok, warn, fail)` as a NEW function before
     `_check_podman_machine_resources`. It imports `SANDBOX_USER`,
     `_sandbox_user_exists`, `resolve_python` from `.macos_user` (all present) and
     probes OS, `sandbox-exec`, sandbox user, interpreter — **and additionally
     probes that `nix` is on PATH and `flake.lock` exists** (the nix layer is now
     load-bearing).
  3. After line 1372 add `is_native_runtime = runtime in NATIVE_RUNTIMES`.
  4. Between line 1414 and the GPU section at 1416 add
     `if runtime == "macos-user": _check_macos_user_backend(ok, warn, fail); console.print()`.
  5. GATE — re-wrap the current (ROCm/builder-aware) Image Build body at 1736–1786
     under `else:` of a new `if is_native_runtime:` branch that prints
     `[bold]Image Build[/bold]` + "Not applicable (native macOS backend)". This is
     a whitespace-sensitive re-indent of the flattened body — the single
     highest-risk mechanical step; a slip won't be caught by imports.
  6. Line 1788 → `if not is_native_runtime and detected_runtime:`.
  7. Wrap the liveness block 1932–1934 in `if not is_native_runtime:`.
  8. (optional) append the "or use the native macOS backend: runtime \"macos-user\""
     line to the no-runtime fail text (~1091).
  9. Verify the early filter at 1026–1028 (`if selected_runtime not in SUPPORTED_RUNTIMES: selected_runtime = None`)
     is acceptable — it only gates the early container-CLI probe; the authoritative
     pick is `runtime, _ = _runtime_for_check(config)` at ~1368, which returns
     `("macos-user", None)` after U3.
- **depends_on:** U1, U3.

### U8 — flake.nix: darwin devShell output + unavailable-package eval
- **kind:** shared-file-sequential
- **files:** `flake.nix`
- **anchors:** `pkgs = nixpkgs.legacyPackages.${system};` line 30;
  `imageSystem = builtins.replaceStrings ["-darwin"] ["-linux"] system;` line 43;
  `resolvedPackageSpecs = map (spec: …)` lines 132–160; `selectOutputs` line 122;
  `extraPackageSpecs` (env → fromJSON) lines 61–64; the outputs attrset at
  lines 589–602 (`packages.* = …`, `devShells.default = …`).
- **change:** add a **darwin** resolution that mirrors `resolvedPackageSpecs`
  (reuse `parseDottedSpec` / `selectOutputs` / `expandSelected` unchanged) but
  resolves against `pkgs` (system = the flake's `system`, which is aarch64-darwin
  on a Mac) instead of `imagePkgs`, and threads `system` (NOT `imageSystem`) into
  the pinned-commit `import (fetchTarball …) { system = system; }` (line 144) and
  the version-override `fetchurl` branch. Consume the SAME `YOLO_EXTRA_PACKAGES`
  JSON contract (`extraPackageSpecs`). Then:
  - Guard each spec for attr presence (`pkgs ? ${name}`) AND darwin availability
    via `pkgs.lib.meta.availableOn { inherit system; } drv`. Partition into
    `darwinKept` and `darwinSkippedNames`.
  - Add output `devShells.yoloDarwinPackages = pkgs.mkShell { packages = <concatMap selectOutputs darwinKept>; };`
    (flake-utils `eachDefaultSystem` publishes it as
    `devShells.aarch64-darwin.yoloDarwinPackages`).
  - Add output `darwinUnavailablePackages = darwinSkippedNames;` (a JSON list of
    strings; published as `darwinUnavailablePackages.aarch64-darwin`, readable by
    `nix eval`). This is the warn-and-skip surface (proposal decision #3).
  - Leave `imageSystem` / `imagePkgs` / all `ociImage*` outputs and
    `devShells.default` untouched.
- **depends_on:** none.
- **cannot verify on Linux** (see §5): the `availableOn { inherit system; }`
  predicate + `pkgs ? name` guards only evaluate meaningfully with
  `system = aarch64-darwin`; on a Linux checkout `pkgs` is a Linux set. Eval/build
  of these outputs is a Mac-only test.

### U9 — NEW src/cli/darwin_packages.py: the native nix materialization module
- **kind:** new-file-parallel-safe
- **files:** `src/cli/darwin_packages.py`
- **change:** see §3 for the full design (functions, signatures, exact nix
  commands, pin, no-build handling, PATH injection, testability).
- **depends_on:** U8 (agrees on the flake attr names `yoloDarwinPackages`,
  `darwinUnavailablePackages`).

### U10 — NEW tests/test_darwin_packages.py
- **kind:** new-file-parallel-safe
- **files:** `tests/test_darwin_packages.py`
- **change:** Linux-runnable unit tests per §4 (pure argv/parse/split builders +
  `subprocess.run` mocked). Template: `tests/test_macos_user.py` (esp.
  `TestDryRun` monkeypatching `m.subprocess.run` with a `called` list).
- **depends_on:** U9.

### U11 — macos_user.py: thread darwin packages into the RunPlan → launch
- **kind:** shared-file-sequential
- **files:** `src/cli/macos_user.py`
- **anchors:** `sandbox_path(home=…)` line 473 (PATH composer);
  `launch_argv(...)` line 498 with the protected quartet at 523/528;
  `RunPlan` dataclass line 787; `build_run_plan(...)` line 822;
  `run_macos_user` line 1008, its inner `_plan()` at 1042 (calls `build_run_plan`
  at 1046), the dry-run branch at 1058, the `_is_macos()` gate at 1066, the
  non-dry `plan = _plan()` at 1074, and the step-2 setup block at 1114–1123;
  `plan_invariants` line 884; `_print_plan` line 934; `macos_sandbox_env` line 985.
  Reuse `_effective_packages` from `src/cli/config.py:325` (append gpu.vaapi extras).
- **change:**
  1. `sandbox_path(home=SANDBOX_HOME, prefix: Optional[List[str]] = None)` —
     insert `prefix` entries after the `go/bin` entry and BEFORE `/usr/bin`, so
     nix packages shadow system tools but the agent shim launchers
     (`~/.yolo-shims`) still win.
  2. `launch_argv(..., path_prefix: Optional[List[str]] = None)` — pass
     `path_prefix` to `sandbox_path`. PATH stays in the caller-protected quartet
     (the store PATH cannot ride through `sandbox_env`, which drops PATH — this is
     the deliberate new channel).
  3. `RunPlan` — add `darwin_path_prefix: List[str] = field(default_factory=list)`,
     `darwin_env: Dict[str, str] = field(default_factory=dict)`,
     `darwin_skipped: List[str] = field(default_factory=list)`,
     `darwin_materialized: bool = False`.
  4. `build_run_plan(..., darwin: Optional["DarwinPackages"] = None)` — when
     present, merge `darwin.env` (non-PATH whitelist) into `sandbox_env`, pass
     `darwin.path_prefix` to `launch_argv`, and populate the new RunPlan fields
     (`darwin_materialized = True`). Stays PURE (no shelling out) — the
     materialization already happened in the caller.
  5. `run_macos_user` — refactor `_plan(darwin=None)` to accept the materialized
     result. Dry-run branch (1058): call `_plan()` with `darwin=None` (stays pure,
     Linux/CI-testable) and have `_print_plan` show the intended package list +
     the `print_dev_env` argv that WOULD run. Non-dry branch: after the
     `_is_macos()` gate (1066) and before the existing `plan = _plan()` at 1074,
     if `_effective_packages(config)` is non-empty call
     `darwin = darwin_packages.materialize(repo_src.parent, _effective_packages(config))`
     inside try/except `darwin_packages.DarwinPackagesError` → print actionable
     message (point at the Apple Container fallback for unavailable packages) and
     `return 1`; then `plan = _plan(darwin)`. Emit a warning line per name in
     `darwin.skipped`.
  6. `plan_invariants` — add a check that fires ONLY when `plan.darwin_materialized`:
     if the source config had packages but `darwin_path_prefix` is empty →
     problem. Do NOT fail the dry-run path (where materialization is skipped).
  7. `_print_plan` — render a Packages section: package list, `print_dev_env`
     argv, and (when materialized) `darwin_path_prefix` + `darwin_skipped`.
  8. Seatbelt needs NO change — `/nix/store` is neither read- nor traversal-denied
     in `seatbelt_profile` (line 389) and the store is world-readable; the nix
     build runs on the HOST user before `sandbox-exec`, not inside it.
- **depends_on:** U9.

### U12 — macos_user.py: macos-setup nix readiness + repoint dangling doc refs
- **kind:** shared-file-sequential
- **files:** `src/cli/macos_user.py`
- **anchors:** `macos_setup` line 1225 (readiness verdict tail);
  dangling doc strings at `macos_user.py:9`, and the `docs/macos-native-user-sandbox-design.md`
  / `docs/handoff-macos-user-backend.md` references in setup/error messages
  (both files are MISSING at HEAD — `fd` shows `macos-no-vm-direction.md`,
  `macos-nix-shell-backend-proposal.md`, `macos.md`, `handoff-macos-ondemand-builder.md`,
  and this new plan, but not those two).
- **change:** in `macos_setup`, add a readiness probe: `nix` on PATH + `flake.lock`
  present (+ optionally that `nix print-dev-env` of the empty devShell succeeds),
  printing a verdict alongside the existing python3/sandbox-exec checks. Repoint
  every dangling `docs/…` string to `docs/design/macos-no-vm-direction.md` (or this
  handoff). Same-file edit → must land after U11.
- **depends_on:** U11.

### U13 — integration tests for the 5 wiring hook sites
- **kind:** shared-file-sequential (edits existing test files) — or new-file if
  a dedicated module is preferred.
- **files:** `tests/test_cli_commands.py`, `tests/test_cli_unit.py`,
  `tests/test_config_merge.py`, `tests/test_cli_check_formatting.py` (the four the
  excision touched — natural regression net).
- **change:** cover config validation accepting `macos-user` (and rejecting it
  off-macOS with the "requires macOS" message via `_native_runtime_check`);
  `run_cmd` dispatch routing to `run_macos_user` (mock it) for
  `runtime: macos-user`; `--dry-run` forwarding from the `_default` callback;
  `check_cmd` gating (`is_native_runtime` skips Image Build / liveness, calls
  `_check_macos_user_backend`). The 68 `test_macos_user.py` tests do NOT exercise
  these hook sites.
- **depends_on:** U2, U3, U5, U6, U7.

### U14 — docs: correct axis-3 wording to devShell/print-dev-env
- **kind:** docs
- **files:** `docs/design/macos-no-vm-direction.md`
- **anchors:** axis-3 table row (line 22) and the acceptance-bar paragraph
  (line 59) — both currently say "native `nix profile`".
- **change:** update to "native aarch64-darwin nix devShell (`nix print-dev-env`)"
  to match the settled decision in `macos-nix-shell-backend-proposal.md:201`,
  cross-linking that doc. Do not alter the `## Decision` structure.
- **depends_on:** none.

---

## 3. Full design — `src/cli/darwin_packages.py`

Mechanism: generate nothing new per-run on the Python side — the flake's
`devShells.<system>.yoloDarwinPackages` (U8) already consumes `YOLO_EXTRA_PACKAGES`.
Python's job is: set that env, run `nix print-dev-env --impure --json` against the
darwin devShell, parse the resulting env, extract the store PATH + a whitelist of
non-PATH vars, and read the skipped-package list. This mirrors the container path's
`image._build_image_store_path` (`YOLO_EXTRA_PACKAGES` env + `nix … --impure`) but
targets a darwin devShell instead of `.#ociImage`.

```python
# src/cli/darwin_packages.py
from __future__ import annotations
import json, os, subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Union

DARWIN_SYSTEM = "aarch64-darwin"
DEVSHELL_ATTR = "yoloDarwinPackages"          # devShells.<sys>.yoloDarwinPackages
UNAVAILABLE_ATTR = "darwinUnavailablePackages" # <attr>.<sys> -> [str]

# Non-PATH vars from print-dev-env we forward to the sandbox. STRICT whitelist:
# print-dev-env dumps the full stdenv env, and env -i exists to scrub host leakage
# — only propagate store-pointing build vars. (risky_claim #3)
ENV_WHITELIST = ("PKG_CONFIG_PATH",)  # extend deliberately; NOT a passthrough of all.

class DarwinPackagesError(RuntimeError): ...

@dataclass
class DarwinPackages:
    path_prefix: List[str] = field(default_factory=list)  # /nix/store/.../bin dirs
    env: Dict[str, str] = field(default_factory=dict)     # whitelisted non-PATH vars
    skipped: List[str] = field(default_factory=list)      # no aarch64-darwin build

def _nix_flags() -> List[str]:
    return ["--extra-experimental-features", "nix-command flakes"]

def build_env(packages) -> Dict[str, str]:              # PURE (mirrors image.py:253-256)
    env = os.environ.copy()
    if packages:
        env["YOLO_EXTRA_PACKAGES"] = json.dumps(packages)
    return env

def print_dev_env_argv(system: str = DARWIN_SYSTEM) -> List[str]:   # PURE
    return ["nix", *_nix_flags(), "print-dev-env", "--impure", "--json",
            f".#devShells.{system}.{DEVSHELL_ATTR}"]

def unavailable_eval_argv(system: str = DARWIN_SYSTEM) -> List[str]: # PURE
    return ["nix", *_nix_flags(), "eval", "--impure", "--json",
            f".#{UNAVAILABLE_ATTR}.{system}"]

def parse_dev_env(json_text: str) -> Dict[str, str]:    # PURE
    data = json.loads(json_text)
    out = {}
    for name, spec in data.get("variables", {}).items():
        if spec.get("type") == "exported":
            out[name] = spec.get("value", "")
    return out

def split_env(dev_env: Dict[str, str]) -> tuple[List[str], Dict[str, str]]:  # PURE
    # PATH -> store bin dirs only (drop host dirs — env -i posture); whitelist rest.
    path_prefix = [p for p in dev_env.get("PATH", "").split(":")
                   if p.startswith("/nix/store/") and p]
    extra = {k: dev_env[k] for k in ENV_WHITELIST if k in dev_env}
    return path_prefix, extra

def locked_nixpkgs_rev(flake_lock: Path) -> str:        # PURE (diagnostics/pin display)
    data = json.loads(flake_lock.read_text())
    return data["nodes"]["nixpkgs"]["locked"]["rev"]    # d407951447dcd00442e97087bf374aad70c04cea

def materialize(repo_root: Path, packages, *, system: str = DARWIN_SYSTEM
                ) -> DarwinPackages:                     # IMPURE (macOS-only)
    env = build_env(packages)
    # 1. skipped set (warn-and-skip; flake already filtered them out of the shell)
    skipped: List[str] = []
    try:
        r = subprocess.run(unavailable_eval_argv(system), cwd=repo_root, env=env,
                           capture_output=True, text=True)
        if r.returncode == 0:
            skipped = json.loads(r.stdout)
    except (OSError, json.JSONDecodeError):
        pass  # non-fatal: skip surfacing, still try the build
    # 2. realize the devShell closure and capture its env
    try:
        proc = subprocess.run(print_dev_env_argv(system), cwd=repo_root, env=env,
                             capture_output=True, text=True)
    except FileNotFoundError as e:
        raise DarwinPackagesError("nix command not found") from e
    if proc.returncode != 0:
        raise DarwinPackagesError(proc.stderr.strip() or "nix print-dev-env failed")
    dev_env = parse_dev_env(proc.stdout)
    path_prefix, extra = split_env(dev_env)
    return DarwinPackages(path_prefix=path_prefix, env=extra, skipped=skipped)
```

**Exact nix commands run (on the Mac, as the host user):**
- `nix --extra-experimental-features "nix-command flakes" eval --impure --json .#darwinUnavailablePackages.aarch64-darwin`
  (with `YOLO_EXTRA_PACKAGES=<json>` in env, `cwd=<repo root>`) → JSON list of
  skipped package names.
- `nix --extra-experimental-features "nix-command flakes" print-dev-env --impure --json .#devShells.aarch64-darwin.yoloDarwinPackages`
  (same env + cwd) → JSON env dump; `--impure` is REQUIRED so the flake reads
  `YOLO_EXTRA_PACKAGES` via `builtins.getEnv` (identical to `image.py:267`).

**Pinning:** none in Python. Because these are flake OUTPUTS, `nix` resolves
`nixpkgs.legacyPackages.aarch64-darwin` against the repo's own `flake.lock`
node — the SAME locked rev
`d407951447dcd00442e97087bf374aad70c04cea` (nixos-unstable) the aarch64-linux
image uses, just the darwin system. Version parity with the container path is
free. `locked_nixpkgs_rev()` exists only for diagnostics / dry-run display; the
lock does the pinning. Per-package `{name, nixpkgs:<rev>}` overrides remain
independent of the lock (U8 threads `system = system` into their `fetchTarball`
import) — flagged as risky_claim #4.

**No-darwin-build handling (warn-and-skip, proposal decision #3):** two flake-side
guards — `pkgs ? ${name}` (attr entirely absent) and
`lib.meta.availableOn { inherit system; } drv` (attr exists but excludes
aarch64-darwin via `meta.platforms`/`badPlatforms`) — filter such specs OUT of
`yoloDarwinPackages.packages` so the build never fails on them, and surface their
names via `darwinUnavailablePackages`. Python emits one warning per name and
proceeds; the Apple Container fallback is the escape hatch for those packages. A
package that CLAIMS darwin support but fails to BUILD is NOT caught by
`availableOn` — it surfaces as a `print-dev-env` failure → `DarwinPackagesError`
with an actionable message (acceptable; matches Linux behavior for a broken pin).

**Where the profile/env lives + PATH injection:** there is no imperative profile.
The devShell closure lives in the shared `/nix/store` (world-readable, 0555). The
resolved store `…/bin` dirs come back in the print-dev-env `PATH`; `split_env`
keeps only the `/nix/store/*` entries. Those are threaded via the RunPlan's new
`darwin_path_prefix` into `sandbox_path()` (inserted after `~/go/bin`, before
`/usr/bin`) — the ONE PATH composer — so `launch_argv`'s baked, caller-protected
PATH exposes the packages to the agent. Non-PATH whitelisted vars ride through
`macos_sandbox_env` → `sandbox_env` → `launch_argv`'s non-protected append loop
(no new channel needed). The build runs on the host user (who has nix), before
`sandbox-exec`; no out-link/GC-root is strictly required for correctness because
print-dev-env realizes the closure, but a per-workspace GC root under
`GLOBAL_STORAGE` is a reasonable follow-up to prevent `nix-collect-garbage` reaping
it between runs (out of scope for the acceptance bar).

**Unit-testability on Linux (mock subprocess):** every function except
`materialize`'s actual nix calls is pure. `materialize` is tested by
monkeypatching `subprocess.run` (as `test_macos_user.py::TestDryRun` does) to
return canned `print-dev-env --json` / `eval --json` stdout and asserting the
returned `DarwinPackages`. The only thing not Linux-testable is the real
`nix print-dev-env` of a darwin closure (§5).

---

## 4. Test plan

**Mockable on Linux (add to `tests/test_darwin_packages.py`, U10):**
- `print_dev_env_argv()` / `unavailable_eval_argv()` — assert exact argv incl.
  `--impure`, `--json`, `.#devShells.aarch64-darwin.yoloDarwinPackages`.
- `build_env(pkgs)` — asserts `YOLO_EXTRA_PACKAGES` = `json.dumps(pkgs)` and
  absent when empty.
- `parse_dev_env(json)` — feed a canned print-dev-env `{"variables": …}` blob;
  assert only `type=="exported"` vars are kept.
- `split_env(dev_env)` — assert PATH partitioned to `/nix/store/*` only, and the
  non-PATH whitelist applied (host `PATH` dirs dropped).
- `locked_nixpkgs_rev(fixture)` — assert `d407951447dcd00442e97087bf374aad70c04cea`.
- `materialize(...)` — monkeypatch `subprocess.run`: success path returns a
  populated `DarwinPackages`; nix-missing → `DarwinPackagesError`; non-zero
  return → `DarwinPackagesError`; skipped list parsed from the eval mock.

**Mockable on Linux (extend `tests/test_macos_user.py`, part of U11):**
- `sandbox_path(prefix=[…])` ordering: prefix after `go/bin`, before `/usr/bin`.
- `launch_argv(path_prefix=[…])`: baked PATH contains the prefix in order.
- `build_run_plan(darwin=DarwinPackages(...))`: RunPlan fields populated,
  non-PATH env merged into `sandbox_env`, `darwin_materialized=True`.
- `plan_invariants`: fires only when `darwin_materialized`; dry-run (no darwin)
  stays clean.
- `_print_plan`: renders the Packages section incl. skipped names.
- `run_macos_user(dry_run=True)`: still executes nothing (materialize NOT called)
  on Linux.

**Integration (U13):** config validation, dispatch routing, `--dry-run`
forwarding, check gating — all Linux-mockable.

**Deferred to a real Mac (§5).**

---

## 5. Cannot verify on Linux — must test on a Mac (aarch64-darwin + nix)

1. **U8 flake eval/build:** `nix build .#devShells.aarch64-darwin.yoloDarwinPackages`
   and `nix eval .#darwinUnavailablePackages.aarch64-darwin` — the
   `availableOn { inherit system; }` predicate and `pkgs ? name` guards are only
   meaningful with `system = aarch64-darwin`; a Linux checkout's `pkgs` is a Linux
   set. Confirm a mixed `packages:` list (one darwin-OK, one Linux-only) yields the
   right kept/skipped partition and a buildable shell.
2. **Native package materialization end-to-end:** `nix print-dev-env --impure --json`
   actually resolves an aarch64-darwin closure, `split_env` yields real
   `/nix/store/*/bin` dirs, and those tools are on the agent's PATH inside
   `sandbox-exec`. This IS the acceptance bar — it can only be proven on a Mac.
3. **Seatbelt + store reads:** the sandboxed agent can read/exec the store closure
   under the `(allow default)` profile with `/nix` not denied; dylib loading from
   the store works under macOS dyld.
4. **`sudo` / passwordless-sudo prompt path** through the TTY proxy during setup
   steps 2–3 (`run_macos_user`), and the sandbox user creation/teardown
   (`macos_setup`/`macos_teardown`).
5. **`_check_macos_user_backend`** actually reporting green on a provisioned Mac
   (OS, sandbox-exec, sandbox user, interpreter, nix, flake.lock).
6. **Per-package `{name, nixpkgs:<rev>}` overrides** resolving against
   aarch64-darwin (U8's `system=system` thread on the pinned fetchTarball import).
7. **aarch64-darwin cache coverage:** an available-but-uncached package triggers a
   native from-source build (slow first run, no VM) — confirm the "building from
   source" notice fires and the run still succeeds.
