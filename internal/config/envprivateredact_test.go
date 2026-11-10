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

// SEP-D16: the nested jail's user layer carries the private set too, and the preflight's does not.
// Cutting "secrets" from filterReadableInheritRoot fails this.
func TestTheNestedInheritCarriesThePrivateEnvSet(t *testing.T) {
	effective := decode(t, `{"env": {"set": {"DEBUG": "1"}, "secrets": {"set": {"SENTRY_DSN": "v"}, "files": ["/k.env"]}}}`)
	for scope, want := range map[InheritScope]bool{InheritNested: true, InheritPreflight: false} {
		out, _ := FilterInherit(effective, scope)
		env, _ := out.Get("env")
		secrets := false
		if m, ok := env.(*jsonx.OrderedMap); ok {
			_, secrets = m.Get("secrets")
		}
		if secrets != want {
			t.Errorf("scope %v: env.secrets crosses = %v, want %v", scope, secrets, want)
		}
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
