package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
)

var updatesNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func updatesOptions(t *testing.T, ch selfupdate.Channel, env map[string]string, st *selfupdate.State) *Options {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if st != nil {
		if err := selfupdate.SaveState(path, *st); err != nil {
			t.Fatal(err)
		}
	}
	return &Options{
		Getenv:             func(k string) string { return env[k] },
		Now:                func() time.Time { return updatesNow },
		UpdateChannel:      func() selfupdate.Channel { return ch },
		UpdateStatePath:    path,
		UpdateCheckEnabled: func() bool { return true },
	}
}

func runUpdatesSection(o *Options) string {
	var buf bytes.Buffer
	o.sectionUpdates(newReporter(&buf, false))
	return buf.String()
}

func TestSectionUpdates(t *testing.T) {
	ch := selfupdate.Channel{Kind: selfupdate.KindHomebrew, Exe: "/opt/homebrew/bin/yolo", Version: "0.10.0"}
	cached := func(mutate func(*selfupdate.State)) *selfupdate.State {
		st := selfupdate.State{CheckedAt: updatesNow.Add(-3 * time.Hour), Identity: ch.Identity(), Kind: ch.Kind, Current: "0.10.0", Latest: "0.10.0"}
		if mutate != nil {
			mutate(&st)
		}
		return &st
	}
	cases := []struct {
		name string
		opts *Options
		want []string
	}{
		{"up to date", updatesOptions(t, ch, nil, cached(nil)),
			[]string{"[PASS] up to date as of 3 hours ago (0.10.0 via homebrew)"}},
		{"update available", updatesOptions(t, ch, nil, cached(func(s *selfupdate.State) { s.Latest, s.Available = "0.11.0", true })),
			[]string{"[WARN] ⬆ yolo-jail 0.11.0 is available (this is 0.10.0) (checked 3 hours ago)", "yolo update"}},
		{"binary-only update", updatesOptions(t, selfupdate.Channel{Kind: selfupdate.KindGoInstall, Version: "0.10.0"}, nil,
			&selfupdate.State{CheckedAt: updatesNow.Add(-time.Hour), Identity: (selfupdate.Channel{Kind: selfupdate.KindGoInstall, Version: "0.10.0"}).Identity(),
				Kind: selfupdate.KindGoInstall, Current: "0.10.0", Latest: "0.11.0", Available: true}),
			[]string{"separate jail source together", "Update the host binary and its separate jail source together."}},
		{"last check failed", updatesOptions(t, ch, nil, cached(func(s *selfupdate.State) { s.Error = "HTTP 403" })),
			[]string{"[WARN] the last update check failed (3 hours ago): HTTP 403"}},
		{"never checked", updatesOptions(t, ch, nil, nil),
			[]string{"[SKIP] no update check has run for this build yet"}},
		{"a check for the previous binary does not count", updatesOptions(t, ch, nil, cached(func(s *selfupdate.State) { s.Identity = "homebrew:0.9.0" })),
			[]string{"[SKIP] no update check has run for this build yet"}},
		{"turned off", updatesOptions(t, ch, map[string]string{selfupdate.DisableEnv: "1"}, cached(nil)),
			[]string{"[SKIP] the update check is off"}},
		{"moved checkout", updatesOptions(t, selfupdate.Channel{Kind: selfupdate.KindUnknown, MissingSourceDir: "/src/gone"}, nil, nil),
			[]string{"[WARN] this yolo was built from /src/gone, which no longer contains the yolo-jail checkout", "--from"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := runUpdatesSection(c.opts)
			if !strings.Contains(out, "Updates") {
				t.Errorf("no Updates heading:\n%s", out)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
		})
	}
}

func TestSectionUpdatesExplainsSourceChecksAreExplicit(t *testing.T) {
	ch := selfupdate.Channel{
		Kind: selfupdate.KindSource, Exe: "/home/u/.local/bin/yolo",
		SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf",
	}
	out := runUpdatesSection(updatesOptions(t, ch, nil, nil))
	if !strings.Contains(out, "source update checks run only on request") ||
		!strings.Contains(out, "remote source check") {
		t.Errorf("missing explicit-source-check guidance:\n%s", out)
	}
}

func TestSectionUpdatesIsSilentWhereItHasNothingToSay(t *testing.T) {
	ch := selfupdate.Channel{Kind: selfupdate.KindHomebrew, Version: "0.10.0"}
	if out := runUpdatesSection(updatesOptions(t, ch, map[string]string{"YOLO_VERSION": "0.10.0"}, nil)); out != "" {
		t.Errorf("printed in a jail:\n%s", out)
	}
	if out := runUpdatesSection(updatesOptions(t, selfupdate.Channel{Kind: selfupdate.KindUnknown}, nil, nil)); out != "" {
		t.Errorf("printed for an unidentified install:\n%s", out)
	}
}

func TestHumanAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "moments",
		time.Minute:      "1 minute",
		5 * time.Minute:  "5 minutes",
		3 * time.Hour:    "3 hours",
		47 * time.Hour:   "47 hours",
		72 * time.Hour:   "3 days",
	} {
		if got := humanAge(d); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", d, got, want)
		}
	}
}

// "update_check": false in the user config reaches the gate fillDefaults
// installs for `yolo check`, not only a test's injected one.
func TestFillDefaultsReadsTheUserConfigUpdateCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := &Options{}
	fillDefaults(o)
	if !o.UpdateCheckEnabled() {
		t.Error("with no user config the check must be on")
	}
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"update_check": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	o = &Options{}
	fillDefaults(o)
	if o.UpdateCheckEnabled() {
		t.Error(`"update_check": false in the user config did not reach yolo check`)
	}
}
