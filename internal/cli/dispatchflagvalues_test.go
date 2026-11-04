package cli

import (
	"reflect"
	"slices"
	"strings"
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
			want: []string{"run", "--network", "host", "--", "bash"},
		},
		{
			name: "-p naming a subcommand is still a profile",
			in:   []string{"-p", "pack", "--", "bash"},
			want: []string{"run", "-p", "pack", "--", "bash"},
		},
		{
			name: "--profile naming a subcommand is still a profile",
			in:   []string{"--profile", "check", "--", "bash"},
			want: []string{"run", "--profile", "check", "--", "bash"},
		},
		{
			name: "-p value",
			in:   []string{"-p", "run", "--", "bash"},
			want: []string{"run", "-p", "run", "--", "bash"},
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
			want: []string{"run", "--network=host", "--", "bash"},
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
		{"--setup host-self on apply", []string{"apply", "--setup", "host-self"}, "apply"},
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
	// `--setup` is among them: a setup name is a VALUE, so `yolo run --setup -h -- x` must read
	// `-h` as the name and not as a help request, exactly as `--network -h` reads it as a mode.
	// The removed `--at` stays a value flag, so its value is never a help request or a verb
	// while the launch refuses it.
	runHelpSkips := []string{"--network", "--profile", "-p", "--setup", "--at"}
	for _, f := range runHelpSkips {
		if !valueTakingFlags[f] {
			t.Errorf("runHelpRequested skips %q's value but valueTakingFlags does not — "+
				"a value spelling a subcommand name would be read as one", f)
		}
	}
}

// TestFrontDoorRoutesEveryHostNotchSpelling covers OQ-2's alias, decided once at the front door
// (routeArgv, docs/plans/notch-convergence.md item 10, row A3; named-setups.md §8): --setup names
// the setup on every verb, so every launch spelling carrying `--setup host-self` means what `yolo
// host` means, wherever the flag sits. The setup tokens and the run token are consumed, because what follows
// is the host exec verb's own flag grammar. Before, only `yolo --setup host-self -- c` routed; the
// explicit-run and bare spellings reached the jail launcher and were refused there.
func TestFrontDoorRoutesEveryHostNotchSpelling(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantSub string
		want    string
	}{
		{"--setup host-self becomes the host subcommand", "--setup host-self -- claude", "host", "host -- claude"},
		{"--setup=host-self too", "--setup=host-self -- claude", "host", "host -- claude"},
		{"the exec half's own flags survive the rewrite", "--setup host-self -p bedrock -- claude",
			"host", "host -p bedrock -- claude"},
		{"after the flags", "-p bedrock --setup host-self -- claude", "host", "host -p bedrock -- claude"},
		{"the explicit run verb", "run --setup host-self -- claude", "host", "host -- claude"},
		{"--setup before an explicit run", "--setup host-self run -- claude", "host", "host -- claude"},
		{"an implicit command start", "run --setup host-self claude --resume", "host", "host -- claude --resume"},
		{"a bare --setup host-self is `yolo host`", "--setup host-self", "host", "host"},
		{"a launch flag is carried for the host parser to judge", "--setup host-self --timing -- claude",
			"host", "host --timing -- claude"},
		{"a jail-launch flag is carried for the host parser to name", "--setup host-self --dry-run -- claude",
			"host", "host --dry-run -- claude"},
		{"a profile named host is not the notch", "-p host --setup podman-self -- claude", "run",
			"run -p host --setup podman-self -- claude"},
		{"another notch is left alone and still runs a jail", "--setup podman-self -- claude", "run",
			"run --setup podman-self -- claude"},
		{"the last --setup wins, as the launcher reads it", "--setup host-self --setup podman-self -- claude", "run",
			"run --setup host-self --setup podman-self -- claude"},
		{"the last --setup wins in the other order too, every --setup consumed", "--setup podman-self --setup host-self -- claude",
			"host", "host -- claude"},
		{"a dangling --setup is not a host setup", "--setup -- claude", "run", "run --setup -- claude"},
		// The removed --at no longer reroutes: the launch refuses it naming `yolo host`.
		{"a removed --at host is a launch the launcher refuses", "--at host -- claude", "run", "run --at host -- claude"},
		{"another host setup is the host verb, named", "--setup host-yolo-account -- claude", "host",
			"host --setup host-yolo-account -- claude"},
		{"--network host is a network mode, never the notch", "--network host -- bash", "run",
			"run --network host -- bash"},
		{"the host verb keeps --setup for its own parser", "host --setup host-self -- claude", "host",
			"host --setup host-self -- claude"},
		{"another verb's --setup is its own", "apply --setup host-self", "apply", "apply --setup host-self"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, got, _ := routeArgv(strings.Fields(tc.in))
			if sub != tc.wantSub || strings.Join(got, " ") != tc.want {
				t.Errorf("routeArgv(%q) = %s %q, want %s %q", tc.in, sub, got, tc.wantSub, tc.want)
			}
		})
	}
	// Main dispatches what routeArgv decided (routeDecision reads the same function).
	if got := routeDecision(strings.Fields("run --setup host-self -- claude")); got != "dispatch:host" {
		t.Errorf("routeDecision(run --setup host-self -- claude) = %q, want dispatch:host", got)
	}
}
