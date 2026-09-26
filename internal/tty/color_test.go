package tty

import "testing"

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestNoColorFollowsTheConvention pins https://no-color.org's rule: present AND
// non-empty disables color; absent or empty does not. Any value counts — the
// convention names no "true" spelling, so "0" and "false" disable too.
func TestNoColorFollowsTheConvention(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"unset", map[string]string{}, false},
		{"empty", map[string]string{NoColorVar: ""}, false},
		{"one", map[string]string{NoColorVar: "1"}, true},
		{"zero is still set", map[string]string{NoColorVar: "0"}, true},
		{"false is still set", map[string]string{NoColorVar: "false"}, true},
		{"another variable", map[string]string{"NOCOLOR": "1", "COLOR": "0"}, false},
	} {
		if got := NoColor(envOf(tc.env)); got != tc.want {
			t.Errorf("%s: NoColor = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestNoColorNilReadsTheProcessEnvironment: a nil getenv is os.Getenv, which is
// what every gate without an injected environment passes.
func TestNoColorNilReadsTheProcessEnvironment(t *testing.T) {
	t.Setenv(NoColorVar, "1")
	if !NoColor(nil) {
		t.Error("NoColor(nil) ignored the process's NO_COLOR=1")
	}
	t.Setenv(NoColorVar, "")
	if NoColor(nil) {
		t.Error("NoColor(nil) treated an empty NO_COLOR as set")
	}
}

// TestColorNeedsAllThree is the gate's truth table: requested, a terminal, and
// no NO_COLOR veto. Every other combination is plain text.
func TestColorNeedsAllThree(t *testing.T) {
	on := envOf(map[string]string{})
	off := envOf(map[string]string{NoColorVar: "1"})
	for _, tc := range []struct {
		name                string
		getenv              func(string) string
		requested, terminal bool
		want                bool
	}{
		{"requested on a terminal", on, true, true, true},
		{"NO_COLOR vetoes a terminal", off, true, true, false},
		{"not requested", on, false, true, false},
		{"not a terminal", on, true, false, false},
		{"nothing", off, false, false, false},
	} {
		if got := Color(tc.getenv, tc.requested, tc.terminal); got != tc.want {
			t.Errorf("%s: Color = %v, want %v", tc.name, got, tc.want)
		}
	}
}
