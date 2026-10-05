package cli

// features_test.go pins `yolo features` (docs/design/patched-forks.md PF-D63): the front door is
// registered, the text form is one name per line a script can match whole, and the JSON form is
// one document carrying the same list with what each name is for.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestFeaturesIsARegisteredCommand(t *testing.T) {
	fn, ok := registry["features"]
	if !ok || reflect.ValueOf(fn).Pointer() != reflect.ValueOf(runFeatures).Pointer() {
		t.Fatal("`yolo features` is not dispatched to runFeatures")
	}
	if _, ok := subcommandUsage["features"]; !ok {
		t.Error("`yolo features --help` has no usage text")
	}
}

func TestYoloFeaturesPrintsOneNamePerLine(t *testing.T) {
	var out bytes.Buffer
	if rc := featuresRun(&out, "text"); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	want := packdecl.Features()
	if len(lines) != len(want) {
		t.Fatalf("%d lines for %d features:\n%s", len(lines), len(want), out.String())
	}
	for i, f := range want {
		if lines[i] != f.Name {
			t.Errorf("line %d = %q, want %q", i, lines[i], f.Name)
		}
	}
	for _, name := range []string{"patch-series", "patched-extensions", "kind:files", "via:source"} {
		if !strings.Contains("\n"+out.String(), "\n"+name+"\n") {
			t.Errorf("%q is not a whole line of the output:\n%s", name, out.String())
		}
	}
}

func TestYoloFeaturesJSONIsOneDocument(t *testing.T) {
	var out bytes.Buffer
	if rc := featuresRun(&out, "json"); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	var doc struct {
		Features []packdecl.Feature `json:"features"`
	}
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.More() {
		t.Error("stdout carries more than one document")
	}
	if !reflect.DeepEqual(doc.Features, packdecl.Features()) {
		t.Errorf("the JSON list is not the feature list")
	}
	if strings.Contains(out.String(), `<`) {
		t.Error("the JSON escapes < and > in a summary")
	}
}

// The command takes no argument: a script that passed one asked a question it did not get answered.
func TestYoloFeaturesRefusesAnArgument(t *testing.T) {
	for args, want := range map[string]string{
		"features":                 "",
		"features --format json":   "",
		"features patch-series":    "patch-series",
		"features --json extra":    "extra",
		"features --format=json x": "x",
	} {
		if got := featuresPositional(strings.Fields(args)); got != want {
			t.Errorf("featuresPositional(%q) = %q, want %q", args, got, want)
		}
	}
}
