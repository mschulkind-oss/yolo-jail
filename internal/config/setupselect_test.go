package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// setupselect_test.go pins SelectSetup, the one selection every verb calls: the design's order
// and the removed YOLO_RUNTIME (refused on the host, a warning in a session). The removed config
// keys are refused by validation (setupsretired_test.go) and choose nothing here.

func selectWith(t *testing.T, verb SetupVerb, flag string, env map[string]string, inSession bool) (SetupChoice, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // no user config: no `setup`
	return SelectSetup(SetupInputs{Verb: verb, Flag: flag, GOOS: "linux", InSession: inSession,
		Getenv: func(k string) string { return env[k] }})
}

func TestSelectSetupOrder(t *testing.T) {
	for _, tc := range []struct {
		name, flag string
		env        map[string]string
		want       string
		src        setup.Source
	}{
		{name: "the OS default", want: "podman-self", src: setup.FromDefault},
		{name: "--setup wins over YOLO_SETUP", flag: "seatbelt-self",
			env: map[string]string{"YOLO_SETUP": "apple-container-self"}, want: "seatbelt-self", src: setup.FromFlag},
		{name: "YOLO_SETUP", env: map[string]string{"YOLO_SETUP": "apple-container-self"},
			want: "apple-container-self", src: setup.FromEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectWith(t, SetupForLaunch, tc.flag, tc.env, false)
			if err != nil {
				t.Fatalf("SelectSetup: %v", err)
			}
			if got.Setup.Name != tc.want || got.Source != tc.src {
				t.Errorf("got %s from %v, want %s from %v", got.Setup.Name, got.Source, tc.want, tc.src)
			}
		})
	}
}

// YOLO_RUNTIME on the host refuses naming the YOLO_SETUP it meant; in a session it warns once and
// the selection goes on (SUP-D6).
func TestSelectSetupWeighsTheRemovedYoloRuntime(t *testing.T) {
	env := map[string]string{"YOLO_RUNTIME": "container"}
	if _, err := selectWith(t, SetupForLaunch, "", env, false); err == nil ||
		!strings.Contains(err.Error(), "YOLO_SETUP=apple-container-self") || !strings.Contains(err.Error(), "unset YOLO_RUNTIME") {
		t.Errorf("a host YOLO_RUNTIME=container was not refused naming YOLO_SETUP=apple-container-self: %v", err)
	}
	got, err := selectWith(t, SetupForLaunch, "", env, true)
	if err != nil {
		t.Fatalf("in a session, YOLO_RUNTIME refused: %v", err)
	}
	if got.Setup.Name != "podman-self" || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "removed and ignored") {
		t.Errorf("in a session: %s, warnings %q; want the default and one warning", got.Setup.Name, got.Warnings)
	}
}

// `yolo host` runs host-self unless --setup names another host setup; a setup with walls is
// refused naming the launch that runs it.
func TestSelectSetupForHost(t *testing.T) {
	got, err := selectWith(t, SetupForHost, "", map[string]string{"YOLO_SETUP": "podman-self"}, false)
	if err != nil || got.Setup.Name != "host-self" {
		t.Errorf("yolo host in a podman-self session selected %q (%v), want host-self", got.Setup.Name, err)
	}
	if _, err := selectWith(t, SetupForHost, "seatbelt-self", nil, false); err == nil ||
		!strings.Contains(err.Error(), "yolo --setup seatbelt-self -- claude") {
		t.Errorf("yolo host --setup seatbelt-self was not refused naming the launch: %v", err)
	}
}

// TestSelectSetupIgnoresAnInheritedSetupThisMachineCannotRun: SUP-D12. Inside a launched session a
// YOLO_SETUP naming a setup this OS cannot run, or one this machine does not define, describes the
// outer session; it is dropped with one line naming it and what was chosen instead, and the OS
// default runs. One this machine can run is still taken, --setup still wins, and outside a
// session the same value is the user's own and is not dropped.
func TestSelectSetupIgnoresAnInheritedSetupThisMachineCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name, flag, env string
		inSession       bool
		want            string
		src             setup.Source
		warned          bool
	}{
		{name: "an Apple Container session's marker, nested on Linux", env: "apple-container-self",
			inSession: true, want: "podman-self", src: setup.FromDefault, warned: true},
		{name: "a Seatbelt session's marker, nested on Linux", env: "seatbelt-yolo-account",
			inSession: true, want: "podman-self", src: setup.FromDefault, warned: true},
		{name: "a user setup the outer machine defined", env: "my-mac-box",
			inSession: true, want: "podman-self", src: setup.FromDefault, warned: true},
		{name: "a marker this machine can run is taken", env: "podman-self",
			inSession: true, want: "podman-self", src: setup.FromEnv},
		{name: "--setup still wins", flag: "podman-self", env: "apple-container-self",
			inSession: true, want: "podman-self", src: setup.FromFlag},
		{name: "outside a session the value is the user's", env: "apple-container-self",
			want: "apple-container-self", src: setup.FromEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectWith(t, SetupForLaunch, tc.flag, map[string]string{"YOLO_SETUP": tc.env}, tc.inSession)
			if err != nil {
				t.Fatalf("SelectSetup: %v", err)
			}
			if got.Setup.Name != tc.want || got.Source != tc.src {
				t.Errorf("got %s from %v, want %s from %v", got.Setup.Name, got.Source, tc.want, tc.src)
			}
			if !tc.warned {
				if len(got.Warnings) != 0 {
					t.Errorf("warned %q, want nothing", got.Warnings)
				}
				return
			}
			if len(got.Warnings) != 1 {
				t.Fatalf("warnings = %q, want one line", got.Warnings)
			}
			w := got.Warnings[0]
			t.Log(w)
			for _, want := range []string{"YOLO_SETUP=" + tc.env, "ignored here", "This launch uses " + tc.want, "--setup <name>"} {
				if !strings.Contains(w, want) {
					t.Errorf("the line does not say %q: %s", want, w)
				}
			}
		})
	}
}
