package entrypoint

import "path/filepath"

// executable wrapper scripts (chrome-devtools-mcp-wrapper, mcp-wrappers/node,
// mcp-wrappers/npx). Each is written then chmod'd |= S_IEXEC.
func GenerateMCPWrappers(e *Env) error {
	if err := writeExecutable(filepath.Join(e.LocalBin(), "chrome-devtools-mcp-wrapper"), chromeWrapper); err != nil {
		return err
	}
	if err := writeExecutable(filepath.Join(e.McpWrappersBin(), "node"), nodeWrapper); err != nil {
		return err
	}
	return writeExecutable(filepath.Join(e.McpWrappersBin(), "npx"), npxWrapper)
}

// chromeWrapper is the chrome-devtools-mcp-wrapper body.
//
// No LD_LIBRARY_PATH export: nix-ld (the /lib64 interpreter) resolves libstdc++
// env-free for the FHS mise node, and the nix /bin/node this wrapper execs is
// RPATH-self-contained — so the scrubbed-child-env case the old export guarded
// against is now covered structurally. See docs/reference/mise-node-dynamic-linking.md
// (step 7). FONTCONFIG_* stay: they are chromium font config, unrelated to the loader.
const chromeWrapper = `#!/bin/bash
# Self-contained wrapper: sets its own env since agents sanitize child processes.
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"

# Internal Chrome debugging defaults (isolated to container)
CHROME_PORT="${CHROME_DEBUG_PORT:-9222}"
CHROME_ADDR="${CHROME_DEBUG_ADDR:-127.0.0.1}"
CHROME_URL="http://$CHROME_ADDR:$CHROME_PORT"

NPM_BIN="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}/bin"
MCP_WRAPPERS_BIN="$HOME/.local/bin/mcp-wrappers"

# WHERE CHROMIUM IS DEPENDS ON HOW THIS LAUNCH GOT ITS PACKAGES (C5,
# docs/reference/image-staging-vs-baking.md, "Store-delivered packages"). A baked image has
# /usr/bin/chromium, a
# symlink mkBinPathLinks lays down; a launch that delivers the image's bulk extras from
# the mounted nix store has no /usr/bin/chromium and a chromium on PATH instead. The
# baked path is tried FIRST so a jail that bakes behaves exactly as it always did, and
# this wrapper is self-contained on purpose (agents sanitize child environments), which
# is why it resolves rather than assuming either answer.
CHROMIUM_BIN="/usr/bin/chromium"
[ -x "$CHROMIUM_BIN" ] || CHROMIUM_BIN="$(command -v chromium 2>/dev/null)"

# Start Chromium if not already running
if ! curl -s "$CHROME_URL/json/version" >/dev/null 2>&1; then
    "$CHROMIUM_BIN" \
        --headless=new \
        --no-sandbox \
        --disable-dev-shm-usage \
        --disable-setuid-sandbox \
        --disable-gpu \
        --disable-software-rasterizer \
        --disable-blink-features=AutomationControlled \
        --disable-breakpad \
        --noerrdialogs \
        --user-agent="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36" \
        --remote-debugging-address=$CHROME_ADDR \
        --remote-debugging-port=$CHROME_PORT \
        &>/dev/null &

    # Wait for Chrome to be ready
    for i in $(seq 1 30); do
        if curl -s "$CHROME_URL/json/version" >/dev/null 2>&1; then
            break
        fi
        sleep 0.2
    done
fi

exec "$MCP_WRAPPERS_BIN/node" "$NPM_BIN/chrome-devtools-mcp" \
    --browser-url "$CHROME_URL" \
    "$@"
`

// nodeWrapper is the mcp-wrappers/node body. No LD_LIBRARY_PATH export — nix-ld
// covers the FHS mise node env-free and this wrapper execs the RPATH-self-contained
// nix /bin/node (see chromeWrapper's note and step 7 of the design doc).
const nodeWrapper = `#!/bin/bash
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/node "$@"
`

// npxWrapper is the mcp-wrappers/npx body. No LD_LIBRARY_PATH export — same
// rationale as nodeWrapper (nix-ld + RPATH-self-contained nix /bin/npx).
const npxWrapper = `#!/bin/bash
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/npx "$@"
`
