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
# a Go toolchain. goreleaser + the brew formula run the script directly; this is
# for local use.
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

    # Warn if PATH resolves `yolo` to some other install (a Homebrew copy, say)
    # — go install would have succeeded while the old binary still wins.
    RESOLVED="$(command -v yolo 2>/dev/null || true)"
    if [ -n "$RESOLVED" ] && [ "$RESOLVED" != "$GOBIN_DIR/yolo" ]; then
        echo "⚠ PATH resolves yolo to $RESOLVED, not the copy just installed at $GOBIN_DIR/yolo." >&2
        echo "  Remove the other install, or put $GOBIN_DIR earlier in PATH." >&2
    fi

    # NOTE: install no longer records repo_path in the user config (that key was
    # retired 2026-07-23). A from-source `yolo` finds the repo from the checkout
    # you launch in (the cwd-walk) or via YOLO_REPO_ROOT — see
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
    nix --extra-experimental-features 'nix-command flakes' build .#ociImage

# Build the minimal image variant used by CI integration (no chromium,
# gcc toolchain, nested-podman, or debug tools — ~1.6–2 GB smaller).
build-image-minimal:
    nix --extra-experimental-features 'nix-command flakes' build .#ociImageMinimal

# Build and load the image into the container runtime
load: build-image
    ./result | {{runtime}} load

# Build BOTH image variants on a Linux host and push their closures to the
# Cachix cache, so macOS users download the prebuilt image (no Linux builder
# needed).
cachix-push CACHE="yolo-jail":
    @command -v cachix >/dev/null || {{ '{ echo "cachix not found: nix profile install nixpkgs#cachix"; exit 1; }' }}
    nix --extra-experimental-features 'nix-command flakes' build .#ociImage --print-out-paths --no-link | cachix push {{CACHE}}
    nix --extra-experimental-features 'nix-command flakes' build .#ociImageMinimal --print-out-paths --no-link | cachix push {{CACHE}}
    @echo "Pushed both image variants to https://{{CACHE}}.cachix.org"

# Run all tests (Go unit + Go container integration suite)
test:
    go test -short ./...
    go test -count=1 -timeout 0 ./integration

# Run fast tests only (skip container integration tests).
test-fast:
    go test -short ./...

# Run linter (Go: vet + staticcheck)
lint:
    go vet ./...
    staticcheck ./...

# Lint without auto-fix (CI mode — fails on violations, doesn't modify files).
lint-ci:
    go vet ./...
    staticcheck ./...
    @dirty="$(gofmt -l $(git ls-files --cached --others --exclude-standard '*.go'))"; test -z "$dirty" || { echo "gofmt needs to run on:"; echo "$dirty"; exit 1; }

# Format code (Go: gofmt on tracked files)
format:
    gofmt -w $(git ls-files --cached --others --exclude-standard '*.go')

# Quality checks (interactive use)
check: format lint test-fast

# Pre-commit hook target (no formatting — just verify and test)
check-ci: lint-ci test-fast

# Full quality checks including container integration tests
check-all: format lint test

# Clean up build artifacts
clean:
    rm -f result
    rm -rf dist/ build/ dist-go/

# Run `just done` at end of task to verify clean state
done: check
    @echo "All checks passed, working tree clean"
