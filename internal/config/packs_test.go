package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// C1: `packs` is USER-SCOPE ONLY, and the guarantee is by CONSTRUCTION —
// LoadPacks reads the user config file directly and never consults the merged map,
// so a workspace value cannot reach it even if validation were bypassed. This is the
// same shape as host_files' source-bearing half, and the reason is the same one that
// retired host_claude_files: a workspace config travels with the repo and is
// agent-editable, so it must not decide what content enters the jail.
func TestLoadPacksIgnoresWorkspaceScopeByConstruction(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs": ["file:///packs/from-user"]}`)
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"packs": ["file:///packs/from-workspace"]}`)

	got, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("packs = %v, want exactly the user entry", got)
	}
	if got[0].Source != "file:///packs/from-user" {
		t.Errorf("source = %q, want the USER entry", got[0].Source)
	}
}

// A `packs` key in workspace scope is a hard ERROR, not silently ignored: an inert
// key looks exactly like a broken feature.
func TestValidatePacksRejectsWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"packs": ["file:///p"]}`)

	errs, _ := ValidateConfig(decode(t, `{}`), ws, nil)
	var found string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.packs") {
			found = e
		}
	}
	if found == "" {
		t.Fatalf("no config.packs error; got %v", errs)
	}
	for _, want := range []string{"user-scope only", "~/.config/yolo-jail/config.jsonc"} {
		if !strings.Contains(found, want) {
			t.Errorf("error %q missing %q", found, want)
		}
	}
}

func TestCheckPacksLowersBothForms(t *testing.T) {
	entries, problems := checkPacks(decodeAny(t, `[
		"file:///home/me/code/acme/tools/agent-pack",
		{"source": "git+ssh://git@github.com/acme/mono//tools/pack?ref=main",
		 "name": "acme", "only": ["skills/rust-*"],
		 "exclude": ["skills/legacy-*"], "allow_exec": true}
	]`))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %v", entries)
	}
	// String sugar derives its name from the last path segment.
	if entries[0].Name != "agent-pack" {
		t.Errorf("derived name = %q, want agent-pack", entries[0].Name)
	}
	if !entries[0].IsLocal() {
		t.Error("file:// entry should be local")
	}
	// Object form: a git subpath name wins over the repo name.
	e := entries[1]
	if e.Name != "acme" || !e.AllowExec {
		t.Errorf("object entry lowered wrong: %+v", e)
	}
	if len(e.Only) != 1 || len(e.Exclude) != 1 {
		t.Errorf("only/exclude lost: %+v", e)
	}
}

// A git subpath address should name itself after the SUBPATH, which is the more
// specific and more useful name.
func TestDefaultPackNameUsesGitSubpath(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"git+ssh://git@github.com/acme/mono//tools/agent-pack?ref=main", "agent-pack"},
		{"git+https://github.com/acme/pack.git", "pack"},
		{"file:///home/me/packs/local-pack/", "local-pack"},
	} {
		if got := defaultPackName(tc.src); got != tc.want {
			t.Errorf("defaultPackName(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestCheckPacksRejectsBadEntries(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`["/no/scheme"]`, "expected a URL with a scheme"},
		{`["http://example.com/pack"]`, "unsupported scheme"},
		{`[{"name": "x"}]`, `missing required "source"`},
		{`[{"source": "file:///p", "bogus": 1}]`, "unknown key"},
		{`[{"source": "file:///p", "allow_exec": "yes"}]`, "expected true or false"},
		// Two packs resolving to the same name would share a staging dir.
		{`["file:///a/dup", "file:///b/dup"]`, "duplicate pack name"},
		{`[{"source": "file:///p", "name": "a/b"}]`, "path separator"},
	} {
		_, problems := checkPacks(decodeAny(t, tc.body))
		if len(problems) == 0 {
			t.Errorf("%s: expected a problem", tc.body)
			continue
		}
		if !strings.Contains(problems[0], tc.want) {
			t.Errorf("%s: problem %q missing %q", tc.body, problems[0], tc.want)
		}
	}
}

// The wire form must round-trip: the host CLI is the only source of truth for
// resolved entries, and the entrypoint never re-reads config.
func TestPacksWireRoundTrip(t *testing.T) {
	in := []PackEntry{
		{Source: "file:///p", Name: "p"},
		{Source: "git+ssh://git@h/o/r//s?ref=main", Name: "s",
			Only: []string{"skills/*"}, AllowExec: true},
	}
	wire, err := MarshalPacks(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalPacks(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip lost entries: %v", out)
	}
	if out[1].Name != "s" || !out[1].AllowExec || len(out[1].Only) != 1 {
		t.Errorf("round-trip mangled entry: %+v", out[1])
	}
	// Empty in, empty out — no argv for a user with no packs.
	if w, _ := MarshalPacks(nil); w != "" {
		t.Errorf("MarshalPacks(nil) = %q, want empty", w)
	}
}

// A PACK APPLIES TO THE WHOLE JAIL. The per-entry `agents` filter is RETIRED: it
// presumed a fixed, known agent list, which is precisely the assumption the pack
// model deletes — a pack that installs an agent is just a pack, and the pack
// machinery knows nothing about agents. So the key must be REJECTED, not silently
// ignored: an inert key that used to work looks exactly like a broken feature, and
// the whole point of the object form's closed key set is that a no-longer-honored
// filter cannot quietly stop filtering.
func TestCheckPacksRejectsRetiredAgentsKey(t *testing.T) {
	for _, body := range []string{
		`[{"source": "file:///p", "agents": ["claude"]}]`,
		// Also rejected when it names a REAL agent list — this is not typo detection.
		`[{"source": "file:///p", "agents": []}]`,
	} {
		entries, problems := checkPacks(decodeAny(t, body))
		if len(problems) != 1 {
			t.Fatalf("%s: problems = %v, want exactly one", body, problems)
		}
		if !strings.Contains(problems[0], "agents") ||
			!strings.Contains(problems[0], "unknown key") {
			t.Errorf("%s: problem = %q, want an `agents` unknown-key rejection",
				body, problems[0])
		}
		if len(entries) != 0 {
			t.Errorf("%s: entry survived a rejected key: %v", body, entries)
		}
	}
}

// Nothing in a lowered entry may carry an agent filter any more, wire form included.
// The wire form is the entrypoint's ONLY view of packs, so a lingering field there
// would be a filter the host could still express and the jail would still honor.
func TestPackEntryHasNoAgentFilterField(t *testing.T) {
	wire, err := MarshalPacks([]PackEntry{{Source: "file:///p", Name: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wire, "agents") {
		t.Errorf("YOLO_PACKS wire form still mentions agents: %s", wire)
	}
	if _, ok := reflect.TypeOf(PackEntry{}).FieldByName("Agents"); ok {
		t.Error("PackEntry.Agents is back — a pack applies to the whole jail")
	}
}

// decodeAny decodes a JSON value of any shape (the packs key is a LIST, so the
// map-only `decode` helper does not fit).
func decodeAny(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

// D4: whether a pack may name a HOST FILE depends on its CONTENT ORIGIN, not on the
// config scope.
//
// The user-scope rule already stops a workspace from naming a pack. It does NOT mean a
// user who installed a third-party pack agreed to hand that repository their
// ~/.claude/settings.json — installing a pack approves distributing skills and prose,
// and a host-file grant is a materially stronger permission. Fetched content may never
// grant; that is exactly the hole a84b11c closed for host_claude_files.
func TestMayGrantHostFilesDependsOnOrigin(t *testing.T) {
	fetched := PackEntry{Source: "git+ssh://git@github.com/acme/mono//p?ref=main"}
	if fetched.MayGrantHostFiles() {
		t.Error("FETCHED content must never grant a host file — that reopens a84b11c")
	}
	if fetched.Origin() != OriginFetched {
		t.Errorf("origin = %v, want fetched", fetched.Origin())
	}

	// Local: the user's own files, which they can already read without yolo's help.
	local := PackEntry{Source: "file:///home/me/packs/mine"}
	if !local.MayGrantHostFiles() {
		t.Error("a local pack is the user's own content and may grant")
	}

	// Embedded: yolo-shipped, reviewed with the release, so the declaration IS the
	// yolo-shipped decision — the same authority the Go table has today.
	embedded := PackEntry{Source: "file:///irrelevant", IsEmbedded: true}
	if !embedded.MayGrantHostFiles() {
		t.Error("an embedded official pack may grant")
	}
	if embedded.Origin() != OriginEmbedded {
		t.Errorf("origin = %v, want embedded", embedded.Origin())
	}
}

// IsEmbedded must NOT be settable from config: it grants privileges, so a
// user-writable field would undo the whole boundary. json:"-" enforces it.
func TestEmbeddedFlagIsNotDecodableFromConfig(t *testing.T) {
	// Even a wire form that tries to claim it must not come back embedded.
	out, err := UnmarshalPacks(`[{"source":"git+https://h/o/r//p?ref=main","name":"evil","IsEmbedded":true,"isEmbedded":true,"embedded":true}]`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("entries = %v", out)
	}
	if out[0].IsEmbedded {
		t.Error("IsEmbedded was decoded from the wire — a fetched pack could self-grant")
	}
	if out[0].MayGrantHostFiles() {
		t.Error("a fetched pack claiming embedded must still not be allowed to grant")
	}
}

// The `packs` config schema must not accept an embedded/grant-ish key either, so the
// privilege cannot be requested at all.
func TestCheckPacksRejectsPrivilegeKeys(t *testing.T) {
	for _, body := range []string{
		`[{"source": "file:///p", "embedded": true}]`,
		`[{"source": "file:///p", "host_files": [".claude/settings.json"]}]`,
	} {
		_, problems := checkPacks(decodeAny(t, body))
		if len(problems) == 0 {
			t.Errorf("%s: expected rejection of an unknown/privilege key", body)
			continue
		}
		if !strings.Contains(problems[0], "unknown key") {
			t.Errorf("%s: problem = %q", body, problems[0])
		}
	}
}
