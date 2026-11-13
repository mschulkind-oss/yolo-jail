---
title: "Plan: October 18 serial landing and verification"
status: accepted
stage: DECIDED
next: "Obtain rootless M1 on both architectures, isolated real-broker before/after evidence, and native-Mac execution; do not infer them from the nested rootful run"
summary: "Records the prior model/Mac landing, full patch/live candidate histories, exact review closure, combined validation and remaining platform obligations."
---

# Plan: October 18 serial landing and verification

**Status:** 2026-10-19. The historical integration record is settled; rootless both-architecture,
real-broker before/after, and native-Mac validation remain owed. The owner follow-up below records
a docs-only ruling and does not change release authority.

**Baseline and integration:** primary `main` was clean at `6683038d29e073e015af9396b117fa36f1e9f63d` before the serial source merges. The patch-branch code entered the integrated history in merge `08e6566c4`; live-config code entered in merge `6f7c72a25`, with the inspected-mount-record repair in `388ea8a1b`. The full patch-branch history through `770d9c2c9f83592d2c904de23a1f4fb36bd9cdb0` and live-config history through `e80f3a2ae3acc3724b83aaa66b207edd9cbd18f4` remain ancestors. `4e7b9b92bfc8d6dbd0e0b5190d0c73efeb6d96c1` is the later one-line documentation repair, not the source-code merge.

## Prior model and Mac source history

The model and Mac/recovery implementation was already in the serial baseline. The original Mac commits `98715e67b` and `41dd11e3f` remain in the source history through merge `4bc941d53`; the model source and test fixes remain through `bfb4f6f47` and `203182fd4`. The `6683038d2` commit records the verification checkpoint in documentation; its own diff is docs-only.

- **Model review:** independent review returned **ACCEPT with notes**, identifying a P2 in all-profile host tests: a negative-only assertion could pass without reaching registry resolution. The later `082d3cab3` regression now resolves each of the nine shipped Pi profiles' own provider mapping, then checks a known and an unknown reference against that provider. Kilo and Bedrock use explicit test-config rows because their packs ship no callable model list; these fixtures make no claim that those dynamic IDs are native catalog or shipped-list entries. `TestCheckResolvesEveryShippedPiProfileModelReference` and `TestPiProfileAndSubagentsExactIDOutranksDefaultAlias` are the named regression evidence.
- **Mac review:** the candidate was independently reviewed before its committed fixes, then proportionately rechecked against those findings; no additional broad panel was started. The stable DM1–DM4 ruling IDs and the Mac design's [§6.2 doorway](../design/macos-user-suite-on-a-developer-mac.md#62-another-sessions-doorway-was-read-as-this-sessions-survivor) and [§6.3 housekeeping](../design/macos-user-suite-on-a-developer-mac.md#63-the-housekeeping-tests-slot-was-not-due-because-its-stamp-is-the-machines) diagnoses remain intact. Changes include exact-count `sudo` allowances, a product-shaped non-root sandbox command/cleanup path, path checks across `/`, process listing for both relevant accounts, and honest countable unified-log skips. Test-only follow-ups `cab34b522` and `201c4009e` added detection for root `sudo` inside literal shell commands and closed quoted/attached `root` and `#0` operands; the guard still permits the product-shaped sandbox user. These are portable static-test improvements, not production behavior or Mac runtime evidence. Native-Mac execution remains unverified.
- **Shared records:** the model-reference release notes and named model/Mac mappings from this source history are retained. The Mac test/documentation work gets no separate user-facing changelog claim; the live config command does. Census output was regenerated and compared; broad environmental drift was restored rather than committed when it did not represent new test readers.

### Historical implementation/test excerpts

These bounded excerpts come from committed historical diffs, not new implementation claims or raw session transcripts: Mac implementation/test/ruling hunks from `1a9ded40b..4bc941d53`, model implementation and exact-id-test hunks from `4bc941d53..bfb4f6f47`, and the earlier all-profile control from `bfb4f6f47..203182fd4`. Later `082d3cab3` replaced the shared-provider fixture with the explicit nine-profile/provider-specific known-and-unknown matrix described above. `6683038d2` recorded the verification checkpoint in docs; it did not add the implementation. The full bounded excerpt collection and original merge-range patches remain in the run artifact; these source-named call sites and regression shapes preserve the earlier landing's reviewable evidence in-tree.

```diff
--- a/internal/entrypoint/packsurfaces.go
+++ b/internal/entrypoint/packsurfaces.go
@@
+ reportModelReferences(e, packs)
```

The committed Pi derive now checks an exact declared model id before falling back to the provider's default alias; the exact-id precedence is pinned by `TestPiProfileAndSubagentsExactIDOutranksDefaultAlias`.

```diff
--- a/packs/pi/derive.lua
+++ b/packs/pi/derive.lua
@@
   local _, id = yolo.model_for(value, provName)
-  if not id and value ~= "default" then
-    _, id = yolo.model_for("default", provName)
-  end
   if id then return id end
   for _, modelId in pairs(models) do
     if modelId == value then return value end
   end
+  if value ~= "default" then
+    _, id = yolo.model_for("default", provName)
+    if id then return id end
+  end
```

```diff
--- a/internal/cli/check/modelreferences_test.go
+++ b/internal/cli/check/modelreferences_test.go
@@
+func TestCheckResolvesEveryShippedPiProfileModelReference(t *testing.T) {
+    cfg := `{"packs": ["pi", "openai-auth", "zai", "openrouter", "kilo", "llamacpp", "bedrock",
+      "cerebras", "meta-provider", "wire-bridge"]}`
+    knownScope := "openai-codex/gpt-6.1-sol[1m]"
+    unknownScope := "openai-codex/yolo-no-such-model"
+    for _, profile := range []string{"codex", "zai", "openrouter", "kilo", "llamacpp", "bedrock",
+        "bedrock-bridge", "cerebras", "meta"} {
+        t.Run(profile, func(t *testing.T) {
+            out, _ := runPacksCheck(t, cfg, profile, scopePack(t, knownScope))
+            if strings.Contains(out, "matches no model") ||
+                strings.Contains(out, "Model reference: 1 model reference not checked") ||
+                !strings.Contains(out, "Model reference: every model pi's config names resolves in its registry (1 reference checked)") {
+                t.Fatalf("shipped profile %q must check and resolve the qualified scope reference %q:\n%s", profile, knownScope, out)
+            }
+            out, _ = runPacksCheck(t, cfg, profile, scopePack(t, unknownScope))
+            if !strings.Contains(out, "which matches no model in pi's registry") ||
+                strings.Contains(out, "Model reference: 1 model reference not checked") {
+                t.Fatalf("shipped profile %q must detect the unknown qualified scope control %q:\n%s", profile, unknownScope, out)
+            }
+        })
+    }
+}
```

The excerpt omits no assertion in this function: the positive known input requires checked registry resolution, and the separate unknown input requires the warning rather than an unchecked result, across all nine shipped Pi profiles.

The Mac ruling that constrained test privilege is retained in the actual landed Decision Ledger row:

```diff
+| OQ-DM1 | **The suite never needs passwordless root `sudo`** (option B, remove the need). The 2026-10-18 ruling rejects tests requesting root privileges not required by the product. Every privileged step goes through what yolo itself installs, the one run-as rule, built by `macosuser.RunAsSandboxArgv`, or is the invoking user's own; a capture plan's prepare steps run exactly as production runs them; the CA test uses a CA the System keychain already holds and never adds one; a guard test fails on a sudo the suite spells itself | 2026-10-18 | [§3.1](#31-class-a--six-tests-spent-a-privilege-yolo-macos-setup-does-not-install), [§3.3](#33-class-c--one-root-sudo-probe-that-read-as-a-missing-precondition), [§5](#5-the-shape) | Candidate implementation; native-Mac runtime unverified |
```

The Mac integration guard parses the test package on all hosts and reports an unapproved sudo literal with the owner and allowed replacement:

```diff
--- /dev/null
+++ b/integration/macosusernorootsudo_test.go
@@
+                if _, ok := allowed[key]; ok {
+                    used[key]++
+                    return true
+                }
+                t.Errorf("%s spells a sudo command (%s) in %s. The macos-user suite takes no "+
+                    "privilege `yolo macos-setup` does not install (OQ-DM1): run a step as the "+
+                    "sandbox account through sandboxCommand, which builds the one run-as rule's "+
+                    "argv (macosuser.RunAsSandboxArgv), and every other step as yourself.",
+                    fset.Position(lit.Pos()), v, name)
```

The Mac cleanup path first removes as the host user, then uses the product's guest command when needed; no new root grant was added. The host runner is constructed through the product's run-as argv:

```go
func sandboxCommand(ctx context.Context, argv ...string) *exec.Cmd {
    full := macosuser.RunAsSandboxArgv(nil, argv...)
    cmd := exec.CommandContext(ctx, full[0], full[1:]...)
    cmd.Dir = "/"
    cmd.Stdin = nil
    return cmd
}
```

The cleanup helper first removes the fixture as the invoking user, makes owned directories searchable/removable and retries, then calls only the product-declared guest cleanup commands with a deadline before one final host retry:

```diff
--- a/integration/macosusergate_test.go
+++ b/integration/macosusergate_test.go
@@
+func removeSandboxWritten(dir string) error {
+    if err := os.RemoveAll(dir); err == nil {
+        return nil
+    }
+    _ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
+        if err == nil && fi.IsDir() {
+            _ = os.Chmod(p, fi.Mode().Perm()|0o700)
+        }
+        return nil
+    })
+    if err := os.RemoveAll(dir); err == nil {
+        return nil
+    }
+    for _, cmd := range macosuser.CaptureScratchGuestCommands(dir) {
+        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
+        c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
+        c.Dir = "/"
+        _ = c.Run()
+        cancel()
+    }
+    return os.RemoveAll(dir)
+}
```

## Patch-branch history and review disposition

- **Initial review:** blocked on two P1 classes—relative-locator resolution/build state could cross clone boundaries, and the offline/default check required a fresh fetch rather than trusting the recorded verdict. The early candidate was not accepted.
- **Repair sequence:** the full retained history adds clone isolation, record-only reporting, offline verdict handling, and host-to-guest mapping of the branch identity. A later caller audit still **BLOCKED** with a P1: default `yolo pack series check` did not hydrate the host mapping and could reject a correct host record in a distinct guest checkout.
- **Final caller repair:** `hydrateSelectedWorkspacePackMappings` runs after loading the explicitly named manifest and before constructing its forks. It transfers only a host-established identity whose recorded workspace-relative path resolves to the exact directory the command named and whose matching contribution still declares a relative branch. The named manifest remains authoritative; another clone cannot borrow that mapping. Production regression `TestSeriesCheckHydratesOnlyTheExactSelectedWorkspacePackIdentity` uses distinct host/guest checkouts and repository identities, deletes mirrors, denies all git invocations, checks good and stale recorded verdicts, and rejects an unrelated clone. The record-only path does not fetch or touch mirrors.
- **Final review:** the caller reviewer returned **ACCEPT**, limited to this narrowed closure. It independently ran the seven targeted CLI, entrypoint, packsrc and packload regressions; no platform/VM runtime was claimed. Exact spelling, consumer mapping and test details remain in [patch branches](../reference/patched-forks.md#PF-D89) and [PF-D98](../reference/patched-forks.md#PF-D98).

## Live-config-apply history and review disposition

- **Initial review:** blocked on four P1s: a mount-record write failure could falsely complete a removal; broker lock acquisition ignored deadlines; absent/unreadable broker scope could be treated as already applied; and process identity was checked after namespace mutation.
- **First repair review:** confirmed those four areas were repaired, then found a new P1: successful mount mutation invalidated its own record, preventing ordinary add/remove/replacement recovery.
- **Final repair:** `updateCtxMountRecord` restores only the launch token read from the inspected running jail and only while the record remains the empty-token invalidation tombstone. A competing launch token is not replaced. The regression drives production apply through invalidation, writer, reader and baseline fold for add→remove and remove→replacement-add; the failure regression keeps a failed write pending rather than folding it.
- **Final closure review:** returned **ACCEPT**, limited to the narrowed mount-record closure. The review confirmed tombstone/token behavior and the exact production lifecycle, plus deadline-bounded broker-scope transactions, missing/erroring live scope remaining pending, and process/mount-namespace identity pinned before source/destination mutation. It also accepted the task-owned configuring skill: one host approval gate, no alternate asker, no restart offer, and no additional confirmation.
- **Retained focused regressions:** `TestConfigApplyRestoresTheInvalidatedMountRecordThroughRealApply`, `TestInvalidatedCtxMountRecordCannotClaimStaleMounts`, `TestConfigApplyRecordFailureInvalidatesMountStateAndKeepsRemovalPending`, `TestLiveScopeTransactionsHonorContextWhileSourceLockIsHeld`, `TestApplyPlanRequiresVerifiedLiveScopeAfterApprovalIsUnchanged`, `TestInjectCtxMountRefusesChangedRuntimePIDBeforeRunningVerb`, and `TestAttachRejectsAChangedProcessOrMountNamespaceBeforeSourceMutation`. The final review's package selection passed; it does not prove a real broker exchange or rootless runtime behavior.
- **Merge conflict reconciliation:** retained both sides of the `docs/plans/BACKLOG.md` conflict: the prior mise-shim finding and the three live-review discoveries. No candidate source was discarded in resolving it.

## Candidate disposition and boundaries

| Slice | Candidate disposition |
| :--- | :--- |
| Model-reference registry, exact-id resolution, host/boot reporting and profile fixtures | Present in the integrated source history before the docs-only `6683038d2` verification record; expanded by the current nine-profile regression at `082d3cab3`. |
| Mac test privilege, log-skip, floor and recovery fixes | Present in the integrated source history before the docs-only `6683038d2` verification record; the portable shell guard was later hardened in `cab34b522` and `201c4009e`. |
| Patch-branch feature and exact named-pack offline caller closure | Code merged in `08e6566c4`, retaining the full `770d9c2c9` history; final caller review accepted. |
| Live config apply, broker live-scope changes, `/ctx` mount injection and bounded repair | Code merged in `6f7c72a25`; follow-up inspected-mount-record fix `388ea8a1b` descends from that merge and retains the full `e80f3a2ae` history. Final closure review accepted with the limits above. |
| Primary movement | The integrated main tip advanced through `4e7b9b92bfc8d6dbd0e0b5190d0c73efeb6d96c1`; that commit itself changes one documentation heading, while the source merges above are its ancestors. |

The integrated source history retains the model/Mac mappings, exact selected-pack mapping, and bounded broker scope, inspected mount-token restoration and process-identity pinning. The later `4e7b9b92b` tip change itself is documentation-only and changes no accepted behavior; the source merges above precede it in history. The integrated work did not widen model-reference claims, give a patch identity to another pack, authorize a restart offer, or bypass candidate verification.

## Validation and remaining obligations

- Candidate verification passed before the primary fast-forward. `just format`, `git diff --check`, and `just check-ci` all returned success; check-ci included Linux and Darwin vet/staticcheck, short tests, changelog and site gates, closed-tree checks, and release checks. The authored docs were separately checked with `uvx vantage-check` (8 files, clean). The one early affected-package run exposed two ordinary `internal/cli/run` issues: the new backend parity needed its rationale, and a stale mount-token mismatch needed its accurate reason. Both were fixed at their production seam; the final affected package selection passed: `go test -count=1 ./internal/cli/check ./internal/cli ./internal/cli/run ./internal/entrypoint ./internal/packdecl ./internal/packload ./internal/packsrc ./internal/hostfloor ./internal/ctxinject ./internal/brokerscope ./internal/ghbroker`.
- `python3 scripts/completion-census.py` was run. Its generated reader map had broad environment-dependent churn; comparison found no actual new readers beyond the existing map, so unrelated output was restored. The shared Go cache was preserved. `just build-go` succeeded, then the candidate's own `dist-go/linux-amd64/yolo` ran in a nested jail from a throwaway workspace with a temporary HOME, `YOLO_REPO_ROOT` pointing at this checkout, and no selected packs: `candidate-nested-ok home=/home/agent workspace=/workspace`.
- The required combined integration suite ran once against the final candidate source and passed: `YOLO_NO_PROGRAM_READINESS=1 go test -count=1 -timeout 0 ./integration` → `ok ... 722.513s`. The nested smoke exercises the candidate binary and boot path but its Podman-in-Podman environment is structurally rootful; it is not rootless integration evidence. The fast-forward is recorded above; the landing receipt records `just done` green on clean main afterward.
- **Outstanding runtime obligations:** rootless Podman live-mount integration, including remount parity, on both required architectures; an isolated real-broker widening/narrowing exchange; and native-Mac execution of the Mac integration tests. The nested rootful remount result is measured below; it is not rootless evidence or resistance proof.
- No push, tag, release, deployment, credential change, cache cleanup, or Pi core/extension work is authorized.

## Landing record

1. The integrated main tip advanced through `4e7b9b92bfc8d6dbd0e0b5190d0c73efeb6d96c1`; its own diff is documentation-only. The patch and live-apply source merges precede it in history; no history was squashed or rewritten.
2. The landing receipt records `just done` passing on clean main after the fast-forward. The four external/runtime obligations above remain open; neither this landing nor its nested smoke discharges them.
3. No push, tag, release or publication is recorded here.

## Blocker assessment at `201c4009e`

**Observed source state:** the source was `201c4009e23b8ce4d99519eed95ffc656f565800` and clean before this documentation update. An ancestry check confirms the model test expansion (`082d3cab3`), Mac shell-command guard (`cab34b522`), and quoted/attached root-operand repair (`201c4009e`) are in the integrated source history. The exact-HEAD GitHub Actions query `gh run list --repo mschulkind-oss/yolo-jail --commit 201c4009e23b8ce4d99519eed95ffc656f565800 --limit 30 --json databaseId,headSha,name,status,conclusion,url,createdAt` succeeded with `[]`; there is no eligible exact-HEAD run or result. This is an availability observation, not a CI pass. Local host facts were Linux/amd64 and Podman `Rootless=false` (`slirp4netns`), so this host cannot discharge the required rootless or native-Mac evidence. A separately reported non-short Linux warm-up is not Mac or rootless evidence and is not counted here. No product/source behavior was changed by the portable model and Mac guard tests.

### Rootless Linux and native Mac

The nested rootful probe is a measured parity result: with effective `CAP_SYS_ADMIN`, the jail remounted both the launch-time and injected read-only binds writable and writes changed both disposable host sentinels. That is **parity, not resistance**. It does not measure rootless behavior or authorize a capability-policy change; see [M1 and its remount result](../design/live-config-apply.md#LA-M1). The canonical instruments are in [`integration/livectxmount_test.go`](../../integration/livectxmount_test.go).

Run the following commands from a clean isolated checkout of source commit `201c4009e23b8ce4d99519eed95ffc656f565800`; this documentation reconciliation is a docs-only descendant and does not alter that code snapshot. On **each** real rootless Linux host (amd64 and arm64), retain the exact source commit, OS/architecture, Podman rootless and network identity, `CAP_SYS_ADMIN` disposition, and verbose executed/skipped test ledger. Require the tested source commit to be `201c4009e23b8ce4d99519eed95ffc656f565800`, `Linux`, the matching architecture, rootless `true`, and actual non-skipped PASS results for both named live-mount tests. A capability-related skip is not remount evidence. Use the existing focused command:

```sh
git rev-parse HEAD
go env GOOS GOARCH
podman info --format '{{.Host.Security.Rootless}}'
podman info --format '{{.Host.RootlessNetworkCmd}}'
YOLO_NO_PROGRAM_READINESS=1 go test -count=1 -timeout 0 -v -run '^TestLiveCtxMount(InjectAndRemove|ReadOnlyRemountParity)$' ./integration
```

On a native Mac already provisioned for the repository's sandbox/Nix contract, use that same clean source checkout and retain its exact commit, native Darwin architecture and executed/skipped ledger; require an actual non-skipped run of the current suite, including the source-guard tests. Do not acquire privilege or run setup under this task's authority. Existing command and privilege boundary: [the Mac suite's done contract](../design/macos-user-suite-on-a-developer-mac.md#12-what-done-looks-like).

```sh
git rev-parse HEAD
go env GOOS GOARCH
YOLO_TEST_MACOS_USER=1 go test -count=1 -timeout 0 -v -run '^TestMacosUser' ./integration
```

The present Linux/amd64 host and its non-rootless Podman cannot substitute for this run.

### Real broker, owner decision, and release boundary

One real baseline request succeeded in the earlier assessment at `9ab03637c` through the existing forwarding route: `gh repo view mschulkind-oss/yolo-jail --json nameWithOwner --jq .nameWithOwner` returned `mschulkind-oss/yolo-jail`. This proves only that metadata read under the existing scope; it is not before/after evidence, does not show widening or narrowing, and says nothing about the host broker's version or capability. The frozen jail binary stamp `1a9ded40b` predates the live-scope implementation, but the **host** broker binary and its bound `live_scope` capability were not observed; infer neither from the jail stamp. The relevant authority and transaction contract remains [§7, GitHub scope live](../design/live-config-apply.md#7-github-scope-live) and [Task B](../design/live-config-apply-plan.md#task-b-live-github-scope-independent).

The remaining before/after test requires an operator-owned disposable workspace and two explicitly approved real repository identities, A and B; an agent must not invent B. The operator must use host `yolo` built from the landed live-apply tree, establish that the bound host broker scope has `live_scope: true`, and retain the broker lifetime identity. With A approved, retain a successful A request; use the existing human `yolo config apply` gate to approve adding B, then retain B's successful request; use that same gate to remove B, then retain the next-request refusal for B. The broker lifetime identity must remain stable through the sequence. If host capability is absent, host yolo must be updated and the disposable fixture freshly launched before testing. Never edit the scope file directly, copy the jail endpoint credential, or record endpoint addresses, certificates, tokens, credential fields or sensitive logs.

At the time of this assessment, [OQ-SP5](../design/per-session-profiles.md#decision-ledger) remained
unanswered: its Answer was blank, and its A/B/C alternatives were for the owner to rule. The
follow-up ruling is recorded below. A likely patch release is not a selected version or publication
authorization; no version, date, tag, push or release is authorized or recorded here.

At that time, this update corrected the current blocker and source-attribution record only. It
added no implementation or tests and changed no capability policy, grants, publication authority
or then-open [OQ-SP5](../design/per-session-profiles.md#decision-ledger).

### Owner's follow-up rulings (2026-10-19)

The assessment above predates the owner's rulings. The former [OQ-SP5](../design/per-session-profiles.md#decision-ledger) is settled in the
[per-session profiles Decision Ledger](../design/per-session-profiles.md#decision-ledger): under
`host_management: own`, edits from an ended host session home use ordinary managed-config capture
into the standard overlay for later managed renders, without wholesale generated-file copying or
native host settings-file writeback. SP-R1 also requires `own` for host command execution; under
`none` or unset, yolo provides no host-launch assistance. These rulings are documentation only at
this stage; implementation remains in the [bounded plan](../design/per-session-profiles-plan.md).
They do not authorize a release or change the earlier release-boundary record.

## Final combined verification at `09178659b`

The software source tested here was clean at `09178659bd98c3a974a1d83f5f2d8d572965c3fa`; the primary checkout's `main` was also clean at that exact commit before this documentation handoff. Ancestry checks against that source passed for the patch caller merge `08e6566c4` and full candidate `770d9c2c9`, the live-apply merge `6f7c72a25`, candidate `e80f3a2ae` and inspected-record repair `388ea8a1b`, plus the model and Mac source/test history (`bfb4f6f47`, `203182fd4`, `082d3cab3`, `98715e67b`, `41dd11e3f`, `4bc941d53`, `cab34b522`, `201c4009e`). The prior documentation-only repair `efe0d88f1` is also an ancestor. This confirms the requested histories were already integrated; nothing was re-merged or reimplemented.

The single full local integration run used the existing Go cache and Podman setup, without changing either:

```sh
YOLO_NO_PROGRAM_READINESS=1 go test -count=1 -timeout 0 -v ./integration
```

It ran on Linux/amd64 with Podman `Rootless=false`; it **passed** with package result `ok ... 744.650s`, outer elapsed time 750 seconds, exit 0 (2026-10-19 01:24:07–01:36:37 UTC). The observed verbose ledger contains 793 test-result events including nested subtests: 633 PASS and 160 SKIP. Both `TestLiveCtxMountInjectAndRemove` and `TestLiveCtxMountReadOnlyRemountParity` passed on this rootful host. `TestARootlessCopyLandsInPodmansStoreWhateverStorageConfSays` skipped because Podman is not rootless. The macos-user integration summary was `executed=0 skipped=111` because this was Linux and `YOLO_TEST_MACOS_USER` was unset; the Mac-only integration behavior was not exercised.

The exact-HEAD `just check-ci` result for source `09178659bd98c3a974a1d83f5f2d8d572965c3fa` is already recorded as passed in the retained worker/scout receipts (`subagent-worker-682ed780-27a2-45a0-9307-7bd62382df94-1` and `subagent-scout-05163b44-3270-479b-8ae6-51941417c41c-1`). Source remained unchanged at that commit, so this task reused that fast-gate provenance and did not rerun it.

### Bounded source excerpts and regression identities

These are literal selected lines from the original first-parent diff for `770d9c2c9` and original diffs for `e80f3a2ae` and `026ab4de`. Original file and hunk headers and every shown source line are retained; omitted lines are described outside the fences. See [PF-D98](../reference/patched-forks.md#PF-D98) for the selected-pack identity contract. The named pack manifest is decoded before this caller hydrates identity. The mapping helper accepts only a host-recorded relative contribution whose workspace-resolved source is the exact directory named to `series check`. Its record/path setup and mapping-map initialization are omitted here:

```diff
diff --git a/internal/cli/patchedseriescheck.go b/internal/cli/patchedseriescheck.go
@@ -106,6 +93,64 @@ func loadPatchedPackDir(dir, name string, errw io.Writer, color bool) (*packload
+	namedDir, ok := resolve(dir)
+	if !ok {
+		return
+	}
+	for _, entry := range entries {
+		for owner, mapping := range entry.PatchBranches {
+			stagedSlug, contribution, ok := strings.Cut(owner, "/")
+			if !ok || stagedSlug != filepath.Base(filepath.FromSlash(entry.Dir)) || !mapping.Mapped {
+				continue
+			}
+			mappedDir := filepath.Join(e.WorkspaceDir(), filepath.FromSlash(mapping.Directory))
+			resolvedMapping, ok := resolve(mappedDir)
+			if !ok || resolvedMapping != namedDir {
+				continue
+			}
+			for _, declared := range p.Decl.Contributions() {
+				if declared.ExtensionName() != contribution || !packsrc.IsRelativeLocator(declared.PatchBranch) {
+					continue
+				}
+				mappings[p.StagedSlug()+"/"+contribution] = packload.PatchBranchMapping{
+					Repository: mapping.Repository, Directory: mappedDir, Mapped: true,
+				}
+			}
+		}
+	}
```

The original caller hunk adds hydration after named-directory load/validation:

```diff
diff --git a/internal/cli/patchedseriescheck.go b/internal/cli/patchedseriescheck.go
@@ -187,6 +234,7 @@ func packSeriesCheck(args []string, out, errw io.Writer, color bool) int {
+	hydrateSelectedWorkspacePackMappings(p, abs)
```

`TestSeriesCheckHydratesOnlyTheExactSelectedWorkspacePackIdentity` ([source](../../internal/cli/patchbranchseriescheck_test.go#L74)) uses distinct host/guest clones, removes mirrors, denies git execution, and rejects both a changed named manifest and an unrelated clone. An empty-token tombstone means the host cannot trust its mount record. Restoration accepts only the inspected launch token while that tombstone remains; a failed write invalidates the record and leaves the change pending. The shown lines come from the original repair diffs; unrelated lines in those hunks are omitted:

```diff
diff --git a/internal/cli/run/ctxmountrecord.go b/internal/cli/run/ctxmountrecord.go
@@ -154,19 +154,21 @@ func ctxMountRecordInvalidated(cname string, envLines []string) bool {
+func updateCtxMountRecord(cname, token string, entries []ctxMountEntry) error {
+	if token == "" {
+		return errors.New("cannot restore a live-mount record without the inspected launch token")
+	}
+	rec, err := loadCtxMountRecord(cname)
+	if err != nil {
+		return err
+	}
+	if rec.Token != "" {
+		return errors.New(ctxMountRecordPath(cname) + " no longer carries the invalidation tombstone")
+	}
+	return writeCtxMountRecord(cname, token, entries)
```

```diff
diff --git a/internal/cli/run/liveapply.go b/internal/cli/run/liveapply.go
@@ -422,26 +455,41 @@ func (o *Options) runApplySteps(run *applyRun, plan []applyChange) []applyChange {
+func (o *Options) noteMountRecord(run *applyRun, token string, have []liveCtxMount, c *applyChange) bool {
+	if err := writeCtxMountRecordSeam(run.cname, token, have); err == nil {
+		return true
+	} else {
+		invalidateErr := invalidateCtxMountRecordSeam(run.cname)
+		c.outcome = outcomeNextRestart
+		c.reason = "the mount succeeded, but its host record could not be updated: " + err.Error()
+		if invalidateErr != nil {
+			c.reason += "; the stale record could not be invalidated: " + invalidateErr.Error()
+		}
+		return false
+	}
+}
```

Regression identities are `TestConfigApplyRestoresTheInvalidatedMountRecordThroughRealApply` ([source](../../internal/cli/run/liveapply_test.go#L402)), `TestInvalidatedCtxMountRecordCannotClaimStaleMounts` ([source](../../internal/cli/run/ctxmountrecord_test.go#L199)), `TestConfigApplyFilesystemRecordRestoreFailureKeepsRemovalPending` ([source](../../internal/cli/run/liveapply_test.go#L548)), and `TestConfigApplyRecordFailureInvalidatesMountStateAndKeepsRemovalPending` ([source](../../internal/cli/run/liveapply_test.go#L623)). The real-apply lifecycle test mocks namespace mutation only; these tests are not rootless or native-Mac runtime evidence.

### Residual platform and service obligations

This run does **not** settle rootless live-mount behavior on Linux/amd64 or Linux/arm64, a real broker's approved-scope widening/narrowing exchange, or native-Mac execution. The rootful remount-parity pass is not evidence of rootless resistance: nested Podman forces the rootful path, as [M1](../design/live-config-apply.md#LA-M1) and the [live-apply implementation plan](../design/live-config-apply-plan.md) state. The Mac suite remains subject to its existing [developer-Mac contract](../design/macos-user-suite-on-a-developer-mac.md#12-what-done-looks-like). No agent session/API, broker credentials, real scope change, host grant, publish, tag, release, or deployment was attempted or authorized. This handoff changes documentation only; no tests were added or updated here. The next verification remains those three obligations under their existing owner and environment requirements.
