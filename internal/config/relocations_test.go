package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unit tests for the user-scope-only cache_relocations loader. Every case
// isolates HOME so paths.UserConfigPath() lands inside a t.TempDir().

// userConfigHome points HOME at a fresh temp dir, writes the given user config
// (when content != ""), and returns the home dir.
func userConfigHome(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Pin the host case: relocation is inert in a jail, and this project is
	// developed from inside its own jail (YOLO_VERSION set). Tests that want the
	// in-jail behavior set YOLO_VERSION back themselves.
	t.Setenv("YOLO_VERSION", "")
	if content != "" {
		write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), content)
	}
	return home
}

// collectWarn records warnings so a test can assert on the skip messages.
func collectWarn(msgs *[]string) Warn {
	return func(msg string) { *msgs = append(*msgs, msg) }
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCacheRelocationsAbsentKey(t *testing.T) {
	userConfigHome(t, `{"packages": ["htop"]}`)
	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil relocations, got %v", got)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
}

func TestLoadCacheRelocationsMissingUserConfig(t *testing.T) {
	userConfigHome(t, "")
	got, err := LoadCacheRelocations(nil) // nil warn must not panic
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil, nil", got, err)
	}
}

func TestLoadCacheRelocationsHappyPathSorted(t *testing.T) {
	home := userConfigHome(t, "")
	hf := mkdir(t, filepath.Join(home, "data", "huggingface"))
	uv := mkdir(t, filepath.Join(home, "data", "uv"))
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"cache_relocations": {"uv": "`+uv+`", "huggingface": "`+hf+`"}}`)

	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	want := []CacheRelocation{{Subdir: "huggingface", Target: hf}, {Subdir: "uv", Target: uv}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Sorted by Subdir even though the config lists uv first — argv must be
	// deterministic.
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
}

func TestLoadCacheRelocationsExpandsTilde(t *testing.T) {
	home := userConfigHome(t, `{"cache_relocations": {"huggingface": "~/data/hf/"}}`)
	target := mkdir(t, filepath.Join(home, "data", "hf"))

	got, err := LoadCacheRelocations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Target != target {
		t.Fatalf("got %v, want one entry targeting %s", got, target)
	}
}

func TestLoadCacheRelocationsReadsIncludes(t *testing.T) {
	home := userConfigHome(t, `{"include_if_found": ["overrides.jsonc"]}`)
	target := mkdir(t, filepath.Join(home, "data", "hf"))
	write(t, filepath.Join(home, ".config", "yolo-jail", "overrides.jsonc"),
		`{"cache_relocations": {"huggingface": "`+target+`"}}`)

	got, err := LoadCacheRelocations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Target != target {
		t.Fatalf("got %v, want one entry targeting %s", got, target)
	}
}

// The workspace config is agent-writable, so it must not be able to introduce a
// read-write host mount even when the process is running in that workspace.
func TestLoadCacheRelocationsIgnoresWorkspaceConfig(t *testing.T) {
	home := userConfigHome(t, "")
	target := mkdir(t, filepath.Join(home, "data", "hf"))
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"cache_relocations": {"huggingface": "`+target+`"}}`)
	t.Chdir(ws)

	got, err := LoadCacheRelocations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("workspace-scoped relocation was honored: %v", got)
	}
}

// A not-yet-created target is KEPT: storage.EnsureCacheRelocations creates the
// last path component before anything is mounted, so a fresh host with the
// config set works without a manual mkdir. Dropping it here would silently
// leave the cache on the filesystem the user was moving it off.
func TestLoadCacheRelocationsKeepsMissingTarget(t *testing.T) {
	home := userConfigHome(t, "")
	good := mkdir(t, filepath.Join(home, "data", "uv"))
	missing := filepath.Join(home, "data", "hf") // parent exists, target does not
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"cache_relocations": {"huggingface": "`+missing+`", "uv": "`+good+`"}}`)

	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	want := []CacheRelocation{{Subdir: "huggingface", Target: missing}, {Subdir: "uv", Target: good}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

// A missing PARENT is still dropped — that is the typo guard (/data/relcoated/…
// would otherwise be MkdirAll'd back onto the root filesystem).
func TestLoadCacheRelocationsSkipsMissingTargetParent(t *testing.T) {
	home := userConfigHome(t, "")
	good := mkdir(t, filepath.Join(home, "data", "uv"))
	typo := filepath.Join(home, "relcoated", "hf") // parent does not exist either
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"cache_relocations": {"huggingface": "`+typo+`", "uv": "`+good+`"}}`)

	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subdir != "uv" {
		t.Fatalf("got %v, want only the uv entry", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], filepath.Dir(typo)) {
		t.Errorf("warnings = %v, want one naming %s", warns, filepath.Dir(typo))
	}
}

func TestLoadCacheRelocationsSkipsBadSyntax(t *testing.T) {
	home := userConfigHome(t, "")
	good := mkdir(t, filepath.Join(home, "data", "hf"))
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"cache_relocations": {"../etc": "`+good+`", "rel": "relative/path",`+
			` "huggingface": "`+good+`"}}`)

	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	// "../etc" is not a single segment; "rel" is relative; "huggingface" is the
	// only usable entry (and it keeps the target the rejected key also named).
	if len(got) != 1 || got[0].Subdir != "huggingface" || got[0].Target != good {
		t.Fatalf("got %v, want only huggingface -> %s", got, good)
	}
	if len(warns) != 2 {
		t.Fatalf("warnings = %v, want 2", warns)
	}
	for _, w := range warns {
		if !strings.HasSuffix(w, "relocation skipped") {
			t.Errorf("warning %q does not say it skipped", w)
		}
	}
}

func TestLoadCacheRelocationsUnparseableUserConfigErrors(t *testing.T) {
	userConfigHome(t, "{not valid jsonc")
	if _, err := LoadCacheRelocations(nil); err == nil {
		t.Errorf("expected an error for an unparseable user config")
	}
}

func TestLoadCacheRelocationsWrongType(t *testing.T) {
	userConfigHome(t, `{"cache_relocations": ["huggingface"]}`)
	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "expected an object") {
		t.Errorf("warnings = %v, want one 'expected an object'", warns)
	}
}

// Relocation is a host-side feature. In a jail the user config is visible (the
// host's file, bind-mounted read-only) but its targets are host paths that are
// not in the jail's mount namespace — emitting one would hand podman a missing
// bind source and kill the container.
func TestLoadCacheRelocationsInJailIsInert(t *testing.T) {
	home := userConfigHome(t, "")
	good := mkdir(t, filepath.Join(home, "data", "hf"))
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"cache_relocations": {"huggingface": "`+good+`"}}`)

	// Host: the entry loads.
	got, err := LoadCacheRelocations(nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("host: got %v, err %v; want one entry", got, err)
	}

	t.Setenv("YOLO_VERSION", "9.9.9-test")
	var warns []string
	got, err = LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("in jail: got %v, want no relocations", got)
	}
	if len(warns) != 0 {
		t.Errorf("in jail: warnings = %v, want silence (nested runs are not a problem to report)", warns)
	}
}

// A ':' anywhere in the target would be eaten by podman's src:dst:options
// parsing, silently turning the rest of the path into mount options.
func TestLoadCacheRelocationsRejectsColonTarget(t *testing.T) {
	home := userConfigHome(t, "")
	parent := mkdir(t, filepath.Join(home, "data"))
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"cache_relocations": {"huggingface": "`+filepath.Join(parent, "hf:models")+`"}}`)

	var warns []string
	got, err := LoadCacheRelocations(collectWarn(&warns))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %v, want the colon target rejected", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "':'") {
		t.Errorf("warnings = %v, want one naming the ':' problem", warns)
	}
}

func TestCheckCacheRelocationSubdir(t *testing.T) {
	// ':' matters as much as '/': podman parses -v as src:dst:options, so a
	// subdir of "hf:ro" mounts at ~/.cache/hf READ-ONLY and "hf:U" recursively
	// chowns the target. Both verified against podman 5.8.4.
	bad := []string{"", ".", "..", "../etc", "a/b", "/abs", "trailing/", "hf:ro", "hf:U", ":"}
	for _, k := range bad {
		if msg := checkCacheRelocationSubdir(k); msg == "" {
			t.Errorf("subdir %q: accepted, want rejected", k)
		}
	}
	for _, k := range []string{"huggingface", "go-build", ".cache-ish", "a.b"} {
		if msg := checkCacheRelocationSubdir(k); msg != "" {
			t.Errorf("subdir %q: rejected (%s), want accepted", k, msg)
		}
	}
}
