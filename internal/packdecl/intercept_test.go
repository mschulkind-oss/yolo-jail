package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

func TestInterceptDecodes(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"github","contributes":[
		{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"]}]}`))
	if len(probs) != 0 {
		t.Fatalf("problems %v", probs)
	}
	c := m.Contributions()[0]
	if c.Kind != KindIntercept || c.Bin != "gh" || !reflect.DeepEqual(c.Forward, []string{"yolo", "gh", "--"}) {
		t.Fatalf("contribution %+v", c)
	}
}

func TestInterceptRefusesWhatNoShimCouldRun(t *testing.T) {
	for name, c := range map[string]struct{ entry, want string }{
		"no forward":      {`{"kind":"intercept","bin":"gh"}`, `needs "forward"`},
		"no bin":          {`{"kind":"intercept","forward":["yolo"]}`, `needs "bin"`},
		"path in bin":     {`{"kind":"intercept","bin":"../gh","forward":["yolo"]}`, "bin"},
		"path forwarder":  {`{"kind":"intercept","bin":"gh","forward":["/usr/bin/yolo"]}`, "bare program name"},
		"forwards itself": {`{"kind":"intercept","bin":"gh","forward":["gh","x"]}`, "exec itself"},
		"empty word":      {`{"kind":"intercept","bin":"gh","forward":["yolo",""]}`, "empty word"},
		"a refusal field": {`{"kind":"intercept","bin":"gh","forward":["yolo"],"message":"no"}`, `does not take "message"`},
		"forward elsewhere": {`{"kind":"requires","bin":"gh","forward":["yolo"]}`,
			`does not take "forward"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"x","contributes":[` + c.entry + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), c.want) {
				t.Fatalf("problems %v, want one mentioning %q", probs, c.want)
			}
		})
	}
}
