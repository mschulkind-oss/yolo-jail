# Container runtime (podman or container)
runtime := env("YOLO_RUNTIME", "podman")

default:
    @just --list

# Build every cmd/ binary into dist-go/<goos>-<goarch>/
build-go:
    ./scripts/build-go.sh

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

# Run all tests (Python integration + Go full suite)
test:
    uv run --group dev python -m pytest tests/
    go test ./...

# Run fast tests only (skip container integration tests).
test-fast:
    uv run --group dev python -m pytest tests/ -m "not slow" -n 4 --dist worksteal
    go test -short ./...

# Run linter (Python: ruff; Go: vet + staticcheck)
lint:
    uv run ruff check .
    go vet ./...
    staticcheck ./...

# Lint without auto-fix (CI mode — fails on violations, doesn't modify files).
lint-ci:
    uv run ruff check .
    uv run ruff format --check .
    go vet ./...
    staticcheck ./...
    @dirty="$(gofmt -l $(git ls-files --cached --others --exclude-standard '*.go'))"; test -z "$dirty" || { echo "gofmt needs to run on:"; echo "$dirty"; exit 1; }

# Format code (Python: ruff; Go: gofmt on tracked files)
format:
    uv run ruff check --fix .
    uv run ruff format .
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
