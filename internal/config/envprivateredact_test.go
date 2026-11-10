package config

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

const privateInline = `{"env": {"set": {"DEBUG": "1"}, "secrets": {"set": {"SENTRY_DSN": "https://dsn-value@sentry"}}}}`

// The assembled copy sits in the workspace, where the jail reads it: a private inline value is
// not in it, and a public one still is (review major 2b).
func TestTheAssembledCopyCarriesNoPrivateInlineValue(t *testing.T) {
	ws, err := resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	merged := decode(t, privateInline)
	if err := WriteAssembledConfig(ws, "podman-self", merged); err != nil {
		t.Fatal(err)
	}
	if err := WriteAssembledOrigins(ws, "podman-self", merged, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{WorkspaceAssembledConfigPath(ws, "podman-self"), WorkspaceAssembledOriginsPath(ws, "podman-self")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "dsn-value") {
			t.Errorf("%s holds a private value:\n%s", path, data)
		}
	}
	data, _ := os.ReadFile(WorkspaceAssembledConfigPath(ws, "podman-self"))
	if !strings.Contains(string(data), "SENTRY_DSN") || !strings.Contains(string(data), `"DEBUG": "1"`) {
		t.Errorf("the copy keeps the private name and the public value:\n%s", data)
	}
	if j, _ := jsonx.DumpsCompact(merged); strings.Contains(j, "<redacted>") {
		t.Error("the caller's config is not changed by the redaction")
	}
}

func TestRedactPrivateEnvAtCoversTheTableAndItsParents(t *testing.T) {
	cfg := decode(t, privateInline)
	envV, _ := cfg.Get("env")
	for _, tc := range []struct {
		path []string
		leak bool
	}{{[]string{"env"}, false}, {[]string{"env", "secrets"}, false}, {[]string{"env", "secrets", "set"}, false},
		{[]string{"env", "secrets", "set", "SENTRY_DSN"}, false}} {
		var steps []configdoc.Step
		var value any = cfg
		for _, k := range tc.path {
			steps = append(steps, configdoc.Step{Key: k})
		}
		value = envV
		if len(tc.path) == 2 {
			s, _ := envV.(interface{ Get(string) (any, bool) }).Get("secrets")
			value = s
		}
		if len(tc.path) >= 3 {
			s, _ := envV.(interface{ Get(string) (any, bool) }).Get("secrets")
			set, _ := s.(interface{ Get(string) (any, bool) }).Get("set")
			value = set
			if len(tc.path) == 4 {
				value, _ = set.(interface{ Get(string) (any, bool) }).Get("SENTRY_DSN")
			}
		}
		j, _ := jsonx.DumpsCompact(RedactPrivateEnvAt(steps, value))
		if strings.Contains(j, "dsn-value") {
			t.Errorf("%v leaks: %s", tc.path, j)
		}
	}
}

// SE-D16 (owner, 2026-11-10: "nested jails get no special privs that the outer jail does not
// have"): the rendered nested scope holds no private value, no private file entry and no private
// set; the public set still crosses. Cutting the strip in filterReadableInheritRoot fails this.
func TestTheNestedInheritCarriesNoPrivateEnvSet(t *testing.T) {
	effective := decode(t, `{"env": {"set": {"DEBUG": "1"}, "secrets": {"set": {"SENTRY_DSN": "dsn-value"}, "files": ["/k/secrets.env"]}}}`)
	for _, scope := range []InheritScope{InheritNested, InheritPreflight} {
		out, _ := FilterInherit(effective, scope)
		rendered, err := RenderFiltered(out, scope, "now")
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"dsn-value", "SENTRY_DSN", "secrets", "/k/secrets.env"} {
			if strings.Contains(rendered, leak) {
				t.Errorf("scope %v carries %q:\n%s", scope, leak, rendered)
			}
		}
	}
	out, _ := FilterInherit(effective, InheritNested)
	if rendered, _ := RenderFiltered(out, InheritNested, "now"); !strings.Contains(rendered, "DEBUG") {
		t.Errorf("the public set still crosses:\n%s", rendered)
	}
}

// The origins record names the digest of the copy beside it, so it must be taken over the REDACTED
// copy: a reader ignores a record whose digest is not the copy's.
func TestTheOriginsRecordDigestMatchesTheRedactedCopy(t *testing.T) {
	ws, err := resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	merged := decode(t, privateInline)
	if err := WriteAssembledConfig(ws, "podman-self", merged); err != nil {
		t.Fatal(err)
	}
	if err := WriteAssembledOrigins(ws, "podman-self", merged, nil); err != nil {
		t.Fatal(err)
	}
	copyBytes, err := os.ReadFile(WorkspaceAssembledConfigPath(ws, "podman-self"))
	if err != nil {
		t.Fatal(err)
	}
	recBytes, err := os.ReadFile(WorkspaceAssembledOriginsPath(ws, "podman-self"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recBytes), copyDigest(copyBytes)) {
		t.Errorf("the record's digest is not the written copy's:\n%s", recBytes)
	}
}
