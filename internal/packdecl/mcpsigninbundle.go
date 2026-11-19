package packdecl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// MCPSignInBundle identifies retained native sign-in content. Its metadata is
// TOML-only; it contributes no server or operation authority (MS-D1, MS-D11).
type MCPSignInBundle struct {
	Bundle        string `json:"bundle"`
	Format        string `json:"format"`
	Origin        string `json:"origin"`
	Revision      string `json:"revision"`
	License       string `json:"license"`
	CallbackRoute bool   `json:"callback_route,omitempty"`
}

func cleanBundlePath(value string) bool {
	return value != "" && value != "." && path.Clean(value) == value && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "~") && !strings.Contains(value, "\\") && !strings.Contains(value, "\x00") && !strings.HasPrefix(value, "../") && value != ".."
}

func signInBundleProblems(label string, b *MCPSignInBundle) []string {
	if b == nil {
		return []string{label + ": requires a TOML mcp_sign_in_bundle declaration; use yolo pack migrate"}
	}
	var problems []string
	for _, f := range []struct{ name, value string }{{"bundle", b.Bundle}, {"license", b.License}} {
		if !cleanBundlePath(f.value) {
			problems = append(problems, label+"."+f.name+": must be a clean pack-relative path; correct the declaration")
		}
	}
	if b.Format != "claude-plugin" {
		problems = append(problems, label+`.format: must be "claude-plugin"; correct the declaration`)
	}
	u, err := url.Parse(b.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		problems = append(problems, label+".origin: must be an HTTPS repository URL; correct the declaration")
	}
	if !regexp.MustCompile(`^[0-9a-fA-F]{40}$`).MatchString(b.Revision) {
		problems = append(problems, label+".revision: must be a 40-hex commit; pin the upstream commit")
	}
	return problems
}

// takeManagedMCPMetadata runs before the legacy normalization bridge, keeping
// new authored vocabulary out of every legacy JSON decoder.
func takeManagedMCPMetadata(root map[string]any, doc *configdoc.Document, mode manifestReadMode) ([]Contribution, []string, string, bool, []string, []string) {
	var bundles []Contribution
	var formats []string
	var form string
	var paste bool
	var problems, skipped []string
	if raw, exists := root["mcp_sign_in_bundle"]; exists {
		delete(root, "mcp_sign_in_bundle")
		rows, ok := raw.([]any)
		if !ok {
			problems = append(problems, "mcp_sign_in_bundle: must be an array of tables; correct the declaration")
		}
		seenBundles := map[string]bool{}
		for i, row := range rows {
			label := fmt.Sprintf("mcp_sign_in_bundle[%d]", i)
			body, _ := json.Marshal(row)
			var b MCPSignInBundle
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			err := dec.Decode(&b)
			if err != nil {
				if mode != manifestStrict && strings.Contains(err.Error(), "unknown field") {
					skipped = append(skipped, label+": skipped: "+err.Error()+"; update yolo")
					continue
				}
				problems = append(problems, label+": "+err.Error()+"; correct the declaration")
				continue
			}
			ps := signInBundleProblems(label, &b)
			if seenBundles[b.Bundle] {
				ps = append(ps, label+": bundle declared twice; keep one declaration")
			}
			seenBundles[b.Bundle] = true
			if mode == manifestTolerant && len(ps) > 0 {
				skipped = append(skipped, ps...)
				continue
			}
			problems = append(problems, ps...)
			bundles = append(bundles, Contribution{Kind: KindMCPSignInBundle, MCPSignInBundle: &b, Origin: ManifestOrigin{Format: ManifestFormatTOML, File: ManifestTOMLName}, Location: locationFor(doc, []configdoc.Step{{Key: "mcp_sign_in_bundle"}, {Index: i, IsIndex: true}}, label)})
		}
	}
	for _, key := range []string{"native_sign_in", "mcp_header_form", "mcp_sign_in_paste"} {
		raw, exists := root[key]
		if !exists {
			continue
		}
		delete(root, key)
		body, _ := json.Marshal(raw)
		var err error
		switch key {
		case "native_sign_in":
			var metadata struct {
				Formats []string `json:"formats"`
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			err = decoder.Decode(&metadata)
			formats = metadata.Formats
		case "mcp_header_form":
			err = json.Unmarshal(body, &form)
		case "mcp_sign_in_paste":
			err = json.Unmarshal(body, &paste)
		}
		if err != nil {
			problems = append(problems, key+": "+err.Error()+"; correct the declaration")
		}
	}
	seenFormats := map[string]bool{}
	for _, format := range formats {
		if seenFormats[format] {
			problems = append(problems, "native_sign_in.formats: duplicate format; keep one declaration")
		}
		seenFormats[format] = true
		if format != "claude-plugin" {
			problems = append(problems, `native_sign_in.formats: only "claude-plugin" is supported; correct the declaration`)
		}
	}
	if form != "" && form != "bare" && form != "json" {
		problems = append(problems, `mcp_header_form: must be "bare" or "json"; correct the declaration`)
	}
	return bundles, formats, form, paste, problems, skipped
}

// SignInBundles returns declarations in authored order, never inferred from files.
func (m *Manifest) SignInBundles() []MCPSignInBundle {
	var out []MCPSignInBundle
	if m == nil {
		return out
	}
	for _, c := range m.Contributes {
		if c.Kind == KindMCPSignInBundle && c.MCPSignInBundle != nil {
			out = append(out, *c.MCPSignInBundle)
		}
	}
	return out
}
