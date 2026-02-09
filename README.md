# YOLO Jail

A restricted, secure Docker environment designed for AI agents (like VS Code Copilot and Gemini) to safely modify codebases.

## Features

- **Isolated:** Runs in a minimal Docker container.
- **Optimized:** Pre-installed with modern, fast tools:
    - `rg` (ripgrep)
    - `fd`
- **Restricted:** Dangerous or slow legacy tools are blocked or shimmed:
    - `grep` -> Redirects to `rg`
    - `find` -> Redirects to `fd`
- **Reproducible:** Defined entirely via Nix Flakes.

## Usage

### Prerequisites
- Docker
- Nix (with flakes enabled)

### Build & Run

1.  **Build the image:**
    ```bash
    nix build .#dockerImage
    docker load < result
    ```
    *Or use Just:*
    ```bash
    just load
    ```

2.  **Run in the current directory:**
    ```bash
    docker run --rm -it -v $(pwd):/workspace yolo-jail
    ```
    *Or use Just:*
    ```bash
    just run
    ```

## Development

See [AGENTS.md](AGENTS.md) for agent-specific instructions and [docs/design](docs/design) for architectural decisions.
