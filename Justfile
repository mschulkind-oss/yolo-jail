default:
    @just --list

# Build the docker image using Nix
build:
    nix --extra-experimental-features 'nix-command flakes' build .#dockerImage

# Build and load the image into the local Docker daemon
load: build
    docker load < result

# Run the jail with shared auth, OAuth tokens, and persistent tools
run:
    @mkdir -p .home .mise-cache
    docker run --rm -it \
        -v $(pwd):/workspace \
        -v $(pwd)/.home:/home/agent \
        -v $(pwd)/.mise-cache:/mise \
        -v ${HOME}/.config/gh:/home/agent/.config/gh:ro \
        -v ${HOME}/.config/gemini-cli:/home/agent/.config/gemini-cli:ro \
        -v ${HOME}/.config/gcloud:/home/agent/.config/gcloud:ro \
        -e HOME=/home/agent \
        -e MISE_DATA_DIR=/mise \
        -e MISE_CONFIG_DIR=/workspace \
        -e PATH=/mise/shims:/bin:/usr/bin \
        --user $(id -u):$(id -g) \
        --workdir /workspace \
        yolo-jail \
        bash -c "mise install && bash"

# Run the jail on a specific target path
run-repo path:
    docker run --rm -it -v {{path}}:/workspace yolo-jail

# Clean up build artifacts
clean:
    rm -f result
