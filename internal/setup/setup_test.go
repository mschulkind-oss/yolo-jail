package setup_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func TestBuiltInNamesDecodeToTheirFields(t *testing.T) {
	for _, s := range setup.BuiltIns() {
		want := string(s.Confinement) + "-" + string(s.RunAs)
		if s.Name != want {
			t.Fatalf("built-in %q is not <confinement>-<run_as> (%q)", s.Name, want)
		}
		if !s.BuiltIn || !setup.IsBuiltIn(s.Name) {
			t.Fatalf("built-in %q is not marked built-in", s.Name)
		}
	}
	if n := len(setup.BuiltIns()); n != 8 {
		t.Fatalf("eight built-ins, got %d", n)
	}
	if setup.IsBuiltIn("mac-as-me") {
		t.Fatal("a user name is not a built-in")
	}
}

// The built-ins are design §5's table, in its order; `yolo setup list` prints them so.
func TestBuiltInsAreTheDesignTableInOrder(t *testing.T) {
	want := []string{
		"podman-self", "apple-container-self", "seatbelt-self", "seatbelt-yolo-account",
		"landlock-self", "landlock-yolo-account", "host-self", "host-yolo-account",
	}
	got := setup.BuiltIns()
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("built-in %d is %q, want %q", i, got[i].Name, name)
		}
	}
	// BuiltIns hands out a copy: a caller editing it cannot change the next caller's answer.
	got[0].Name = "mutated"
	if setup.BuiltIns()[0].Name != "podman-self" {
		t.Fatal("BuiltIns returned the package's own slice")
	}
}

func TestAnOmittedFieldTakesItsOwnDefault(t *testing.T) {
	s, err := setup.Decode("mac-as-me", map[string]string{"confinement": "seatbelt"})
	if err != nil || s.RunAs != setup.RunAsSelf {
		t.Fatalf("run_as default is self regardless of confinement; got %+v, %v", s, err)
	}
	s, err = setup.Decode("lab", map[string]string{"run_as": "yolo-account"})
	if err != nil || s.Confinement != setup.ConfinementPodman {
		t.Fatalf("confinement default is podman regardless of run_as; got %+v, %v", s, err)
	}
	if s.BuiltIn || s.Name != "lab" {
		t.Fatalf("a decoded user setup is not built-in and keeps its name: %+v", s)
	}
	s, err = setup.Decode("empty", nil)
	if err != nil || s.Confinement != setup.ConfinementPodman || s.RunAs != setup.RunAsSelf {
		t.Fatalf("an empty table is both defaults: %+v, %v", s, err)
	}
}

func TestDecodeRefusals(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   []string
	}{
		{"lab", map[string]string{"confinement": "seatbelt", "policy": "allow-default"}, []string{
			"setups.lab: `policy` is not a setup field; Seatbelt has one rule list. Remove it:\n    yolo config unset setups.lab.policy --user",
			"For fewer refusals, run as the yolo account with no Seatbelt:\n    yolo host --setup host-yolo-account -- claude",
		}},
		{"lab", map[string]string{"network": "host"}, []string{
			"setups.lab: `network` is not a setup field; a setup has `confinement` and `run_as`. Remove it:\n    yolo config unset setups.lab.network --user",
		}},
		{"lab", map[string]string{"confinement": "docker"}, []string{
			`setups.lab: confinement = "docker" is not a confinement. The values: podman, apple-container, seatbelt, landlock, host.`,
			"yolo config set setups.lab.confinement podman --user",
		}},
		{"lab", map[string]string{"run_as": "account"}, []string{
			`setups.lab: run_as = "account" is not a run_as. The values: self, yolo-account.`,
			"yolo config set setups.lab.run_as self --user",
		}},
		{"podman-self", map[string]string{"confinement": "podman"}, []string{
			"[setups.podman-self] redefines a built-in setup. Give yours its own name:\n    yolo setup new my-podman --from podman-self",
		}},
		{"Fast", nil, []string{`setups.Fast: "Fast" is not a setup name`}},
	}
	for _, c := range cases {
		_, err := setup.Decode(c.name, c.fields)
		if err == nil {
			t.Errorf("Decode(%q, %v) accepted", c.name, c.fields)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("Decode(%q, %v):\n%s\nwants %q", c.name, c.fields, err, w)
			}
		}
	}
}

func TestDefaultPerOS(t *testing.T) {
	if setup.Default("linux").Name != "podman-self" || setup.Default("darwin").Name != "apple-container-self" {
		t.Fatal("defaults are podman-self on Linux and apple-container-self on macOS")
	}
}

func TestSelectionOrder(t *testing.T) {
	user := map[string]setup.Setup{"mac-as-me": {Name: "mac-as-me", Confinement: setup.ConfinementSeatbelt, RunAs: setup.RunAsSelf}}
	got, src, err := setup.Select("", "", "mac-as-me", user, "darwin")
	if err != nil || got.Name != "mac-as-me" || src != setup.FromUserConfig {
		t.Fatalf("user config: %+v %v %v", got, src, err)
	}
	got, src, _ = setup.Select("", "podman-self", "mac-as-me", user, "darwin")
	if got.Name != "podman-self" || src != setup.FromEnv {
		t.Fatalf("env outranks config: %+v %v", got, src)
	}
	got, src, _ = setup.Select("seatbelt-self", "podman-self", "mac-as-me", user, "darwin")
	if got.Name != "seatbelt-self" || src != setup.FromFlag {
		t.Fatalf("flag outranks env: %+v %v", got, src)
	}
	got, src, _ = setup.Select("", "", "", nil, "linux")
	if got.Name != "podman-self" || src != setup.FromDefault {
		t.Fatalf("default: %+v %v", got, src)
	}
	got, src, _ = setup.Select("", "", "", nil, "darwin")
	if got.Name != "apple-container-self" || src != setup.FromDefault {
		t.Fatalf("darwin default: %+v %v", got, src)
	}
	if _, _, err := setup.Select("fast", "", "", user, "linux"); err == nil || !strings.Contains(err.Error(), `setup "fast" is not defined`) {
		t.Fatalf("undefined name: %v", err)
	}
}

func TestAnUndefinedNameListsTheDefinedOnes(t *testing.T) {
	user := map[string]setup.Setup{
		"zeta":      {Name: "zeta", Confinement: setup.ConfinementPodman, RunAs: setup.RunAsSelf},
		"mac-as-me": {Name: "mac-as-me", Confinement: setup.ConfinementSeatbelt, RunAs: setup.RunAsSelf},
	}
	_, _, err := setup.Select("", "fast", "", user, "linux")
	if err == nil {
		t.Fatal("an undefined YOLO_SETUP was accepted")
	}
	want := `setup "fast" is not defined. Defined here: podman-self, apple-container-self, seatbelt-self, seatbelt-yolo-account, landlock-self, landlock-yolo-account, host-self, host-yolo-account, mac-as-me, zeta (yolo setup list)`
	if err.Error() != want {
		t.Fatalf("got\n%s\nwant\n%s", err, want)
	}
}

// A name that breaks the grammar is refused before the lookup, naming where it came from:
// it would otherwise reach a container name and a keeper key (SUP-D3).
func TestSelectRefusesAMalformedName(t *testing.T) {
	for _, c := range []struct{ flag, env, cfg, where string }{
		{"Bad.Name", "", "", "--setup"},
		{"", "Bad.Name", "", "YOLO_SETUP"},
		{"", "", "Bad.Name", "your user config"},
	} {
		_, _, err := setup.Select(c.flag, c.env, c.cfg, nil, "linux")
		if err == nil || !strings.Contains(err.Error(), `"Bad.Name" is not a setup name`) || !strings.Contains(err.Error(), c.where) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestSourceNamesWhereTheChoiceCameFrom(t *testing.T) {
	for src, want := range map[setup.Source]string{
		setup.FromFlag:        "--setup",
		setup.FromEnv:         "YOLO_SETUP",
		setup.FromUserConfig:  "your user config",
		setup.FromDefault:     "this OS's default",
		setup.FromRunningJail: "the running jail",
	} {
		if src.String() != want {
			t.Errorf("%d: %q, want %q", src, src.String(), want)
		}
		if src == setup.FromRunningJail && src.LaunchLabel() != "running jail" {
			t.Errorf("%d: LaunchLabel = %q, want running jail", src, src.LaunchLabel())
		}
	}
}

func TestNotBuiltRefusesNamingTheNearest(t *testing.T) {
	cases := map[string]string{ // name on linux -> required substring
		"seatbelt-self":         "uses Seatbelt, which only macOS has",
		"seatbelt-yolo-account": "uses Seatbelt, which only macOS has. On this machine:\n    yolo --setup podman-self -- claude",
		"landlock-self":         "is not built yet. The nearest built setup on Linux:\n    yolo --setup podman-self -- claude",
		"landlock-yolo-account": "is not built yet. The nearest built setup on Linux:\n    yolo --setup podman-self -- claude",
		"host-yolo-account":     "not built",
		"apple-container-self":  "Apple Container",
	}
	for name, want := range cases {
		err := setup.Buildable(setup.MustBuiltIn(name), "linux", func(string) bool { return true })
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s on linux: %v", name, err)
		}
	}
	if err := setup.Buildable(setup.MustBuiltIn("host-yolo-account"), "darwin", nil); err != nil {
		t.Fatalf("host-yolo-account is built on macOS (SU-R6): %v", err)
	}
}

func TestExactRefusalTexts(t *testing.T) {
	all := func(string) bool { return true }
	cases := []struct {
		s    setup.Setup
		goos string
		want string
	}{
		{setup.MustBuiltIn("seatbelt-self"), "linux",
			"setup \"seatbelt-self\" uses Seatbelt, which only macOS has. On this machine:\n    yolo --setup podman-self -- claude"},
		{setup.MustBuiltIn("landlock-self"), "linux",
			"setup \"landlock-self\" is not built yet. The nearest built setup on Linux:\n    yolo --setup podman-self -- claude"},
		{setup.MustBuiltIn("landlock-self"), "darwin",
			"setup \"landlock-self\" uses Landlock, which only Linux has. On this machine:\n    yolo --setup seatbelt-self -- claude"},
		{setup.MustBuiltIn("landlock-yolo-account"), "darwin",
			"setup \"landlock-yolo-account\" uses Landlock, which only Linux has. On this machine:\n    yolo --setup seatbelt-yolo-account -- claude"},
		{setup.MustBuiltIn("host-yolo-account"), "linux",
			"setup \"host-yolo-account\" is not built yet on Linux: Linux has no yolo account. The nearest built setup on Linux:\n    yolo host -- claude"},
		{setup.Setup{Name: "lab", Confinement: setup.ConfinementPodman, RunAs: setup.RunAsYoloAccount}, "darwin",
			"setups.lab: Podman cannot run as the yolo account yet. Run it as you:\n    yolo config set setups.lab.run_as self --user\nOr use the built-in:\n    yolo --setup podman-self -- claude"},
		{setup.Setup{Name: "lab", Confinement: setup.ConfinementAppleContainer, RunAs: setup.RunAsYoloAccount}, "darwin",
			"setups.lab: Apple Container cannot run as the yolo account yet. Run it as you:\n    yolo config set setups.lab.run_as self --user\nOr use the built-in:\n    yolo --setup apple-container-self -- claude"},
	}
	for _, c := range cases {
		err := setup.Buildable(c.s, c.goos, all)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s on %s:\n%v\nwant\n%s", c.s.Name, c.goos, err, c.want)
		}
	}
}

func TestBuiltOnItsOwnOS(t *testing.T) {
	all := func(string) bool { return true }
	for _, c := range []struct{ name, goos string }{
		{"podman-self", "linux"}, {"podman-self", "darwin"}, {"host-self", "linux"}, {"host-self", "darwin"},
		{"apple-container-self", "darwin"}, {"seatbelt-self", "darwin"}, {"seatbelt-yolo-account", "darwin"},
		{"host-yolo-account", "darwin"},
	} {
		if err := setup.Buildable(setup.MustBuiltIn(c.name), c.goos, all); err != nil {
			t.Errorf("%s on %s: %v", c.name, c.goos, err)
		}
	}
}

func TestAMissingTechnologyNamesItsInstall(t *testing.T) {
	only := func(have ...string) func(string) bool {
		return func(bin string) bool {
			for _, h := range have {
				if h == bin {
					return true
				}
			}
			return false
		}
	}
	// The Mac default without Apple Container, Podman installed: the generic refusal and the
	// default's own sentence (design §6) both name the install and an installed setup.
	err := setup.Buildable(setup.Default("darwin"), "darwin", only("podman"))
	var missing *setup.MissingTechnologyError
	if !errors.As(err, &missing) {
		t.Fatalf("want a MissingTechnologyError, got %v", err)
	}
	if missing.Binary != "container" || missing.Alternative != "podman-self" {
		t.Fatalf("%+v", missing)
	}
	wantGeneric := "setup \"apple-container-self\" needs Apple Container, which is not installed. Install it (https://github.com/apple/container), or choose an installed setup:\n    yolo --setup podman-self -- claude          (yolo setup list shows the rest)"
	if err.Error() != wantGeneric {
		t.Fatalf("got\n%s\nwant\n%s", err, wantGeneric)
	}
	wantDefault := "The default setup on this Mac, apple-container-self, needs Apple Container, which is not installed. Install it (https://github.com/apple/container), or choose an installed setup:\n    yolo --setup podman-self -- claude          (yolo setup list shows the rest)"
	if got := missing.AsDefault(); got != wantDefault {
		t.Fatalf("got\n%s\nwant\n%s", got, wantDefault)
	}

	// Without either container runtime the Mac still has Seatbelt.
	err = setup.Buildable(setup.MustBuiltIn("podman-self"), "darwin", only())
	if !errors.As(err, &missing) || missing.Alternative != "seatbelt-self" {
		t.Fatalf("%v", err)
	}
	// And offering it, the refusal says what it gives up: it runs as you and confines less.
	if !strings.Contains(err.Error(), "seatbelt-self runs the agent as you") ||
		!strings.Contains(err.Error(), "confines less than podman-self") {
		t.Fatalf("the seatbelt-self alternative does not say it confines less:\n%v", err)
	}

	// Linux without Podman has no other setup with walls; the refusal names the install and
	// the list, and never offers host as a fallback.
	err = setup.Buildable(setup.Default("linux"), "linux", only())
	if !errors.As(err, &missing) {
		t.Fatalf("want a MissingTechnologyError, got %v", err)
	}
	want := "setup \"podman-self\" needs Podman, which is not installed. Install it (https://podman.io/docs/installation):\n    yolo setup list          (shows the setups built here)"
	if err.Error() != want {
		t.Fatalf("got\n%s\nwant\n%s", err, want)
	}
	if strings.Contains(missing.AsDefault(), "yolo host") || !strings.HasPrefix(missing.AsDefault(), "The default setup on this machine, podman-self,") {
		t.Fatalf("%s", missing.AsDefault())
	}
}

func TestNetworkModeTheTechnologyCannotHold(t *testing.T) {
	if p := setup.NetworkModeProblem(setup.MustBuiltIn("apple-container-self"), "host", linuxHere); !strings.Contains(p, "always runs bridged") {
		t.Fatal(p)
	}
	if p := setup.NetworkModeProblem(setup.MustBuiltIn("seatbelt-self"), "bridge", linuxHere); !strings.Contains(p, "always shares the Mac's network") {
		t.Fatal(p)
	}
	for _, mode := range []string{"", "bridge", "host"} {
		if p := setup.NetworkModeProblem(setup.MustBuiltIn("podman-self"), mode, linuxHere); p != "" {
			t.Fatal(p)
		}
	}
	for _, c := range []struct{ name, mode string }{
		{"apple-container-self", ""}, {"apple-container-self", "bridge"},
		{"seatbelt-self", ""}, {"seatbelt-self", "host"}, {"seatbelt-yolo-account", "host"},
		{"host-self", ""}, {"host-self", "host"}, {"landlock-self", "bridge"}, {"landlock-self", "host"},
	} {
		if p := setup.NetworkModeProblem(setup.MustBuiltIn(c.name), c.mode, linuxHere); p != "" {
			t.Errorf("%s with %q: %s", c.name, c.mode, p)
		}
	}
}

func TestNetworkModeExactTexts(t *testing.T) {
	tail := " For this launch, a setup that can:\n    yolo --setup podman-self -- claude\nOr, if the project does not need it:  yolo config unset network.mode --project"
	for _, c := range []struct{ name, mode, want string }{
		{"apple-container-self", "host", `network.mode = "host": setup apple-container-self always runs bridged, so the jail cannot share this machine's network.` + tail},
		{"seatbelt-yolo-account", "bridge", `network.mode = "bridge": setup seatbelt-yolo-account always shares the Mac's network, so the agent cannot have a loopback of its own.` + tail},
		{"host-self", "bridge", `network.mode = "bridge": setup host-self always shares this machine's network, so the agent cannot have a loopback of its own.` + tail},
	} {
		if got := setup.NetworkModeProblem(setup.MustBuiltIn(c.name), c.mode, linuxHere); got != c.want {
			t.Errorf("%s %s:\n%s\nwant\n%s", c.name, c.mode, got, c.want)
		}
	}
}

// TestNetworkRefusalsNameTheirScopeAndTheFlag: the unset step names the scope that holds the key,
// and a typed --network names the flag and dropping it, never a config key (SUP-D13).
func TestNetworkRefusalsNameTheirScopeAndTheFlag(t *testing.T) {
	ac := setup.MustBuiltIn("apple-container-self")
	if got := setup.NetworkModeProblemIn(ac, "host", "--user", linuxHere); !strings.HasSuffix(got, "yolo config unset network.mode --user") {
		t.Errorf("user scope: %s", got)
	}
	want := "--network host: setup apple-container-self always runs bridged, so the jail cannot share this machine's network." +
		" For this launch, a setup that can:\n    yolo --setup podman-self -- claude\n" +
		"Or, if the project does not need it:  launch without --network host"
	if got := setup.NetworkFlagProblem(ac, "host", linuxHere); got != want {
		t.Errorf("flag:\n%s\nwant\n%s", got, want)
	}
	for _, c := range []struct{ name, mode string }{
		{"podman-self", "host"}, {"podman-self", "bridge"}, {"apple-container-self", "bridge"}, {"seatbelt-self", "host"},
	} {
		if p := setup.NetworkFlagProblem(setup.MustBuiltIn(c.name), c.mode, linuxHere); p != "" {
			t.Errorf("%s --network %s: %s", c.name, c.mode, p)
		}
	}
}

func TestNameGrammar(t *testing.T) {
	for _, bad := range []string{"Fast", "a.b", "-x", "x-", strings.Repeat("a", 33), "", "a_b", "a b"} {
		if setup.ValidName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"a", "x1", "mac-as-me", strings.Repeat("a", 32), "seatbelt-yolo-account"} {
		if !setup.ValidName(good) {
			t.Errorf("%q refused", good)
		}
	}
	for _, s := range setup.BuiltIns() {
		if !setup.ValidName(s.Name) {
			t.Errorf("built-in %q breaks the grammar", s.Name)
		}
	}
}

func TestDescribeSpellsEveryField(t *testing.T) {
	for name, want := range map[string]string{
		"seatbelt-self":         "Seatbelt, deny-by-default, as you; shares the Mac's network",
		"seatbelt-yolo-account": "Seatbelt, deny-by-default, as the yolo account; shares the Mac's network",
		"podman-self":           "Podman, as you; bridged unless network.mode is host",
		"apple-container-self":  "Apple Container, as you; always bridged",
		"host-self":             "no walls, as you; shares this machine's network",
		"host-yolo-account":     "no walls, as the yolo account; shares this machine's network",
		"landlock-self":         "Landlock (not built), as you",
	} {
		if got := setup.Describe(setup.MustBuiltIn(name)); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// The three values the pipeline consumes today are derived from confinement alone; run_as
// never changes them (no field reads another, design §4).
func TestDerivedValuesFollowConfinementOnly(t *testing.T) {
	for _, c := range []struct {
		conf    setup.Confinement
		backend string
		kind    setup.Kind
	}{
		{setup.ConfinementPodman, "podman", setup.KindJail},
		{setup.ConfinementAppleContainer, "container", setup.KindJail},
		{setup.ConfinementSeatbelt, "macos-user", setup.KindGuest},
		{setup.ConfinementLandlock, "", setup.KindGuest},
		{setup.ConfinementHost, "", setup.KindHost},
	} {
		for _, ra := range []setup.RunAs{setup.RunAsSelf, setup.RunAsYoloAccount} {
			s := setup.Setup{Name: "x", Confinement: c.conf, RunAs: ra}
			if s.Backend() != c.backend || s.Kind() != c.kind {
				t.Errorf("%s/%s: backend %q kind %v, want %q %v", c.conf, ra, s.Backend(), s.Kind(), c.backend, c.kind)
			}
		}
	}
}

func TestSuggestNameIsAlwaysValid(t *testing.T) {
	for in, want := range map[string]string{
		"Fast": "fast", "a.b": "a-b", "-x-": "x", "My  Lab!": "my-lab", "": "my-setup", "...": "my-setup",
		strings.Repeat("A", 40): strings.Repeat("a", 32),
	} {
		got := setup.SuggestName(in)
		if got != want || !setup.ValidName(got) {
			t.Errorf("SuggestName(%q) = %q, want %q (valid)", in, got, want)
		}
	}
}

func TestMustBuiltInPanicsOnAnUnknownName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustBuiltIn(\"nope\") did not panic")
		}
	}()
	setup.MustBuiltIn("nope")
}

var linuxHere = setup.Here{GOOS: "linux"}

// TestTheNetworkRefusalNamesASetupThatRunsHere: the first step names a built-in that holds the
// mode and runs on this OS, spelled for where it is typed (Choose): `bridge` names the OS's
// default container setup, so a Mac is told apple-container-self, and `host` names podman-self,
// the one container setup that shares the machine's network; in a session the step is
// YOLO_SETUP, never a `yolo --setup` line.
func TestTheNetworkRefusalNamesASetupThatRunsHere(t *testing.T) {
	for _, c := range []struct {
		name, mode string
		here       setup.Here
		want, not  string
	}{
		{"seatbelt-self", "bridge", setup.Here{GOOS: "darwin"}, "\n    yolo --setup apple-container-self -- claude", "podman-self"},
		{"host-self", "bridge", setup.Here{GOOS: "darwin"}, "\n    yolo --setup apple-container-self -- claude", "podman-self"},
		{"host-self", "bridge", linuxHere, "\n    yolo --setup podman-self -- claude", "apple-container-self"},
		{"apple-container-self", "host", setup.Here{GOOS: "darwin"}, "\n    yolo --setup podman-self -- claude", "apple-container-self -- claude"},
		{"seatbelt-self", "bridge", setup.Here{GOOS: "darwin", InSession: true}, "a setup that can: " + setup.Choose("apple-container-self", true) + ".", "yolo --setup"},
		{"apple-container-self", "host", setup.Here{GOOS: "darwin", InSession: true}, "a setup that can: " + setup.Choose("podman-self", true) + ".", "yolo --setup"},
	} {
		s := setup.MustBuiltIn(c.name)
		for _, got := range []string{setup.NetworkModeProblem(s, c.mode, c.here), setup.NetworkFlagProblem(s, c.mode, c.here)} {
			if !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
				t.Errorf("%s %s %+v: want %q and not %q:\n%s", c.name, c.mode, c.here, c.want, c.not, got)
			}
		}
	}
}
