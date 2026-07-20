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
