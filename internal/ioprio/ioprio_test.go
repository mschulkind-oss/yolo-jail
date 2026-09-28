package ioprio

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func decodeValue(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(`{"v": ` + s + `}`))
	if err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	got, _ := v.(*jsonx.OrderedMap).Get("v")
	return got
}

// TestParseReadsBothFormsTheSameWay: the string is the priority alone (IO-D3), so every
// string has an object twin that means exactly the same, and null and {} mean unset.
func TestParseReadsBothFormsTheSameWay(t *testing.T) {
	for _, tc := range []struct {
		json string
		want Priority
	}{
		{`null`, Normal},
		{`{}`, Normal},
		{`{"priority": null}`, Normal},
		{`"normal"`, Normal},
		{`{"priority": "normal"}`, Normal},
		{`"low"`, Low},
		{`{"priority": "low"}`, Low},
		{`"idle"`, Idle},
		{`{"priority": "idle"}`, Idle},
	} {
		got, problems := Parse(decodeValue(t, tc.json), "config.resources.io")
		if len(problems) > 0 || got != tc.want {
			t.Errorf("Parse(%s) = %q %q, want %q and no problems", tc.json, got, problems, tc.want)
		}
	}
}

// TestParseRefusesEverythingElse: exactly three lowercase words, in a string or under
// `priority`, and nothing else in the object — `weight` included until OQ-IO7 ships one.
// Each refusal names the path and, for a bad word, the three it could have been.
func TestParseRefusesEverythingElse(t *testing.T) {
	for _, tc := range []struct {
		json string
		want string
	}{
		{`""`, `config.resources.io: expected "idle", "low" or "normal" (got "")`},
		{`"Low"`, `config.resources.io: expected "idle", "low" or "normal" (got "Low")`},
		{`"high"`, `config.resources.io: expected "idle", "low" or "normal"`},
		{`7`, `config.resources.io: expected a string`},
		{`true`, `config.resources.io: expected a string`},
		{`["low"]`, `config.resources.io: expected a string`},
		{`{"priority": "Low"}`, `config.resources.io.priority: expected "idle", "low" or "normal" (got "Low")`},
		{`{"priority": 7}`, `config.resources.io.priority: expected a string`},
		{`{"weight": 100}`, `config.resources.io.weight: unknown key`},
		{`{"priority": "low", "class": "be"}`, `config.resources.io.class: unknown key`},
	} {
		got, problems := Parse(decodeValue(t, tc.json), "config.resources.io")
		if got != Normal {
			t.Errorf("Parse(%s) = %q; a refused value must apply nothing", tc.json, got)
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("Parse(%s) problems = %q, want one containing %q", tc.json, problems, tc.want)
		}
	}
}

// TestFromResourcesReadsTheIoKey is the reader every consumer past validation uses: it must
// see the shorthand and the object alike, and read a missing block or key as unset.
func TestFromResourcesReadsTheIoKey(t *testing.T) {
	if got := FromResources(nil); got != Normal {
		t.Errorf("FromResources(nil) = %q, want normal", got)
	}
	res := jsonx.NewOrderedMap()
	res.Set("memory", "8g")
	if got := FromResources(res); got != Normal {
		t.Errorf("no io key: %q, want normal", got)
	}
	res.Set("io", "idle")
	if got := FromResources(res); got != Idle {
		t.Errorf(`"io": "idle" = %q`, got)
	}
	obj := jsonx.NewOrderedMap()
	obj.Set("priority", "low")
	res.Set("io", obj)
	if got := FromResources(res); got != Low {
		t.Errorf(`"io": {"priority": "low"} = %q`, got)
	}
	res.Set("io", "bogus")
	if got := FromResources(res); got != Normal {
		t.Errorf("an invalid value = %q, want normal (nothing applied)", got)
	}
}

// TestKernelValues pins the Linux encoding, class << 13 | level: low is BE7 and idle is
// the IDLE class, and normal makes no call at all.
func TestKernelValues(t *testing.T) {
	if v, ok := Low.KernelValue(); !ok || v != 2<<13|7 || Describe(v) != "be/7" {
		t.Errorf("low = %d %v (%s), want BE7", v, ok, Describe(v))
	}
	if v, ok := Idle.KernelValue(); !ok || v != 3<<13 || Describe(v) != "idle" {
		t.Errorf("idle = %d %v (%s), want IDLE", v, ok, Describe(v))
	}
	if _, ok := Normal.KernelValue(); ok {
		t.Error("normal must make no call")
	}
	if Describe(0) != "none/0" {
		t.Errorf("unset reads %q, want none/0", Describe(0))
	}
	if Normal.Declared() || !Low.Declared() || !Idle.Declared() {
		t.Error("only low and idle declare anything")
	}
}
