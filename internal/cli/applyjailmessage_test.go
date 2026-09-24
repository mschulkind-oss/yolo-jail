package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyAtJailDoesNotClaimTheLaunchInstallsPrograms pins the jail notch's pointer message
// to what a launch actually does (docs/design/jail-notch-readiness.md, alternative B — "the
// cheap half … should ship either way").
//
// The message used to say "`yolo -- true` to provision and exit". A launch builds the image,
// stages the selected packs and renders their config, but every `program` a selected pack
// declares is installed by its lazy launcher in ~/.yolo/bin/launch on FIRST INVOCATION
// (internal/entrypoint/shims.go, the launcher's cold branch). So the command yolo pointed at
// for provisioning exited 0 having installed no agent CLI.
func TestApplyAtJailDoesNotClaimTheLaunchInstallsPrograms(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "jail"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --at jail rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	// The message wraps; compare on single-spaced text so a reflow cannot hide a claim.
	got := strings.Join(strings.Fields(out.String()), " ")

	if strings.Contains(got, "provision and exit") {
		t.Errorf("apply --at jail still says `yolo -- true` provisions:\n%s", got)
	}
	// The truth has to be stated, not merely the falsehood removed: a reader told nothing
	// about when programs arrive will assume the launch installed them.
	// "builds the image" must stay qualified: macos-user is a jail-notch backend with no image.
	for _, want := range []string{"~/.yolo/bin/launch", "first time", "`yolo -- true`",
		"builds the image (on a container runtime; macos-user has none)"} {
		if !strings.Contains(got, want) {
			t.Errorf("apply --at jail does not say when declared programs install; missing %q:\n%s",
				want, got)
		}
	}
}

// TestApplyUsageDoesNotClaimJailProvisioning pins `yolo apply --help` to the same truth: at
// the jail notch the verb is a pointer to the launch and provisions nothing itself, so an
// example captioned "provision the jail, launch nothing" promised a result it cannot produce.
func TestApplyUsageDoesNotClaimJailProvisioning(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--help"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --help rc = %d\nstdout:\n%s\nstderr:\n%s", rc, out.String(), errw.String())
	}
	help := out.String() + errw.String()
	if !strings.Contains(help, "yolo apply — ") {
		t.Fatalf("apply --help did not print the usage:\n%s", help)
	}
	if strings.Contains(help, "provision the jail, launch nothing") {
		t.Errorf("apply --help still claims `yolo apply` provisions the jail:\n%s", help)
	}
}

// TestTopLevelHelpDoesNotClaimApplyProvisionsTheJail pins the `yolo --help` blurb for apply to
// the same truth as its own usage: the old "Provision the environment without launching" made
// the jail-notch claim TestApplyUsageDoesNotClaimJailProvisioning removed from `apply --help`.
func TestTopLevelHelpDoesNotClaimApplyProvisionsTheJail(t *testing.T) {
	var blurb string
	for _, c := range commandHelp {
		if c.name == "apply" {
			blurb = c.blurb
		}
	}
	if blurb == "" {
		t.Fatal("commandHelp has no apply entry")
	}
	if strings.Contains(strings.ToLower(blurb), "provision the environment without launching") {
		t.Errorf("yolo --help still says apply provisions without launching: %q", blurb)
	}
	if !strings.Contains(blurb, "points at the launch") {
		t.Errorf("yolo --help's apply blurb does not say what it does at jail: %q", blurb)
	}
}
