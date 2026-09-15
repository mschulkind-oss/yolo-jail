package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

func shippedPiPack(t *testing.T) *packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == "pi" {
			return p
		}
	}
	t.Fatal("the pi pack is not embedded")
	return nil
}

// The adapter has to reach Pi through its real extension discovery directory. A source
// file that exists in the pack but has no files contribution is dead documentation, so
// this assertion starts from the shipped declaration that the renderer consumes.
func TestShippedPiPackDeliversOpenAIAuthExtension(t *testing.T) {
	p := shippedPiPack(t)
	needs := p.Decl.DeclaredNeeds()
	if len(needs) != 1 || needs[0].Pack != "openai-auth" || len(needs[0].WhenBins) != 0 {
		t.Fatalf("pi needs = %v, want one unconditional openai-auth need", needs)
	}
	var found bool
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.From == "extensions/yolo-openai-auth.js" &&
			c.Into == ".pi/agent/extensions/yolo-openai-auth.js" {
			found = true
		}
	}
	if !found {
		t.Fatal("pi pack does not deliver the yolo OpenAI adapter to Pi's extension directory")
	}
	if _, err := os.Stat(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js")); err != nil {
		t.Fatalf("declared Pi OpenAI extension is absent: %v", err)
	}
	home := t.TempDir()
	if _, err := RenderHostFiles(p, home, filesReq(t), false); err != nil {
		t.Fatalf("rendering the shipped Pi extension: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", "yolo-openai-auth.js")); err != nil {
		t.Fatalf("Pi cannot discover the rendered extension: %v", err)
	}
}

// Pi's own lock is scoped to one workspace auth.json. The adapter must therefore ask the
// machine broker on every login and refresh, and must never put its canonical refresh token
// in that workspace file. A broker that is already authenticated must not start another
// browser flow. This executes the shipped extension with a fake yolo client.
func TestPiOpenAIAuthExtensionReusesBrokerLoginAndRefreshes(t *testing.T) {
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extension := filepath.Join(dir, "extension.mjs")
	if err := os.WriteFile(extension, source, 0o644); err != nil {
		t.Fatal(err)
	}
	// ⚠ `wc -l | tr -d ' '`, AND THE `tr` IS THE WHOLE REASON THIS TEST PASSES ON A MAC.
	// BSD `wc` right-pads its count to 8 columns; GNU `wc` does not. So the call counter
	// below interpolated as `access-       2` on darwin and `access-2` on Linux, and the
	// harness's `first.access !== "access-2"` failed with "broker calls were not sequenced"
	// — a message that points at sequencing when the fault is whitespace. Measured on macOS
	// 26.5; it reddened `check-macos` while `check-go` stayed green, which is what made it
	// read as a macOS behaviour difference in the adapter rather than in the fixture.
	//
	// The sibling site in openaiauth_prelaunch_test.go does NOT need this: it feeds the
	// count to `[ … -gt 1 ]`, and POSIX integer comparison tolerates leading blanks. The
	// workflows that count test lines already carry the same `tr` for the same reason.
	yolo := filepath.Join(dir, "yolo")
	if err := os.WriteFile(yolo, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$3" in
  status) printf '{"logged_in":true,"login_required":false}\n' ;;
  login) printf 'unexpected browser login\n' >&2; exit 9 ;;
  token) printf '{"access_token":"access-%s","refresh_token":"must-not-escape","expires_at":4102444800000,"account_id":"acct-1","generation":4}\n' "$(wc -l < "$CALLS" | tr -d ' ')" ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(harness, []byte(`
import extension from "./extension.mjs";
let registration;
extension({ registerProvider(name, config) { registration = { name, config }; } });
if (registration.name !== "openai-codex") throw new Error("wrong provider: " + registration.name);
const oauth = registration.config.oauth;
const first = await oauth.login({});
const second = await oauth.refreshToken(first, new AbortController().signal);
if (first.refresh !== "yolo-broker:4" || second.refresh !== "yolo-broker:4") throw new Error("refresh secret escaped");
if (first.access !== "access-2" || second.access !== "access-3") throw new Error("broker calls were not sequenced");
if (first.expires !== 4102444800000 || second.accountId !== "acct-1") throw new Error("view shape lost");
if (oauth.getApiKey(second) !== "access-3") throw new Error("access token not resolved");
`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command("node", harness)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CALLS="+calls)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing Pi OpenAI extension: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "unexpected browser login") {
		t.Fatalf("an existing broker login started a browser flow: %q", output)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "internal openai-auth-client status\ninternal openai-auth-client token\ninternal openai-auth-client token\n"
	if string(got) != want {
		t.Fatalf("broker calls = %q, want %q", strings.TrimSpace(string(got)), strings.TrimSpace(want))
	}
}

func TestPiOpenAIAuthExtensionStartsBrowserOnlyWhenStatusRequiresLogin(t *testing.T) {
	p := shippedPiPack(t)
	source, err := os.ReadFile(filepath.Join(p.Root, "extensions", "yolo-openai-auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo"), []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$3" in
  status) printf '{"logged_in":false}\n' ;;
  login) printf 'Open this URL: https://example.test/login\n' >&2; printf '{"ok":true}\n' ;;
  token) printf '{"access_token":"access","expires_at":4102444800000,"generation":1}\n' ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.mjs"), []byte(`
import extension from "./extension.mjs";
let oauth;
extension({ registerProvider(_name, config) { oauth = config.oauth; } });
const result = await oauth.login({});
if (result.access !== "access" || result.refresh !== "yolo-broker:1") throw new Error("bad credentials");
`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command("node", filepath.Join(dir, "harness.mjs"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CALLS="+calls)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("executing Pi OpenAI extension: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "https://example.test/login") {
		t.Fatalf("login URL was not forwarded: %q", output)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "internal openai-auth-client status\ninternal openai-auth-client login\ninternal openai-auth-client token\n"
	if string(got) != want {
		t.Fatalf("broker calls = %q, want %q", strings.TrimSpace(string(got)), strings.TrimSpace(want))
	}
}
