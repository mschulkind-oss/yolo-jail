package cli

// hostlaunchcontext_test.go is THE CALL-SITE PIN for the host notch's archaeology records
// (hostlaunchcontext.go): two `yolo host -- mytool` launches through hostMain, so deleting either
// record's call in hostExec or hostLaunch fails a cell here.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestAHostLaunchRecordsItsInvocationAndWhatChanged(t *testing.T) {
	const secret = "do-not-log-this-argument"
	home, cwd := timingHome(t, `{"host_management":"own","host_apply_on_launch":false,"profile": {"*": null}}`)
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { return nil }
	t.Cleanup(func() { hostSyscallExec = orig })
	origArgv := hostInvocationArgv
	hostInvocationArgv = func() []string { return []string{"host", "--", "mytool", secret} }
	t.Cleanup(func() { hostInvocationArgv = origArgv })

	launch := func() string {
		var errw bytes.Buffer
		if rc := hostMain([]string{"--", "mytool", secret}, io.Discard, &errw, false, nil); rc != 0 {
			t.Fatalf("rc = %d\n%s", rc, errw.String())
		}
		if strings.Contains(errw.String(), "invocation:") || strings.Contains(errw.String(), "config files:") {
			t.Errorf("a record reached the terminal:\n%s", errw.String())
		}
		log := readFile(t, run.HostLaunchLogPath())
		return log[strings.LastIndex(log, "=== yolo host launch"):]
	}

	first := launch()
	id := hostLaunchID()
	for _, w := range []string{
		id + "   invocation: yolo host -- mytool <1 argument>\n",
		id + "   profile: ",
		id + "   config: no previous launch recorded\n",
		id + "   config files: " + filepath.Join(home, ".config", "yolo-jail", "config.jsonc") + " (regular file, modified ",
		id + "   packs: ",
		id + "   captured edits: none",
	} {
		if !strings.Contains(first, w) {
			t.Errorf("the first host launch's block lacks %q:\n%s", w, first)
		}
	}
	code := paths.JailShortHash(runtime.FromWorkspace(cwd))
	for _, f := range []string{run.LastLaunchConfigName, run.LastLaunchFactsName} {
		p := filepath.Join(run.HostLaunchStateDir(), code+"-"+f)
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("the host launch left no %s: %v", p, err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v, want 0600: it holds the whole config", p, st.Mode().Perm())
		}
	}

	time.Sleep(10 * time.Millisecond)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"host_management":"own", "profile": {"*": null}, "perf_logging": false}`)
	second := launch()
	for _, w := range []string{
		id + "   config: effective config changed since the previous launch (",
		`+  "perf_logging": false`,
		id + "   packs: unchanged since the previous launch",
	} {
		if !strings.Contains(second, w) {
			t.Errorf("the second host launch's block lacks %q:\n%s", w, second)
		}
	}
	assertMachineLogsNever(t, secret, cwd)
}
