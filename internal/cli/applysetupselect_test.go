package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// applysetupselect_test.go pins `yolo apply`'s selection: with no --setup it acts for the setup a
// launch from here would select (config.SelectSetup), so YOLO_SETUP and the user's `setup` move
// it as they move the launch; and a host setup run as the yolo account, which is not built, is
// refused by apply and the config verbs with `yolo host`'s words rather than answered for the
// real home.

// TestApplyWithoutSetupActsForTheSelectedSetup: YOLO_SETUP=host-self (and the user's `setup`)
// route a bare `yolo apply` to the host notch, which in a jail refuses with its own line; and
// YOLO_SETUP=podman-self outranks the user's host-self and keeps it at the jail notch.
func TestApplyWithoutSetupActsForTheSelectedSetup(t *testing.T) {
	const hostRefusal = "yolo apply: refusing — this process is inside a jail"
	for _, tc := range []struct {
		name, env, user string
		wantHost        bool
	}{
		{name: "YOLO_SETUP=host-self", env: "host-self", wantHost: true},
		{name: "the user's setup", user: "host-self", wantHost: true},
		{name: "YOLO_SETUP outranks the user's setup", env: "podman-self", user: "host-self"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := withHomeAndCwd(t)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("YOLO_VERSION", "0.0.0-test") // in a jail: the host apply refuses, writing nothing
			t.Setenv("YOLO_RUNTIME", "")
			t.Setenv("YOLO_SETUP", tc.env)
			userCfg := "\n"
			if tc.user != "" {
				userCfg = "setup = \"" + tc.user + "\"\n"
			}
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), userCfg)
			writeFile(t, filepath.Join(repo, "yolo-jail.toml"), "\n")
			var out, errw bytes.Buffer
			applyMain([]string{"--dry-run"}, &out, &errw, false, nil)
			got := out.String() + errw.String()
			if tc.wantHost != strings.Contains(got, hostRefusal) {
				t.Errorf("host notch reached = %v, want %v:\n%s", !tc.wantHost, tc.wantHost, got)
			}
			if !tc.wantHost && !strings.Contains(got, "apply at confinement jail") {
				t.Errorf("apply did not act at the jail notch:\n%s", got)
			}
		})
	}
}

// TestHostYoloAccountIsRefusedAlikeByEveryVerb: `--setup host-yolo-account` is not built, and
// apply and the config verbs refuse it with the words `yolo host` uses, instead of answering
// for the real home as host-self would. On a Mac, where only the run_as stops it.
func TestHostYoloAccountIsRefusedAlikeByEveryVerb(t *testing.T) {
	home, _ := withHomeAndCwd(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_RUNTIME", "")
	t.Setenv("YOLO_SETUP", "")
	onPlatform(t, true) // on Linux every verb refuses it sooner: Linux has no yolo account
	want := hostYoloAccountRefusal("host-yolo-account")

	var hostErr bytes.Buffer
	if _, rc := resolveHostSetup("host-yolo-account", func(string) string { return "" }, "darwin", &hostErr); rc == 0 ||
		!strings.Contains(hostErr.String(), want) {
		t.Fatalf("yolo host did not refuse host-yolo-account with the shared words (rc %d):\n%s", rc, hostErr.String())
	}

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--setup", "host-yolo-account"}, &out, &errw, false, nil); rc != 2 ||
		!strings.Contains(errw.String(), want) {
		t.Errorf("apply --setup host-yolo-account rc=%d, want 2 and the shared refusal:\n%s%s", rc, out.String(), errw.String())
	}

	if _, refusal := resolveConfigTarget("host-yolo-account"); refusal != want {
		t.Errorf("config --setup host-yolo-account refusal = %q, want %q", refusal, want)
	}
	if tgt, refusal := resolveConfigTarget("host-self"); refusal != "" || tgt.notch != render.KindHost {
		t.Errorf("host-self is still the real home's target: %+v %q", tgt, refusal)
	}
}
