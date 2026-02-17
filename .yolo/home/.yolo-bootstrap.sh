#!/bin/bash
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export GOPATH="${GOPATH:-$HOME/go}"
export GOBIN="$GOPATH/bin"
export PATH="$NPM_CONFIG_PREFIX/bin:$GOBIN:$PATH"

# Install binaries if missing.
if ! command -v chrome-devtools-mcp >/dev/null; then
    echo "Installing MCP tools via npm..."
    YOLO_BYPASS_SHIMS=1 npm install -g chrome-devtools-mcp @modelcontextprotocol/server-sequential-thinking pyright typescript-language-server typescript
fi

if [ ! -f "$GOBIN/mcp-language-server" ]; then
    if command -v go >/dev/null; then
        echo "Installing mcp-language-server via go..."
        mkdir -p "$GOBIN"
        YOLO_BYPASS_SHIMS=1 go install github.com/isaacphi/mcp-language-server@latest
    else
        echo "Warning: go not found, skipping mcp-language-server install"
    fi
fi

# Install showboat
if ! command -v showboat >/dev/null; then
    echo "Installing showboat..."
    YOLO_BYPASS_SHIMS=1 pip install showboat
fi
