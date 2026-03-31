# Container runtime (podman or docker)
runtime := env("YOLO_RUNTIME", "podman")

default:
    @just --list

# Install editable package and patch finder for relocatable paths (host + jail)
setup:
    #!/usr/bin/env bash
    set -euo pipefail

    # Editable install into mise's Python
    uv pip install -e .

    # Locate the generated finder module
    SITE_PACKAGES="$(python3 -c 'import site; print(site.getsitepackages()[0])')"
    FINDER="$SITE_PACKAGES/__editable___yolo_jail_0_1_0_finder.py"

    if [ ! -f "$FINDER" ]; then
        echo "ERROR: finder not found at $FINDER" >&2
        exit 1
    fi

    REPO_ROOT="$(pwd)"

    # Patch the static MAPPING to resolve dynamically via YOLO_REPO_ROOT
    python3 - "$FINDER" "$REPO_ROOT" <<'PYEOF'
    import re, sys
    finder_path, repo_root = sys.argv[1], sys.argv[2]
    with open(finder_path) as f:
        content = f.read()
    m = re.search(r"^MAPPING:.*$", content, re.MULTILINE)
    if not m:
        print("WARN: MAPPING line not found, already patched?", file=sys.stderr)
        sys.exit(0)
    new_block = (
        "import os as _os\n"
        f"_YOLO_ROOT = _os.environ.get('YOLO_REPO_ROOT', '{repo_root}')\n"
        "MAPPING: dict[str, str] = {'src': _os.path.join(_YOLO_ROOT, 'src')}"
    )
    content = content[:m.start()] + new_block + content[m.end():]
    with open(finder_path, "w") as f:
        f.write(content)
    PYEOF

    echo "Patched $FINDER"
    echo "  Host fallback: $REPO_ROOT"
    echo "  Jail: uses \$YOLO_REPO_ROOT (/opt/yolo-jail)"

# Build the Python package
build:
    uv build

# Install yolo as a standalone tool (decoupled from source tree)
install: build
    uv tool install dist/*.whl --force

# Build + install (deploy the yolo CLI)
deploy: install
    @echo "yolo-jail deployed. Verify: which yolo"

# Build the container image using Nix
build-image:
    nix --extra-experimental-features 'nix-command flakes' build .#dockerImage

# Build and load the image into the container runtime
load: build-image
    {{runtime}} load < result

# Run all tests
test:
    uv run --group dev python -m pytest tests/

# Run fast tests only (skip container integration tests)
test-fast:
    uv run --group dev python -m pytest tests/ -m "not slow"

# Run linter
lint:
    uv run ruff check .

# Format code
format:
    uv run ruff check --fix .
    uv run ruff format .

# All quality checks
check: format lint test

# Clean up build artifacts
clean:
    rm -f result
    rm -rf dist/ build/

# Sync after merging a PR on public repo: fetch, rebase chain, push
sync:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "=== Syncing with public remote ==="
    jj git fetch --remote public
    jj rebase -r staging -d main
    jj rebase -r dev -d staging
    just push
    echo "=== Sync complete ==="
    jj log --limit 5

# Push bookmarks to remotes (main→both, dev+staging→private only)
push:
    jj git push --bookmark main --remote public
    jj git push --bookmark main --bookmark dev --bookmark staging --remote private

# Pre-promote quality gate
prepromote:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "=== Pre-promote quality checks ==="

    # Staging has changes
    if [ -z "$(jj diff -r staging --summary 2>/dev/null)" ]; then
        echo "FAIL: staging has no changes to promote."
        exit 1
    fi

    # Description is proper (not placeholder)
    DESC="$(jj log -r staging --no-graph -T description 2>/dev/null)"
    if [ -z "$DESC" ] || echo "$DESC" | grep -qi "^staging:"; then
        echo "FAIL: staging description must be a proper release description."
        exit 1
    fi

    # Run full quality gates including container integration tests
    just format
    just lint
    just test
    echo "=== All pre-promote checks passed ==="

# Promote staging to main
promote: prepromote
    #!/usr/bin/env bash
    set -euo pipefail
    DESC="$(jj log -r staging --no-graph -T description 2>/dev/null)"
    echo "--- Promoting staging to main ---"
    echo "Description: $DESC"
    jj bookmark set main -r staging
    jj new --insert-after main --insert-before dev
    jj bookmark set staging -r @
    jj desc -m "Staging: accumulating changes for next public release"
    jj edit dev
    just push
    echo "Promote complete."
