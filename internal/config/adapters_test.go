package config

// adapters_test.go pins the `adapters` config key
// (docs/reference/protocol-resolution.md#the-adapters-address): the one field of an
// adapter a user may set, and the scope rule that governs it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// writeUserAdapters puts an `adapters` block in the user config and returns the home.
func writeUserAdapters(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"adapters": `+body+`}`)
	return home
}

// The override reaches the composer keyed exactly as packload identifies a pair, which is
// the whole point of AdapterKey being one spelling: a config that named a conversion
// differently from the way core identifies one would be a second vocabulary for one fact.
func TestLoadAdapterAddresses(t *testing.T) {
	writeUserAdapters(t, `{"openai->anthropic": {"address": "http://127.0.0.1:9214"}}`)
	got, err := LoadAdapterAddresses(nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := packload.AdapterKey("openai", "anthropic"); got[want] != "http://127.0.0.1:9214" {
		t.Errorf("LoadAdapterAddresses() = %#v, want %q → the override", got, want)
	}
}

// THE KEY IS THE PAIR, and a key that is not one names no conversion, so it cannot be
// honored loosely. The ADDRESS obeys the provider-address rule — an http/https URL with no
// userinfo — because it is the same kind of fact.
func TestAdapterEntriesAreValidated(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"openai": {"address": "http://127.0.0.1:1"}}`, `spelled "<from>-><to>"`},
		{`{"->anthropic": {"address": "http://127.0.0.1:1"}}`, `spelled "<from>-><to>"`},
		{`{"openai->anthropic": {}}`, `needs an "address"`},
		{`{"openai->anthropic": {"address": "ftp://x/y"}}`, "http or https"},
		{`{"openai->anthropic": {"address": "https://u:t@x/y"}}`, "credential"},
		{`{"openai->anthropic": {"addr": "http://127.0.0.1:1"}}`, "unknown key"},
	} {
		writeUserAdapters(t, tc.body)
		var errs []string
		validateAdapters(t.TempDir(), &errs)
		if !strings.Contains(strings.Join(errs, "; "), tc.want) {
			t.Errorf("%s: want a problem containing %q, got %v", tc.body, tc.want, errs)
		}
	}
}

// A WORKSPACE CONFIG MAY NOT WRITE IT, because the key decides where an agent's inference
// goes and a workspace file travels with the repo and is agent-editable — the same line
// `providers.<name>.endpoints` draws, and the same one `packs` and `profiles` draw.
func TestAdaptersAreRefusedAtWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName),
		[]byte(`{"adapters": {"openai->anthropic": {"address": "http://127.0.0.1:9214"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var errs []string
	validateAdapters(ws, &errs)
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "user-scope only") {
		t.Errorf("a workspace `adapters` block must be refused: %v", errs)
	}
	// It must say what the key does and does NOT move: the pair stays the pack's claim.
	if !strings.Contains(joined, "stays a pack's declaration") {
		t.Errorf("the refusal must say only the address is user-settable: %v", errs)
	}
}

// A conversion NOTHING DECLARES is inert rather than invalid — the open-vocabulary rule
// the protocol names themselves follow. Which pairs exist is the packs' business.
func TestAnOverrideForAnUndeclaredPairIsInert(t *testing.T) {
	writeUserAdapters(t, `{"grpc->anthropic": {"address": "http://127.0.0.1:9214"}}`)
	var errs []string
	validateAdapters(t.TempDir(), &errs)
	if len(errs) != 0 {
		t.Errorf("an override naming a conversion no pack declares must be inert: %v", errs)
	}
}
