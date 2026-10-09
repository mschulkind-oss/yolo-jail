package jsonx

import (
	"strings"
	"testing"
)

// unsupported is a value jsonx has no case for, standing in for the []DeviceEntry
// that made `yolo-serial list --json` print `{"devices": }`.
type unsupported struct{ Path string }

// TestNestedEncodeErrorPropagates pins that an unencodable value NESTED inside an
// object or array fails the whole Dumps call, in every mode, rather than being
// written as nothing and leaving invalid JSON behind a nil error.
func TestNestedEncodeErrorPropagates(t *testing.T) {
	om := NewOrderedMap()
	om.Set("devices", []unsupported{{Path: "/dev/ttyUSB0"}})
	deep := NewOrderedMap()
	deep.Set("outer", []any{map[string]any{"inner": unsupported{}}})
	values := map[string]any{
		"ordered map member":   om,
		"string map member":    map[string]any{"devices": unsupported{}},
		"array element":        []any{"ok", unsupported{}},
		"deeply nested member": deep,
	}
	wantPath := map[string]string{
		"ordered map member":   ".devices",
		"string map member":    ".devices",
		"array element":        "[1]",
		"deeply nested member": ".outer[0].inner",
	}
	dumps := map[string]func(any) (string, error){
		"DumpsSnapshot": DumpsSnapshot,
		"DumpsCompact":  DumpsCompact,
		"DumpsIndent":   func(v any) (string, error) { return DumpsIndent(v, 2) },
	}
	for vname, v := range values {
		for dname, dump := range dumps {
			out, err := dump(v)
			if err == nil {
				t.Errorf("%s(%s) = %q, nil error; want an unsupported-type error", dname, vname, out)
				continue
			}
			if !strings.Contains(err.Error(), "unsupported type") {
				t.Errorf("%s(%s) error = %v; want it to name the unsupported type", dname, vname, err)
			}
			if want := wantPath[vname]; !strings.Contains(err.Error(), "(at "+want+")") {
				t.Errorf("%s(%s) error = %v; want it to name the path %s", dname, vname, err, want)
			}
			if out != "" {
				t.Errorf("%s(%s) returned partial output %q alongside its error", dname, vname, out)
			}
		}
	}
}

// TestMustDumpsCompactPanicsOnAnEncodeError pins MustDumpsCompact's contract: the same
// bytes as DumpsCompact for an encodable value, and a panic naming the failure, never "",
// for one that is not.
func TestMustDumpsCompactPanicsOnAnEncodeError(t *testing.T) {
	v := map[string]any{"b": []string{"x"}, "a": int64(1)}
	want, err := DumpsCompact(v)
	if err != nil {
		t.Fatal(err)
	}
	if got := MustDumpsCompact(v); got != want {
		t.Errorf("MustDumpsCompact = %q, want %q", got, want)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustDumpsCompact returned for an unencodable value")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "(at .devices)") {
			t.Errorf("panic = %v, want it to name the value's path", r)
		}
	}()
	MustDumpsCompact(map[string]any{"devices": unsupported{}})
}
