package journald

import "testing"

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
