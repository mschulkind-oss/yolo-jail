package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unit tests for ValidateConfig's cache_relocations rules. Other validators are
// covered by the differential oracle; these are the filesystem-touching ones.

// validateCache runs ValidateConfig over a config containing only
// cache_relocations and returns just the cache_relocations errors.
func validateCache(t *testing.T, workspace, body string) []string {
	t.Helper()
	// Pin the host case explicitly. The target-parent probe is host-only (see
	// validateCacheRelocations), and this project is developed from inside its
	// own jail, where YOLO_VERSION is set — without this the suite would assert
	// host behavior while running as if in a jail.
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"cache_relocations": `+body+`}`), workspace, nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.cache_relocations") {
			out = append(out, e)
		}
	}
	return out
}

func TestValidateCacheRelocationsKnownKey(t *testing.T) {
	// Without the knownTopLevelConfigKeys entry every config carrying the key
	// would fail with "unknown key".
	errs, _ := ValidateConfig(decode(t, `{"cache_relocations": {}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "unknown key") {
			t.Errorf("unexpected error: %s", e)
		}
	}
}

func TestValidateCacheRelocationsBadSubdirs(t *testing.T) {
	ws := t.TempDir()
	parent := filepath.Join(ws, "data")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "hf")
	for _, key := range []string{"../etc", "a/b", ".", "..", ""} {
		errs := validateCache(t, ws, `{"`+key+`": "`+target+`"}`)
		if len(errs) != 1 || !strings.Contains(errs[0], "invalid subdir") {
			t.Errorf("subdir %q: errors = %v, want one 'invalid subdir'", key, errs)
		}
	}
}

func TestValidateCacheRelocationsTargetRules(t *testing.T) {
	ws := t.TempDir()
	parent := filepath.Join(ws, "data")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	hf := filepath.Join(parent, "hf")

	cases := []struct {
		name string
		body string
		want string
	}{
		{"relative target", `{"huggingface": "data/hf"}`, "must be an absolute path"},
		{"non-string target", `{"huggingface": 7}`, "expected an absolute host path string"},
		{"not an object", `["huggingface"]`, "expected an object mapping"},
		{
			"duplicate targets",
			`{"huggingface": "` + hf + `", "uv": "` + hf + `/"}`,
			"is already relocated from subdir",
		},
		{
			"missing target parent",
			`{"huggingface": "` + filepath.Join(ws, "typo", "hf") + `"}`,
			"parent directory of the target does not exist",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateCache(t, ws, tc.body)
			if len(errs) != 1 || !strings.Contains(errs[0], tc.want) {
				t.Errorf("errors = %v, want one containing %q", errs, tc.want)
			}
		})
	}
}

// A missing FINAL segment is fine — storage.EnsureCacheRelocations creates it.
// Only a missing parent is an error (a missing parent means a typo).
func TestValidateCacheRelocationsMissingFinalSegmentIsOK(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	errs := validateCache(t, ws, `{"huggingface": "`+filepath.Join(ws, "data", "hf")+`"}`)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

func TestValidateCacheRelocationsRejectsWorkspaceScope(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "hf")
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"cache_relocations": {"huggingface": "`+target+`"}}`)

	// ValidateConfig sees only the merged map, so the workspace-scope error can
	// only come from the re-read of the workspace config.
	errs := validateCache(t, ws, `{"huggingface": "`+target+`"}`)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	for _, want := range []string{"user-scope only", "~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("error %q does not name %q", errs[0], want)
		}
	}
}

func TestValidateCacheRelocationsWorkspaceLocalScopeAlsoRejected(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "hf")
	write(t, filepath.Join(ws, WorkspaceLocalConfigName),
		`{"cache_relocations": {"huggingface": "`+target+`"}}`)

	errs := validateCache(t, ws, `{"huggingface": "`+target+`"}`)
	if len(errs) != 1 || !strings.Contains(errs[0], "user-scope only") {
		t.Errorf("errors = %v, want one 'user-scope only'", errs)
	}
}

// Regression: a valid HOST relocation must not brick jails. In a jail the
// merged config is the host-written snapshot (or the read-only-mounted host user
// config), so it carries the host's targets — paths deliberately absent from the
// jail's mount namespace. Stat'ing them there turned every nested `yolo` run and
// every in-jail `yolo check` into "Invalid jail config". The shape and scope
// rules must still fire; only the filesystem probe is host-only.
func TestValidateCacheRelocationsSkipsTargetParentInJail(t *testing.T) {
	ws := t.TempDir()
	hostOnly := "/data/relocated/yolo-jail/cache/huggingface"
	body := `{"huggingface": "` + hostOnly + `"}`

	if errs := validateCache(t, ws, body); len(errs) != 1 ||
		!strings.Contains(errs[0], "parent directory of the target does not exist") {
		t.Fatalf("host: errors = %v, want the missing-parent error (typo protection)", errs)
	}

	// validateCache pins YOLO_VERSION=""; set it back to model a jail.
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	errs, _ := ValidateConfig(decode(t, `{"cache_relocations": `+body+`}`), ws, nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.cache_relocations") {
			t.Errorf("in jail: unexpected error %q — a valid host config must not brick nested jails", e)
		}
	}

	// Shape errors still fire in a jail: only the fs probe is gated.
	errs, _ = ValidateConfig(decode(t, `{"cache_relocations": {"../etc": "`+hostOnly+`"}}`), ws, nil)
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e, "config.cache_relocations") {
			found = true
		}
	}
	if !found {
		t.Errorf("in jail: bad subdir accepted; shape rules must apply everywhere")
	}
}

// User scope only: no workspace file, valid entry -> clean.
func TestValidateCacheRelocationsUserScopeClean(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "hf")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if errs := validateCache(t, ws, `{"huggingface": "`+target+`"}`); len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}
