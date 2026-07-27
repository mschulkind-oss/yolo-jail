package entrypoint

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// hostFilesTestEnv builds an Env with a fake jail home, a fake /ctx/host-user
// mount (via the overridable hostUserDir), and a writable workspace for the §5
// sidecars. It returns the env and the mount dir so a test can seed the host
// source bytes at <mount>/<slug>.
func hostFilesTestEnv(t *testing.T) (*Env, string) {
	t.Helper()
	home := t.TempDir()
	ctx := t.TempDir()
	ws := t.TempDir()

	orig := hostUserDir
	hostUserDir = ctx
	t.Cleanup(func() { hostUserDir = orig })

	return &Env{Home: home, Workspace: ws, Vars: map[string]string{}}, ctx
}

// setHostFiles marshals entries into the YOLO_HOST_FILES env var the loop reads.
func setHostFiles(t *testing.T, e *Env, entries ...config.HostFileEntry) {
	t.Helper()
	wire, err := config.MarshalHostFiles(entries)
	if err != nil {
		t.Fatalf("MarshalHostFiles: %v", err)
	}
	e.Vars["YOLO_HOST_FILES"] = wire
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestHostFilesReadonlyRendersAndLocks: a source-bearing json entry in the
// default readonly mode merges the host bytes over defaults, writes the surface
// at 0o444, and re-renders (restoring host-side changes) on the next boot —
// proving the 0o444→0o644→re-lock chmod dance does not wedge the re-render.
func TestHostFilesReadonlyRendersAndLocks(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:     ".config/mytool/config.json",
		Source:   "/host/config.json", // presence marks source-bearing; slug is from Path
		Codec:    "json",
		Defaults: map[string]any{"level": "info", "colour": true},
		Mode:     config.HostFileModeReadonly,
	}
	// Seed the /ctx/host-user/<slug> mount with the host bytes.
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"level":"debug"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/mytool/config.json")
	got := decodeJSONFile(t, dest)
	if got["level"] != "debug" {
		t.Errorf("level = %v, want debug (host over default)", got["level"])
	}
	if got["colour"] != true {
		t.Errorf("colour = %v, want true (default survives)", got["colour"])
	}
	if fi, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o444 {
		t.Errorf("mode = %o, want 0444 (readonly)", fi.Mode().Perm())
	}

	// Boot 2: the dest is 0o444 from boot 1. A host change must still propagate,
	// which requires the re-render to succeed despite the read-only dest.
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"level":"warn"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	if got := decodeJSONFile(t, dest); got["level"] != "warn" {
		t.Errorf("boot 2 level = %v, want warn (host change must re-render over 0444)", got["level"])
	}
	// No sidecars for a readonly surface — it is not the capture mode.
	if _, err := os.Stat(prismOverlayPath(e, "user", entry.Slug())); !os.IsNotExist(err) {
		t.Errorf("readonly surface must not write an overlay sidecar (stat err %v)", err)
	}
}

// TestHostFilesOnceSeedsThenLeavesAlone: a source-less `once` entry seeds from
// its defaults when absent, and an in-jail edit survives the next boot untouched
// (no re-render, no sidecar).
func TestHostFilesOnceSeedsThenLeavesAlone(t *testing.T) {
	e, _ := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:     ".config/seed.json",
		Codec:    "json",
		Defaults: map[string]any{"seeded": true},
		Mode:     config.HostFileModeOnce,
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/seed.json")
	if got := decodeJSONFile(t, dest); got["seeded"] != true {
		t.Errorf("seeded = %v, want true", got["seeded"])
	}

	// Agent edits the seeded file. `once` must never touch it again.
	if err := os.WriteFile(dest, []byte(`{"seeded":false,"mine":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeJSONFile(t, dest)
	if got["seeded"] != false || got["mine"] != float64(1) {
		t.Errorf("once re-touched the file: %v (edit must persist verbatim)", got)
	}
}

// TestHostFilesCopyOverwritesEveryBoot: a `copy` entry is regenerated every boot,
// discarding an in-jail edit.
func TestHostFilesCopyOverwritesEveryBoot(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:   ".config/copy.json",
		Source: "/host/copy.json",
		Codec:  "json",
		Mode:   config.HostFileModeCopy,
	}
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"n":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/copy.json")
	// Agent edit, then boot 2 must overwrite it back to the host value.
	if err := os.WriteFile(dest, []byte(`{"n":999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	if got := decodeJSONFile(t, dest); got["n"] != float64(1) {
		t.Errorf("n = %v, want 1 (copy overwrites the in-jail edit)", got["n"])
	}
}

// TestHostFilesCaptureEditSurvives: the overlay exception — a capture entry
// re-renders every boot AND preserves an in-jail edit through the §5 sidecar.
func TestHostFilesCaptureEditSurvives(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:   ".config/capture.json",
		Source: "/host/capture.json",
		Codec:  "json",
		Mode:   config.HostFileModeCapture,
	}
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"host":"a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/capture.json")
	edited := decodeJSONFile(t, dest)
	edited["mine"] = "keepme"
	editedBytes := mustJSON(t, edited)
	if err := os.WriteFile(dest, editedBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeJSONFile(t, dest)
	if got["mine"] != "keepme" {
		t.Errorf("captured edit lost: %v", got)
	}
	if got["host"] != "a" {
		t.Errorf("host key lost: %v", got)
	}
	// Capture is the ONE mode that writes a sidecar.
	if _, err := os.Stat(prismOverlayPath(e, "user", entry.Slug())); err != nil {
		t.Errorf("capture mode must write an overlay sidecar: %v", err)
	}
}

// TestHostFilesRawNoTrailingNewline is the regression for the codec-aware newline
// fix: a `raw` surface promises a byte-exact round-trip, so the rendered file must
// carry NO appended "\n" that the source did not have.
func TestHostFilesRawNoTrailingNewline(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:   ".config/tool.conf",
		Source: "/host/tool.conf",
		Codec:  "raw",
		Mode:   config.HostFileModeCopy,
	}
	// Deliberately no trailing newline on the source.
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte("key=value"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/tool.conf")
	if got := readFile(t, dest); got != "key=value" {
		t.Errorf("raw surface = %q, want %q byte-exact (no appended newline)", got, "key=value")
	}
}

// TestHostFilesContentEmptyFile: a source-less entry with "content": "" declares
// a deliberately empty file, and the loop must create it (HasContent, not a
// nothing-to-compose skip).
func TestHostFilesContentEmptyFile(t *testing.T) {
	e, _ := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:       ".config/empty.conf",
		Codec:      "raw",
		Content:    "",
		HasContent: true,
		Mode:       config.HostFileModeOnce,
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/empty.conf")
	if got := readFile(t, dest); got != "" {
		t.Errorf("empty-content file = %q, want empty", got)
	}
}

// TestHostFilesDirCopiesTree: a directory entry recursively copies the mounted
// tree at /ctx/host-user/<slug> into the jail home, running no codec.
func TestHostFilesDirCopiesTree(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:   ".config/tooldir",
		Source: "/host/tooldir/",
		IsDir:  true,
		Mode:   config.HostFileModeCopy,
	}
	// Seed a small tree at the mount slug.
	slugDir := filepath.Join(ctx, entry.Slug())
	if err := os.MkdirAll(filepath.Join(slugDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slugDir, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slugDir, "sub", "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot: %v", err)
	}
	base := filepath.Join(e.Home, ".config/tooldir")
	if got := readFile(t, filepath.Join(base, "a.txt")); got != "A" {
		t.Errorf("a.txt = %q, want A", got)
	}
	if got := readFile(t, filepath.Join(base, "sub", "b.txt")); got != "B" {
		t.Errorf("sub/b.txt = %q, want B", got)
	}
}

// TestHostFilesMissingSourceFailsOpen: a source-bearing entry whose /ctx mount is
// absent (host source not present, or macos-user with no /ctx) falls back to its
// defaults layer rather than crashing the boot step.
func TestHostFilesMissingSourceFailsOpen(t *testing.T) {
	e, _ := hostFilesTestEnv(t) // nothing seeded at the mount slug
	entry := config.HostFileEntry{
		Path:     ".config/fallback.json",
		Source:   "/host/absent.json",
		Codec:    "json",
		Defaults: map[string]any{"fallback": true},
		Mode:     config.HostFileModeReadonly,
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot: %v", err)
	}
	dest := filepath.Join(e.Home, ".config/fallback.json")
	if got := decodeJSONFile(t, dest); got["fallback"] != true {
		t.Errorf("fallback = %v, want true (missing source → defaults layer)", got["fallback"])
	}
}

// TestConfigureHostFilesEmptyEnvIsNoop: an unset YOLO_HOST_FILES is the feature
// off — no error, and no files created.
func TestConfigureHostFilesEmptyEnvIsNoop(t *testing.T) {
	e, _ := hostFilesTestEnv(t)
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("empty env: %v", err)
	}
	if entries, _ := os.ReadDir(e.Home); len(entries) != 0 {
		t.Errorf("empty YOLO_HOST_FILES created %d home entries, want none", len(entries))
	}
}

// TestHostFilesHomeRootViaSymlink is the entrypoint half of the ~/.npmrc case
// (docs/design/composed-file-permissions.md §7.5). The CLI stages a DANGLING
// relative symlink in the :ro home base pointing into a writable overlay; the
// render must then work unchanged — the write follows the link, `once` seeds
// because Stat on a dangling link is ENOENT, and readonly's chmod lands on the
// real target rather than replacing the link.
func TestHostFilesHomeRootViaSymlink(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	// The writable overlay the link points into (the jail's ~/.config).
	if err := os.MkdirAll(filepath.Join(e.Home, ".config", "yolo-home"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The CLI's staging: a dangling relative symlink at the home-root path.
	link := filepath.Join(e.Home, ".npmrc")
	if err := os.Symlink(filepath.Join(".config", "yolo-home", "x"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Fatalf("fixture symlink must start dangling, got %v", err)
	}

	entry := config.HostFileEntry{
		Path:   ".npmrc",
		Source: "/host/.npmrc",
		Codec:  "raw",
		Mode:   config.HostFileModeOnce,
	}
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte("--smart-case\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	// The bytes must land THROUGH the link, in the writable overlay.
	target := filepath.Join(e.Home, ".config", "yolo-home", "x")
	if got := readFile(t, target); got != "--smart-case\n" {
		t.Errorf("content did not land in the overlay target: %q", got)
	}
	if got := readFile(t, link); got != "--smart-case\n" {
		t.Errorf("content not readable at the home-root path: %q", got)
	}
	if !isSymlink(t, link) {
		t.Error("the render replaced the symlink with a regular file; writes must follow it")
	}

	// `once` must now leave an in-jail edit alone (Stat succeeds through the link).
	if err := os.WriteFile(target, []byte("--mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	if got := readFile(t, target); got != "--mine\n" {
		t.Errorf("once re-seeded over an in-jail edit: %q", got)
	}
}

// TestHostFilesHomeRootReadonlyChmodsTarget: readonly mode must lock the file the
// symlink points at, not replace the link with a 0o444 regular file.
func TestHostFilesHomeRootReadonlyChmodsTarget(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	if err := os.MkdirAll(filepath.Join(e.Home, ".config", "yolo-home"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.Home, ".npmrc")
	if err := os.Symlink(filepath.Join(".config", "yolo-home", "y"), link); err != nil {
		t.Fatal(err)
	}

	entry := config.HostFileEntry{
		Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly,
	}
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte("a=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	target := filepath.Join(e.Home, ".config", "yolo-home", "y")
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o444 {
		t.Errorf("target mode = %o, want 0444", fi.Mode().Perm())
	}
	if !isSymlink(t, link) {
		t.Error("readonly mode replaced the symlink instead of chmod'ing its target")
	}
	// A host-side change must still re-render over the 0o444 target.
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte("a=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	if got := readFile(t, target); got != "a=2\n" {
		t.Errorf("host change did not propagate through the symlink: %q", got)
	}
}

// isSymlink reports whether path is a symlink (Lstat, not Stat).
func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return fi.Mode()&os.ModeSymlink != 0
}

// TestCaptureOverlayBootNotice: a capture surface rendering with a NON-EMPTY
// overlay must announce it. An overlay outranks the host layer permanently, so a
// divergence recorded only in a sidecar is invisible state — the notice makes it
// visible at the moment it is applied, and names the command that explains it.
func TestCaptureOverlayBootNotice(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)
	var stderr bytes.Buffer
	e.Stderr = &stderr

	entry := config.HostFileEntry{
		Path:   ".config/captured.json",
		Source: "/host/captured.json",
		Codec:  "json",
		Mode:   config.HostFileModeCapture,
	}
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"host":"a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	// Boot 1 seeds; the overlay is empty, so nothing should be announced.
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	if strings.Contains(stderr.String(), "captured in-jail edits") {
		t.Errorf("empty overlay produced a notice (it must stay quiet):\n%s", stderr.String())
	}

	// An in-jail edit, then boot 2 must capture it AND announce it.
	dest := filepath.Join(e.Home, ".config/captured.json")
	if err := os.WriteFile(dest, []byte(`{"host":"a","mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := stderr.String()
	for _, want := range []string{"~/.config/captured.json", "1 key from captured in-jail edits", "yolo config diff"} {
		if !strings.Contains(got, want) {
			t.Errorf("boot notice missing %q:\n%s", want, got)
		}
	}
}

// TestNoNoticeForNonCaptureModes: readonly/once/copy write no sidecar, so they can
// never diverge and must never print the notice.
func TestNoNoticeForNonCaptureModes(t *testing.T) {
	for _, mode := range []string{
		config.HostFileModeReadonly, config.HostFileModeOnce, config.HostFileModeCopy,
	} {
		e, ctx := hostFilesTestEnv(t)
		var stderr bytes.Buffer
		e.Stderr = &stderr
		entry := config.HostFileEntry{
			Path: ".config/plain.json", Source: "/host/plain.json", Codec: "json", Mode: mode,
		}
		if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"a":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		setHostFiles(t, e, entry)
		if err := ConfigureHostFiles(e); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if strings.Contains(stderr.String(), "captured in-jail edits") {
			t.Errorf("mode %s printed a capture notice:\n%s", mode, stderr.String())
		}
	}
}

// TestOverlayEntryCount covers the shapes an overlay sidecar can hold: an object
// counts keys, a keyless surface (raw string / lines array) counts as one whole
// file, and every empty form is zero.
func TestOverlayEntryCount(t *testing.T) {
	for _, c := range []struct {
		json string
		want int
	}{
		{`{}`, 0},
		{`{"a":1,"b":2}`, 2},
		{`null`, 0},
		{`""`, 0},
		{`"edited content"`, 1},
		{`[]`, 0},
		{`["line"]`, 1},
		{``, 0},
		{`   `, 0},
		{`not json`, 0},
	} {
		if got := overlayEntryCount([]byte(c.json)); got != c.want {
			t.Errorf("overlayEntryCount(%q) = %d, want %d", c.json, got, c.want)
		}
	}
}

// A9: `transform` is a DOCUMENTED host_files key (config_ref: "path to a Lua
// hook; works on every codec") that was schema-validated, parsed, path-cleaned,
// and copied onto the surface — and then never read. Every Inputs.Script producer
// filled it from the global config.lua pair only, so a user's per-surface hook was
// silently ignored. This pins that the hook actually runs.
func TestHostFilesPerSurfaceTransformRuns(t *testing.T) {
	e, ctx := hostFilesTestEnv(t)

	// A per-surface hook that rewrites a key. Surfaces synthesized from host_files
	// use agent "user" (hostFileSurface), so the hook registers under that name.
	hook := filepath.Join(t.TempDir(), "hook.lua")
	if err := os.WriteFile(hook, []byte(`
yolo.transform("user", function(ctx)
  ctx.config.injected = "by-transform"
  ctx.config.level = "rewritten"
end)`), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := config.HostFileEntry{
		Path:      ".config/xform/config.json",
		Source:    "/host/xform.json",
		Codec:     "json",
		Transform: hook,
		Mode:      config.HostFileModeReadonly,
	}
	if err := os.WriteFile(filepath.Join(ctx, entry.Slug()), []byte(`{"level":"debug"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setHostFiles(t, e, entry)

	if err := ConfigureHostFiles(e); err != nil {
		t.Fatalf("boot: %v", err)
	}
	got := decodeJSONFile(t, filepath.Join(e.Home, ".config/xform/config.json"))
	if got["injected"] != "by-transform" {
		t.Errorf("per-surface transform did not run: %v", got)
	}
	if got["level"] != "rewritten" {
		t.Errorf("transform must be able to overwrite a host value: %v", got)
	}
}

// A missing or unreadable transform file must FAIL the surface, not be silently
// skipped: a user who names a hook and gets no hook has no way to tell.
func TestHostFilesMissingTransformIsAnError(t *testing.T) {
	e, _ := hostFilesTestEnv(t)
	entry := config.HostFileEntry{
		Path:      ".config/xform2/config.json",
		Codec:     "json",
		Defaults:  map[string]any{"a": 1},
		Transform: filepath.Join(t.TempDir(), "does-not-exist.lua"),
		Mode:      config.HostFileModeReadonly,
	}
	setHostFiles(t, e, entry)
	if err := ConfigureHostFiles(e); err == nil {
		t.Error("a named-but-missing transform must be an error, not a silent skip")
	}
}
