package agents

import (
	"reflect"
	"testing"
)

func TestInjectYoloFlags(t *testing.T) {
	// gemini: --yolo injected after the binary.
	if got := InjectYoloFlags([]string{"gemini"}); !reflect.DeepEqual(got, []string{"gemini", "--yolo"}) {
		t.Errorf("gemini = %v", got)
	}
	// gemini with -y already present: no --yolo (alias suppression).
	if got := InjectYoloFlags([]string{"gemini", "-y", "chat"}); !reflect.DeepEqual(got, []string{"gemini", "-y", "chat"}) {
		t.Errorf("gemini -y = %v (should not add --yolo)", got)
	}
	// copilot: two flags, order preserved.
	if got := InjectYoloFlags([]string{"copilot", "sub"}); !reflect.DeepEqual(got, []string{"copilot", "--yolo", "--no-auto-update", "sub"}) {
		t.Errorf("copilot = %v", got)
	}
	// copilot with --yolo already present: only --no-auto-update added.
	if got := InjectYoloFlags([]string{"copilot", "--yolo"}); !reflect.DeepEqual(got, []string{"copilot", "--no-auto-update", "--yolo"}) {
		t.Errorf("copilot dup = %v", got)
	}
	// Non-agent head: unchanged.
	if got := InjectYoloFlags([]string{"bash", "-c", "echo"}); !reflect.DeepEqual(got, []string{"bash", "-c", "echo"}) {
		t.Errorf("bash = %v", got)
	}
	// Empty: unchanged.
	if got := InjectYoloFlags(nil); got != nil {
		t.Errorf("nil = %v", got)
	}
	// Input not mutated.
	in := []string{"gemini", "chat"}
	_ = InjectYoloFlags(in)
	if !reflect.DeepEqual(in, []string{"gemini", "chat"}) {
		t.Errorf("input mutated: %v", in)
	}
}
