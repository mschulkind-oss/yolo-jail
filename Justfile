# Container runtime (podman or docker)
runtime := env("YOLO_RUNTIME", "podman")

default:
    @just --list

# Build the container image using Nix
build:
    nix --extra-experimental-features 'nix-command flakes' build .#dockerImage

# Build and load the image into the container runtime
load: build
    {{runtime}} load < result

# Run all tests
test:
    uv run pytest tests/

# Clean up build artifacts
clean:
    rm -f result
