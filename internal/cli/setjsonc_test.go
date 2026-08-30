package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// The setJSONCBool comment-blindness cases. The writer searches text; the reader
// (loadUserScopeConfig) strips comments first — any find-the-key decision that does not
// go through jsoncCommentSpans lets the two select DIFFERENT occurrences, and
// `wrappers enable` exits 0 over a byte-identical file while the live key keeps its old
// value. Round-trips go through config.HostWrappersEnabled(), the reader, never string
// equality.

// TestSetJSONCBoolEditsTheLiveKeyNotTheComment: a commented-out key above a live one is
// the canonical no-op — the old writer edited the comment and reported success.
func TestSetJSONCBoolEditsTheLiveKeyNotTheComment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, "{\n  // \"host_wrappers\": false,\n  \"host_wrappers\": true\n}\n")

	if err := setHostWrappers(false); err != nil {
		t.Fatalf("setHostWrappers: %v", err)
	}
	if config.HostWrappersEnabled() {
		t.Error("the write did not round-trip — the reader still sees true")
	}
}

// TestSetJSONCBoolDoesNotDuplicateWhenTheCommentHasNoBool: a comment carrying the key
// and a colon but no boolean used to fall through to the INSERT branch and write a
// duplicate key the live one silently beat.
func TestSetJSONCBoolDoesNotDuplicateWhenTheCommentHasNoBool(t *testing.T) {
	text := "// \"host_wrappers\": see yolo config-ref\n{\n  \"host_wrappers\": true\n}\n"
	got, ok := setJSONCBool(text, "host_wrappers", false)
	if !ok {
		t.Fatal("the live key must be editable")
	}
	// The DUPLICATE to rule out is a second LIVE key; the comment's occurrence is the
	// comment's business. Count occurrences outside comment spans.
	spans := jsoncCommentSpans(got)
	live := 0
	for base := 0; ; {
		rel := strings.Index(got[base:], `"host_wrappers"`)
		if rel < 0 {
			break
		}
		if !coveredBy(spans, base+rel) {
			live++
		}
		base += rel + 1
	}
	if live != 1 {
		t.Errorf("%d live occurrences of the key after the edit, want exactly 1:\n%s", live, got)
	}
	if !strings.Contains(got, `"host_wrappers": false`) {
		t.Errorf("the live key was not flipped:\n%s", got)
	}
	if !strings.Contains(got, "// \"host_wrappers\": see yolo config-ref") {
		t.Errorf("the comment must survive verbatim:\n%s", got)
	}
}

// TestSetJSONCBoolRefusesAnUneditableLiveKey: a block comment between the colon and the
// value leaves no safe edit — the answer is a REFUSAL (the caller tells the user to edit
// by hand), never an insert that would create a duplicate the live key silently wins.
func TestSetJSONCBoolRefusesAnUneditableLiveKey(t *testing.T) {
	text := "{\n  \"host_wrappers\": /* deliberately weird */ true\n}\n"
	got, ok := setJSONCBool(text, "host_wrappers", false)
	if ok {
		t.Fatalf("an uneditable live key must refuse, got an edit:\n%s", got)
	}
	if got != text {
		t.Errorf("a refusal must leave the text untouched")
	}
}

// TestSetJSONCBoolIsNotFooledBySlashesInStrings: a `//` inside a quoted value is not a
// comment, and must not blind the writer to a later live key.
func TestSetJSONCBoolIsNotFooledBySlashesInStrings(t *testing.T) {
	text := "{\n  \"url\": \"https://example//x\",\n  \"host_wrappers\": true\n}\n"
	got, ok := setJSONCBool(text, "host_wrappers", false)
	if !ok || !strings.Contains(got, `"host_wrappers": false`) {
		t.Fatalf("ok = %v, got:\n%s", ok, got)
	}
	if !strings.Contains(got, `"https://example//x"`) {
		t.Errorf("the string value must survive untouched:\n%s", got)
	}
}

// TestSetJSONCBoolEditsATrailingCommentedLine: the value's own line may carry a
// trailing comment; the edit must flip the value and keep the comment.
func TestSetJSONCBoolEditsATrailingCommentedLine(t *testing.T) {
	text := "{\n  \"host_wrappers\": true, // see yolo config-ref\n}\n"
	got, ok := setJSONCBool(text, "host_wrappers", false)
	if !ok || !strings.Contains(got, `"host_wrappers": false, // see yolo config-ref`) {
		t.Fatalf("ok = %v, got:\n%s", ok, got)
	}
}
