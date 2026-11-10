package run

import (
	"strings"
	"testing"
)

// "Nothing to apply" compares the workspace config with what the jail was handed, and says
// nothing about who reads it. A jail's host brokers run the yolo that launched it and reread the
// user config at every call as THAT yolo reads it; on 2026-11-09 a jail launched by an older yolo
// kept refusing the `branch:main` push grant its scope file held, while `yolo config apply` on
// the newer host yolo reported nothing to apply. When the jail was launched by another yolo, the
// verdict names that and the step that brings its brokers up to this one.
func TestConfigApplyNothingToApplyNamesAJailLaunchedByAnotherYolo(t *testing.T) {
	const host, jail = "0.17.1", "0.16.0+87.g14cc75609b4c"
	o, buf := applyFixture(t, `{}`, `{}`, applyJail{running: true, env: "YOLO_VERSION=" + jail + "\n"})
	t.Setenv("YOLO_VERSION", host)
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"Nothing to apply", "yolo " + jail, "yolo " + host, "yolo stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, out)
		}
	}

	o, buf = applyFixture(t, `{}`, `{}`, applyJail{running: true, env: "YOLO_VERSION=" + host + "\n"})
	t.Setenv("YOLO_VERSION", host)
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if out := buf.String(); !strings.Contains(out, "Nothing to apply") || strings.Contains(out, "yolo stop") {
		t.Errorf("a jail launched by this yolo got a restart note, or no verdict:\n%s", out)
	}
}
