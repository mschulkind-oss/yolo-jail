package agents

import (
	"reflect"
	"testing"
)

func TestInjectYoloFlags(t *testing.T) {
	// ALIAS SUPPRESSION: -y is the same switch as --yolo (YoloFlagAliases), so a
	// user who already passed -y must not also get --yolo. copilot is the only
	// remaining --yolo agent (gemini, the original motivating case, was removed),
	// so it carries this coverage now.
	if got := InjectYoloFlags([]string{"copilot", "-y", "chat"}); !reflect.DeepEqual(got, []string{"copilot", "--no-auto-update", "-y", "chat"}) {
		t.Errorf("copilot -y = %v (should not add --yolo)", got)
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
