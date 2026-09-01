package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestRewriteArgv(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"--", "echo", "foo"}, []string{"run", "--", "echo", "foo"}},
		{[]string{"run", "--", "echo"}, []string{"run", "--", "echo"}},
		{[]string{"broker", "restart"}, []string{"broker", "restart"}},
		{[]string{"-v", "--", "ls"}, []string{"-v", "run", "--", "ls"}},
		{[]string{"check"}, []string{"check"}},
		{[]string{"ps"}, []string{"ps"}},
		{nil, nil},
	}
	for _, tc := range cases {
		got := RewriteArgv(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("RewriteArgv(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSubcommand(t *testing.T) {
	cases := map[string]string{
		"run --":      "run",
		"check":       "check",
		"broker stop": "broker",
		"-v run":      "run",
		"--version":   "",
		"":            "",
		"bogus -- x":  "",
	}
	for in, want := range cases {
		args := strings.Fields(in)
		if got := Subcommand(args); got != want {
			t.Errorf("Subcommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsNative(t *testing.T) {
	for _, sub := range []string{"check", "doctor", "run", "ps", "broker", "prune"} {
		if !IsNative(sub) {
			t.Errorf("IsNative(%q) = false, want true", sub)
		}
	}
	if IsNative("not-a-subcommand") {
		t.Error("IsNative(\"not-a-subcommand\") = true, want false")
	}
	if IsNative("") {
		t.Error("IsNative(\"\") = true, want false")
	}
}

// A run flag's VALUE is not a subcommand name. `namesSubcommand` has skipped those
// values since the `--network host` collision was fixed, but `firstPositional` — the
// other half of the same decision, the one that decides between "unknown command" and
// the run route — never got the same skip. Nothing noticed until global -p made
// `yolo -p dev` (no command) a spelling a user would actually type: the front door
// answered "unknown command \"dev\"" and the run pipeline, which implements the flag,
// was never reached. Every value in valueTakingFlags is user text, so `-p host` and
// `--profile check` were the same trap waiting.
func TestAFlagValueIsNotASubcommand(t *testing.T) {
	cases := map[string]string{
		"-p dev":          "run", // global -p: the name is a profile, not a verb
		"-p host":         "run", // the value spells a registry key on purpose
		"--profile check": "run",
		"-p dev -- bash":  "dispatch:run", // the --→run rewrite, with the value skipped
		"chekc":           "unknown",      // a real typo'd subcommand still errors
	}
	for in, want := range cases {
		if got := routeDecision(strings.Fields(in)); got != want {
			t.Errorf("routeDecision(%q) = %q, want %q", in, got, want)
		}
	}
}
