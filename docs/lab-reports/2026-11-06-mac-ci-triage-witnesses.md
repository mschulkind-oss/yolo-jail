---
title: "Mac CI triage: the scheduled nightly and macos-user failures since 2026-10-27"
date: 2026-11-06
tags: [lab-report, ci, macos, macos-user, integration]
summary: "Every failure of the scheduled Nightly macOS Integration and macos-user backend runs from 11-03 to 11-06, classified from Linux. Five test and workflow fixes, one report-gap fix, and the witnesses only a Mac can settle."
---

# Mac CI triage: scheduled nightly and macos-user failures

**Triaged 2026-11-06 from a Linux jail**, against the runs' own logs and annotations. Nothing
was re-run or dispatched. The scheduled runs on 11-06 test `11e21ce18`, the last pushed
commit, which predates every fix below, so they will fail the same way until a push.

**Classes.** *Infra* is the runner or the network. *Defect* is a real yolo defect. *Test* is a
test or workflow defect: a wrong expectation, a flake, or a test left stale by a rename.

## Observed

| Run | Failure | Class | Disposition |
| :--- | :--- | :--- | :--- |
| Nightly 11-05 (42041252889, `163fd8fb`) shard 6 | `podman.pkg` download: six HTTP 504s within 26 s, then `curl` gave up | Infra | The retry now runs until its 180 s budget, not its count (`cdca5fc45`) |
| Same run, shard 3 | `TestConvertLaterSilencesTheOffer`: image build failed, the report quoting only "container builder did not start" | Defect (report gap) + Test | Report fixed (`0ec931135`); test no longer builds an image (`98de05bee`, see [§Open 1](#1-the-hello-image-build-on-the-mac)) |
| Same run, shard 8 | Every test passed (`ok` at 3597 s), then the 60-minute step cap fired | Test (shard balance) | Durations table regenerated (`b490cada3`); see [§Open 4](#4-the-60-minute-step-cap) |
| Nightly 11-04 (41841732393, the v0.16.1 tag commit `3ccc28def766`) | `TestTheJailsYoloCarriesItsMatchedBundleStamp` rejected `<tag>+g<hash>` | Test | Fixed (`60c1e7a63`); any release-tag commit failed it |
| Nightly 11-03 scheduled (41646540619) | `TestAnAttachIsNotOfferedTheConversion` timed out at 1203 s on a cold image build | Test | Already fixed on main by `5342558ff` |
| Nightly 11-03 dispatch (41710801947) shard 3 | `curl: (6) Could not resolve host: github.com` | Infra | Covered by `cdca5fc45` |
| Same run, shard 7 | 60-minute step cap | Test (shard balance) | `b490cada3` |
| Same run, shard 11 | `TestOfferAnsweredNoLaunchesOnTheOldFiles` timed out at 1205 s on the `hello` build | Test | Fixed (`98de05bee`), as in [§Open 1](#1-the-hello-image-build-on-the-mac) |
| macos-user 11-05 (42050716053, `163fd8fb`) | Footer test printed `guest` for the jail notch; DP-B19 header mismatch | Test (stale after the named-setups rename) | Main fixed both in `3bd66c7cf` and `ba501d31b`; `ba501d31b` set DP-B19 to `seatbelt-self`, which is wrong for the account launch, fixed in `b4ec77515` |
| Same run | `TestMacosUserTwoConcurrentLaunchesOfOneWorkspace`: "A none, B joined" | Test (stale keeper phrase) | Fixed (`7fbca26d3`) |
| macos-user 41852634686, 41656640903 | Vendor-install jobs cancelled, never acquired by a runner | Infra | `rerun-unacquired.yml` already re-runs these (41852634686 reached attempt 3) |
| Nightly 11-06 (42228358413, `11e21ce18`) shard 10 | "The hosted runner lost communication with the server" during checkout | Infra | None |

Two lines in those logs look like failures and are not. The AppleScript `-4960` belongs to the
passing `TestMacosUserPasteboardProbe` measurement. The sudo-narrowing "errors" are the
script's own echo text, and that step passed. No shard on 11-05 emitted "the podman machine
never came up"; that text is the step script's unreached error line.

## Fixed and committed

- `60c1e7a63` `test(integration)`: the matched-stamp check accepts both `version.Normalize`
  forms, `<tag>+g<hash>` and `<tag>+<n>.g<hash>`. A `-short` test pins the parser.
- `7fbca26d3` `test(integration)`: the HD10 verdict reads the guest-notch keeper spawn line
  ("this workspace's guest-notch macos-user host services") as well as the jail-notch one.
- `b4ec77515` `test(macos-user)`: DP-B19's Linux mirror tweaks the setup to
  `seatbelt-yolo-account`, the one `runMacosUser` launches, and the integration row expects
  that name.
- `0ec931135` `fix(image)`: when the plain macOS build fails and the Linux-builder offload
  fails too, the failure report keeps the plain build's newest lines, then the offload's, and
  names why the builder did not start (`containerbuilder.Session.Failure`). Before, the report
  showed one line and dropped the build's own error.
- `cdca5fc45` `ci(nightly-macos)`: `curl --retry 40` in both podman install copies, so
  `--retry-max-time 180` ends a CDN outage, not the count of five.
- `b490cada3` `ci(nightly-macos)`: `.github/nightly-macos-durations.txt` regenerated from the
  shard jobs of the last five runs. The simulated longest shard drops from 40–54 to 36–49
  minutes.
- `98de05bee` `test(integration)`: the config-migration tests convert an `env.set` witness
  instead of `packages: ["hello"]`, so none of them builds an image.

## Open

### 1. The hello image build on the Mac

The configmigration tests asked for `packages: ["hello"]` only to have something to convert.
On the Mac that forces a fresh image build, and on 11-05 the plain build failed and the
offload's builder did not start. `98de05bee` converts an `env.set` witness instead, so
these tests build nothing. The underlying build failure is still unexplained.

**Witnesses for the Mac agent**, on a commit carrying `0ec931135`:

- W1. Launch a throwaway workspace whose config asks for `packages: ["hello"]`, with the
  Cachix substituter as CI has it. Record the whole build-failure report. It should now show the
  plain build's tail, the separator line, and the offload's tail with the reason the builder
  did not start. Name which of the five `Start` reasons it was.
- W2. Run `TestConvertLaterSilencesTheOffer` and `TestOfferAnsweredNoLaunchesOnTheOldFiles`. Record
  that neither prints an image build.

**Decision needed:** whether W1's reason is a builder defect (fix in `internal/containerbuilder`)
or the runner's podman machine (report only).

### 2. The macos-user setup names

- W3. Run `TestMacosUserFooterSaysItsSetup` and
  `TestMacosUserBriefingAndLaunchLinesDescribeThisBackend`. Record that the DP-B19 header reads
  `setup seatbelt-yolo-account`.
- W4. Run `TestMacosUserTwoConcurrentLaunchesOfOneWorkspace`. Record the spawn line each launch
  printed, and that the verdict is one keeper and one join.

**Decision needed:** none if they pass.

### 3. The version stamp on a tag commit

- W5. On the next release tag's nightly, confirm `TestTheJailsYoloCarriesItsMatchedBundleStamp`
  passes on the on-tag form.

### 4. The 60-minute step cap

Shard 8 on 11-05 passed every test and was still cut off at 60 minutes. The regenerated table
leaves the longest shard at about 49 simulated minutes, which is little margin against runner
variance.

- W6. Read the next two scheduled runs' longest shard step time.

**Decision needed (owner):** if any shard passes 55 minutes, either add shards or raise the step
cap. Each costs runner minutes; neither is decided here.

### 5. The legacy home's move under the yolo account

Named setups slice 6a-ii moves a project's legacy home, `.yolo/home`, to the selected setup's
home, `.yolo/setups/<setup>/home`, on the first launch with no jail of the project running
([SUP-D9](../design/named-setups-plan.md#SUP-D9),
[SUP-D31](../design/named-setups-plan.md#SUP-D31)). Under `seatbelt-yolo-account` the yolo
account owns that home, and the invoking user renames it into a directory the user owns. Only
Linux has run the move. If APFS refuses the rename with `EACCES`, the move falls through to its
"left in place" line. That line's manual `mv` would fail the same way, and every later launch
would print it again.

The Mac run measured who owns such a home
([run report §4](2026-11-06-mac-ci-triage-witnesses-run.md#4-who-owns-a-seatbelt-yolo-account-home)):
the `.yolo/home` directory is the invoking user's, and only its contents are the yolo account's.
Renaming a directory needs write access only to the directory that holds it, `.yolo`, so
`EACCES` is not expected. W7 still stands as the live confirmation.

- W7. In a throwaway workspace, launch `seatbelt-yolo-account` once on a yolo from before 6a-ii,
  so the account lays `.yolo/home`. Then launch it once on a commit carrying 6a-ii. Record the
  launch's home line, and `ls -leO` of `.yolo/home` and `.yolo/setups/seatbelt-yolo-account/home`
  before and after the second launch.

**Decision needed:** none if the move prints "Moved this project's home". On `EACCES`, the move
needs a step the account can take, and its "left" line needs a remedy that works.

**Mac run, 2026-11-06 at `9e1e9d8d3`** ([report](2026-11-06-mac-ci-triage-witnesses-run.md)): W1's build passes natively, so the failure is not reproduced. W2 passes under `podman-self` with no image build, but both tests fail under `apple-container-self` on the second launch. W3 and W4 pass. W5 and W6 cannot run yet. The `apple-container-self` refusal and the `yolo ps` row finding the run reported: fix in progress, 2026-11-06.

## Evidence

- Run logs and job annotations: `gh run view -R mschulkind-oss/yolo-jail <id> --log-failed`,
  and the check-runs API for 42228358413 while it ran.
- `go test -short` on `./integration`, `./internal/image`, `./internal/containerbuilder` and
  `./internal/cli/run`, also with `TMPDIR` behind a symlink (the darwin path class). Clean.
- `GOOS=darwin go vet ./...`. Clean.
- `scripts/test_nightly_shard.py`. Passes against the new table.
- The five configmigration tests, run in a nested jail on Linux on `98de05bee`: all pass in 125 s, with no image build.
