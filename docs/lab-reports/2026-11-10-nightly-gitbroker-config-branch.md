---
title: "Nightly Git broker fetch failure"
date: 2026-11-10
status: open
tags: [lab-report, git-broker, integration, macos, podman, ci]
summary: "Nightly 42833734953 failed during fetch with a missing object; the empty-destination TLS-broker reproduction and independent cleanup-race probe now pass locally, while the rootless Mac transport witness remains pending."
---

# Nightly Git broker fetch failure

## Observed

Nightly run [42833734953](https://github.com/mschulkind-oss/yolo-jail/actions/runs/42833734953),
`integration-macos (5)`, failed at `TestGitBrokerConfigBranch` on the second (`without` branch grant)
fixture. The preceding successful push and later push-refusal/request/approval text are expected test
behavior, not the failure. The failing command's `git fetch -q origin` reported:

```text
fatal: bad object dfda6460dc2908d5754b5b263183a52a39e23371
error: fixture.test:o/r.git did not send all necessary objects
```

The host daemon reported its `git-upload-pack` request exited 0 and emitted 531 bytes. That is host-side
output evidence only: neither the exit status nor crossing socket-write counters establish that Git
received or stored the objects. The original root cause is not proven. The exact failed job log is
preserved at the public CI job link above; a local copy is retained outside the repository.

## Landed diagnostic baseline

- Main commit `3b1be0bc15d1` added failure diagnostics for the fixture's upstream object id, host bare
  repository `git fsck`, fake-SSH argv, broker log, and host-side git-broker crossing byte counts.
- The crossing counts measure socket writes on the host side, not client receipt or object storage.
  The corrected W-GB1 below adds distinct fetch/push exit statuses, client `git cat-file` and `git
  fsck` results, and fixture-only packet tracing; these observations narrow, but do not conclusively
  distinguish, all transport, negotiation/helper, or client object-store failures.
- No production Git transport change has been made. The Mac failure has not been reproduced, and no
  transport defect has been isolated.

## Separate Linux cleanup failure

The initial isolated Linux target run also failed during detached-writer cleanup, independently of the
Mac fetch failure. Its retained worker transcript records:

```text
revalidating pid 41557 (sh) HOME ownership while argv was empty: process environment was cleared
```

Evidence pointer: `2e6087c3-7755-48b7-91b7-398dc92172d6_worker_transcript.jsonl:61` (the failure is
also discussed in review `gitbroker-ci-investigation.md`). The subsequent green runs did not establish
that this error was harmless. The reported path was `integration/preparationworkers_test.go`'s
`reobserveTransientEmptyArguments`. An injected-probe regression case now exercises an already-owned,
classifiable process becoming same-identity/empty-argv/cleared-HOME and then disappearing. It requires
successful bounded waiting and no signal, while retaining fail-closed behavior for an unclassifiable
live process. With the old immediate-error condition restored, the focused test failed with the same
HOME-ownership/cleared-environment error; with the fix it passed along with the adjacent cleanup cases.
This covers the injected sequence, not every possible real-process cleanup race.

## Open

The exact Mac transport witness has not been run here. This Podman host is rootful
(`podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'` printed
`false slirp4netns`), and podman-in-podman forces host networking. The original Mac `bad object` failure
did not reproduce in the earlier local Linux target runs; that does not settle the Mac VM hop.

Run the rootless Podman witness in [the W-GB1 witness](../reference/git-broker.md#a-mac-witness-still-to-run-w-gb1)
on main at `2da5c65e8` or later (based on reviewed main `3b1be0bc15d1`). It selects the backend with
`YOLO_SETUP=podman-self`, requires five explicit `TestGitBrokerConfigBranch` PASS results and no SKIP,
and asks for the Podman rootless network mode to be recorded. A passing witness establishes
current-main behavior, not the original failure's cause. A failure must be interpreted with the
complete fixture diagnostics; host crossing counters alone are not proof of bytes received or stored
by Git.

## Evidence

- `gh -R mschulkind-oss/yolo-jail run view 42833734953 --job 129892741730 --log` — original 1,654-line
  job log, retained outside the repository.
- `git diff 4fe6d30d9 3b1be0bc15d1 -- integration/gitbroker_test.go internal/gitbroker` — verified that
  the integration fixture/fetch transport logic was unchanged (diagnostics were added); the diff
  included refusal-log advice changes in `internal/gitbroker/broker.go` (and its test), so the earlier
  “scoped sources unchanged” claim was too broad.
- The isolated Linux target integration test passed once with the added client diagnostics and separate
  fetch/push statuses. The host reports
  `podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'` →
  `false slirp4netns`; this rootful podman-in-podman run does not test the Mac rootless VM hop.
- `go test ./internal/gitbroker -run '^TestFetchIntoEmptyDestinationThroughTheBroker$' -count=10 -timeout 3m`
  — passed ten transfers through the real helper/TLS broker from a bare clone of an empty seed into a
  separately initialized empty destination; commit, tree, and client fsck checks all passed.
- `go test ./integration -run '^(TestStopHomeProcessesWaitsForOwnedEmptyArgvWithClearedHOMEToDisappear|TestStopHomeProcessesWaitsOutAnEmptyArgvUnderUncertainHOME|TestStopHomeProcessesReobservesClearedHOMEAcrossExecTransition)$' -count=1 -timeout 3m`
  — passed the new injected case and the adjacent cleanup ownership cases. With the old immediate-error
  branch temporarily restored, the isolated new test failed on the same cleared-HOME/empty-argv error
  and sent no signal; with the change restored it passed.
- `YOLO_SETUP=podman-self go test -run '^TestGitBrokerConfigBranch$' -count=1 -timeout 18m -v ./integration`
  — passed on Linux and printed `--- PASS: TestGitBrokerConfigBranch`; its with-grant branch reported
  fetch/client checks `0` and push `0`, and its no-grant branch reported fetch/client checks `0` and
  push `1` with the expected approval refusal. This is not W-GB1 because the host is rootful.
- `python3 scripts/completion-census.py` — passed; `census: 176 packages, 18056 tests, 16 at a time`,
  `census: wrote scripts/completion-readers.json (1066 reading tests in 43 packages)`.
- `git diff --check` — passed; the scoped source and docs diff has no whitespace errors.
- `go env GOPATH GOCACHE GOMODCACHE GOOS GOARCH` — `/home/agent/go`,
  `/home/agent/.cache/go-build`, `/home/agent/go/pkg/mod`, `linux`, `amd64`; tests use the shared Go
  caches and normal HOME.
