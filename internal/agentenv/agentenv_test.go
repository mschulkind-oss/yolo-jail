package agentenv

import (
	"reflect"
	"testing"
)

// agentenv_test.go pins the package's two survivors: Var and Apply. The COMPOSITION that
// produces vars moved to the env-derive runner (internal/packload AgentEnv, OQ-CS8) —
// the agent's own pack states the provider→environment binding in its derive.lua — and
// its tests live there. What stays here is the overlay half both notches exec through:
// `yolo host` Applies the vars over os.Environ(), and the container notch emits the same
// vars as `-e` pairs, so Apply's assignment/removal semantics are shared currency
// whichever pack composed them.

func TestApplyOverlaysInPlaceAndAppends(t *testing.T) {
	environ := []string{"PATH=/bin", "AWS_REGION=old", "HOME=/home/a"}
	got := Apply(environ, []Var{
		{Key: "AWS_REGION", Value: "us-east-1"},
		{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"},
	})
	want := []string{"PATH=/bin", "AWS_REGION=us-east-1", "HOME=/home/a", "CLAUDE_CODE_USE_BEDROCK=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply = %q\nwant %q", got, want)
	}
	// The input must not be mutated — callers pass os.Environ() and may reuse it.
	if environ[1] != "AWS_REGION=old" {
		t.Errorf("Apply mutated its input: %q", environ)
	}
}

// TestApplyUnsetRemovesRatherThanEmpties is the §2.2 `unset AWS_PROFILE` case, and the
// distinction is the whole reason Var has an Unset field: AWS_PROFILE= is not the same
// as no AWS_PROFILE, and no config surface can express the latter at all. An env derive
// spells one with ctx.tombstone (packload.AgentEnv).
func TestApplyUnsetRemovesRatherThanEmpties(t *testing.T) {
	got := Apply([]string{"PATH=/bin", "AWS_PROFILE=work", "HOME=/home/a"},
		[]Var{{Key: "AWS_PROFILE", Unset: true}})
	want := []string{"PATH=/bin", "HOME=/home/a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply unset = %q\nwant %q", got, want)
	}
	for _, kv := range got {
		if envKey(kv) == "AWS_PROFILE" {
			t.Fatalf("AWS_PROFILE survived as %q", kv)
		}
	}
}

func TestApplyUnsetOfAbsentKeyIsQuiet(t *testing.T) {
	got := Apply([]string{"PATH=/bin"}, []Var{{Key: "AWS_PROFILE", Unset: true}})
	if !reflect.DeepEqual(got, []string{"PATH=/bin"}) {
		t.Errorf("Apply = %q", got)
	}
}

// TestApplyUnsetThenSetEndsSet: order within vars is honored, so a removal followed by an
// assignment of the same key leaves it SET. Without this, "unset then set" would silently
// lose the assignment.
func TestApplyUnsetThenSetEndsSet(t *testing.T) {
	got := Apply([]string{"AWS_PROFILE=work"}, []Var{
		{Key: "AWS_PROFILE", Unset: true},
		{Key: "AWS_PROFILE", Value: "fresh"},
	})
	if !reflect.DeepEqual(got, []string{"AWS_PROFILE=fresh"}) {
		t.Errorf("Apply = %q, want [AWS_PROFILE=fresh]", got)
	}
}

// TestApplyDuplicateInheritedKey: execve semantics are last-wins, so a duplicated key in
// the inherited environ must collapse to one slot before the overlay targets it.
func TestApplyDuplicateInheritedKey(t *testing.T) {
	got := Apply([]string{"A=1", "A=2", "B=3"}, []Var{{Key: "A", Value: "3"}})
	want := []string{"A=3", "B=3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply = %q\nwant %q", got, want)
	}
}

func TestApplyHandlesBareKeyEntry(t *testing.T) {
	got := Apply([]string{"WEIRD"}, []Var{{Key: "WEIRD", Value: "now-set"}})
	if !reflect.DeepEqual(got, []string{"WEIRD=now-set"}) {
		t.Errorf("Apply = %q", got)
	}
}
