package packdecl

import (
	"reflect"
	"strings"
	"testing"
)

func TestNativeSignInFormatsUsesCanonicalMetadataTable(t *testing.T) {
	m, problems := DecodeTOML([]byte("name=\"fixture\"\n[native_sign_in]\nformats=[\"claude-plugin\"]\n"))
	if len(problems) != 0 || m == nil || !reflect.DeepEqual(m.MCPSignInFormats, []string{"claude-plugin"}) {
		t.Fatalf("canonical format metadata not read: %v", problems)
	}
	for _, body := range []string{"mcp_sign_in_formats=[\"claude-plugin\"]", "native_sign_in_formats=[\"claude-plugin\"]", "native_sign_in=\"claude-plugin\"", "[native_sign_in]\nformats=\"claude-plugin\"", "[native_sign_in]\nformats=[\"unknown\"]", "[native_sign_in]\nformats=[\"claude-plugin\"]\nfuture=true"} {
		if _, problems := DecodeTOML([]byte("name=\"fixture\"\n" + body)); len(problems) == 0 {
			t.Fatalf("invalid shape or unpublished alias accepted: %s", body)
		}
		if strings.HasPrefix(body, "[native_sign_in]") || strings.HasPrefix(body, "native_sign_in=") {
			for _, decode := range []func([]byte) (*Manifest, []string, []string){DecodeTOMLForUse, DecodeTOMLTolerant} {
				if _, problems, _ := decode([]byte("name=\"fixture\"\n" + body)); len(problems) == 0 {
					t.Fatalf("known invalid metadata accepted across skew: %s", body)
				}
			}
		}
	}
}

func TestManagedMCPSignInBundleThroughManifestDecode(t *testing.T) {
	manifest, problems := DecodeTOML([]byte(`name = "fixture"
[[mcp_sign_in_bundle]]
bundle = "skills/slack"
format = "claude-plugin"
origin = "https://github.com/slackapi/slack-skills-plugin"
revision = "5b8802105740b5c045c97247fded2e73b205e95e"
license = "skills/slack/LICENSE"
`))
	if len(problems) != 0 || manifest == nil {
		t.Fatalf("accepted sign-in declaration refused: %v", problems)
	}
	encoded, err := EncodeTOML(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "[[mcp_sign_in_bundle]]") {
		t.Fatalf("sign-in declaration lost on encode: %s", encoded)
	}
	decoded, again := DecodeTOML(encoded)
	if len(again) != 0 {
		t.Fatalf("round-trip refused: %v", again)
	}
	if !reflect.DeepEqual(manifest.SignInBundles(), decoded.SignInBundles()) {
		t.Fatal("bundle round-trip changed declaration")
	}
}

func TestManagedMCPMetadataHasNoLegacyFeatureSpelling(t *testing.T) {
	for _, body := range []string{`{"native_sign_in":{"formats":["claude-plugin"]}}`, `{"mcp_header_form":"json"}`, `{"mcp_sign_in_paste":true}`, `{"contributes":[{"kind":"mcp-sign-in-bundle"}]}`, `{"contributes":[{"kind":"mcp-sign-in-bundle","bundle":"b","format":"claude-plugin"}]}`} {
		if _, problems := Decode([]byte(body)); len(problems) == 0 {
			t.Fatalf("legacy feature accepted: %s", body)
		}
	}
	for _, decode := range []func([]byte) (*Manifest, []string, []string){DecodeForUse, DecodeTolerant} {
		m, _, _ := decode([]byte(`{"native_sign_in":{"formats":["claude-plugin"]},"mcp_header_form":"json","mcp_sign_in_paste":true}`))
		if m != nil && (len(m.MCPSignInFormats) > 0 || m.MCPHeaderForm != "" || m.MCPSignInPaste) {
			t.Fatal("legacy skew granted capability")
		}
	}
}

func TestManagedMCPMetadataStrictUseTolerantParity(t *testing.T) {
	data := []byte(`name="fixture"
mcp_header_form="json"
mcp_sign_in_paste=true
[native_sign_in]
formats=["claude-plugin"]
[[mcp_sign_in_bundle]]
bundle="b"
format="claude-plugin"
origin="https://example.invalid/repo"
revision="0123456789abcdef0123456789abcdef01234567"
license="b/LICENSE"
callback_route=true
`)
	strict, problems := DecodeTOML(data)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, decode := range []func([]byte) (*Manifest, []string, []string){DecodeTOMLForUse, DecodeTOMLTolerant} {
		m, ps, skipped := decode(data)
		if len(ps) > 0 || len(skipped) > 0 || !reflect.DeepEqual(m, strict) {
			t.Fatalf("parity: %+v %v %v", m, ps, skipped)
		}
	}
	encoded, err := EncodeTOML(strict)
	if err != nil {
		t.Fatal(err)
	}
	m, ps := DecodeTOML(encoded)
	if len(ps) > 0 || !reflect.DeepEqual(m.SignInBundles(), strict.SignInBundles()) || m.MCPHeaderForm != "json" || !m.MCPSignInPaste || !reflect.DeepEqual(m.MCPSignInFormats, strict.MCPSignInFormats) {
		t.Fatalf("capability round-trip: %s %v", encoded, ps)
	}
	for _, tc := range []struct{ old, new string }{{`bundle="b"`, `bundle="../b"`}, {`license="b/LICENSE"`, `license="/host/LICENSE"`}, {`format="claude-plugin"`, `format="unknown"`}, {`origin="https://example.invalid/repo"`, `origin="http://example.invalid/repo"`}, {`revision="0123456789abcdef0123456789abcdef01234567"`, `revision="latest"`}, {`callback_route=true`, `callback_route="yes"`}} {
		bad := []byte(strings.Replace(string(data), tc.old, tc.new, 1))
		if _, ps := DecodeTOML(bad); len(ps) == 0 {
			t.Fatalf("invalid field accepted: %s", bad)
		}
		if _, ps, _ := DecodeTOMLForUse(bad); len(ps) == 0 {
			t.Fatalf("use read accepted invalid known field: %s", bad)
		}
	}
	unknown := append(append([]byte(nil), data...), []byte("future_identity=true\n")...)
	if _, ps := DecodeTOML(unknown); len(ps) == 0 {
		t.Fatal("strict unknown bundle field accepted")
	}
	for _, decode := range []func([]byte) (*Manifest, []string, []string){DecodeTOMLForUse, DecodeTOMLTolerant} {
		m, ps, skipped := decode(unknown)
		if len(ps) > 0 || len(skipped) != 1 || len(m.SignInBundles()) != 0 {
			t.Fatalf("unknown bundle gained authority: %v %v", ps, skipped)
		}
	}
}
