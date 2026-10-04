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
	// `--at` joined them when parseRunArgs grew its own `--at` case (DP-B22): a notch
	// token is a VALUE, so `yolo run --at -h -- x` must read `-h` as the notch and not
	// as a help request, exactly as `--network -h` reads it as a mode.
	runHelpSkips := []string{"--network", "--profile", "-p", "--at"}
	for _, f := range runHelpSkips {
		if !valueTakingFlags[f] {
			t.Errorf("runHelpRequested skips %q's value but valueTakingFlags does not — "+
				"a value spelling a subcommand name would be read as one", f)
		}
	}
}

// TestFrontDoorRoutesEveryHostNotchSpelling covers OQ-2's alias, decided once at the front door
// (routeArgv, docs/plans/notch-convergence.md item 10, row A3): --at names the notch on every
// other verb, so every launch spelling carrying `--at host` means what `yolo host` means,
// wherever the flag sits. The notch tokens and the run token are consumed, because what follows
// is the host exec verb's own flag grammar. Before, only `yolo --at host -- c` routed; the
// explicit-run and bare spellings reached the jail launcher and were refused there.
func TestFrontDoorRoutesEveryHostNotchSpelling(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantSub string
		want    string
	}{
		{"--at host becomes the host subcommand", "--at host -- claude", "host", "host -- claude"},
		{"--at=host too", "--at=host -- claude", "host", "host -- claude"},
		{"the exec half's own flags survive the rewrite", "--at host -p bedrock -- claude",
			"host", "host -p bedrock -- claude"},
		{"after the flags", "-p bedrock --at host -- claude", "host", "host -p bedrock -- claude"},
		{"the explicit run verb", "run --at host -- claude", "host", "host -- claude"},
		{"--at before an explicit run", "--at host run -- claude", "host", "host -- claude"},
		{"an implicit command start", "run --at host claude --resume", "host", "host -- claude --resume"},
		{"a bare --at host is `yolo host`", "--at host", "host", "host"},
		{"a launch flag is carried for the host parser to judge", "--at host --timing -- claude",
			"host", "host --timing -- claude"},
		{"a jail-launch flag is carried for the host parser to name", "--at host --dry-run -- claude",
			"host", "host --dry-run -- claude"},
		{"a profile named host is not the notch", "-p host --at jail -- claude", "run",
			"run -p host --at jail -- claude"},
		{"another notch is left alone and still runs a jail", "--at jail -- claude", "run",
			"run --at jail -- claude"},
		{"the last --at wins, as the launcher reads it", "--at host --at jail -- claude", "run",
			"run --at host --at jail -- claude"},
		{"the last --at wins in the other order too, every --at consumed", "--at jail --at host -- claude",
			"host", "host -- claude"},
		{"a dangling --at is not a host notch", "--at -- claude", "run", "run --at -- claude"},
		{"--network host is a network mode, never the notch", "--network host -- bash", "run",
			"run --network host -- bash"},
		{"the host verb keeps --at for its own parser", "host --at host -- claude", "host",
			"host --at host -- claude"},
		{"another verb's --at is its own", "apply --at host", "apply", "apply --at host"},
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
	if got := routeDecision(strings.Fields("run --at host -- claude")); got != "dispatch:host" {
		t.Errorf("routeDecision(run --at host -- claude) = %q, want dispatch:host", got)
	}
}
