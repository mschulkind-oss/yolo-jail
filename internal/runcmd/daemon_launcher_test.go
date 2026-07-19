package runcmd

import "testing"

// TestDaemonLauncherPATH covers the simplified daemonLauncher: a plain PATH
// lookup. On PATH → [name]; off PATH → nil (the nil-vs-bare-name contract the
// external-service spawn path relies on). The former YOLO_GO_DAEMONS/
// YOLO_GO_BIN_DIR migration seam was dead code and is gone.
func TestDaemonLauncherPATH(t *testing.T) {
	// On PATH → the console-script name.
	onPath := &Options{
		LookPath: func(name string) (string, bool) { return "/usr/bin/" + name, true },
	}
	if got := onPath.daemonLauncher("yolo-host-processes"); len(got) != 1 || got[0] != "yolo-host-processes" {
		t.Errorf("on PATH: got %v, want [yolo-host-processes]", got)
	}

	// Off PATH → nil (nothing to launch).
	offPath := &Options{
		LookPath: func(string) (string, bool) { return "", false },
	}
	if got := offPath.daemonLauncher("yolo-host-processes"); got != nil {
		t.Errorf("off PATH: got %v, want nil", got)
	}
}
