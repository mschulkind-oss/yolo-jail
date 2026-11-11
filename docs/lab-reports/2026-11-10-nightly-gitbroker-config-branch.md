---
title: "Nightly Git broker fetch failure"
date: 2026-11-10
status: open
tags: [lab-report, git-broker, integration, macos, podman, ci]
summary: "Nightly 42833734953 failed in the no-grant half of TestGitBrokerConfigBranch because fetch itself reported a missing object; Linux repeats pass through policy behavior but cannot verify the Mac Podman VM transport."
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

The host daemon reported its `git-upload-pack` request exited 0 and emitted 531 bytes. This log does
not show whether the bare repository was complete before the fetch or whether the jail received all
bytes, so the root cause is not proven. The exact failed job log is preserved at the public CI job
link above; a local copy is retained outside the repository for this handoff.

## Fixed and committed

- `520b739e5db1` updates the failure diagnostic in `integration/gitbroker_test.go` to print the
  fixture's advertised upstream id, `git fsck` result for the bare repository, fake-SSH argv,
  host broker log and `git-broker` crossing byte counts.
- No production change was made: the macOS transfer failure was not reproduced and no transport
  defect has been isolated.

## Open

The exact Linux target test is not a Mac transport witness. This worker's Podman host is rootful
(`podman info --format '{{.Host.Security.Rootless}} {{.Host.RootlessNetworkCmd}}'` printed
`false slirp4netns`), and podman-in-podman forces host networking. The first isolated run exercised
both policy branches but exited red in detached-writer cleanup on a transient `sh` process whose
HOME had already been cleared. A subsequent single run and two `-count=3` runs exited 0; the cleanup
error did not recur, and the Mac bad-object failure did not reproduce.

Run the rootless Podman witness in [`git-broker-plan.md` § Mac checks](../design/git-broker-plan.md#mac-checks)
on main at `520b739e5db1` or later. The new failure evidence should distinguish a bad/incomplete
fixture repository from a host-to-jail stream discrepancy. No Mac result has been run in this
worktree. This remains an open CI failure until that witness either reproduces and identifies a
fixable cause or establishes the current main result.

## Evidence

- `gh -R mschulkind-oss/yolo-jail run view 42833734953 --job 129892741730 --log` — saved verbatim
  as the linked 1,654-line job log.
- `git diff 4fe6d30d9 HEAD -- integration/gitbroker_test.go internal/gitbroker` — empty before the
  diagnostic update; those scoped sources at the failed CI SHA matched this worktree.
- `go test -run '^TestGitBrokerConfigBranch$' -count=1 -timeout 8m ./integration` — failed after
  both branch behavior assertions passed, during the detached-writer cleanup's HOME ownership
  revalidation; not the macOS missing-object failure.
- `go test -run '^TestGitBrokerConfigBranch$' -count=1 -timeout 8m ./integration` — passed.
- `go test -run '^TestGitBrokerConfigBranch$' -count=3 -timeout 18m ./integration` — passed before
  and after the diagnostic update (both runs).
- `python3 scripts/completion-census.py` — passed; `census: 176 packages, 18052 tests, 16 at a time`,
  `census: wrote scripts/completion-readers.json (1065 reading tests in 43 packages)`.
- `go env GOPATH GOCACHE GOMODCACHE GOOS GOARCH` — `/home/agent/go`,
  `/home/agent/.cache/go-build`, `/home/agent/go/pkg/mod`, `linux`, `amd64`; builds used the shared
  caches and did not isolate HOME.
