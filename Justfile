# Container runtime (podman or container)
runtime := env("YOLO_RUNTIME", "podman")

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

# Install the host binary (yolo) to $GOBIN or $GOPATH/bin
install:
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
        echo "    just build-go && ./dist-go/linux-\$(go env GOARCH)/yolo -- bash" >&2
        echo "  (run the freshly-built binary BY PATH — not the installed one)." >&2
        echo "  To ship to the host, run 'just install' / 'just deploy' on the host." >&2
        exit 1
    fi

    VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo unknown)"
    COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    LDFLAGS="-X github.com/mschulkind-oss/yolo-jail/internal/version.buildVersion=${VERSION} -X github.com/mschulkind-oss/yolo-jail/internal/version.GitCommit=${COMMIT}"

    # --- Retire the pre-Go (Python) install ---
    # Upgrading from the uv-installed Python distribution leaves console-script
    # symlinks in GOBIN. `go install` refuses to overwrite the one named `yolo`
    # ("already exists and is not an object file"), so clear them first. Runs
    # via `go run` because it has to happen before the install it unblocks.
    go run ./cmd/yolo internal migrate-host

    go install -ldflags "$LDFLAGS" ./cmd/yolo
    GOBIN_DIR="$(go env GOBIN 2>/dev/null || true)"
    [ -n "$GOBIN_DIR" ] || GOBIN_DIR="$(go env GOPATH)/bin"
    echo "Installed to $GOBIN_DIR"

    # AN INSTALL PATH NOBODY RUNS IS NOT AN INSTALL. `go install` writes to GOBIN and
    # says nothing about what `yolo` RESOLVES to: an older copy earlier on PATH (a
    # package-manager one, a stray ~/.local/bin/yolo) silently keeps winning, so every
    # `just deploy` looks like it worked while the launcher on the machine never moves.
    # That is invisible until a host<->jail contract changes and the boot dies naming
    # neither half — see version.SourceSkew, which catches the SYMPTOM at launch; this
    # catches the CAUSE at install. Warn rather than fail: a machine with no `yolo` on
    # PATH yet is a first install, not a mistake.
    RESOLVED="$(command -v yolo 2>/dev/null || true)"
    if [ -n "$RESOLVED" ] && [ "$(readlink -f "$RESOLVED" 2>/dev/null || echo "$RESOLVED")" != "$(readlink -f "$GOBIN_DIR/yolo" 2>/dev/null || echo "$GOBIN_DIR/yolo")" ]; then
        echo >&2
        echo "⚠ WARNING: this install is NOT the yolo your shell runs." >&2
        echo "    installed:  $GOBIN_DIR/yolo" >&2
        echo "    resolves:   $RESOLVED" >&2
        echo "  Every launch will keep using the older one. Put $GOBIN_DIR ahead of it on" >&2
        echo "  PATH (or remove $RESOLVED), then run 'hash -r'." >&2
        echo >&2
    fi

    # Stage a SELF-CONTAINED flake bundle so an installed `yolo` builds the jail
    # image from ANY directory — no source checkout, no YOLO_REPO_ROOT, ever.
    # This is the "two files and a binary" prebuilt bundle (flake.nix +
    # flake.lock + bin/linux-<arch>/) that the flake's prebuilt short-circuit
    # consumes with no toolchain — the same bundle Homebrew and the release
    # archive ship. reporoot.Resolve step 4 finds it.
    #
    # THE PATH COMES FROM THE BINARY, not from $GOBIN arithmetic. `yolo internal
    # bundle-dir` prints paths.FlakeBundleDir() — a dedicated leaf UNDER the
    # state dir ($HOME/.local/share/yolo-jail/flake-bundle). The first cut
    # computed $(dirname $GOBIN)/share/yolo-jail, which for GOBIN=~/.local/bin
    # collapsed onto $HOME/.local/share/yolo-jail — the whole state dir — and the
    # staging script's `rm -rf $DEST` deleted it. Asking the binary for the one
    # path it actually resolves removes both the arithmetic and the drift.
    #
    # NATIVE ARCH ONLY. The shipped bundle (goreleaser/brew) is arch-agnostic and
    # builds both amd64+arm64, but a LOCAL install runs on one host — building the
    # foreign arch is a cold cross-compile of the whole module graph. Narrow to
    # this host's arch, reusing the warm cache from the `go install` just above.
    #
    # STAGE INTO A FRESH GENERATION, THEN SWAP — never over the live path. A
    # launch mounts <bundle>/bin/linux-<arch> into the jail as its yolo binaries,
    # and a bind mount pins an inode rather than a path, so the staging script's
    # `rm -rf $DEST` used to delete pid1 out from under every RUNNING jail:
    # measured 2026-09-09, a jail up since +1114 was bricked by the install that
    # took the host to +1160, and stayed bricked. `--stage` prints a new
    # generation dir, `--activate` points the stable path at it atomically, and
    # jails still mounting an older generation keep working until they exit
    # (internal/flakebundle; the housekeeping slot collects the rest).
    YOLO_BIN="$GOBIN_DIR/yolo"
    BUNDLE_GEN="$("$YOLO_BIN" internal bundle-dir --stage)"
    echo "Staging flake bundle (linux/$(go env GOARCH)) → $BUNDLE_GEN"
    YOLO_BUNDLE_ARCHES="$(go env GOARCH)" ./scripts/stage-source-bundle.sh "$BUNDLE_GEN"
    "$YOLO_BIN" internal bundle-dir --activate "$BUNDLE_GEN"

    # Warn if PATH resolves `yolo` to some other install (a Homebrew copy, say)
    # — go install would have succeeded while the old binary still wins.
    RESOLVED="$(command -v yolo 2>/dev/null || true)"
    if [ -n "$RESOLVED" ] && [ "$RESOLVED" != "$GOBIN_DIR/yolo" ]; then
        echo "⚠ PATH resolves yolo to $RESOLVED, not the copy just installed at $GOBIN_DIR/yolo." >&2
        echo "  Remove the other install, or put $GOBIN_DIR earlier in PATH." >&2
    fi

    # NOTE: install no longer records repo_path in the user config (that key was
    # retired 2026-07-23), and the cwd stopped selecting the repo on 2026-08-31.
    # The staged bundle above is what an installed `yolo` builds from, in EVERY
    # directory including a checkout; YOLO_REPO_ROOT is the only way to point it
    # at live source. So this staging step is not a convenience for the
    # checkout-less case any more — it is the from-source developer's only
    # delivery path, and the reason `just install` has to be re-run after a commit
    # that changes the image (version.SourceSkew refuses the mismatch). See
    # internal/reporoot.Resolve and docs/research/repo-root-and-distribution.md.

# Install yolo CLI and prime the Claude OAuth broker state. Safe to re-run.
deploy: install
    #!/usr/bin/env bash
    set -euo pipefail

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
        if ! command -v yolo >/dev/null 2>&1; then
            echo "ERROR: yolo not on PATH after install" >&2
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

        # Generate CA + leaf in the state dir (idempotent).
        yolo internal daemon claude-oauth-broker --init-ca >/dev/null

        echo "✓ claude-oauth-broker state primed at $HOME/.local/share/yolo-jail/state/claude-oauth-broker"
    fi

    # Restart the singleton broker so this deploy's binary is live immediately.
    if command -v yolo >/dev/null 2>&1; then
        yolo broker restart 2>&1 | sed 's/^/  /' || true
    fi

    echo "yolo-jail deployed. Verify: yolo loopholes list"

# Build the container image using Nix
build-image:
    nix --extra-experimental-features 'nix-command flakes' build .#ociImage .#imageCopier

# Build the minimal image variant used by CI integration (no chromium,
# gcc toolchain, nested-podman, or debug tools — ~1.6–2 GB smaller).
build-image-minimal:
    nix --extra-experimental-features 'nix-command flakes' build .#ociImageMinimal

# Build and DELIVER the image into the container runtime.
#
# Since layer-aware delivery landed (docs/design/layer-aware-image-delivery.md)
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
# (internal/image/autoload.go, deliverViaArchive): that backend's VM owns its
# store, so there is no containers-storage to negotiate blobs with and the whole
# image crosses as one file. The file is removed afterwards — a leftover is what
# makes the NEXT copy fail, since skopeo will not write over an existing archive.
load: build-image
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{ runtime }}" = "container" ]; then
        archive=$(mktemp -t yolo-jail-image.XXXXXX.oci)
        rm -f "$archive"
        trap 'rm -f "$archive"' EXIT
        ./result-1/bin/skopeo --insecure-policy copy \
            "nix:$(readlink -f ./result)" \
            "oci-archive:$archive:yolo-jail:latest"
        container image load -i "$archive"
    else
        ./result-1/bin/skopeo --insecure-policy copy \
            "nix:$(readlink -f ./result)" \
            containers-storage:localhost/yolo-jail:latest
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
    go test -short ./...
    # YOLO_TEST_REAL_PACK_INSTALLS keeps the tests that install a shipped pack's program
    # from its VENDOR. CI does not set it on the push path — that question is asked on a
    # `packs/**` change and weekly instead, because no commit can cause a vendor's release
    # to break (docs/design/agent-install-in-ci.md §6.1.1). A local full run wants
    # everything, so this recipe asks for it.
    YOLO_TEST_REAL_PACK_INSTALLS=1 go test -count=1 -timeout 0 ./integration

# Run fast tests only (skip container integration tests).
test-fast:
    go test -short ./...

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
# pre-commit gate — `just check-ci`, the one a contributor runs first, before any
# runner — still blind. (That gate is a DISCIPLINE, not an installed hook: nothing
# in this repo writes `.git/hooks/pre-commit`, which is why AGENTS.md's workflow
# step 4 says to run it by hand.) ci.yml needs no change at all: `check-go` runs
# `just check-ci`, so it inherits whatever this recipe grows.
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
# Run linter (Go: vet + staticcheck), once per GOOS this tree targets.
lint:
    GOOS=linux go vet ./...
    GOOS=linux staticcheck ./...
    GOOS=darwin go vet ./...
    GOOS=darwin staticcheck -checks=inherit,-SA4023 ./...

# ONE COPY OF THE LINT COMMANDS, reached by dependency rather than restated: the
# hook (`check-ci`) and the interactive recipe (`check`) have to run the same
# passes, and two hand-kept lists is exactly how a gate ends up applied on one
# path and not the other. `lint` modifies nothing, so there is nothing for a
# "CI mode" to withhold; what this recipe adds is the gofmt cleanliness check.
#
# Lint (CI mode — every `lint` pass, plus a gofmt cleanliness check).
lint-ci: lint
    @dirty="$(gofmt -l $(git ls-files --cached --others --exclude-standard '*.go'))"; test -z "$dirty" || { echo "gofmt needs to run on:"; echo "$dirty"; exit 1; }

# Format code (Go: gofmt on tracked files)
format:
    gofmt -w $(git ls-files --cached --others --exclude-standard '*.go')

# Quality checks (interactive use)
check: format lint test-fast

# Pre-commit hook target (no formatting — just verify and test)
check-ci: lint-ci test-fast

# Install the versioned pre-commit hook into .git/hooks.
#
# Git cannot track hooks, so the script lives at hooks/pre-commit and this recipe copies it into
# place — a clone does not deliver it, which is why installing is a per-clone step. The hook
# runs the same `just check-ci` CI runs; install it so a red commit is caught locally instead
# of in CI.
install-hooks:
    @mkdir -p .git/hooks
    @cp hooks/pre-commit .git/hooks/pre-commit
    @chmod +x .git/hooks/pre-commit
    @echo "installed .git/hooks/pre-commit — it runs 'just check-ci'"

# Full quality checks including container integration tests
check-all: format lint test

# Clean up build artifacts
clean:
    rm -f result
    rm -rf dist/ build/ dist-go/

# Run `just done` at end of task to verify clean state.
#
# The tree check is not decoration: `check` depends on `format`, which runs
# `gofmt -w`, so this recipe can DIRTY the tree itself. It used to print
# "working tree clean" unconditionally — a claim it never verified — and
# AGENTS.md tells agents to run it and report its output, so a stale working
# tree could be reported as a clean one.
done: check
    @if [ -n "$(git status --porcelain)" ]; then \
        echo; \
        echo "Checks passed, but the working tree is DIRTY:"; \
        echo; \
        git status --short; \
        echo; \
        echo "Commit before calling the task done (gofmt may have just rewritten a file)."; \
        exit 1; \
    fi
    @echo "All checks passed, working tree clean"
