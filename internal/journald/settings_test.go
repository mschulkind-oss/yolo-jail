package journald

import (
	"os"
	"path/filepath"
	"testing"
)

// The settings file is the ONLY thing that decides whether this bridge can read the
// whole host journal, and every one of these cases is a way a config or a filesystem
// can be wrong. They all resolve to ModeUser, and that is the assertion: the widening
// direction has no accidental route into it.
//
// The old shape had one — `--mode` was a free string, and ParseRequest's test is
// `mode == "user"`, so "usr", "" and "Full" all behaved as FULL. That is exactly the
// class this table exists to keep closed.
func TestLoadSettingsResolvesEveryFailureToUserMode(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		path string
	}{
		{"no --settings at all", ""},
		{"file does not exist", filepath.Join(dir, "absent.json")},
		{"file is not JSON", write("garbage.json", "not json at all")},
		{"file is a JSON list, not an object", write("list.json", `["full"]`)},
		{"the key is absent", write("empty.json", `{}`)},
		{"the key is null", write("null.json", `{"full": null}`)},
		{"the key is false", write("false.json", `{"full": false}`)},
		// The one that would bite hardest under a truthiness coercion: a QUOTED
		// false is a true-ish string in every Truthy() this repo has, and here it
		// would mean the whole host journal.
		{"the key is the STRING \"false\"", write("strfalse.json", `{"full": "false"}`)},
		{"the key is the STRING \"true\"", write("strtrue.json", `{"full": "true"}`)},
		{"the key is the number 1", write("one.json", `{"full": 1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LoadSettings(tc.path); got != ModeUser {
				t.Errorf("LoadSettings = %q, want %q — every unreadable, absent or "+
					"wrong-typed value must fall to the NARROW mode, or a broken settings "+
					"file is an escalation", got, ModeUser)
			}
		})
	}
}

// And the one case that does escalate: a real boolean true, which is the only shape
// core's validator would have written (the declaration is `"type": "bool"`).
func TestLoadSettingsHonoursAGenuineBooleanTrue(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(`{"full": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(p); got != ModeFull {
		t.Fatalf("LoadSettings = %q, want %q — `full: true` is the escalation the user "+
			"config asked for, and dropping it silently narrows what somebody wrote", got, ModeFull)
	}
}

// The mode this daemon runs in has to be the one ParseRequest branches on, and the
// two live in different files. ModeUser is not decoration: ParseRequest tests the
// literal "user", so a constant that drifted from it would leave a daemon that
// believes it is narrow and passes every arg through.
func TestModeUserIsTheStringParseRequestNarrowsOn(t *testing.T) {
	got := ParseRequest([]byte(`{"args":["-n","5"]}`), ModeUser)
	if got.ErrText != "" {
		t.Fatalf("unexpected error: %s", got.ErrText)
	}
	if len(got.Args) == 0 || got.Args[0] != "--user" {
		t.Fatalf("ParseRequest(ModeUser) produced %v; ModeUser must be the exact string "+
			"ParseRequest prepends --user for, or the narrow mode is narrow in name only", got.Args)
	}
	full := ParseRequest([]byte(`{"args":["-n","5"]}`), ModeFull)
	if len(full.Args) != 2 || full.Args[0] != "-n" {
		t.Fatalf("ParseRequest(ModeFull) produced %v, want the client's args unchanged", full.Args)
	}
}
