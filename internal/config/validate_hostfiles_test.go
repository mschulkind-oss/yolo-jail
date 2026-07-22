package config

import (
	"strings"
	"testing"
)

// hostFilesErrs runs ValidateConfig on a config whose `key` holds `entries` and
// returns only the errors mentioning that key. resolver is nil (no loopholes in
// these fixtures, so validateLoopholes short-circuits on the absent section).
func hostFilesErrs(t *testing.T, key string, entries ...string) []string {
	t.Helper()
	m := decode(t, "{}")
	list := make([]any, len(entries))
	for i, e := range entries {
		list[i] = e
	}
	m.Set(key, list)
	errs, _ := ValidateConfig(m, t.TempDir(), nil)
	var got []string
	for _, e := range errs {
		if strings.Contains(e, key) {
			got = append(got, e)
		}
	}
	return got
}

// TestHostAgentFilesAllowsSubdirs is the FR regression (scratch/yolo-fr-host-pi-
// files-subdirs.md): a pi/claude provider whose helper script lives under a
// subdir of ~/.pi/agent (e.g. "mantle/mint-token.mjs", referenced by a
// models.json !command apiKey) must be stageable via host_pi_files. The mount
// side (ROFileMountArg) and the copy side (syncHostPiFiles) already handle
// subpaths; only validateHostAgentFiles blocked them.
func TestHostAgentFilesAllowsSubdirs(t *testing.T) {
	for _, key := range []string{"host_pi_files", "host_claude_files"} {
		t.Run(key, func(t *testing.T) {
			if errs := hostFilesErrs(t, key, "settings.json", "models.json", "mantle/mint-token.mjs"); len(errs) != 0 {
				t.Errorf("relative subpath rejected: %v", errs)
			}
		})
	}
}

// TestHostAgentFilesRejectsUnsafePaths keeps the guard fail-closed: absolute
// paths and `..` traversal must still be rejected (mirrors the
// workspace_readonly / per_side_paths guard style).
func TestHostAgentFilesRejectsUnsafePaths(t *testing.T) {
	for _, key := range []string{"host_pi_files", "host_claude_files"} {
		t.Run(key+"/absolute", func(t *testing.T) {
			if errs := hostFilesErrs(t, key, "/etc/passwd"); len(errs) == 0 {
				t.Error("absolute path was accepted; want rejected")
			}
		})
		t.Run(key+"/dotdot", func(t *testing.T) {
			if errs := hostFilesErrs(t, key, "mantle/../../secrets/key.pem"); len(errs) == 0 {
				t.Error("`..` traversal was accepted; want rejected")
			}
		})
		t.Run(key+"/backslash", func(t *testing.T) {
			// Windows-style separators aren't a valid relative POSIX subpath and
			// stay rejected (the container is Linux).
			if errs := hostFilesErrs(t, key, `mantle\mint-token.mjs`); len(errs) == 0 {
				t.Error("backslash path was accepted; want rejected")
			}
		})
	}
}
