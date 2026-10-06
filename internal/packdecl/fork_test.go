package packdecl

// fork_test.go pins the manifest half of the fork route (docs/design/forked-programs-as-packs.md,
// FP-D5 and FP-D6): what a `via: "source"` program must carry, what it may not, and that a fork's
// own contribution installs nothing by itself.

import (
	"strings"
	"testing"
)

// goodFork is a valid fork contribution; each case below breaks one thing about it.
func goodFork() Contribution {
	return Contribution{
		Kind: KindProgram, Bin: "pi", Via: ViaSource, ForkOf: "pi",
		Source:   "git+https://github.com/you/pi-fork?ref=main",
		Build:    `npm ci && npm run build && npm install -g "$(npm pack --silent)"`,
		Produces: []string{".npm-global/bin/pi", ".npm-global/lib/node_modules/pi-fork"},
	}
}

// TestAForkValidates is the floor under the refusal table: an equivalence between "refused" and
// "broken" is satisfiable by refusing everything.
func TestAForkValidates(t *testing.T) {
	if probs := validateContribution("contributes[0]", goodFork()); len(probs) > 0 {
		t.Fatalf("a well-formed fork is refused:\n%s", strings.Join(probs, "\n"))
	}
	// platforms (where it builds) and node_floor are the two program fields a fork may add.
	c := goodFork()
	c.Platforms, c.NodeFloor = []string{"linux"}, "22.19"
	if probs := validateContribution("contributes[0]", c); len(probs) > 0 {
		t.Fatalf("a fork declaring platforms and node_floor is refused:\n%s", strings.Join(probs, "\n"))
	}
	// A git+file:// repository has revisions, unlike a file:// directory.
	c = goodFork()
	c.Source = "git+file:///srv/git/pi-fork?ref=v1"
	if probs := validateContribution("contributes[0]", c); len(probs) > 0 {
		t.Fatalf("a git+file:// fork source is refused:\n%s", strings.Join(probs, "\n"))
	}
}

func TestForkRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Contribution)
		want string
	}{
		{"no fork_of", func(c *Contribution) { c.ForkOf = "" }, `needs "fork_of"`},
		{"bad fork_of", func(c *Contribution) { c.ForkOf = "a/b" }, "is not a pack name"},
		{"no source", func(c *Contribution) { c.Source = "" }, `needs "source"`},
		{"no build", func(c *Contribution) { c.Build = "  " }, `needs "build"`},
		{"multi-line build", func(c *Contribution) { c.Build = "make\nmake install" }, "ONE command line"},
		{"no produces", func(c *Contribution) { c.Produces = nil }, `needs "produces"`},
		{"file:// source", func(c *Contribution) { c.Source = "file:///home/me/pi-fork" },
			"a file:// directory has none"},
		{"unparseable source", func(c *Contribution) { c.Source = "https://github.com/you/fork" },
			"unsupported scheme"},
		{"unpinned source", func(c *Contribution) { c.Source = "git+https://github.com/you/fork" },
			"missing required ?ref="},
		{"absolute produces", func(c *Contribution) { c.Produces = []string{"/usr/local/bin/pi"} },
			"must be relative"},
		{"dotdot produces", func(c *Contribution) { c.Produces = []string{".local/bin/../../x"} },
			`must not contain ".."`},
		{"unclean produces", func(c *Contribution) { c.Produces = []string{".local/bin//pi"} },
			"must be a clean home-relative path"},
		{"outside the surfaces", func(c *Contribution) {
			c.Produces = []string{".local/bin/pi", ".config/pi/settings.json"}
		}, "is not inside a program surface"},
		{"a whole surface", func(c *Contribution) { c.Produces = []string{".local/bin/pi", ".local"} },
			"names a whole program surface"},
		{"duplicate produces", func(c *Contribution) { c.Produces = []string{".local/bin/pi", ".local/bin/pi"} },
			"is already listed"},
		{"no program path", func(c *Contribution) { c.Produces = []string{".local/share/pi/pi"} },
			"none of the entries is the program itself"},
		{"program under a surface not on PATH", func(c *Contribution) {
			c.Produces = []string{".codex/packages/standalone/bin/pi"}
		}, "none of the entries is the program itself"},
		{"package", func(c *Contribution) { c.Package = "pi" }, `does not take "package"`},
		{"url", func(c *Contribution) { c.URL = "https://x/i.sh" }, `does not take "url"`},
		{"flags", func(c *Contribution) { c.Flags = []string{"--x"} }, `does not take "flags"`},
		{"update", func(c *Contribution) { c.Update = []string{"update"} }, `does not take "update"`},
		{"installer_env", func(c *Contribution) { c.InstallerEnv = map[string]string{"PREFIX": "~/.local"} },
			`does not take "installer_env"`},
		{"refresh", func(c *Contribution) {
			c.Refresh = &Refresh{Argv: []string{"update"}, Lock: ".s/.yolo-update.lock"}
		}, `does not take "refresh"`},
		{"probe_args", func(c *Contribution) { c.ProbeArgs = []string{"--version"} }, `does not take "probe_args"`},
		{"temp_caches", func(c *Contribution) { c.TempCaches = []string{"jiti"} }, `does not take "temp_caches"`},
		{"install_hints", func(c *Contribution) { c.InstallHints = map[string]string{"brew": "pi"} },
			`does not take "install_hints"`},
		{"protocols", func(c *Contribution) { c.Protocols = []string{"openai"} }, `does not take "protocols"`},
		{"provider_sets", func(c *Contribution) { c.ProviderSets = true }, `does not take "provider_sets"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := goodFork()
			tc.edit(&c)
			probs := validateContribution("contributes[0]", c)
			joined := strings.Join(probs, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tc.want, joined)
			}
		})
	}
}

// TestForkFieldsOffAForkAreRefused: fork_of without via source, and the fork fields on another
// via or another kind — each a declaration no consumer reads.
func TestForkFieldsOffAForkAreRefused(t *testing.T) {
	cases := []struct {
		name string
		c    Contribution
		want string
	}{
		{"fork_of on an npm program", Contribution{Kind: KindProgram, Bin: "pi", Via: "npm",
			Package: "pi", ForkOf: "pi"}, `only a "program" with via "source" takes "fork_of"`},
		{"source on an installer program", Contribution{Kind: KindProgram, Bin: "x", Via: "installer",
			URL: "https://x/i.sh", Source: "git+https://h/r?ref=main"}, `takes "source"`},
		{"produces on requires", Contribution{Kind: KindRequires, Bin: "x",
			Produces: []string{".local/bin/x"}}, `kind "requires" does not take "produces"`},
		{"build on env", Contribution{Kind: KindEnv, Vars: map[string]string{"A": "1"},
			Build: "make"}, `kind "env" does not take "build"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := strings.Join(validateContribution("contributes[0]", tc.c), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tc.want, joined)
			}
		})
	}
}

// TestAForksOwnContributionInstallsNothing: a fork's program reaches every reader through the base
// pack's rewritten program, so its own contribution projects to no Install and no host dep — two
// of either would be two programs for one bin.
func TestAForksOwnContributionInstallsNothing(t *testing.T) {
	m := &Manifest{Contributes: []Contribution{goodFork()}}
	if got := m.InstallContributions(); len(got) != 0 {
		t.Errorf("a fork's own contribution projected to installs %+v", got)
	}
	if got := m.DepRequirements(); len(got) != 0 {
		t.Errorf("a fork's own contribution projected to host deps %+v", got)
	}
}

// TestTheRewrittenBaseProgramProjectsTheForksDelivery: the shape the fork rewrite gives the base's
// program — via source, no fork_of, ForkedBy set — projects to Install.Kind "source" carrying the
// fork's recipe, and names no host remedy: a fork is never built on the host.
func TestTheRewrittenBaseProgramProjectsTheForksDelivery(t *testing.T) {
	base := goodFork()
	base.ForkOf, base.ForkedBy = "", "pi-matt"
	m := &Manifest{Contributes: []Contribution{base}}
	ins := m.InstallContributions()
	if len(ins) != 1 {
		t.Fatalf("got %d installs, want 1", len(ins))
	}
	in := ins[0]
	if in.Kind != InstallKindSource || in.Source != base.Source || in.Build != base.Build ||
		in.ForkedBy != "pi-matt" || strings.Join(in.Produces, ",") != strings.Join(base.Produces, ",") {
		t.Errorf("the projection lost the fork's delivery: %+v", in)
	}
	if got := in.ProgramPath(); got != ".npm-global/bin/pi" {
		t.Errorf("ProgramPath = %q, want the produces entry on PATH", got)
	}
	deps := m.DepRequirements()
	if len(deps) != 1 || deps[0].SelfInstall != "" || deps[0].SelfInstallVia != "" {
		t.Errorf("a source-built program must name no host install command: %+v", deps)
	}
}

// TestTolerantDecodeKeepsAFork: a build that knows the via keeps it on the in-jail path (the
// skip is for a via the build does not know), and the fork's own contribution survives decode so
// the in-jail loader can run the same rewrite the host ran.
func TestTolerantDecodeKeepsAFork(t *testing.T) {
	data := []byte(`{"contributes":[{"kind":"program","bin":"pi","via":"source","fork_of":"pi",
		"source":"git+https://github.com/you/pi-fork?ref=main","build":"make install",
		"produces":[".local/bin/pi"]}]}`)
	m, problems, skipped := DecodeTolerant(data)
	if len(problems) > 0 || len(skipped) > 0 {
		t.Fatalf("tolerant decode: problems %v, skipped %v", problems, skipped)
	}
	if len(m.Contributes) != 1 || !m.Contributes[0].IsFork() {
		t.Fatalf("the fork contribution did not survive: %+v", m.Contributes)
	}
	if _, strictProblems := Decode(data); len(strictProblems) > 0 {
		t.Errorf("strict decode refused a valid fork: %v", strictProblems)
	}
}
