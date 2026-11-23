---
title: "Native capture compatibility context: bounded repair plan"
date: 2026-10-29
status: accepted
stage: BUILT
next: "Root batch gate and unresolved native parse witness"
---

# Native capture compatibility context

**Status:** 2026-10-29. Capture-context repair is built in the reviewed candidate and assembled for the combined gate. **Measured natively 2026-10-29** ([Mac evidence](macos-native-tls.md#mac-evidence-witness-run), [OQ-MT8](macos-native-tls.md#OQ-MT8)): a native `yolo capture copilot` gets a valid context. The separate self-mode parse report was Copilot's version ending a sentence (`1.0.103.`), fixed by `f44d66d64`.

Written against `8c0bc42f3a72e19b9f27a06f33b1fdf4a63c4679`.
Behavior authority: [program delivery](program-delivery.md); scope and constraints are the accepted
Astra tactical wave. Design wins on behavior, tree wins on fact, plan is perishable advice.

## Settled boundary

A native capture's generated program launcher must receive its resolved self/account mode, not
missing metadata or composed user metadata. Reuse the launch's existing protected JSON carrier;
no schema, compatibility bypass, version threshold, SecurityServer grant, or broker change.
Capture roots are its own staging root and staging home; account mode has no owner temp roots.
Self mode carries only its resolved user-temp/shared-temp grants already present in its profile.
Unknown capture modes must remain invalid: do not let `captureRunAs`'s account fallback mint valid
account context for an invalid requested mode.

The capture producer calls the shared bootstrap builder, which marks a native workspace,
but does not compose the JSON context that the compatibility guard requires. The joined regression
below reproduced RED on baseline through a read-only Go overlay: all three cases emitted
`invalid macos-user launch context` (package runtime 0.016s, exit 1). The writer must retain that
same test permanently and confirm RED before production edits. This does not establish the
cause of the old native self-mode parse report, whose raw version output remains missing.

## Map and reuse

| Path | Bounded change |
| :--- | :--- |
| `internal/macosuser/capture.go` | `buildCapturePlanAt`: replace composed context with capture-owned context before bootstrap/driver env composition |
| `internal/macosuser/capturecompatibility_test.go` | New permanent joined capture-plan → bootstrap variables → real launcher generator → install-only guard regression |
| `scripts/completion-readers.json` | Generated census output for the new external-package/file-reading test |

Reuse `MarshalSandboxTmpdirValidationContext`, `withoutEnvVar`, `withEnvVar`,
`argvEnvValue`, `sandboxEnvFileValue`, and the existing launch producer in
`internal/macosuser/runplan.go` as the carrier/equality shape, without editing that file.
The owning-pack decoder and strict 4096-byte legacy files loader remain unchanged.

## Task — independent capture producer repair

**Preconditions:** baseline ancestry present, writer tree clean; no other writer owns the capture
producer hunk. Stop and report if RED differs, source is dirty, symbols disappear, a file outside
this fence is needed, or a gate fails outside scope. Do not reset, stash, amend, or clean caches.

**Tests first:** write the complete permanent regression below; run only that test, preserve RED
output, and hold with the lead before any production edit. Fixture setup choices are cheap; exact
behavior, fake version-only invocation, and production callers are not choices.

```go
func TestCapturePlanCompatibilityOwnsItsResolvedContext(t *testing.T) {
    for _, tc := range []struct{ name string; mode Mode; version string; refuse bool }{
        {"account", ModeAccount, "1.0.103", false},
        {"self-compatible", ModeSelf, "1.0.92", false},
        {"self-affected", ModeSelf, "1.0.103", true},
    } {
        t.Run(tc.name, func(t *testing.T) {
            t.Cleanup(entrypoint.OverrideImageProbeBase(t.TempDir()))
            t.Cleanup(packload.OverrideSkewTolerance(false))
            root := t.TempDir()
            pack := filepath.Join(root, "copilot")
            if err := os.MkdirAll(pack, 0755); err != nil { t.Fatal(err) }
            for _, name := range []string{"pack.json", "compatibility.json"} {
                data, err := os.ReadFile(filepath.Join("..", "..", "packs", "copilot", name))
                if err != nil { t.Fatal(err) }
                if err := os.WriteFile(filepath.Join(pack, name), data, 0644); err != nil { t.Fatal(err) }
            }
            if err := os.WriteFile(filepath.Join(root, "_pack-tree.json"),
                []byte(`{"packs":[{"name":"copilot","dir":"copilot"}]}`), 0644); err != nil { t.Fatal(err) }
            opts := testCaptureOptions()
            opts.Bin, opts.Mode, opts.CaptureRoot, opts.HostPackRoot = "copilot", tc.mode, t.TempDir(), root
            opts.HostHome = t.TempDir()
            opts.DarwinUserTempDir = filepath.Join(opts.HostHome, "temp")
            opts.SandboxEnv.Set(paths.MacosUserTmpdirContextEnv, `{"mode":2,"workspace":"/spoof","home":"/spoof"}`)
            plan := BuildCapturePlan(opts)
            vars := map[string]string{"JAIL_HOME": plan.StagingHome, "YOLO_PACK_ROOT": root, entrypoint.AgentUpdatesEnv: "false"}
            for _, key := range []string{"YOLO_DARWIN_WORKSPACE", paths.MacosUserTmpdirContextEnv} {
                if value, ok := argvEnvValue(plan.BootstrapArgv, key); ok { vars[key] = value }
            }
            // The staged fixture stands for StagePackCommands' sealed guest copy; context is unmodified.
            e := entrypoint.DarwinEnvFrom(vars, plan.StagingHome)
            e.Stderr = io.Discard
            if err := entrypoint.GenerateAgentLaunchers(e); err != nil { t.Fatal(err) }
            if err := os.MkdirAll(e.LocalBin(), 0755); err != nil { t.Fatal(err) }
            fake := "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = --version ] || exit 91\nprintf '%s\\n' '"+tc.version+"'\n"
            if err := os.WriteFile(filepath.Join(e.LocalBin(), "copilot"), []byte(fake), 0755); err != nil { t.Fatal(err) }
            cmd := exec.Command(filepath.Join(e.LaunchDir(), "copilot"))
            cmd.Env = append(os.Environ(), "HOME="+plan.StagingHome, "PATH="+e.LocalBin()+":"+os.Getenv("PATH"), "YOLO_INSTALL_ONLY=1")
            out, err := cmd.CombinedOutput()
            invalid := strings.Contains(string(out), "invalid macos-user launch context")
            refused := strings.Contains(string(out), "macos-user self's required SecurityServer deny")
            if invalid || refused != tc.refuse || (err != nil) != tc.refuse {
                t.Fatalf("capture context/guard mismatch: invalid=%v refused=%v want=%v err=%v output=%s", invalid, refused, tc.refuse, err, out)
            }
            raw, ok := argvEnvValue(plan.BootstrapArgv, paths.MacosUserTmpdirContextEnv)
            ctx, ctxErr := ParseSandboxTmpdirValidationContext(raw)
            session, present := sandboxEnvFileValue(plan.EnvFileContent, paths.MacosUserTmpdirContextEnv)
            if !ok || ctxErr != nil || !present || session != raw || ctx.Mode != tc.mode || ctx.Workspace != plan.StagingRoot || ctx.Home != plan.StagingHome {
                t.Fatalf("capture-owned bootstrap/driver context missing or unequal: %q %q %v", raw, session, ctxErr)
            }
            if tc.mode == ModeAccount && (ctx.DarwinUserTempDir != "" || ctx.SharedTmp) { t.Fatal("account capture invented owner temp roots") }
        })
    }
}
```

Imports required by this file: standard `io`, `os`, `os/exec`, `path/filepath`, `strings`, `testing`;
project `entrypoint`, `packload`, `paths`. Resolve fixture-minted temp paths with `EvalSymlinks` where
path equality requires it, preserving Darwin reproducibility.

**Change:** compose and overwrite protected context from capture's resolved roots/mode before calling
`buildBootstrapEnv`; explicitly relay its final value into bootstrap (same contract as run plan).
Preserve the driver environment's identical value and original caller `SandboxEnv` immutability.
If marshaling fails, retain fail-closed behavior rather than substituting account or a bypass.

**Gates:** permanent RED command `go test -short ./internal/macosuser -run '^TestCapturePlanCompatibilityOwnsItsResolvedContext$' -count=1 -v`;
then GREEN same command, affected capture/context tests, owning-pack/version-only entrypoint controls,
`just format`, and `python3 scripts/completion-census.py` for external fixture reads. A nested smoke
may verify container non-regression only; it cannot establish native Mac capture behavior.
Root owns combined landing/full integration gate; no per-lane full suite.

**Fences:** capture producer and new regression file only (enforced by lead diff review). Read-only
runplan, entrypoint/shims, host launch/auth, A inherited scope and D view lifecycle; no overlap edits.
Real installed agent CLIs may be invoked only with `--version`. Deterministic repository-owned fake
executables may exercise dispatch only when explicit executable/PATH confinement prevents fallthrough
to real agents or network/API/login/credential use. No real credentials/keychains.

**Commit:** `fix(macosuser): carry resolved compatibility context into captures`.
**Report:** exact RED and GREEN receipts, paths, diffstat, final SHA, unresolved native parse witness.
No Main landing/push/install/deploy/release. Leave the manual writer tree locked for Root.
