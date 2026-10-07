# Release-wiring helper contract

`release-wiring` is a trusted, inert-data boundary. It validates data and workflow identity; it must never execute a target commit's hook, GoReleaser config, manifest command, artifact, wheel, or other supplied code. Any caller that prepares artifacts must do so in the read-only stage and pass only downloaded files plus independently trusted checkout/config inputs to these commands. Publication jobs must validate the complete input set before issuing writes.

## CLI and caller contract

The command spellings and arguments are a shared interface with the release workflow integrator:

- `validate-assets --repo-root ROOT --sha FULL_SHA --version VERSION --trusted-config CONFIG --dir DIR` prints JSON asset basenames only after the exact expected asset set and `checksums.txt` digests validate. Default `--repo-root` is `.`, `--trusted-config` is `.goreleaser.yaml`, and `--dir` is `release-assets`.
- `claim-publication` takes no arguments. It consumes `GITHUB_REPOSITORY`, `RELEASE_VERSION`, `RELEASE_SHA`, `REQUEST_RUN_ID`, `RELEASE_RUN_ID`, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`, `GITHUB_REF_TYPE`, `GITHUB_REF_NAME`, `GITHUB_EVENT_NAME`, `GH_TOKEN` or `GITHUB_TOKEN`, `GITHUB_API_URL`, and `GITHUB_UPLOADS_URL`. It only attempts the fixed-name create-only claim after verifying the tag commit and an existing non-draft release. The write caller must separately verify publisher provenance and release-version order first.
- `check-version-order` takes no arguments. It consumes `GITHUB_REPOSITORY`, `RELEASE_VERSION`, `GH_TOKEN` or `GITHUB_TOKEN`, `GITHUB_API_URL`, and optional `RELEASE_ORDER_ALLOW_CURRENT=1`. The current-version exception is for the publisher step only, after successful original-release provenance is verified; it is not permission for a new release request or replay.
- `verify-release-request` takes no arguments. It consumes repository/version/SHA/request-run identity, read-only GitHub API credentials, and main `workflow_dispatch` context. It expects an in-progress first request run titled exactly `Release request vVERSION @ FULL_SHA`.
- `verify-publisher-provenance` takes no arguments. It consumes repository/version/SHA/request-run/release-run identity, read-only API credentials, and main `workflow_dispatch` context. The release run must be the exact successful first main-sourced `Release vVERSION @ FULL_SHA / request POSITIVE_RUN_ID` run. The request identity must match the exact request title.
- `validate-wheels --dir DIR --version VERSION` prints a JSON list only when the directory contains exactly the six expected platform wheel basenames, each a regular, bounded ZIP with matching distribution name/version metadata and no unsafe, duplicate, or path-conflicting members. Default directory is `dist`.

Run IDs are positive decimal Actions run IDs. Commit identifiers are full lowercase 40-character SHAs. The version grammar is the repository's `X.Y.Z` or `X.Y.Z-pre` form. Environment values are data, never shell fragments.

## Safety properties and boundaries

- Git blob/tree reads are data-only; the helper does not execute target code or parse target GoReleaser YAML as an executable configuration. A target `.goreleaser.yaml` must byte-match the separately supplied trusted config; expected basenames come from that config's fixed release archive shape plus the target's declarative pack binary manifests.
- Filesystem inputs must be real directories and regular files, with exact names, bounded size, no symlink acceptance, and verified checksums for release assets. Wheel ZIP metadata paths/types and collisions are checked without extracting or executing wheel contents.
- GitHub API errors are status-only; response bodies are discarded, API URLs must be HTTPS without userinfo/query/fragment, API redirects are not followed, and claim upload outcomes other than a definite create are treated as ambiguous/conflicting with no automatic retry, deletion, or clobber.
- Claim creation is not proof of registry completion and is not administrator-immutable. The caller must gate it on exact request/release-run identity and order checks. Legacy versions without provenance, reruns, duplicates, partial/unknown outcomes, and older-version publication refuse.

## Integration dependency / validation status

The workflow callsites are owned by the separate flow integrator. At the component base they still lack the main-scoped `run-name`, supplied environment, and helper invocations, so `TestMainScopedReleaseCallerUsesAnchoredRunNamesAndTrustedMainInputs` is intentionally retained as a red integration assertion until that caller lands. The helper APIs themselves are tested offline. These tests do not establish actual pinned GoReleaser artifact preparation/output behavior, Actions display-title transport, live GitHub/PyPI identity acceptance, or registry writes; the workflow integrator/reviewer must establish the preparation contract from supported pinned tooling or report that blocker without assuming it.
