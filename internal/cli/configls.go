package cli

// configls.go implements `yolo config ls` — the one-screen answer to "how is this
// file constructed, and is anything hidden winning over it?"
//
// It exists because the composition engine grew enough moving parts (five layers,
// four host_files modes, per-surface codecs, an optional capture overlay) that
// file construction stopped being answerable by reading docs. More pointedly: the
// §5 capture overlay outranks the host layer PERMANENTLY and had no user-facing
// view at all, so a surface could silently diverge from what its layers would
// produce with the divergence recorded only in a sidecar the user has never heard
// of (docs/design/composed-file-permissions.md §5). This is the missing half of a
// mechanism already in production, not new-feature polish.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// surfaceRow is one line of the listing: a surface and how it is constructed.
type surfaceRow struct {
	Surface  string // "<owner>/<name>"
	Path     string // the ~-relative destination
	Codec    string
	Mode     string   // the posture: readonly | once | copy | capture
	Layers   []string // contributing layers, lowest precedence first
	Overlay  int      // captured overlay keys (-1 = surface writes no sidecar)
	HasFile  bool     // the destination exists on disk
	Reserved bool     // declared in the manifest but never rendered at boot
}

// prismSurfaceMode reports the posture of a BUILTIN surface. The builtin surfaces
// carry no mode field in the manifest — the posture is implied by which render
// helper the boot path calls, which is why this table is hand-maintained here and
// pinned by TestBuiltinSurfaceModesMatchBootPath.
//
// "capture" = rendered via renderSurfaceStateful (writes last_render + overlay
// sidecars, in-jail edits survive). "copy" = rendered via renderSurfaceComputed
// (pure per-boot overwrite, no sidecars, in-jail edits discarded).
// surfaceModeUnrendered marks a surface yolo does NOT compose: the agent owns the
// file and yolo only asserts individual keys into it (claude/config via
// writeClaudeJSON's read-modify-write). ls/diff/reset and render all skip these.
const surfaceModeUnrendered = "unrendered"

var prismSurfaceMode = map[string]string{
	"claude/settings": "capture",
	"pi/settings":     "capture",
	"copilot/config":  "capture",
	"opencode/config": "capture",
	"codex/config":    "capture",
	"agy/settings":    "capture",
	"mise/config":     "capture",
	"copilot/mcp":     "copy",
	"copilot/lsp":     "copy",
	"agy/mcp":         "copy",
	"claude/config":   surfaceModeUnrendered,
}

// configLs implements `yolo config ls [--all]`.
//
// In-jail, only surfaces whose destination EXISTS are listed by default: the
// builtin manifest declares every agent's surfaces while a jail configures only
// the selected ones, so listing all 12 in a claude-only jail would report files
// that do not and will not exist. --all lists the whole manifest. Host-side,
// presence is unknowable (the destinations are in the JAIL home, not the
// developer's), so everything is listed regardless — see composedFileExists.
func configLs(args []string, out, errw io.Writer, color bool) int {
	all := false
	for _, a := range args {
		switch {
		case isHelpToken(a):
			io.WriteString(out, configUsage+"\n")
			return 0
		case a == "--all":
			all = true
		default:
			fmt.Fprintf(errw, "yolo config ls: unknown argument %q\n\n%s\n", a, configUsage)
			return 2
		}
	}

	rows := collectSurfaceRows(all)
	if len(rows) == 0 {
		fmt.Fprintln(out, "No composed surfaces found.")
		return 0
	}
	writeSurfaceTable(out, rows, color)
	return 0
}

// collectSurfaceRows builds the listing: every builtin surface, then every
// user-declared host_files entry.
func collectSurfaceRows(all bool) []surfaceRow {
	var rows []surfaceRow
	for _, s := range agentcfg.BuiltinManifest().Surfaces() {
		key := s.Agent + "/" + s.Name
		mode := prismSurfaceMode[key]
		row := surfaceRow{
			Surface:  key,
			Path:     s.Path,
			Codec:    s.Codec,
			Mode:     mode,
			Layers:   builtinLayers(s),
			Overlay:  -1,
			HasFile:  composedFileExists(s.Path),
			Reserved: mode == surfaceModeUnrendered,
		}
		if mode == "capture" {
			row.Overlay = overlayKeyCount(s.Agent, s.Name)
		}
		if all || row.HasFile {
			rows = append(rows, row)
		}
	}
	rows = append(rows, hostFileRows()...)
	return rows
}

// hostFileRows lists the user's host_files entries. Read with probeSource=false:
// `config ls` is an inspection command and must never fail (or differ) because a
// host path is absent, and in-jail those paths are not in the mount namespace.
func hostFileRows() []surfaceRow {
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		cfg = jsonx.NewOrderedMap()
	}
	entries, err := config.LoadHostFiles(cfg, func(string) {}, false)
	if err != nil {
		return nil
	}
	var rows []surfaceRow
	for _, e := range entries {
		row := surfaceRow{
			Surface: "user/" + e.Slug(),
			Path:    "~/" + e.Path,
			Codec:   e.Codec,
			Mode:    e.Mode,
			Layers:  hostFileLayers(e),
			Overlay: -1,
			HasFile: composedFileExists("~/" + e.Path),
		}
		if e.IsDir {
			row.Codec = "(dir)"
		}
		if e.Mode == config.HostFileModeCapture {
			row.Overlay = overlayKeyCount("user", e.Slug())
		}
		rows = append(rows, row)
	}
	return rows
}

// builtinLayers names the layers a builtin surface composes from. `computed` is
// not derivable from the manifest (it is supplied per-surface by the boot caller),
// so it is reported from the same hand-maintained knowledge as the mode.
func builtinLayers(s manifest.Surface) []string {
	var layers []string
	if s.Defaults != nil {
		layers = append(layers, "defaults")
	}
	if surfaceHasHostLayer[s.Agent+"/"+s.Name] {
		layers = append(layers, "host")
	}
	if surfaceHasComputedLayer[s.Agent+"/"+s.Name] {
		layers = append(layers, "computed")
	}
	if s.Transform != "" {
		layers = append(layers, "transform")
	}
	if s.Managed != nil {
		layers = append(layers, "managed")
	}
	return layers
}

// surfaceHasHostLayer marks the surfaces whose boot render is handed host bytes.
// Only two exist, because agents.AgentSpec.HostFiles has exactly two entries —
// which host files cross into the jail is a credential boundary fixed in code.
var surfaceHasHostLayer = map[string]bool{
	"claude/settings": true,
	"pi/settings":     true,
}

// surfaceHasComputedLayer marks the surfaces whose boot render is handed a
// per-boot dynamic layer (MCP tables, LSP toggles, mise tools).
var surfaceHasComputedLayer = map[string]bool{
	"claude/settings": true,
	"codex/config":    true,
	"opencode/config": true,
	"mise/config":     true,
	"copilot/mcp":     true,
	"copilot/lsp":     true,
	"agy/mcp":         true,
}

// hostFileLayers names the layers a host_files entry composes from.
func hostFileLayers(e config.HostFileEntry) []string {
	var layers []string
	if e.Defaults != nil {
		layers = append(layers, "defaults")
	}
	switch {
	case e.SourceBearing():
		layers = append(layers, "host")
	case e.HasContent:
		layers = append(layers, "content")
	}
	if e.Transform != "" {
		layers = append(layers, "transform")
	}
	if e.Managed != nil {
		layers = append(layers, "managed")
	}
	return layers
}

// overlayKeyCount reads a surface's capture-overlay sidecar and reports how many
// keys it holds: 0 for an empty/absent overlay, and for a KEYLESS surface (raw /
// lines, whose overlay is a whole-file scalar or list) 1 when it carries anything
// at all — a file with no keys has exactly one "key", itself.
func overlayKeyCount(agent, name string) int {
	data, err := os.ReadFile(prismOverlayPath(agent, name))
	if err != nil {
		return 0
	}
	v, err := jsonx.Decode(data)
	if err != nil || v == nil {
		return 0
	}
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return t.Len()
	case []any:
		if len(t) == 0 {
			return 0
		}
		return 1
	case string:
		if t == "" {
			return 0
		}
		return 1
	default:
		return 1
	}
}

// prismOverlayPath is the CLI-side twin of entrypoint.prismOverlayPath. The
// sidecars are per-WORKSPACE (<workspace>/.yolo/prism/), so the CLI must resolve
// them against the same workspace the entrypoint used — the cwd, which is the
// workspace root for both a host-side and an in-jail invocation.
func prismOverlayPath(agent, name string) string {
	return filepath.Join(prismSidecarDir(), agent+"-"+name+".overlay.json")
}

// prismLastRenderPath is the CLI-side twin of entrypoint.prismLastRenderPath.
func prismLastRenderPath(agent, name string) string {
	return filepath.Join(prismSidecarDir(), agent+"-"+name+".last_render")
}

// prismSidecarDir is the per-workspace sidecar directory. A var so tests can
// point it at a temp workspace — without that seam an in-jail test run would read
// (and `reset` would DELETE) the real /workspace sidecars.
var prismSidecarDir = func() string {
	return filepath.Join(workspaceRoot(), ".yolo", "prism")
}

// workspaceRoot is the workspace whose sidecars this invocation is about: the
// CWD, walked upward to the nearest dir holding a .yolo/ (so the command works
// from a subdirectory, like git does).
//
// Deliberately NOT hardcoded to /workspace when in-jail. That shortcut is wrong
// the moment the CLI is run from a DIFFERENT workspace than the one it is jailed
// for — exactly what happens in a nested jail, and in the integration tests, where
// `yolo config ls` runs against a temp workspace while /workspace is the outer
// checkout. It silently read the wrong sidecars and `reset` would have deleted
// them.
func workspaceRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := wd; ; {
		if st, err := os.Stat(filepath.Join(dir, ".yolo")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return wd
}

// composedFileExists reports whether a surface's rendered file is present.
//
// A composed surface lives in the home of the jail THIS WORKSPACE launches, which
// is the process home only when the command runs inside that very jail. Two ways
// to get it wrong, both observed:
//
//   - host-side, expandHome points at the developer's OWN dotfiles, so every file
//     the jail did render reads as "absent" and a host dotfile the jail never
//     wrote reads as "present";
//   - in-jail but for a DIFFERENT workspace (a nested jail, and every integration
//     test), the process home belongs to the outer jail while the surfaces belong
//     to the inner one — same wrong answer.
//
// So presence is only knowable when the resolved workspace is the one this jail was
// launched for, which YOLO_HOST_DIR records. Otherwise we decline to claim absence
// rather than print a confidently wrong column.
func composedFileExists(surfacePath string) bool {
	if !surfacesAreLocal() {
		return true // unknowable here; never claim the file is missing
	}
	_, err := os.Lstat(expandHome(surfacePath))
	return err == nil
}

// surfacesAreLocal reports whether the process home is the home of the jail that
// owns the workspace we resolved. True only in-jail AND when YOLO_HOST_DIR (the
// host path of the workspace this jail was launched for) matches it.
func surfacesAreLocal() bool {
	if os.Getenv("YOLO_VERSION") == "" {
		return false // host-side
	}
	// In-jail the workspace is bind-mounted at /workspace; a resolved root anywhere
	// else means we are inspecting some OTHER workspace's surfaces.
	return workspaceRoot() == "/workspace"
}

// writeSurfaceTable renders the listing plus the divergence footer.
func writeSurfaceTable(out io.Writer, rows []surfaceRow, color bool) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Surface < rows[j].Surface })

	widths := []int{len("SURFACE"), len("PATH"), len("CODEC"), len("MODE"), len("LAYERS")}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		row := []string{r.Surface, r.Path, r.Codec, r.Mode, strings.Join(r.Layers, " ")}
		if row[4] == "" {
			row[4] = "-"
		}
		if row[2] == "" {
			row[2] = "-"
		}
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
		cells = append(cells, row)
	}

	pr := richtext.Printer{W: out, Color: color}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-len(s)) }
	pr.Printf("[bold]%s  %s  %s  %s  %s  %s[/bold]",
		pad("SURFACE", widths[0]), pad("PATH", widths[1]), pad("CODEC", widths[2]),
		pad("MODE", widths[3]), pad("LAYERS", widths[4]), "OVERLAY")

	diverged := 0
	for i, r := range rows {
		c := cells[i]
		overlay := "–"
		switch {
		case r.Reserved:
			overlay = "[dim](not rendered at boot)[/dim]"
		case r.Overlay > 0:
			diverged++
			overlay = fmt.Sprintf("[yellow]%d %s ⚠[/yellow]", r.Overlay, plural(r.Overlay, "key", "keys"))
		}
		missing := ""
		if !r.HasFile {
			missing = " [dim](absent)[/dim]"
		}
		pr.Printf("%s  %s  %s  %s  %s  %s%s",
			pad(c[0], widths[0]), pad(c[1], widths[1]), pad(c[2], widths[2]),
			pad(c[3], widths[3]), pad(c[4], widths[4]), overlay, missing)
	}

	if diverged > 0 {
		pr.Printf("")
		pr.Printf("[yellow]⚠ %d %s captured in-jail edits that outrank the host layer.[/yellow]",
			diverged, plural(diverged, "surface has", "surfaces have"))
		pr.Printf("  Inspect: [cyan]yolo config diff <agent> --surface <name>[/cyan]")
		pr.Printf("  Discard: [cyan]yolo config reset <agent> --surface <name>[/cyan]")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
