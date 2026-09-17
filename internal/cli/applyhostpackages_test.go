package cli

// applyhostpackages_test.go pins the one thing docs/design/provisioner-sets.md §9 step 3 asks
// for: `yolo host apply` and `yolo describe` say the SAME thing about `packages:`.
//
// The defect was a disagreement between two shipped commands (OQ-NX8). `packages` is a config
// KEY and not a pack KIND, so the host FieldSet census never saw it and the apply printed
// nothing about it at all, while describe reported the resolved profile. So the assertion here
// is not a wording — a wording is what drifts — but an EQUALITY between the two commands' own
// output, taken from one config in one home. A second renderer that happened to agree today
// would pass; a second renderer that stopped agreeing would fail, which is the whole point.
//
// Every test uses a t.TempDir() home and a t.TempDir() nix profile. Nothing is materialized:
// the profile is a symlink these tests write, because both commands read the GC ROOT rather
// than asking nix (printPackageProfile's own contract — a read-only report must stay instant).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
)

// hostPackagesFixture points a throwaway $HOME at `cfg` and returns the home. `packages`
// entries are the config's; the caller writes the whole config so each test can vary the
// `packs` half independently — the two keys are independent, which is itself one of the
// things under test.
func hostPackagesFixture(t *testing.T, cfg string) string {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), cfg)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// resolveProfileRoot writes the GC-root symlink both commands read, and returns its target.
// A real materialization is a nix build; the root is what a materialization LEAVES, and it is
// the only thing either command looks at.
func resolveProfileRoot(t *testing.T, home string) string {
	t.Helper()
	target := t.TempDir()
	link := darwinpkg.ProfileRootLink(home)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return target
}

// packagesBlock extracts what a report says about `packages:` — the `packages` line plus the
// indented continuation under it, which is where the store path and the GC root are named.
//
// It keys on `packages ` with the space, because `describe` also prints a `packs` line and a
// prefix match on `pack` would silently fold two different keys into one answer.
func packagesBlock(report string) []string {
	var block []string
	for _, line := range strings.Split(report, "\n") {
		switch {
		case strings.HasPrefix(line, "packages "):
			block = append(block, line)
		case len(block) > 0 && strings.HasPrefix(line, confinementLabelPad):
			block = append(block, line)
		case len(block) > 0:
			return block
		}
	}
	return block
}

// describePackagesBlock and applyPackagesBlock run the two commands over whatever config the
// fixture wrote and return what each said about `packages:`. Both in the posture that writes
// nothing: describe never writes, and the apply's default posture is the dry run.
func describePackagesBlock(t *testing.T) []string {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := describeMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("describe rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	return packagesBlock(out.String())
}

func applyPackagesBlock(t *testing.T) []string {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, false, nil); rc != 0 {
		t.Fatalf("host apply (dry run) rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	return packagesBlock(out.String())
}

// THE GOAL, end to end: over one non-trivial config — the host notch, a pack selected, and a
// `packages` list carrying a platform-filtered entry — the two commands' `packages:` reports
// are byte-identical.
//
// The platform-filtered entry is what makes the config non-trivial rather than long: it is
// dropped from the count on every platform but darwin, so a second renderer reading the raw
// `packages` list instead of config.EffectivePackages would agree on the store path and
// disagree on the number — the exact half-agreement an equality assertion is for.
func TestHostApplyAndDescribeAgreeAboutPackages(t *testing.T) {
	home := hostPackagesFixture(t, `{"confinement":"host","packs":["claude"],
	  "packages":["ripgrep",{"name":"mesa","platforms":["darwin"]},"fd"]}`)
	target := resolveProfileRoot(t, home)

	want := describePackagesBlock(t)
	if len(want) == 0 {
		t.Fatal("describe said nothing about `packages:` — the fixture no longer reaches the " +
			"report this test is about")
	}
	// The resolved store path, as evidence that the block compared below is the RESOLVED
	// report and not the declared-but-unresolved one. Without it a fixture whose GC root
	// stopped resolving would still compare two equal strings and prove nothing.
	if !strings.Contains(strings.Join(want, "\n"), target) {
		t.Fatalf("describe did not name the resolved profile %s:\n%s", target,
			strings.Join(want, "\n"))
	}

	got := applyPackagesBlock(t)
	if len(got) == 0 {
		t.Fatalf("`yolo host apply` said nothing about `packages:` while describe said:\n%s\n"+
			"That is the disagreement provisioner-sets.md §9 step 3 closes.",
			strings.Join(want, "\n"))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the two commands disagree about `packages:`\n describe: %q\napply:    %q",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// The same equality with NO PACKS CONFIGURED, which is a second call site and not a second
// case of the first: the apply returns early from the zero-packs branch, so a report wired
// only into the main path is silent for exactly the config where `packages:` is the only thing
// left to say anything about.
func TestHostApplyReportsPackagesWithNoPacksConfigured(t *testing.T) {
	home := hostPackagesFixture(t, `{"confinement":"host","packages":["ripgrep"]}`)
	resolveProfileRoot(t, home)

	want := describePackagesBlock(t)
	if len(want) == 0 {
		t.Fatal("describe said nothing about `packages:` for a config that declares one")
	}
	got := applyPackagesBlock(t)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("with no packs configured the two commands disagree about `packages:`\n"+
			"describe: %q\napply:    %q", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// A config declaring NO packages gets no `packages:` line from either command — the gate is
// shared too, so the agreement is not bought by making the apply unconditionally noisy. This
// is the case every existing host-apply test is in, which is why none of them had to change.
func TestHostApplySaysNothingAboutPackagesWhenNoneAreDeclared(t *testing.T) {
	hostPackagesFixture(t, `{"confinement":"host","packs":["claude"]}`)
	if block := describePackagesBlock(t); len(block) != 0 {
		t.Fatalf("describe reported `packages:` for a config that declares none: %q", block)
	}
	if block := applyPackagesBlock(t); len(block) != 0 {
		t.Fatalf("host apply reported `packages:` for a config that declares none: %q", block)
	}
}
