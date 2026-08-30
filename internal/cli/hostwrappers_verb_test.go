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

// TestHostWrappersVerbDispatch pins the verb grammar: an unknown verb is a FAILURE that
// names itself and the legal verbs, help tokens print the usage, and NO argument means
// `status` rather than an error.
//
// Fails if hostWrappers stops dispatching (e.g. an early `return 0` before the switch):
// the unknown-verb exit code, the usage, and the default-verb output all vanish with it.
func TestHostWrappersVerbDispatch(t *testing.T) {
	wrappersTestHome(t)

	cases := []struct {
		name    string
		args    []string
		wantRC  int
		wantOut []string
		wantErr []string
	}{
		{
			name:    "unknown verb exits 1 and names itself and the legal ones",
			args:    []string{"frobnicate"},
			wantRC:  1,
			wantErr: []string{`unknown verb "frobnicate"`, "status, enable or disable"},
		},
		{
			name:    "-h prints the host usage",
			args:    []string{"-h"},
			wantRC:  0,
			wantOut: []string{"yolo host"},
		},
		{
			name:    "--help prints the host usage",
			args:    []string{"--help"},
			wantRC:  0,
			wantOut: []string{"yolo host"},
		},
		{
			name:    "help prints the host usage",
			args:    []string{"help"},
			wantRC:  0,
			wantOut: []string{"yolo host"},
		},
		{
			name:    "no argument defaults to status",
			args:    nil,
			wantRC:  0,
			wantOut: []string{"host_wrappers  false", "not generated yet"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			if rc := hostWrappers(tc.args, &out, &errw, false); rc != tc.wantRC {
				t.Errorf("hostWrappers(%q) rc = %d, want %d (stderr: %q)", tc.args, rc, tc.wantRC, errw.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, out.String())
				}
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(errw.String(), want) {
					t.Errorf("stderr missing %q:\n%s", want, errw.String())
				}
			}
		})
	}
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

// TestHostWrappersEnableDisableRoundTripsThroughTheReader pins the config write behind
// `wrappers enable`/`disable` — BOTH branches of setHostWrappers (create-from-scratch
// when no user config exists, and the targeted edit when one does) — by reading the
// result back through config.HostWrappersEnabled, the same reader apply consults.
//
// The file's TEXT is deliberately not asserted: the writer is a textual edit and the
// reader is a parser, and a previous finding showed the two can select different
// textual occurrences, so only the reader's verdict is the contract.
//
// Fails if setHostWrappers stops writing (a no-op leaves the reader at false).
func TestHostWrappersEnableDisableRoundTripsThroughTheReader(t *testing.T) {
	cases := []struct {
		name string
		seed string // "" means no user config file exists at all
	}{
		{name: "no config file: created from scratch", seed: ""},
		{name: "config without the key: inserted", seed: `{"packs": ["claude"]}`},
		{name: "config with the key already false: replaced", seed: `{"host_wrappers": false, "packs": ["claude"]}`},
		{
			name: "config with a comment: the comment survives the edit",
			seed: "{\n  // keep me\n  \"host_wrappers\": false,\n  \"packs\": []\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := wrappersTestHome(t)
			if tc.seed == "" {
				if _, err := os.Stat(paths.UserConfigPath()); !os.IsNotExist(err) {
					t.Fatalf("precondition: a user config already exists at %s", paths.UserConfigPath())
				}
			} else {
				userCfg(t, home, tc.seed)
			}

			var out, errw bytes.Buffer
			if rc := hostWrappers([]string{"enable"}, &out, &errw, false); rc != 0 {
				t.Fatalf("enable rc = %d (stderr: %q)", rc, errw.String())
			}
			if !strings.Contains(out.String(), "host_wrappers = true") {
				t.Errorf("enable did not report the new value:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "to generate the wrappers") {
				t.Errorf("enable did not point at the apply that generates them:\n%s", out.String())
			}
			if !config.HostWrappersEnabled() {
				t.Error("after enable, config.HostWrappersEnabled() = false — the write did not round-trip")
			}
			if tc.seed == "" {
				if _, err := os.Stat(paths.UserConfigPath()); err != nil {
					t.Errorf("enable created no config file: %v", err)
				}
			}

			out.Reset()
			errw.Reset()
			if rc := hostWrappers([]string{"disable"}, &out, &errw, false); rc != 0 {
				t.Fatalf("disable rc = %d (stderr: %q)", rc, errw.String())
			}
			if !strings.Contains(out.String(), "to remove them") {
				t.Errorf("disable did not point at the apply that removes them:\n%s", out.String())
			}
			if config.HostWrappersEnabled() {
				t.Error("after disable, config.HostWrappersEnabled() = true — the write did not round-trip")
			}
		})
	}
}

// TestHostWrappersEnableFailureIsALoudNonZeroExit: setHostWrappers can fail (a user
// config with no object in it leaves nowhere to put the key), and the verb must turn
// that into a non-zero exit on stderr rather than a success the user will believe.
func TestHostWrappersEnableFailureIsALoudNonZeroExit(t *testing.T) {
	home := wrappersTestHome(t)
	userCfg(t, home, "not json at all")

	var out, errw bytes.Buffer
	if rc := hostWrappers([]string{"enable"}, &out, &errw, false); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(errw.String(), "could not find a place to set host_wrappers") {
		t.Errorf("stderr = %q, want the setHostWrappers failure named", errw.String())
	}
}

// TestHostWrappersEnableThenApplyAssertIsOneFlow walks the journey the enable message
// points at, through the verbs a user actually types: `wrappers enable` flips the
// opt-in, the NEXT `host apply --assert` generates the wrappers, and `wrappers disable`
// plus the same apply takes them back off the user's PATH.
//
// Pins the split of responsibilities: enable alone must not write wrappers (only the
// config), and apply alone would not have opted in. Fails if hostWrappers stops
// dispatching or setHostWrappers stops writing.
func TestHostWrappersEnableThenApplyAssertIsOneFlow(t *testing.T) {
	home := wrappersTestHome(t)
	userCfg(t, home, `{"packs": ["claude"]}`)
	dir := paths.WrapDirUnder(home)
	wrapper := filepath.Join(dir, "claude")

	var out, errw bytes.Buffer
	if rc := hostWrappers([]string{"enable"}, &out, &errw, false); rc != 0 {
		t.Fatalf("enable rc = %d (stderr: %q)", rc, errw.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("`wrappers enable` created the wrapper dir itself — only apply writes wrappers")
	}

	// The apply the enable message points at is what generates them.
	out.Reset()
	errw.Reset()
	if rc := hostApply([]string{"--assert"}, &out, &errw, false, strings.NewReader("")); rc != 0 {
		t.Fatalf("host apply --assert rc = %d (stderr: %q)", rc, errw.String())
	}
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("no wrapper after enable + apply --assert: %v", err)
	}
	if string(body) != hostwrap.Body("claude") {
		t.Errorf("wrapper body = %q, want hostwrap.Body(claude)", body)
	}
	if !strings.Contains(out.String(), hostwrap.PathLine(dir)) {
		t.Errorf("the apply that created the wrappers did not print the PATH line:\n%s", out.String())
	}

	// The return journey: disable plus the same apply removes them from the user's PATH.
	out.Reset()
	errw.Reset()
	if rc := hostWrappers([]string{"disable"}, &out, &errw, false); rc != 0 {
		t.Fatalf("disable rc = %d (stderr: %q)", rc, errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := hostApply([]string{"--assert"}, &out, &errw, false, strings.NewReader("")); rc != 0 {
		t.Fatalf("host apply --assert rc = %d (stderr: %q)", rc, errw.String())
	}
	if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
		t.Error("a wrapper survived disable + apply --assert — it is still first on the user's PATH")
	}
}
