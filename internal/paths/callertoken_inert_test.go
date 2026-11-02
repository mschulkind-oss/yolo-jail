package paths

import "testing"

func TestLoopholeInertEnvFoldsTheLoopholeName(t *testing.T) {
	for in, want := range map[string]string{
		"serial":         "YOLO_LOOPHOLE_INERT_SERIAL",
		"host-processes": "YOLO_LOOPHOLE_INERT_HOST_PROCESSES",
		"macos-log":      "YOLO_LOOPHOLE_INERT_MACOS_LOG",
		"--":             "",
	} {
		if got := LoopholeInertEnv(in); got != want {
			t.Errorf("LoopholeInertEnv(%q) = %q, want %q", in, got, want)
		}
	}
}
