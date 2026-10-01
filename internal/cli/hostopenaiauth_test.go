package cli

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
)

type fakeManagedHostLaunch struct {
	ran  bool
	argv []string
}

func (f *fakeManagedHostLaunch) Environ(base []string) []string {
	return append(base, "YOLO_TEST_MANAGED_AUTH=1")
}

// fakeManagedArgvFlag is what the fake's Argv adds, standing in for openaiauthhost's --no-daemon.
const fakeManagedArgvFlag = "--yolo-test-managed-rewrite"

func (f *fakeManagedHostLaunch) Argv(argv []string) ([]string, []string) {
	out := append([]string{argv[0], fakeManagedArgvFlag}, argv[1:]...)
	return out, []string{"yolo CHANGED the command you asked for: (the managed launch's fake rewrite)"}
}

func (*fakeManagedHostLaunch) NotServed(string) string { return "" }

func TestGeneratedCodexWrapperReachesManagedHostLaunch(t *testing.T) {
	if body := hostwrap.Body("codex"); !strings.Contains(body, `exec yolo host -- codex "$@"`) {
		t.Fatalf("Codex wrapper bypasses managed host launch:\n%s", body)
	}
}

func (f *fakeManagedHostLaunch) Run(_ string, argv []string, env []string, _ io.Reader, _, _ io.Writer) (int, bool) {
	f.ran = true
	f.argv = argv
	found := false
	for _, item := range env {
		found = found || item == "YOLO_TEST_MANAGED_AUTH=1"
	}
	if !found {
		return 98, true
	}
	return 23, true
}

func TestHostExecUsesManagedOpenAIAuthLaunch(t *testing.T) {
	// A home of its own: hostExec composes from the user config, so without this the test
	// asserted whatever this machine's config says. It failed inside a jail whose generated
	// config snapshot an older launcher wrote with a key this build refuses.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	original := prepareOpenAIAuthHost
	fake := &fakeManagedHostLaunch{}
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = original })
	// `true` resolves before preparation, while the fake Run prevents syscall.Exec.
	rc := hostExec(nil, []string{"true"}, io.Discard, io.Discard, nil)
	if rc != 23 || !fake.ran {
		t.Fatalf("hostExec = %d, ran=%v; managed launch call site was bypassed", rc, fake.ran)
	}
}

// THE MANAGED LAUNCH'S ARGV REWRITE REACHES THE EXEC, AND IS SAID (OQ-CDX1): `yolo host --
// codex` runs with the argv the managed launch returns — openaiauthhost adds --no-daemon, so the
// launch never attaches to a background server an earlier launch left behind — and prints its
// disclosure. The fake stands in for that rewrite; openaiauthhost's own tests pin the rule.
// Through the real hostExec, so deleting the Argv call site fails this.
func TestHostExecRunsTheManagedLaunchsArgvRewriteAndSaysSo(t *testing.T) {
	// A home of its own, as TestHostExecUsesManagedOpenAIAuthLaunch has: hostExec composes from
	// the user config, so without it this test read the machine's real config and failed in a jail
	// whose config still carries the retired use_profiles key.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	original := prepareOpenAIAuthHost
	fake := &fakeManagedHostLaunch{}
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = original })
	var errw strings.Builder
	if rc := hostExec(nil, []string{"true", "arg"}, io.Discard, &errw, nil); rc != 23 {
		t.Fatalf("hostExec = %d; the managed launch was not reached\n%s", rc, errw.String())
	}
	if want := []string{"true", fakeManagedArgvFlag, "arg"}; strings.Join(fake.argv, " ") != strings.Join(want, " ") {
		t.Errorf("the managed launch ran %q, want its own rewrite %q", fake.argv, want)
	}
	if !strings.Contains(errw.String(), "yolo host: yolo CHANGED the command you asked for: (the managed launch's fake rewrite)") {
		t.Errorf("the managed launch's rewrite was not disclosed:\n%s", errw.String())
	}
}
