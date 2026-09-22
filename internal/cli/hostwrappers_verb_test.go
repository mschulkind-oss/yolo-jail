package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// wrappersTestHome points every path the wrappers verb resolves through at a temp home:
// paths.UserConfigPath (where the opt-in is written), paths.WrapDir (what status lists)
// and config.HostWrappersEnabled all derive from $HOME. PATH is pinned to a directory
// that cannot contain the wrap dir so the on/off-PATH verdict is decided by the test,
// not by the shell that happened to run it.
func wrappersTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "/nonexistent-wrappers-test-bin")
	return home
}

// TestHostWrappersStatusReportsStateWrappersAndPathVerdict pins what `wrappers status`
// tells a user deciding whether to opt in: the CURRENT enabled state as the reader sees
// it, the wrapper names already generated (sorted), and the on/off-PATH verdict.
//
// Fails if hostWrappersStatus stops printing any of those, or if the `status` dispatch
// in hostWrappers is removed.
func TestHostWrappersStatusReportsStateWrappersAndPathVerdict(t *testing.T) {
	cases := []struct {
		name      string
		cfg       string
		wrappers  []string
		dirExists bool // the "not generated yet" verdict needs the dir to be ABSENT
		onPath    bool
		want      []string
		notWant   []string
	}{
		{
			name:      "disabled, nothing generated, off PATH",
			cfg:       `{"packs": ["claude"]}`,
			dirExists: false,
			want: []string{
				"host_wrappers  false",
				"wrapper dir",
				"not generated yet",
				"NOT on this shell's PATH",
				"$PATHLINE", // resolved in the subtest: the remediation handed to the user
				"$CFGPATH",  // resolved in the subtest: where the opt-in lives
			},
		},
		{
			name:      "enabled, wrappers listed sorted",
			cfg:       `{"host_wrappers": true}`,
			wrappers:  []string{"pi", "claude"}, // written out of order on purpose
			dirExists: true,
			want:      []string{"host_wrappers  true", "claude pi"},
			notWant:   []string{"not generated yet"},
		},
		{
			name:      "enabled and on PATH is reported without the nag",
			cfg:       `{"host_wrappers": true}`,
			wrappers:  []string{"claude"},
			dirExists: true,
			onPath:    true,
			want:      []string{"on PATH"},
			notWant:   []string{"NOT on this shell's PATH"},
		},
		{
			name:      "an existing but empty generated dir says so",
			cfg:       `{}`,
			dirExists: true,
			want:      []string{"generated, empty"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := wrappersTestHome(t)
			dir := paths.WrapDirUnder(home)
			userCfg(t, home, tc.cfg)
			if tc.dirExists {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range tc.wrappers {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.onPath {
				t.Setenv("PATH", dir+string(os.PathListSeparator)+"/nonexistent-wrappers-test-bin")
			}

			var out, errw bytes.Buffer
			if rc := hostWrappers([]string{"status"}, &out, &errw, false); rc != 0 {
				t.Fatalf("status rc = %d (stderr: %q)", rc, errw.String())
			}
			for _, want := range tc.want {
				switch want {
				case "$PATHLINE":
					want = hostwrap.PathLine(dir)
				case "$CFGPATH":
					want = paths.UserConfigPath()
				}
				if !strings.Contains(out.String(), want) {
					t.Errorf("status output missing %q:\n%s", want, out.String())
				}
			}
			for _, ban := range tc.notWant {
				if strings.Contains(out.String(), ban) {
					t.Errorf("status output must not contain %q:\n%s", ban, out.String())
				}
			}
		})
	}
}

// TestWrappersDeriveFromHostManagementOwn is the behaviour that replaced `wrappers enable`.
//
// The verb existed so a user could set one boolean. The boolean is now DERIVED: `host_management:
// "own"` means yolo composes the agent's config file whole, and a config file cannot carry a
// credential — the value arrives only through `yolo host -- <agent>` — so `own` with no wrappers on
// PATH is a half-configured host that was reachable by declaring one key without knowing a second
// existed.
func TestWrappersDeriveFromHostManagementOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  string
		want bool
		why  string
	}{
		{"own derives ON", `{"host_management": "own"}`, true,
			"own means yolo owns the config file; without wrappers nothing delivers the environment it assumes"},
		{"explicit false still wins", `{"host_management": "own", "host_wrappers": false}`, false,
			"someone who wants yolo to own their files and refuses a PATH claim must be able to say so"},
		{"explicit true without own", `{"host_wrappers": true}`, true,
			"the key still works on its own"},
		{"assert does NOT derive", `{"host_management": "assert"}`, false,
			"assert is host_management's own unset default, so deriving from it would switch a PATH claim " +
				"on for every user who declared nothing"},
		{"nothing declared", `{}`, false,
			"a user who asked for neither must get neither"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			t.Chdir(t.TempDir())
			userCfg(t, home, tc.cfg)
			if got := config.HostWrappersEnabled(); got != tc.want {
				t.Errorf("HostWrappersEnabled() = %v, want %v — %s", got, tc.want, tc.why)
			}
		})
	}
}

// The removed verbs REFUSE rather than 404, and the refusal explains the derivation — a user who
// typed `enable` because a doc or their shell history told them to needs to learn that the key is
// usually unnecessary now, not just that a subcommand is gone.
func TestRemovedWrapperVerbsRefuseAndExplain(t *testing.T) {
	for _, verb := range []string{"enable", "disable"} {
		var out, errw bytes.Buffer
		rc := hostMain([]string{"wrappers", verb}, &out, &errw, false, nil)
		if rc != 2 {
			t.Errorf("`wrappers %s` rc = %d, want 2 (misuse)", verb, rc)
		}
		got := errw.String()
		for _, want := range []string{"was removed", "host_management", "host_wrappers", "status"} {
			if !strings.Contains(got, want) {
				t.Errorf("`wrappers %s` refusal must mention %q; got: %s", verb, want, got)
			}
		}
	}
}

// `status` survives, because it READS. That is the whole distinction the removal drew: yolo reports
// what is in effect and does not edit the file that decides it.
func TestWrappersStatusSurvives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"host_management": "own"}`)

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"wrappers"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("bare `wrappers` (status) rc = %d, want 0: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "host_wrappers") {
		t.Errorf("status must report the key's effective value; got: %s", out.String())
	}
}
