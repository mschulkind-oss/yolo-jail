package mcpauth

import (
	"encoding/json"
	"testing"
)

func TestCanonicalJSONSortsKeysWithoutEscapingHTML(t *testing.T) {
	got, err := canonicalJSON(json.RawMessage(`{"z":"<2>","a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1,"z":"<2>"}` {
		t.Fatalf("canonical JSON = %s", got)
	}
}
