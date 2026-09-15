package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
)

type fakeManagedHostLaunch struct{ ran bool }

func (f *fakeManagedHostLaunch) Environ(base []string) []string {
	return append(base, "YOLO_TEST_MANAGED_AUTH=1")
}

func TestGeneratedCodexWrapperReachesManagedHostLaunch(t *testing.T) {
	if body := hostwrap.Body("codex"); !strings.Contains(body, `exec yolo host -- codex "$@"`) {
		t.Fatalf("Codex wrapper bypasses managed host launch:\n%s", body)
	}
}

func (f *fakeManagedHostLaunch) Run(_ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) (int, bool) {
	f.ran = true
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
	original := prepareOpenAIAuthHost
	fake := &fakeManagedHostLaunch{}
	prepareOpenAIAuthHost = func(string, io.Writer) (managedOpenAIHostLaunch, error) { return fake, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = original })
	t.Setenv("YOLO_ACCEPT_CONFIG_CHANGES", "1")
	// `true` resolves before preparation, while the fake Run prevents syscall.Exec.
	rc := hostExec(nil, []string{"true"}, io.Discard, io.Discard, nil)
	if rc != 23 || !fake.ran {
		t.Fatalf("hostExec = %d, ran=%v; managed launch call site was bypassed", rc, fake.ran)
	}
}
