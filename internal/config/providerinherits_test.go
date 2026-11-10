package config

import (
	"strings"
	"testing"
)

// A user config may write `inherits = ""` (opt out of a pack's inheritance) or a provider
// name; a non-string is an error naming the key. Run through ValidateConfig so removing the
// check from validateProviderEntries fails it.
func TestProviderInheritsShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	for _, tc := range []struct {
		body string
		bad  bool
	}{
		{`{"providers":{"bedrock-key":{"inherits":""}}}`, false},
		{`{"providers":{"bedrock-key":{"inherits":"bedrock"}}}`, false},
		{`{"providers":{"bedrock-key":{"inherits":5}}}`, true},
		{`{"providers":{"bedrock-key":{"inherits":["bedrock"]}}}`, true},
	} {
		errs, _ := ValidateConfig(decode(t, tc.body), ws, nil)
		joined := strings.Join(errs, "\n")
		if tc.bad {
			if !strings.Contains(joined, "providers.bedrock-key.inherits:") {
				t.Errorf("%s: no error naming the key; got %v", tc.body, errs)
			}
		} else if strings.Contains(joined, "inherits") || strings.Contains(joined, "unknown") {
			t.Errorf("%s: unexpected errors %v", tc.body, errs)
		}
	}
}
