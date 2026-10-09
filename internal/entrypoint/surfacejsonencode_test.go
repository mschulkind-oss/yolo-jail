package entrypoint

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestJSONSurfaceRefusesAnUnencodableValue pins that a `json` surface whose composed object
// holds a value jsonx cannot encode is REFUSED, like the toml and yaml arms, instead of
// rendering as "\n" (the encoder's error was dropped) and truncating the agent's config file.
func TestJSONSurfaceRefusesAnUnencodableValue(t *testing.T) {
	type notJSON struct{ X int }
	surface := manifest.Surface{Agent: "claude", Name: "settings", Codec: "json"}
	obj := jsonx.NewOrderedMap()
	obj.Set("env", map[string]any{"BAD": notJSON{1}})
	text, err := encodeSurfaceObject(surface, obj, nil, nil)
	if err == nil {
		t.Fatalf("encodeSurfaceObject = %q, nil error; want a refusal", text)
	}
	if text != "" {
		t.Errorf("refusal carried text %q", text)
	}
	if !strings.Contains(err.Error(), ".env.BAD") || !strings.Contains(err.Error(), "left untouched") {
		t.Errorf("error = %v; want the value's path and that the file is left untouched", err)
	}
}

// TestJSONSurfaceEncodesIndent2 pins the success bytes the refusal must not move.
func TestJSONSurfaceEncodesIndent2(t *testing.T) {
	surface := manifest.Surface{Agent: "claude", Name: "settings", Codec: "json"}
	obj := jsonx.NewOrderedMap()
	obj.Set("z", "1")
	obj.Set("a", []any{int64(2)})
	text, err := encodeSurfaceObject(surface, obj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"z\": \"1\",\n  \"a\": [\n    2\n  ]\n}\n"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}
