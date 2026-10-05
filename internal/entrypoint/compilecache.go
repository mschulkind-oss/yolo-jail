package entrypoint

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// compilecache.go keeps a program's COMPILE CACHES per workspace across jail restarts
// (docs/design/pi-extension-store-builds.md §9 findings #1 and #7, XB-D30 to XB-D32): the code a
// program compiled on its last start, which a jail restart used to throw away.
//
// TWO CACHES, ONE DIRECTORY. Both live under CompileCacheDirRel, in the workspace's own home
// state: ~/.local is per workspace on every backend (a bind on podman, the state dir Apple
// Container binds whole, a link into the workspace's sidecar on macos-user), so nothing here is
// ever machine-wide. That is the rule the design states as XB-P4, compiled code one jail wrote
// must never run in another, and it is why neither cache goes in ~/.cache, which every jail on the
// machine shares.
//
//   - Node's own compile cache (XB-D31), for a program its launcher runs under a Node it resolved
//     (a declared node_floor): NODE_COMPILE_CACHE names <dir>/node unless the environment already
//     names one. Unset, Node's module.enableCompileCache() writes to <tmpdir>/node-compile-cache,
//     which a restart empties. Not set for a program delivered by npm that is a native binary
//     (copilot's loader spawns one, opencode's bin is one): Node would cache nothing of theirs and
//     only reach the processes they start.
//   - The temporary-directory caches a pack declares (Install.TempCaches, XB-D30): right before the
//     exec, <tmpdir>/<name> is linked to <dir>/tmp/<name> when nothing is there yet. For pi that is
//     jiti's, which jiti puts at os.tmpdir()/jiti with no setting that moves it. The link names the
//     home by path, so in every jail it resolves to that jail's own home; an entry already there,
//     a directory a program made first or a link someone else made, is never replaced.
//
// BOUNDED (XB-D32): neither cache prunes itself, and a path-keyed cache only grows (a scratch home
// adds a full set of entries), so at most once a day the launcher removes the files no program
// has rewritten for COMPILE_CACHE_MAX_AGE days. A live entry removed that way is compiled again
// once, on the next start.
//
// Spliced right before the exec in every template, after the tree gate, so a launch the gate
// stops makes nothing, and after the install-only exit, so a capture never records a cache.

// CompileCacheDirRel is where a launcher keeps the compile caches, relative to the jail home.
const CompileCacheDirRel = ".local/state/yolo/compile-cache"

// compileCacheShellFn defines the step and runs it. Same splice contract as npmLauncherTemplate:
// every sentinel is a shquote'd literal in a bare position, and HAS_TEMP_CACHES gates the array
// for bash 3.2's reason. A program with neither cache returns at the first line and makes nothing.
const compileCacheShellFn = `# --- compile caches kept per workspace (pi-extension-store-builds.md XB-D30 to XB-D32) ---
NODE_COMPILE=__YOLO_NODE_COMPILE__
HAS_TEMP_CACHES=__YOLO_HAS_TEMP_CACHES__
TEMP_CACHES=(__YOLO_TEMP_CACHES__)
COMPILE_CACHE_DIR="$HOME/` + CompileCacheDirRel + `"
COMPILE_CACHE_MAX_AGE=7 # days since a cache file was last written
_yolo_compile_caches() {
    [ "$NODE_COMPILE" = "1" ] || [ "$HAS_TEMP_CACHES" = "1" ] || return 0
    if [ "$NODE_COMPILE" = "1" ] && [ -z "${NODE_COMPILE_CACHE:-}" ] &&
        mkdir -p "$COMPILE_CACHE_DIR/node" 2>/dev/null; then
        export NODE_COMPILE_CACHE="$COMPILE_CACHE_DIR/node"
    fi
    if [ "$HAS_TEMP_CACHES" = "1" ]; then
        # Node's os.tmpdir(), spelled as Node spells it: the first of TMPDIR, TMP and TEMP that is
        # set and not empty, else /tmp, with one trailing slash dropped.
        local tmp="${TMPDIR:-${TMP:-${TEMP:-/tmp}}}" name
        [ "${#tmp}" -le 1 ] || tmp="${tmp%/}"
        for name in "${TEMP_CACHES[@]}"; do
            mkdir -p "$COMPILE_CACHE_DIR/tmp/$name" 2>/dev/null || continue
            if [ ! -e "$tmp/$name" ] && [ ! -L "$tmp/$name" ]; then
                ln -s "$COMPILE_CACHE_DIR/tmp/$name" "$tmp/$name" 2>/dev/null || true
            fi
        done
    fi
    _yolo_prune_compile_caches
}
# _yolo_prune_compile_caches runs at most once a day per workspace, on its own stamp. It removes
# files only, never a directory, so a running program's cache directory is never taken from it.
_yolo_prune_compile_caches() {
    local stamp="$COMPILE_CACHE_DIR/.yolo-pruned"
    [ -d "$COMPILE_CACHE_DIR" ] || return 0
    if [ -f "$stamp" ] && [ "$(( $(date +%s) - $(_stamp_mtime "$stamp") ))" -lt 86400 ]; then
        return 0
    fi
    touch "$stamp" 2>/dev/null || return 0
    command -v find >/dev/null 2>&1 || return 0
    YOLO_BYPASS_SHIMS=1 find "$COMPILE_CACHE_DIR" -type f ! -name .yolo-pruned \
        -mtime +"$COMPILE_CACHE_MAX_AGE" -exec rm -f {} + 2>/dev/null || true
}
_yolo_compile_caches || true
`

// compileCacheSplices renders the step's sentinel pairs for inst. node says whether the launcher
// runs the program under a Node it resolved: a declared node_floor, which only the npm and source
// templates honor (the native one execs the vendor's binary as it is).
func compileCacheSplices(inst *packdecl.Install, node bool) []string {
	return []string{
		"__YOLO_NODE_COMPILE__", shquote.Quote(boolFlag(node && inst.NodeFloor != "")),
		"__YOLO_HAS_TEMP_CACHES__", shquote.Quote(boolFlag(len(inst.TempCaches) > 0)),
		"__YOLO_TEMP_CACHES__", shquote.Join(inst.TempCaches),
	}
}

// startupSplices is every startup sentinel pair a template carries for inst, the version probe's
// (probeargs.go) and the compile caches', riding on the end of each generator's pair list as
// refreshSplices does. node is compileCacheSplices'.
func startupSplices(inst *packdecl.Install, node bool) []string {
	return append(probeArgsSplices(inst.ProbeArgs), compileCacheSplices(inst, node)...)
}
