package configmigrate

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A named repository's broker cells are typed by the broker's declaration, not the schema, so
// the schema check must not call one unknown (it would drop a grant the user wrote).
func TestANamedRepositoryBrokerCellIsNotUnknownToTheSchema(t *testing.T) {
	decoded, err := jsonx.Decode([]byte(`{"repos":{"lib":{"requests":{"github":{"runs":["ci.yml"]}}}},"bogus":1}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decoded %T", decoded)
	}
	got := unknownToReadableSchema(m, ScopeProject)
	if len(got) != 1 || got[0] != "bogus" {
		t.Fatalf("unknown = %v, want only [bogus]", got)
	}
}
