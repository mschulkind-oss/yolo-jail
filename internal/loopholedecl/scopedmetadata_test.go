package loopholedecl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scopedDeclarationFixture = `name = "fixture"
transport = "loopback-tls"
[call_broker]
source = "fixture"
front = "cli"
program = "fake"
classifier = ["yolo", "internal", "call-broker-classify", "fixture"]
[call_broker.scope]
roots = "declared"
[[call_broker.resource]]
type = "root"
[[call_broker.cell]]
name = "list"
kind = "read"
max_for = "1h"
`

func TestScopedDeclarationMetadata(t *testing.T) {
	valid := strings.Replace(scopedDeclarationFixture, "roots = \"declared\"", "roots = \"declared\"\nnesting = \"segment\"\nseparator = \"/\"\nanchors = [\"s3://\", \"ce://\"]", 1) + `
[call_broker.context]
unset = ["SYNTHETIC_TOKEN"]
route_anchors = ["s3://"]
[call_broker.context.fields]
profile = "SYNTHETIC_PROFILE"
`
	m, err := Decode([]byte(valid), "/packs/fixture/loopholes/fixture")
	if err != nil {
		t.Fatalf("TOML scoped metadata refused: %v", err)
	}
	raw, _ := json.Marshal(m.CallBroker)
	for _, want := range []string{"segment", "SYNTHETIC_PROFILE", "SYNTHETIC_TOKEN", "read"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("hash input omits %s: %s", want, raw)
		}
	}
	for _, tc := range []struct{ name, from, to string }{
		{"H10-mode", `nesting = "segment"`, `nesting = "glob"`},
		{"H10-separator", `separator = "/"`, `separator = "//"`},
		{"H10-prefix-anchor", `anchors = ["s3://", "ce://"]`, `anchors = ["s3://", "s3://b"]`},
		{"H31-read-tables", `kind = "read"`, `kind = "read"
tables = []`},
		{"H31-read-default", `kind = "read"`, `kind = "read"
default = { all = false }`},
		{"H31-reserved-read", `name = "list"`, `name = "read"`},
		{"unknown-kind", `kind = "read"`, `kind = "other"`},
		{"invalid-route-anchor", `route_anchors = ["s3://"]`, `route_anchors = ["gs://"]`},
		{"invalid-env", `profile = "SYNTHETIC_PROFILE"`, `profile = "bad-env"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(strings.Replace(valid, tc.from, tc.to, 1)), "/packs/fixture/loopholes/fixture"); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	// New metadata must change the hash, while every absent new field stays absent.
	hash := CallBrokerDeclarationHash(m.CallBroker)
	for _, tc := range []struct{ from, to string }{
		{`kind = "read"`, `kind = "write"
tables = ["all"]`},
		{`separator = "/"`, `separator = ":"`},
		{`anchors = ["s3://", "ce://"]`, `anchors = ["s3://", "new://"]`},
		{`route_anchors = ["s3://"]`, `route_anchors = ["s3://", "ce://"]`},
		{`SYNTHETIC_TOKEN`, `OTHER_TOKEN`}, {`SYNTHETIC_PROFILE`, `OTHER_PROFILE`},
	} {
		changed, err := Decode([]byte(strings.Replace(valid, tc.from, tc.to, 1)), "/packs/fixture/loopholes/fixture")
		if err != nil {
			t.Fatal(err)
		}
		if CallBrokerDeclarationHash(changed.CallBroker) == hash {
			t.Fatalf("hash unchanged after %s", tc.from)
		}
	}
}

func TestScopedMetadataPreservesOldDeclarationHashes(t *testing.T) {
	for name, want := range map[string]string{"aws": "79e4ffa37a9c498a03ee4d90f11e0c635d780b005b064fa4d5d4db92236b021a", "circleci": "f1c5ac9e670e4ae6556936ab38ffb1276b23b1f8ac801eeca5c5326752a15f85"} {
		path := filepath.Join("..", "..", "packs", name, "loopholes", name+"-broker", "loophole.toml")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Decode(raw, filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if got := CallBrokerDeclarationHash(m.CallBroker); got != want {
			t.Fatalf("%s old hash=%s want=%s", name, got, want)
		}
	}
}

func TestContextDeclaredQuotedFieldIdentity(t *testing.T) {
	raw := scopedDeclarationFixture + `
[call_broker.context.fields]
"Profile équipe"="SYNTHETIC_PROFILE"
`
	m, err := Decode([]byte(raw), "/packs/fixture/loopholes/fixture")
	if err != nil {
		t.Fatal(err)
	}
	if m.CallBroker.Context.Fields["Profile équipe"] != "SYNTHETIC_PROFILE" {
		t.Fatal("quoted declared field identity changed")
	}
	for _, field := range []string{"", "source"} {
		changed := strings.Replace(raw, `"Profile équipe"`, fmt.Sprintf("%q", field), 1)
		if _, err := Decode([]byte(changed), "/packs/fixture/loopholes/fixture"); err == nil {
			t.Fatalf("invalid/colliding field %q accepted", field)
		}
	}
}

func TestNativeReadMetadataCompatibility(t *testing.T) {
	for name, want := range map[string]string{"git": "e9f7319cce0823ee4f845e05726f40a530641c8292a523d22b0fb6a6ea5af5ad", "github": "5e0486184f6759d06b907eeafb03289370e67c8ce50c3f69e806d33022499b49"} {
		path := filepath.Join("..", "..", "packs", name, "loopholes", name+"-broker", "loophole.toml")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Decode(raw, filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if got := BrokeredDeclarationHash(m.Brokered); got != want {
			t.Fatalf("%s old native hash=%s", name, got)
		}
		changed := strings.Replace(string(raw), "[brokered]", "[brokered]\nread_max_for = \"1h\"", 1)
		c, err := Decode([]byte(changed), filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if c.Brokered.ReadMaxFor != "1h" || BrokeredDeclarationHash(c.Brokered) == want {
			t.Fatal("native cap absent from hash")
		}
	}
	legacy := `{"name":"fixture","transport":"loopback-tls","brokered":{"source":"fixture","remote_host":"github.com","read_max_for":"1h"}}`
	if _, err := Decode([]byte(legacy), "/packs/fixture/loopholes/fixture"); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("H25 legacy native cap accepted: %v", err)
	}
	if _, _, err := DecodeTolerant([]byte(legacy), "/packs/fixture/loopholes/fixture"); err == nil {
		t.Fatal("tolerant legacy native cap accepted")
	}
}

func TestScopedMetadataLegacyCellKindRefused(t *testing.T) {
	for _, decode := range []func([]byte, string) (*Manifest, error){Decode, func(b []byte, d string) (*Manifest, error) { m, _, err := DecodeTolerant(b, d); return m, err }} {
		raw := `{"name":"fixture","transport":"loopback-tls","call_broker":{"source":"fixture","front":"cli","program":"fake","classifier":["yolo","internal","call-broker-classify","fixture"],"scope":{"roots":"declared"},"resource":[{"type":"root"}],"cell":[{"name":"list","kind":"read"}]}}`
		if _, err := decode([]byte(raw), "/packs/fixture/loopholes/fixture"); err == nil || !strings.Contains(err.Error(), "unknown key") {
			t.Fatalf("legacy cell kind is not strict unknown key: %v", err)
		}
		old := strings.Replace(raw, `"name":"list","kind":"read"`, `"name":"list","tables":["all"]`, 1)
		before, err := decode([]byte(old), "/packs/fixture/loopholes/fixture")
		if err != nil {
			t.Fatal(err)
		}
		if before.CallBroker.Cells[0].Kind != "" {
			t.Fatal("old write kind gained serialization")
		}
		for _, changed := range []string{
			strings.Replace(old, `"roots":"declared"`, `"roots":"declared","nesting":"exact"`, 1),
			strings.Replace(old, `"roots":"declared"`, `"roots":"declared","separator":"/"`, 1),
			strings.Replace(old, `"roots":"declared"`, `"roots":"declared","anchors":["x://"]`, 1),
			strings.Replace(old, `"scope":`, `"context":{"fields":{"selector":"SYNTHETIC_SELECTOR"}},"scope":`, 1),
		} {
			if _, err := decode([]byte(changed), "/packs/fixture/loopholes/fixture"); err == nil || !strings.Contains(err.Error(), "unknown key") {
				t.Fatalf("new legacy declaration metadata accepted: %v", err)
			}
		}
	}
}
