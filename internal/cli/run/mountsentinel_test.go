package run

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// AWS-AUTH'S MOUNT SENTINEL IS WRITTEN BY THE LAUNCH, through the production assembly call site
// (assembleRunCmd → loopholesRuntimeArgs → prepareMountSentinels). Its manifest declares the
// same inert `.mount-sentinel` openai-auth does, for the same reason — a nonempty state_files
// keeps credentials.json out of the jail — and the only writer was gated on openai-auth's NAME,
// so every launch with aws-auth enabled warned "loophole aws-auth: skipping state file, host
// source missing: …/.mount-sentinel".
//
// The state dir exists already, as it does once the host daemon has minted a credential: that
// is the case that warned (a launch on a machine whose daemon never ran has no state dir and
// mounts nothing). Deleting the call site fails this test on the warning and the mount both.
func TestAWSAuthLaunchWritesItsMountSentinel(t *testing.T) {
	home := retireHome(t)
	p := officialPack(t, "aws-auth")
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{p}))
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	stateDir := loopholes.StateDirFor("aws-auth")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "credentials.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })

	o := goldenOptions("/ws", home)
	o.Stderr = w
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.cfg.Set("loopholes", newConfig("aws-auth", newConfig("enabled", true)))
	in.packs = []*packload.Pack{p}
	argv := o.assembleRunCmd(in)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = oldStderr
	printed, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()

	sentinel := filepath.Join(stateDir, loopholes.MountSentinelName)
	joined := strings.Join(argv, "\n")
	if want := sentinel + ":/var/lib/yolo-jail/loopholes/aws-auth/.mount-sentinel:ro"; !strings.Contains(joined, want) {
		t.Errorf("aws-auth's mount sentinel is absent from the production argv; want %q", want)
	}
	if strings.Contains(string(printed), "skipping state file") {
		t.Errorf("a launch with aws-auth enabled still warns about its state-file marker:\n%s", printed)
	}
	if strings.Contains(joined, "credentials.json") {
		t.Errorf("aws-auth's minted credentials crossed into the jail:\n%s", joined)
	}
	info, err := os.Stat(sentinel)
	if err != nil {
		t.Fatalf("the launch wrote no sentinel: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("sentinel mode = %o, want 600", info.Mode().Perm())
	}
}

// A SENTINEL THE LAUNCH CANNOT WRITE IS SAID, naming the loophole, why, and the next step. Here
// a plain file sits where aws-auth's state directory goes, so the directory cannot be made.
func TestAnUnwritableMountSentinelIsSaidWithItsFix(t *testing.T) {
	home := retireHome(t)
	p := officialPack(t, "aws-auth")
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{p}))
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	stateDir := loopholes.StateDirFor("aws-auth")
	if err := os.MkdirAll(filepath.Dir(stateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateDir, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr strings.Builder
	o := goldenOptions("/ws", home)
	o.Stderr = &stderr
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	in.cfg.Set("loopholes", newConfig("aws-auth", newConfig("enabled", true)))
	in.packs = []*packload.Pack{p}
	_ = o.assembleRunCmd(in)

	got := stderr.String()
	for _, want := range []string{
		"Warning: loophole aws-auth: could not write its mount sentinel in " + stateDir +
			": create the state directory: ",
		"Make that directory one you own and can write; the next launch writes the marker again.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch did not say %q:\n%s", want, got)
		}
	}
}
