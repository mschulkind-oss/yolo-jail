package config

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// A problem the config had before an edit is tolerated after it even when the edit moved its line;
// a problem only the planned config has still refuses.
func TestSplitPreexistingProblemsIgnoresAMovedLineButNotANewProblem(t *testing.T) {
	before := []string{`~/.config/yolo-jail/config.toml:8:1: provider bedrock model "a": model has no authored price facts`}
	planned := []string{
		`~/.config/yolo-jail/config.toml:11:1: provider bedrock model "a": model has no authored price facts`,
		`~/.config/yolo-jail/config.toml:14:1: provider bedrock model "b": model has no authored price facts`,
	}
	refusing, tolerated := splitPreexistingProblems(planned, before)
	if !reflect.DeepEqual(refusing, planned[1:]) || !reflect.DeepEqual(tolerated, planned[:1]) {
		t.Fatalf("refusing=%q tolerated=%q", refusing, tolerated)
	}
	if refusing, tolerated := splitPreexistingProblems(planned, nil); !reflect.DeepEqual(refusing, planned) || tolerated != nil {
		t.Fatalf("with nothing pre-existing every problem refuses: refusing=%q tolerated=%q", refusing, tolerated)
	}
}

func TestEditProblemsReadsOnlyAValidationFinding(t *testing.T) {
	wrapped := fmt.Errorf("validate planned config: %w", &EditProblemsError{Problems: []string{"p"}})
	if got := EditProblems(wrapped); !reflect.DeepEqual(got, []string{"p"}) {
		t.Fatalf("EditProblems = %q", got)
	}
	if got := EditProblems(errors.New("parse error")); got != nil {
		t.Fatalf("a non-finding error named problems: %q", got)
	}
	if got := wrapped.Error(); got != "validate planned config: validate planned effective config: p" {
		t.Fatalf("the refusal text changed: %q", got)
	}
}

func TestResolvedProblemsIsWhatTheEditNoLongerHas(t *testing.T) {
	before := []string{"f.toml:3:1: a", "f.toml:9:1: b"}
	if got := ResolvedProblems(before, []string{"f.toml:5:1: b"}); !reflect.DeepEqual(got, before[:1]) {
		t.Fatalf("ResolvedProblems = %q", got)
	}
	if got := ResolvedProblems(before, before); got != nil {
		t.Fatalf("nothing resolved, got %q", got)
	}
}
