default:
    @just --list

# Build the docker image using Nix
build:
    nix --extra-experimental-features 'nix-command flakes' build .#dockerImage

# Build and load the image into the local Docker daemon
load: build
    docker load < result

# Run the jail with current user mapping and persistent mise cache
run:
    @mkdir -p .mise-cache
    docker run --rm -it \
        -v $(pwd):/workspace \
        -v $(pwd)/.mise-cache:/mise \
        -e MISE_DATA_DIR=/mise \
        -e MISE_CONFIG_DIR=/workspace \
        --user $(id -u):$(id -g) \
        yolo-jail

# Run the jail on a specific target path
run-repo path:
    docker run --rm -it -v {{path}}:/workspace yolo-jail

# Clean up build artifacts
clean:
    rm -f result
