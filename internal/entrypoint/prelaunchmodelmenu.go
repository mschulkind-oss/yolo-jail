package entrypoint

import (
	"encoding/json"

	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// prelaunchmodelmenu.go is the launcher half of a program's MODEL MENU (packdecl.ModelMenu, a
// term coined there; docs/design/model-lists-and-pickers.md MM-D9, MM-D22): the step that writes
// a menu file from the program's own catalog and yolo's list, and adds the flag naming it to the
// program's argv, before the exec.
//
// WHY THE LAUNCHER AND NOT A DERIVE. A derive's sandbox has no io and runs at the boot render,
// while the menu's entries come from the INSTALLED program's own output (codex's catalog carries
// each model's prompt text), and the program installs lazily, at first use. The launcher is the
// first place both inputs exist, which is MM-D9's reason for putting it there.
//
// WHY A FLAG AND NOT A CONFIG KEY. MM-D9's letter names the file from codex's config through the
// selection. codex refuses to start when a `model_catalog_json` it was given names no file
// (codex 0.159.2, core/src/config/mod.rs load_catalog_json: the read's error is the config's),
// and a config the boot renders would name the file for every codex start, the launcher's or
// not. So the file is named by the one step that knows it wrote one, for the one run it wrote it
// for (MM-D22).
//
// WHY DECLARED AND NOT KEYED ON "codex": both templates are shared by every program, and a
// bin-named branch in them is how core learns what an agent is (AGENTS.md's first rule). The
// argv, the catalog's shape and the flag are the pack's declaration; this file renders whatever
// a pack declares, and internal/modelmenu does the projection in Go, so the shell only carries
// words.

// modelMenuDeclShell is the menu's BAKED declaration, spliced into both templates' header beside
// the refresh's. Same splice contract as npmLauncherTemplate: every sentinel is a shquote'd
// literal in a bare position. The spec is the pack's declaration as ONE JSON argument, decoded by
// the Go side, so no field of it is ever parsed in shell.
const modelMenuDeclShell = `# The pack's declared MODEL MENU (packdecl.ModelMenu; model-lists-and-pickers.md MM-D9,
# MM-D22), as one JSON argument for "yolo internal model-menu". BAKED, like everything above.
HAS_MODEL_MENU=__YOLO_HAS_MODEL_MENU__
MODEL_MENU_SPEC=__YOLO_MODEL_MENU_SPEC__
`

// modelMenuShellFn defines _yolo_model_menu, which the exec block calls right after
// _yolo_launch_argv, so the words it adds sit ahead of the launch flags and the user's own argv.
//
// The Go side prints the flag words NUL-terminated, and only when it wrote a menu; the read loop
// below is the whole of the shell's part. Nothing here can fail the launch: a missing yolo, a
// program too old to print its catalog or a list naming no model all end in no words, and the
// program starts on its own catalog. YOLO_NO_LAUNCH_FLAGS=1 skips it with the launch flags, since
// its words are a pack's flag too. __YOLO_EXEC_PREFIX__ is the npm template's resolved
// interpreter, as in prelaunchRefreshShellFn; the native template renders it empty.
const modelMenuShellFn = `
# --- the program's model menu (model-lists-and-pickers.md MM-D9, MM-D22) ----------------
_yolo_model_menu() {
    [ "$HAS_MODEL_MENU" = "1" ] || return 0
    [ "${` + NoLaunchFlagsEnv + `:-}" = "1" ] && return 0
    command -v yolo >/dev/null 2>&1 || return 0
    local w n=0
    local menu_words=()
    while IFS= read -r -d '' w; do
        menu_words+=("$w")
        n=$((n + 1))
    done < <(YOLO_BYPASS_SHIMS=1 yolo internal ` + modelmenu.Verb + ` --bin="$BIN" --spec="$MODEL_MENU_SPEC" -- __YOLO_EXEC_PREFIX__"$REAL_BIN" || true)
    [ "$n" -gt 0 ] || return 0
    YOLO_ARGV=("${menu_words[@]}" ${YOLO_ARGV[@]+"${YOLO_ARGV[@]}"})
}
`

// modelMenuSplices returns the two sentinel replacements carrying m, the program's declared
// menu, nil when it declares none. HAS_MODEL_MENU is "0" and the spec empty then, so a program
// without one carries the function and never calls into yolo.
func modelMenuSplices(m *packdecl.ModelMenu) []string {
	spec := ""
	if m != nil {
		b, err := json.Marshal(m)
		if err == nil {
			spec = string(b)
		}
	}
	return []string{
		"__YOLO_HAS_MODEL_MENU__", shquote.Quote(boolFlag(spec != "")),
		"__YOLO_MODEL_MENU_SPEC__", shquote.Quote(spec),
	}
}
