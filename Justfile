default:
    @just --list

# Build the docker image using Nix
build:
    nix --extra-experimental-features 'nix-command flakes' build .#dockerImage

# Build and load the image into the local Docker daemon
load: build
    docker load < result

# Run the jail with shared auth and persistent state
run:
    @mkdir -p .home .mise-cache
    docker run --rm -it \
        -v $(pwd):/workspace \
        -v $(pwd)/.home:/home/agent \
        -v $(pwd)/.mise-cache:/mise \
        -v ${HOME}/.config/gh:/home/agent/.config/gh:ro \
        -e HOME=/home/agent \
        -e MISE_DATA_DIR=/mise \
        -e MISE_CONFIG_DIR=/workspace \
        -e GOOGLE_API_KEY=${GOOGLE_API_KEY} \
        --user $(id -u):$(id -g) \
        --workdir /workspace \
        yolo-jail

# Run the jail on a specific target path
run-repo path:
    docker run --rm -it -v {{path}}:/workspace yolo-jail

# Clean up build artifacts
clean:
    rm -f result
