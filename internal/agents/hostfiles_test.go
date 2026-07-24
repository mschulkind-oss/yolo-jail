package agents

import (
	"reflect"
	"testing"
)

// TestHostFilesDeclarations pins the yolo-declared per-agent host-file set — the
// credential boundary that replaced the retired host_claude_files/host_pi_files
// config keys (plan §10.4 D1/D2). Only claude and pi read a host file, and each
// reads exactly settings.json; every other agent crosses nothing (zero value).
func TestHostFilesDeclarations(t *testing.T) {
	cases := map[string]HostFilesSpec{
		"claude": {Dir: ".claude", Files: []string{"settings.json"}},
		"pi":     {Dir: ".pi/agent", Files: []string{"settings.json"}},
	}
	for name, want := range cases {
		spec, ok := Get(name)
		if !ok {
			t.Fatalf("agent %q not found", name)
		}
		if !reflect.DeepEqual(spec.HostFiles, want) {
			t.Errorf("%s HostFiles = %+v, want %+v", name, spec.HostFiles, want)
		}
	}
	for _, name := range []string{"copilot", "gemini", "opencode", "codex", "agy"} {
		spec, ok := Get(name)
		if !ok {
			t.Fatalf("agent %q not found", name)
		}
		if spec.HostFiles.Dir != "" || spec.HostFiles.Files != nil {
			t.Errorf("%s: want zero-value HostFiles, got %+v", name, spec.HostFiles)
		}
	}
}
