package cli

import (
	"reflect"
	"slices"
	"testing"
)

// TestRewriteArgvSkipsFlagValues is the regression for a silent CHANGE OF MEANING, not a
// parse error.
//
// `--network host` puts the token "host" before `--` as a flag VALUE. When the pre-`--`
// scan compared every token against the registry, adding a `host` subcommand turned
// `yolo --network host -- bash` from "run bash in a host-networked jail" into "run bash
// at the host notch" — the same argv, silently relocated OUTSIDE the sandbox. Nothing
// would have failed; the user would just no longer be in a jail.
func TestRewriteArgvSkipsFlagValues(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "--network host is a flag value, not the host subcommand",
			in:   []string{"--network", "host", "--", "bash"},
			want: []string{"--network", "host", "run", "--", "bash"},
		},
		{
			name: "-p naming a subcommand is still a profile",
			in:   []string{"-p", "pack", "--", "bash"},
			want: []string{"-p", "pack", "run", "--", "bash"},
		},
		{
			name: "--profile naming a subcommand is still a profile",
			in:   []string{"--profile", "check", "--", "bash"},
			want: []string{"--profile", "check", "run", "--", "bash"},
		},
		{
			name: "--pack-profile value",
			in:   []string{"--pack-profile", "run", "--", "bash"},
			want: []string{"--pack-profile", "run", "run", "--", "bash"},
		},
		{
			name: "a real leading subcommand still suppresses the rewrite",
			in:   []string{"check", "--", "claude"},
			want: []string{"check", "--", "claude"},
		},
		{
			name: "a real subcommand after a skipped flag value still suppresses it",
			in:   []string{"--network", "bridge", "check", "--", "claude"},
			want: []string{"--network", "bridge", "check", "--", "claude"},
		},
		{
			name: "the host subcommand suppresses the rewrite",
			in:   []string{"host", "--", "claude"},
			want: []string{"host", "--", "claude"},
		},
		{
			name: "host with its own flags before -- still suppresses it",
			in:   []string{"host", "-p", "bedrock", "--", "claude"},
			want: []string{"host", "-p", "bedrock", "--", "claude"},
		},
		{
			name: "glued --flag=value needs no skip",
			in:   []string{"--network=host", "--", "bash"},
			want: []string{"--network=host", "run", "--", "bash"},
		},
		{
			name: "boolean flags do not swallow the next token",
			in:   []string{"--new", "check", "--", "claude"},
			want: []string{"--new", "check", "--", "claude"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RewriteArgv(slices.Clone(tc.in)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RewriteArgv(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSubcommandSkipsFlagValues: Subcommand carries the same scan and needed the same
// fix. Without it the rewrite could be correct and resolution still land on `host`.
func TestSubcommandSkipsFlagValues(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"--network host resolves to no subcommand", []string{"--network", "host", "--", "bash"}, ""},
		{"-p pack resolves to no subcommand", []string{"-p", "pack", "--", "bash"}, ""},
		{"--at host on apply", []string{"apply", "--at", "host"}, "apply"},
		{"a real subcommand resolves", []string{"check", "--", "claude"}, "check"},
		{"a subcommand after a boolean flag resolves", []string{"--new", "check"}, "check"},
		{"a subcommand name as --network's value does not", []string{"--network", "check"}, ""},
		{"the host subcommand resolves", []string{"host", "--", "claude"}, "host"},
		{"--network host is the network mode, not the host notch", []string{"--network", "host", "--", "bash"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Subcommand(tc.in); got != tc.want {
				t.Errorf("Subcommand(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestValueTakingFlagsCoverRunHelpSkips pins the two independent copies of this skip
// together. runHelpRequested (runcmd.go) has always carried its own list; if a flag is
// added there and not here, `yolo --newflag host -- bash` silently relocates again.
func TestValueTakingFlagsCoverRunHelpSkips(t *testing.T) {
	// The flags runHelpRequested consumes a value for, transcribed from its switch.
	runHelpSkips := []string{"--network", "--pack-profile", "-p"}
	for _, f := range runHelpSkips {
		if !valueTakingFlags[f] {
			t.Errorf("runHelpRequested skips %q's value but valueTakingFlags does not — "+
				"a value spelling a subcommand name would be read as one", f)
		}
	}
}

// TestRewriteArgvHostNotchAlias covers OQ-2's alias: --at names the notch on every other
// verb, so `yolo --at host -- claude` has to mean what `yolo host -- claude` means. The
// notch tokens are consumed, because what follows is the host exec verb's own flag
// grammar and `--at` is not part of it.
func TestRewriteArgvHostNotchAlias(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "--at host becomes the host subcommand",
			in:   []string{"--at", "host", "--", "claude"},
			want: []string{"host", "--", "claude"},
		},
		{
			name: "--at=host too",
			in:   []string{"--at=host", "--", "claude"},
			want: []string{"host", "--", "claude"},
		},
		{
			name: "the exec half's own flags survive the rewrite",
			in:   []string{"--at", "host", "-p", "bedrock", "--", "claude"},
			want: []string{"host", "-p", "bedrock", "--", "claude"},
		},
		{
			name: "another notch is left alone and still runs a jail",
			in:   []string{"--at", "jail", "--", "claude"},
			want: []string{"--at", "jail", "run", "--", "claude"},
		},
		{
			name: "a dangling --at is not a host notch",
			in:   []string{"--at", "--", "claude"},
			want: []string{"--at", "run", "--", "claude"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RewriteArgv(slices.Clone(tc.in)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RewriteArgv(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
