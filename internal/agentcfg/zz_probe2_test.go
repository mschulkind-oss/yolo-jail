package agentcfg_test

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

func TestProbeTOMLIntDecode(t *testing.T) {
	a, err := codec.TOML{}.Decode([]byte("k = 8192\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := codec.TOML{}.Decode([]byte("k = 8192.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	am := a.(map[string]any)
	bm := b.(map[string]any)
	t.Logf("int form  -> %T %v", am["k"], am["k"])
	t.Logf("float form-> %T %v", bm["k"], bm["k"])
	t.Logf("DeepEqual across forms: %v", reflect.DeepEqual(am, bm))
	// what does the emitter do with an int64 vs float64
	e1, _ := codec.TOML{}.Encode(map[string]any{"k": int64(8192)})
	e2, _ := codec.TOML{}.Encode(map[string]any{"k": float64(8192)})
	t.Logf("encode int64 8192  -> %q", e1)
	t.Logf("encode float64 8192-> %q", e2)
}
