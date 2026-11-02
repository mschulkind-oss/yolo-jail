# Container runtime (podman or container)
runtime := env("YOLO_RUNTIME", "podman")

# Preserve argument boundaries in recipes that forward variadic arguments.
set positional-arguments

default:
    @just --list

# One-time developer setup: toolchain (mise) + Go module deps.
setup:
    #!/usr/bin/env bash
    set -euo pipefail
    if command -v mise >/dev/null 2>&1; then
        mise install
    else
        echo "⚠ mise not found — install it (https://mise.jdx.dev) to get the" >&2
        echo "  pinned Go/Node/just/staticcheck toolchain from mise.toml." >&2
    fi
    go mod download
    echo "Setup complete. Next: just check"

# Build every cmd/ binary into dist-go/<goos>-<goarch>/
build-go:
    ./scripts/build-go.sh

# Stage the prebuilt "two files and a binary" bundle (share/yolo-jail/) an
# installed binary needs to build the jail image with no toolchain: flake.nix +
# flake.lock + bin/linux-{amd64,arm64}/. Cross-compiles both arches, so it needs
# a Go toolchain. `just install`, goreleaser, and the brew formula all run the
# underlying script; this recipe is the manual entry point (e.g. to inspect the
# bundle) and defaults to a build-output dir.
stage-bundle DEST="dist/bundle/share/yolo-jail":
    ./scripts/stage-source-bundle.sh {{ DEST }}

# Install the host binary as $GOBIN/<name> (default yolo; else $GOPATH/bin), replacing a deployed or dev launcher there
install name="yolo":
    #!/usr/bin/env bash
    set -euo pipefail

    # `just install` is a HOST-only operation. Inside a jail YOLO_VERSION is set,
    # and the baked /bin/yolo is version-locked to this jail's image. `go install`
    # drops a copy in $GOBIN (a mise Go dir that sits AHEAD of /bin on PATH and is
    # host-shared + persistent), silently shadowing the baked binary with a stale
    # one — the exact trap that makes a fixed jail look broken. In-jail you never
    # want that: rebuild the IMAGE, not a GOBIN binary.
    if [ -n "${YOLO_VERSION:-}" ]; then
        echo "✗ 'just install' is host-only — refusing inside a jail (YOLO_VERSION set)." >&2
        echo "  It would go-install a copy into \$GOBIN that shadows the baked /bin/yolo" >&2
        echo "  on PATH with a stale binary. To test Go changes here:" >&2
        echo "    just dev -- -- bash" >&2
        echo "  (the wrapper builds and invokes a matched launcher by path)." >&2
        echo "  To ship to the host, run 'just install' / 'just deploy' on the host." >&2
        exit 1
    fi

    python3 scripts/launcher-slot.py deploy --repo "$(pwd -P)" --home "$HOME" --name "$1"

# Build and print a matched development launcher without activating/installing it.
build-dev-bundle:
    python3 scripts/matched-bundle.py build-dev --repo "$(pwd -P)" --home "$HOME"

# Reuse/build the current matched artifact, then exec it with exactly these args.
dev *args:
    ./scripts/dev-wrapper.sh "$@"

# Install yolo CLI and prime the Claude OAuth broker state. Safe to re-run. `just deploy yolo-stable` installs under another name.
deploy name="yolo": (install name) (_host-state name)

# Like deploy, but $GOBIN/<name> becomes a link to this checkout's dev wrapper: every run builds (or reuses) a bundle matched to the checkout as it stands.
deploy-dev name="yolo": (_install-dev name) (_host-state name)

# Point $GOBIN/<name> at scripts/dev-wrapper.sh, replacing a deployed or dev launcher there.
_install-dev name:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -n "${YOLO_VERSION:-}" ]; then
        echo "✗ 'just deploy-dev' is host-only — refusing inside a jail (YOLO_VERSION set)." >&2
        echo "  In a jail, test Go changes with: just dev -- <args>" >&2
        exit 1
    fi
    python3 scripts/launcher-slot.py deploy-dev --repo "$(pwd -P)" --name "$1"

# The host state both deploy recipes keep current, whichever launcher is in the slot.
_host-state name:
    #!/usr/bin/env bash
    set -euo pipefail
    launcher="$(python3 scripts/launcher-slot.py path --name "$1")"

    # --- Retire pre-broker Claude token refresher install ---
    if command -v systemctl >/dev/null 2>&1; then
        for unit in claude-token-refresher.timer claude-token-refresher.service; do
            if systemctl --user is-enabled "$unit" >/dev/null 2>&1 \
              || systemctl --user is-active "$unit" >/dev/null 2>&1; then
                systemctl --user disable --now "$unit" 2>/dev/null || true
                echo "  retired legacy $unit"
            fi
        done
        rm -f "$HOME/.config/systemd/user/claude-token-refresher.service"
        rm -f "$HOME/.config/systemd/user/claude-token-refresher.timer"
        systemctl --user daemon-reload 2>/dev/null || true
    fi

    # --- Claude OAuth broker loophole (bundled) ---
    if ! command -v openssl >/dev/null 2>&1; then
        echo "⚠ openssl not found — skipping claude-oauth-broker state init"
    else
        if [ ! -x "$launcher" ]; then
            echo "ERROR: $launcher is missing or not executable after install" >&2
            exit 1
        fi

        # Retire stale copies of the manifest from pre-bundled installs.
        rm -rf "$HOME/.local/share/yolo-jail/modules/claude-oauth-broker"
        if [ -d "$HOME/.local/share/yolo-jail/loopholes/claude-oauth-broker" ]; then
            STATE_DIR="$HOME/.local/share/yolo-jail/state/claude-oauth-broker"
            mkdir -p "$STATE_DIR"
            for f in ca.crt ca.key server.crt server.key refresh.lock; do
                src_f="$HOME/.local/share/yolo-jail/loopholes/claude-oauth-broker/$f"
                [ -f "$src_f" ] && mv "$src_f" "$STATE_DIR/$f" 2>/dev/null || true
            done
            rm -rf "$HOME/.local/share/yolo-jail/loopholes/claude-oauth-broker"
            echo "  migrated legacy loopholes/claude-oauth-broker → bundled + state split"
        fi
        # Retire the pre-split systemd unit if present.
        if command -v systemctl >/dev/null 2>&1; then
            if systemctl --user is-enabled claude-oauth-broker.service >/dev/null 2>&1; then
                systemctl --user disable --now claude-oauth-broker.service 2>/dev/null || true
                rm -f "$HOME/.config/systemd/user/claude-oauth-broker.service"
                systemctl --user daemon-reload
                echo "  retired pre-split claude-oauth-broker.service"
            fi
        fi

        # Generate CA + leaf in the state dir (idempotent). Through a dev launcher this first run
        # builds the matched bundle, which proves the link works.
        "$launcher" internal daemon claude-oauth-broker --init-ca >/dev/null

        echo "✓ claude-oauth-broker state primed at $HOME/.local/share/yolo-jail/state/claude-oauth-broker"
    fi

    # Nothing to restart: each jail starts its own host daemons and ends them with it (HD-R1,
    # docs/design/host-daemon-ownership.md), so a running jail keeps the ones it started and the
    # next launch starts this deploy's.
    echo "  Running jails keep the host daemons they started; launch a jail again to use this build's. \`yolo host-daemon status\` lists them."

    echo "yolo-jail deployed as $launcher. Verify: $1 loopholes list"

# Undo `just deploy`/`just install` (any platform) so a packaged yolo runs; `just undeploy --dry-run` previews.
undeploy *args:
    sh scripts/undeploy.sh "$@"

# Build the container image using Nix
build-image:
    nix --extra-experimental-features 'nix-command flakes' build .#ociImage .#imageCopier

# Build the minimal image variant used by CI integration (no chromium,
# gcc toolchain, nested-podman, or debug tools — ~1.6–2 GB smaller).
build-image-minimal:
    nix --extra-experimental-features 'nix-command flakes' build .#ociImageMinimal

# Build and DELIVER the image into the container runtime.
#
# Since layer-aware delivery landed (docs/reference/image-staging-vs-baking.md#delivering-into-the-runtime)
# `./result` is a nix2container image.json, not a script whose stdout is a
# docker-archive, and the thing that reads it is the patched skopeo `build-image`
# realizes beside it at ./result-1. `skopeo copy` negotiates per blob with
# containers-storage, so a re-load after a flake.nix-only edit moves ~26 MB
# instead of 3.4 GB.
#
# IT DISPATCHES ON THE RUNTIME, and it did not until 2026-09-19 — it wrote to
# `containers-storage:` unconditionally, which is podman's store and not a place
# Apple Container ever reads. So on a Mac running that backend `just load` was a
# SILENT no-op against the runtime in use: it reported success, moved bytes, and
# left the image the tests actually resolve exactly as stale as before. The
# harness's own degraded message says "run `just load`", so following the
# instruction did nothing and said nothing.
#
# The Apple Container arm is the archive hop yolo's own launch path takes
# (internal/image/autoload.go, deliverViaArchive): that backend has no
# containers-storage. Its images service runs on the Mac and keeps blobs in a
# content store there, whose only ways in are `container image load` and `pull`,
# so there is nothing to negotiate blobs with and the whole image crosses as one
# file (docs/reference/image-staging-vs-baking.md, "The delta archive"). The file is removed
# afterwards — a leftover is what makes the NEXT copy fail, since skopeo will not
# write over an existing archive.
#
# THE BRANCH IS THE LAUNCH'S: only podman on Linux writes containers-storage, and
# every other runtime takes the archive and its own loader (image.ImageLoadCmd:
# `container image load -i`, else `<runtime> load -i`). That is also exactly what
# `yolo internal image-copy` accepts, so podman on a Mac — whose store is inside
# the podman machine — no longer reaches the verb only to be refused.
load: build-image
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{ runtime }}" != "podman" ] || [ "$(uname -s)" != "Linux" ]; then
        archive=$(mktemp -t yolo-jail-image.XXXXXX.oci)
        rm -f "$archive"
        trap 'rm -f "$archive"' EXIT
        ./result-1/bin/skopeo --insecure-policy copy \
            "nix:$(readlink -f ./result)" \
            "oci-archive:$archive:yolo-jail:latest"
        if [ "{{ runtime }}" = "container" ]; then
            container image load -i "$archive"
        else
            "{{ runtime }}" load -i "$archive"
        fi
    else
        # THE LAUNCH'S OWN COPY, not a shell spelling of it: `yolo internal
        # image-copy` reads `podman info` once and runs image.DeliveryCopyArgvFor —
        # `podman unshare --` for a rootless store, and on a rootless podman the
        # store podman reports named on the destination with its driver options,
        # because a copier left to its own storage.conf lookup can resolve one
        # podman does not read (issue #47). `go run` because this recipe must not
        # depend on which `yolo` happens to be installed.
        go run ./cmd/yolo internal image-copy --runtime "{{ runtime }}" \
            --copier ./result-1/bin/skopeo \
            --image "$(readlink -f ./result)" \
            --ref localhost/yolo-jail:latest
    fi

# Build BOTH image variants on a Linux host and push their closures to the
# Cachix cache, so macOS users download the prebuilt image (no Linux builder
# needed).
cachix-push CACHE="yolo-jail":
    @command -v cachix >/dev/null || {{ '{ echo "cachix not found: nix profile install nixpkgs#cachix"; exit 1; }' }}
    nix --extra-experimental-features 'nix-command flakes' build .#ociImage --print-out-paths --no-link | cachix push {{CACHE}}
    nix --extra-experimental-features 'nix-command flakes' build .#ociImageMinimal --print-out-paths --no-link | cachix push {{CACHE}}
    # The copier is the one attr here that no PUBLIC cache serves: nix2container's
    # `nix:` transport is a patch over nixpkgs' skopeo. Pushing it is an
    # OPTIMIZATION and may never become load-bearing (OQ-LI1) — a miss means the
    # consumer builds it, and that is all it means.
    nix --extra-experimental-features 'nix-command flakes' build .#imageCopier --print-out-paths --no-link | cachix push {{CACHE}}
    @echo "Pushed both image variants and the copier to https://{{CACHE}}.cachix.org"

# Run all tests (Go unit + Go container integration suite)
test:
    python3 scripts/go_packages.py run go test -short
    # YOLO_TEST_REAL_PACK_INSTALLS keeps the tests that install a shipped pack's program
    # from its VENDOR. CI does not set it on the push path — that question is asked on a
    # `packs/**` change and weekly instead, because no commit can cause a vendor's release
    # to break (docs/reference/agent-install-in-ci.md#three-triggers-matched-to-three-causes). A local full run wants
    # everything, so this recipe asks for it.
    YOLO_TEST_REAL_PACK_INSTALLS=1 python3 scripts/go_packages.py run go test -count=1 -timeout 0 -- ./integration

# Run fast tests only (skip container integration tests).
# -timeout is per PACKAGE: internal/cli alone takes about seven minutes on a CI
# runner, close enough to go test's default of ten that a slower runner trips it
# with no test hung.
test-fast:
    python3 scripts/go_packages.py run go test -short -timeout=20m

# Run linter (Go: vet + staticcheck) ONCE PER BUILD CONFIGURATION THIS TREE
# TARGETS — not once per machine.
#
# A `//go:build !linux` FILE IS INVISIBLE TO A SINGLE-GOOS GATE. Platform
# primitives here are split `_linux.go` / `_other.go`, and the toolchain
# type-checks and analyzes only the half the current GOOS selects. One pass on a
# Linux machine therefore never reads the non-linux halves, nor any darwin-only
# file (`internal/macosuser`'s darwin tests included), so findings in them
# accumulate silently until a from-source contributor on macOS meets the backlog
# on their first `just check` — which is GitHub issue #42. Both GOOS values are
# named explicitly so `just lint` means the same thing on a Mac as it does here;
# the bare `staticcheck ./...` it replaced meant "whichever half my laptop
# compiles".
#
# THE GATE IS HERE AND NOT IN ci.yml's `check-macos` JOB because the class needs
# a GOOS, not a Mac: `GOOS=darwin staticcheck ./...` reproduces issue #42 from
# Linux. Putting it on the macOS runner would mean installing `just` and
# staticcheck there to buy coverage this pass already has, and would leave the
# landing gate — `just check-ci`, the one a contributor runs first, before any
# runner — still blind. ci.yml needs no change at
# all: `check-go` runs `just check-ci`, so it inherits whatever this recipe grows.
#
# WHY THESE TWO GOOS VALUES AND NO MORE. They are the two this tree compiles
# under. `GOOS=windows go build ./...` does not (syscall.Kill, syscall.Stat_t,
# unix.Faccessat and unix.TIOCGETA are all absent there), so a third pass would
# report a broken build rather than a lint finding. The one file outside both
# worlds is `internal/serialdaemon/serial_other.go` (`!linux && !darwin`), the
# completeness arm of a constraint set rather than a target; it is named as
# unanalyzed in internal/capture/lintgate_pin_test.go rather than left for
# someone to discover. GOARCH stays the host's — nothing here is
# arch-conditional, and both GOOS values support both arches yolo ships on.
#
# WHY THE DARWIN PASS DROPS SA4023. SA4023 ("impossible comparison of interface
# value with untyped nil") is a claim about ONE build configuration. A caller
# that checks the error from a split primitive is correct — the linux half can
# succeed — but under GOOS=darwin the `_other.go` half is an unconditional
# refusal, so the check fires on the SPLIT, at every such call site, on code with
# nothing wrong with it. Dropping it here rather than annotating each call site
# keeps the fix from having to be re-applied for every `_other.go` refusal added
# later, and leaves SA4023 fully live under GOOS=linux, where the real
# implementations are in view and it can still find something. The cost, stated
# plainly: a genuine SA4023 reachable only on darwin would go unreported.
#
# COST: the darwin pass roughly doubles a COLD `just lint`, because it type-checks
# the tree a second time. Warm it is close to free — the darwin object cache is
# built once and reused, and only changed packages are re-analyzed. No figure is
# written here on purpose: it would be a measurement of one machine's cache state
# on one day, and the next reader would have no way to tell a stale number from a
# regression.
#
# NO -trimpath HERE, ON PURPOSE. It would take the checkout path out of the build-cache
# key, so a new worktree reuses another checkout's vet/staticcheck work — but a cached
# finding is then replayed with the file path of the checkout that produced it, which can
# name a sibling worktree or a file that no longer exists (found in review, 2026-09-28).
# The warm gate's gain comes from check-ci's [parallel], not from this.
#
# Run linter (Go: vet + staticcheck), once per GOOS this tree targets.
lint:
    GOOS=linux python3 scripts/go_packages.py run go vet
    GOOS=linux python3 scripts/go_packages.py run staticcheck
    GOOS=darwin python3 scripts/go_packages.py run go vet
    GOOS=darwin python3 scripts/go_packages.py run staticcheck -checks=inherit,-SA4023

# ONE COPY OF THE LINT COMMANDS, reached by dependency rather than restated: the
# landing gate (`check-ci`) and the interactive recipe (`check`) have to run the same
# passes, and two hand-kept lists is exactly how a gate ends up applied on one
# path and not the other. `lint` modifies nothing, so there is nothing for a
# "CI mode" to withhold; what this recipe adds is the gofmt cleanliness check.
#
# Lint (CI mode — every `lint` pass, plus a gofmt cleanliness check).
lint-ci: lint
    @dirty="$(gofmt -l $(git ls-files --cached --others --exclude-standard -- '*.go' ':(exclude)vendor/'))"; test -z "$dirty" || { echo "gofmt needs to run on:"; echo "$dirty"; exit 1; }
    # The release gate's own tests, and the check that CHANGELOG.md's sections still extract.
    # `just release` and both release workflows share scripts/changelog-section.sh, so a rule
    # changed there shows up here first rather than on a tag that can never be moved.
    sh scripts/test-changelog-section.sh
    sh scripts/test-undeploy.sh
    # CHANGELOG.md's links become the release body's, and a release is never edited after its
    # tag, so a dead link or anchor there has to fail here, before it can ship.
    scripts/vantage-check.sh CHANGELOG.md
    python3 scripts/test-check-userguide-closed-tree.py
    python3 scripts/check-userguide-closed-tree.py userguide
    python3 scripts/test-check-site-output-dir.py
    python3 scripts/check-site-output-dir.py
    python3 scripts/test-site-base-href.py
    # vantage-check runs through a wrapper that keeps it off the output pipe it would share with
    # test-fast under check-ci's [parallel]; the script says why.
    python3 scripts/test-vantage-check.py
    scripts/vantage-check.sh userguide/
    # The Git-aware Go package boundary and `just done`'s front door are regression-tested,
    # including their production call sites.
    python3 scripts/test-go-packages.py
    python3 scripts/test-completion-check.py
    python3 scripts/test_completion_census.py
    # The matched launcher snapshot, install-publication and wrapper regressions are permanent
    # lint-ci coverage, and therefore run under check-ci too.
    python3 scripts/test_matched_bundle.py
    # The launcher slot `just deploy` and `just deploy-dev` share: what they replace and refuse.
    python3 scripts/test_launcher_slot.py
    # The macOS nightly's duration-balanced shard partition, and the workflow step that calls it.
    python3 scripts/test_nightly_shard.py
    # An answered question left in question form still reads as a decision to make, and only
    # `vantage-check index` lists it, so check the index (the design-doc skill's compaction rule).
    python3 scripts/test-check-answered-questions.py
    python3 scripts/check-answered-questions.py

# Format code (Go: gofmt on tracked files; vendor/ is upstream code and never reformatted)
format:
    gofmt -w $(git ls-files --cached --others --exclude-standard -- '*.go' ':(exclude)vendor/')

# Quality checks (interactive use)
check: format lint test-fast

# `[parallel]` runs the two dependencies at once: neither writes anything the other reads, so
# the gate takes as long as the slower of them instead of their sum. A failure in either still
# fails the recipe with that dependency's exit code. Their output interleaves; each line is
# still whole, and the failing command is named in just's own `error: recipe ... failed` line.
# `check` stays serial on purpose: its `format` rewrites the files `lint` and `test-fast` read.
# `[parallel]` needs just >= 1.42, below the 1.47 floor `lint`'s `[env]` already sets.
#
# Run it once before landing, not on every commit: there is no pre-commit hook, by ruling
# (AGENTS.md, Workflow step 4).
#
# The landing gate CI also runs (no formatting — just verify and test).
[parallel]
check-ci: lint-ci test-fast

# Full quality checks including container integration tests
check-all: format lint test

# Clean up build artifacts
clean:
    rm -f result
    rm -rf dist/ build/ dist-go/

# Run `just done` at end of task: check what changed since the last verified commit, read-only.
#
# It verifies the task, not the tree from scratch (docs/reference/change-aware-completion.md). The
# baseline is the nearest ancestor of HEAD that an earlier `just done` verified on a clean tree
# with the same toolchain; records are shared by every worktree of the repository. From there it
# runs only what the change can reach — lint for the changed Go packages and their importers,
# short tests for the changed packages alone (the importer closure's tests are CI's), the tests
# recorded as reading a changed file (scripts/completion-readers.json), and
# the checks for changed documents — and the full `check-ci` when there is no baseline or a gate
# input moved. It never formats: a dirty tree is refused before any check runs, which is why it no
# longer depends on `check`, whose `gofmt -w` could rewrite the work it was verifying.
#
# It is not the landing gate: landing still runs `just check-ci` (AGENTS.md, Workflow).
done:
    @python3 scripts/completion-check.py

# Request a release for one frozen commit. The host performs the cheap local gates, then
# dispatches release-request.yml on main with that exact SHA. Trusted-main eligibility and
# target-source preparation run in read-only jobs; a separate trusted mutation job creates the
# immutable tag, waits for the exact original Release/GoReleaser run to succeed, then dispatches
# publish.yml on main. Dispatch acceptance is not publication success; inspect Actions. See
# docs/reference/release-process.md for capability boundaries and fail-closed partial state.
#
# IT TAKES NO VERSION (release-process.md PTG-D3): the version is CHANGELOG.md's newest dated
# section, the one the release-prep commit renamed from [Unreleased] (scripts/release-version.sh).
# An argument is refused, naming the version it would release. A version already tagged at
# another commit is refused; one tagged at HEAD goes on, for the tag-only resume the request
# workflow verifies.
#
# Request a release of CHANGELOG.md's newest dated section, for this exact commit
release *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -n {{ quote(args) }} ]; then
        if v="$(sh scripts/release-version.sh 2>/dev/null)"; then
            echo "✗ 'just release' takes no version; it releases $v from CHANGELOG.md — run 'just release' with no argument." >&2
        else
            echo "✗ 'just release' takes no version; it reads it from CHANGELOG.md's newest dated section — run 'just release' with no argument." >&2
        fi
        exit 2
    fi
    if ! v="$(sh scripts/release-version.sh)"; then
        echo "  No release request was sent." >&2
        exit 1
    fi
    echo "Releasing v$v, CHANGELOG.md's newest dated section."
    if [ -n "$(git status --porcelain)" ]; then
        echo "✗ the working tree is dirty — commit or stash first. No release request was sent." >&2
        git status --short >&2
        exit 1
    fi
    sha="$(git rev-parse HEAD)"
    if ! git fetch --quiet --prune origin main; then
        echo "✗ could not refresh origin/main — restore connectivity and retry. No release request was sent." >&2
        exit 1
    fi
    if ! git merge-base --is-ancestor "$sha" FETCH_HEAD; then
        echo "✗ HEAD ($sha) is not contained in origin/main — push/merge this exact commit first." >&2
        echo "  No release request was sent and no newer main commit will be substituted." >&2
        exit 1
    fi
    if ! refs="$(git ls-remote origin "refs/tags/v$v" "refs/tags/v$v^{}")"; then
        echo "✗ could not ask origin whether v$v is already tagged — restore connectivity and retry. No release request was sent." >&2
        exit 1
    fi
    if [ -n "$refs" ]; then
        # An annotated tag lists its object and, peeled, its commit; a lightweight one only
        # its commit.
        tagged="$(printf '%s\n' "$refs" | awk -v peeled="refs/tags/v$v^{}" '$2 == peeled { print $1 }')"
        [ -n "$tagged" ] || tagged="$(printf '%s\n' "$refs" | awk 'NR == 1 { print $1 }')"
        if [ "$tagged" = "$sha" ]; then
            echo "v$v is already tagged at HEAD; requesting it again, which only a tag-only release can resume (docs/reference/release-process.md#resuming-a-tag-only-release)."
        elif [ "$(sh scripts/release-version.sh --unreleased)" = content ]; then
            echo "✗ v$v, CHANGELOG.md's newest dated section, is already tagged at $tagged, and [Unreleased] holds the changes since — rename '## [Unreleased]' to '## [X.Y.Z] - $(date -u +%Y-%m-%d)' for the next version, commit, and run 'just release' again. No release request was sent." >&2
            exit 1
        else
            echo "✗ v$v, CHANGELOG.md's newest dated section, is already tagged at $tagged, and nothing is under [Unreleased] — there is nothing new to release. No release request was sent." >&2
            exit 1
        fi
    fi
    if ! sh scripts/changelog-section.sh "$v" >/dev/null; then
        echo "✗ CHANGELOG.md has no usable section for $v — write and commit it, then retry. No release request was sent." >&2
        exit 1
    fi
    if ! gh workflow run release-request.yml --ref main -f "version=$v" -f "sha=$sha"; then
        echo "✗ could not submit the release request — check gh authentication and Actions permissions, then retry. No tag was created by this command." >&2
        exit 1
    fi
    echo "release request submitted for v$v at $sha; inspect the release-request Actions run, then both publisher runs."
