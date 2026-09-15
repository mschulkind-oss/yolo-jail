package codec

import (
	"reflect"
	"strings"
	"testing"
)

func TestYAMLRoundTripStructuredConfig(t *testing.T) {
	in := map[string]any{
		"providers": map[string]any{
			"proxy": map[string]any{
				"api":    "openai-responses",
				"models": []any{map[string]any{"id": "model-1", "input": []any{"text", "image"}}},
			},
		},
	}
	encoded, err := (YAML{}).Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "providers:") {
		t.Fatalf("YAML output omitted top-level mapping:\n%s", encoded)
	}
	got, err := (YAML{}).Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %#v, want %#v", got, in)
	}
}

func TestYAMLRefusesNonStringMapKey(t *testing.T) {
	_, err := (YAML{}).Decode([]byte("1: value\n"))
	if err == nil || !strings.Contains(err.Error(), "not a string") {
		t.Fatalf("Decode non-string map key error = %v, want a clear refusal", err)
	}
}
