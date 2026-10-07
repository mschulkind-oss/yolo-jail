# Exact-commit pre-tag release gate

`just release VERSION` submits the current clean, full `HEAD` SHA as data to `release-request.yml` on `main`; it does not create a local tag or substitute a newer main commit. The host checks the notes, official pack-binary pins and main ancestry. Dispatch acceptance is not publication success.

## Trusted source and capability boundaries

Each workflow checks out its own main dispatch SHA for orchestration, with checkout credentials unpersisted. `preflight.sh` requires a main branch context, exact commit identity, refreshed main ancestry and the regular `ci.yml` main/push run for the exact repository/SHA. The CI gate checks workflow identity, attempt-specific required jobs and architectures/shards, and refreshes its proof. Missing, failed, cancelled or timed-out proof refuses.

Only after eligibility does a separate **read-only** job check out target source under `target/`. Its notes, official binary pins, source bundle and wheels are checked/built without publication secrets. A new trusted-main job, which checks out no target source, refreshes exact CI, main/tag identity and version order immediately before creating the annotated tag and its create-only ref. A preparation or final-CI failure leaves the version unreserved. An existing ref, even at the same SHA, refuses; no force, delete or move operation is provided.

The request concurrency group is per version, without cancelling a running request. GitHub can replace a pending request with another pending request; this is not durable deduplication. The immutable ref creation is the final conflict boundary. Release and registry workflows share a global concurrency group to serialize release-order checks and `latest` writes. That group also has only one retained pending run, not a durable queue. Different-version requests can still leave a reserved tag if subsequent publication refuses or a pending publisher is replaced; the system is not transactional.

## Release assets and the original successful run

The request dispatches `release.yml` on main with the frozen version/SHA and originating request run ID. A real first, in-progress main release-request run with the exact request title must authorize initial Release creation. A matching tag or existing release object alone is not authorization.

GoReleaser **v2.18.2** prepares target builds in a read-only job using `release --clean --skip=publish --release-notes FILE`. The target config must byte-match the trusted config. Target hooks never receive release, tap, registry or OIDC publication credentials. There is no later GoReleaser invocation in a write-capable job and no assumed `continue`, artifact reuse or replay contract.

The official [quick start](https://goreleaser.com/quick-start/#a-note-about-the-flags) documents `--skip=publish`; the pinned CLI's help and [release implementation](https://github.com/goreleaser/goreleaser/blob/v2.18.2/cmd/release.go) supply the version-specific command contract. `TestPinnedRealGoReleaserReadonlyPreparationFeedsActualPackagingCaller`, enabled with `GORELEASER_TEST_BINARY`, runs that actual CLI offline against a tiny Go project using the production archive matrix, source-bundle layout and checksum/extra-file settings. It replaces expensive project hooks with inert fixture inputs. The production packaging step consumes the observed `dist/artifacts.json` records, archives, checksums and staged pack files, rejecting unknown/missing paths, unsafe file kinds, duplicate records and checksum disagreement. This fixture is not a full production release build or a live upload proof.

The write job runs trusted code only. It re-renders notes from the target Git blob as inert text, verifies the complete expected asset names/types/digests against trusted config and declarative target manifests, refuses an existing release, creates an initial draft, uploads all files without clobber and finalizes that draft only after every upload succeeds. The four platform archives retain the host binary and prebuilt source bundle; declared official pack binaries and checksums remain separate assets. The body is the exact target changelog section with tag-pinned links; prerelease versions retain the prerelease flag. Initial draft finalization is allowed, but an existing complete or partial release is never replayed or overwritten. Homebrew's source-build formula is updated by trusted main code.

The normal Release `run-name` is exactly `Release vVERSION @ FULL_SHA / request POSITIVE_RUN_ID`. The request waits for exactly one original, first-attempt matching Release run to complete successfully before dispatching `publish.yml` on main. Failed, rerun, multiple, unknown or timed-out runs refuse; neither parallel dispatch acceptance nor release-object presence substitutes for success. The tap checker receives the inert title as `workflow_run.display_title` and enforces the requested version. Missing/malformed normal main metadata refuses, rather than comparing the tap against itself. Historical tag-trigger metadata remains decodable.

## One-shot registry publication

`publish.yml` keeps its filename, direct `uv publish ... --trusted-publishing always` call, `pypi` environment and `id-token: write` identity. Before any build, trusted preflight verifies exact CI/tag/main identity, monotonic published-release version order and the exact original request/successful Release run IDs and titles. A legacy release without this request provenance cannot use this route.

A separate trusted write job repeats these checks and creates the fixed-name `yolo-publication-claim.json` asset on the already-published release. It binds tag, SHA and request/Release/publisher run IDs. The [asset upload API](https://docs.github.com/en/rest/releases/assets#upload-a-release-asset) refuses duplicate names; the caller never deletes, renames, clobbers, retries an ambiguous upload or silently recreates the claim. Duplicate, rerun, partial, older-version and unknown-state attempts fail closed before target builds or registry writes. The claim is workflow-retained, **not administrator-immutable**, and it does not prove registry completion.

Target wheels, Nix closures and builder images are built in separate read-only jobs without publishing secrets. Publishing jobs execute trusted tools on inert handoffs only: wheels undergo exact platform/name/version/ZIP checks before direct uv upload; Nix store paths are validated and imported as data; image archives are checked for regular-file and platform identity before trusted registry copy/index commands. Publication remains nontransactional across PyPI, Cachix and GHCR. The uv `--check-url` option is not a claim of wheel hash equality or general recovery; the create-only claim forbids cross-run replay of this route. A later failed job leaves the claim and all partial publication state for owner inspection. Each registry-writing job independently refuses any Actions attempt other than 1 at its first step, before credential actions or writes; a failed-jobs-only rerun cannot reuse cached successful preflight/claim prerequisites to publish again. The normal release-upload and claim jobs have the same entry refusal. The separate Homebrew-only backfill contract is unchanged.

## Main-scoped Homebrew-only backfill

The retained version-only `release.yml` dispatch defaults to `homebrew-only` on **main**, labeled `Homebrew-only vVERSION`. Trusted current tools resolve the immutable old tag and require a non-draft published release, exact main/CI/tag proof and read-only target notes/pin checks. The old source need not contain the new release-wiring tools. The formula write runs from a separate trusted main checkout; GoReleaser and the registry publisher are skipped. This explicit legacy backfill preserves the tap checker's versionless behavior; only normal Release titles carry its required expected version. A historical target still must satisfy the current eligibility gate; this is not a waiver for missing CI or unreadable release state.

## Partial state and validation limits

After tag creation, any dispatch, build, upload, claim or registry failure can leave partial state. Do not rerun the request, delete/move the tag or claim, clobber assets, or use generic `gh run rerun --failed`. Inspect the immutable SHA and original runs read-only, then ask the owner to review the outcome before any further write:

```console
git ls-remote origin refs/tags/vVERSION
gh run list --repo OWNER/REPO --workflow release-request.yml --limit 20
gh run list --repo OWNER/REPO --workflow release.yml --limit 20
gh run list --repo OWNER/REPO --workflow publish.yml --limit 20
gh run view RUN_ID --repo OWNER/REPO
gh release view vVERSION --repo OWNER/REPO
```

There is no tag-push or release-event publisher trigger in the new workflow source. Human-created tags have already reserved a version and do not acquire normal publication authorization by object presence. The Homebrew-only route is the sole retained explicit backfill; there is no generic automatic publication recovery.

Tests execute actual workflow shell callers with local Git objects, stateful fake APIs/registries, and mutation regressions, plus the optional pinned real CLI fixture. They do not run GitHub Actions or establish live permissions, display-title transport, OIDC exchange, PyPI/registry acceptance or administrator behavior. The parent's read-only PyPI environment policy inspection supports main-source identity compatibility, not live OIDC proof. Independent review and the parent's combined landing gate remain required; this proposal changes no repository settings, secrets, live tags or published state.
