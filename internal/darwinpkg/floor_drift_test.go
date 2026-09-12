package darwinpkg

// floor_drift_test.go keeps floor.go's three constants honest against flake.nix,
// which is where the floor is actually resolved and where its fatal lives.
//
// WITHOUT THIS GATE THE POLICY ASSERTION IS DECORATIVE. floor_policy_test.go
// asserts that nothing on the DERIVED floor is GNU userland, and it derives the
// floor from ImageCoreNames — a Go slice. Add `gnumake` to flake.nix's
// coreFloorNames and forget this file, and the policy test keeps passing about a
// list that no longer describes the jail. The two tests are one mechanism: this
// one says the Go list IS the flake's list, that one says the flake's list obeys
// OQ-P2.
//
// It parses nix source textually, which is crude and is the only option — `go
// test` cannot evaluate a flake, and requiring nix on the unit gate would make
// the guard skip exactly where CI is thinnest.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// readFlake returns flake.nix's source, or skips: an absent flake means a shipped
// bundle or a trimmed checkout, not a regression (same reasoning as
// flakeattr_test.go's cross-check).
func readFlake(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "flake.nix"))
	if err != nil {
		t.Skipf("cannot read flake.nix (%v) — skipping cross-check", err)
	}
	return string(data)
}

// nixStringList returns the double-quoted strings in the `<name> = [ … ];`
// binding, in source order. Comments are stripped first, so a package named in a
// comment inside the list cannot be mistaken for an entry.
func nixStringList(src, name string) ([]string, bool) {
	src = stripNixComments(src)
	open := strings.Index(src, name+" = [")
	if open < 0 {
		return nil, false
	}
	start := open + strings.Index(src[open:], "[")
	depth := 0
	end := -1
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, false
	}
	body := src[start+1 : end]
	var out []string
	for {
		i := strings.Index(body, `"`)
		if i < 0 {
			break
		}
		rest := body[i+1:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			break
		}
		out = append(out, rest[:j])
		body = rest[j+1:]
	}
	return out, true
}

// TestFloorConstantsMatchTheFlake is the drift gate proper. Each list is compared
// entry-for-entry AND in order: flake order is what makes a diff between the two
// files readable, and an out-of-order match would let a reordering hide a swap.
func TestFloorConstantsMatchTheFlake(t *testing.T) {
	flake := readFlake(t)

	for _, tc := range []struct {
		nixName string
		goName  string
		want    []string
		why     string
	}{
		{"coreFloorNames", "ImageCoreNames", ImageCoreNames,
			"the floor's INPUT — a name added to the image core that Go does not know " +
				"about is a name the policy assertion never examines"},
		{"noncontainerFloorUnbuildable", "FloorExcludedUnbuildable", FloorExcludedUnbuildable,
			"the NECESSITY half of the exclusion list"},
		{"noncontainerFloorPolicy", "FloorExcludedPolicy", FloorExcludedPolicy,
			"OQ-P2's POLICY half — the one the nix fatal cannot protect"},
	} {
		got, ok := nixStringList(flake, tc.nixName)
		if !ok {
			t.Errorf("flake.nix has no `%s = [ … ];` binding; %s reads it by name, so a "+
				"rename has to move this gate with it", tc.nixName, tc.goName)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s has %d entries, flake.nix's %s has %d — %s\n  go:  %v\n  nix: %v",
				tc.goName, len(tc.want), tc.nixName, len(got), tc.why, tc.want, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s[%d] = %q, flake.nix's %s[%d] = %q — %s",
					tc.goName, i, tc.want[i], tc.nixName, i, got[i], tc.why)
			}
		}
	}
}

// TestEveryExclusionExcludesSomething: an exclusion naming a package the image
// does not bake protects nothing and reads as if it did.
//
// It is the failure mode a list like this actually has. A package dropped from
// the image core leaves its exclusion behind, and the next reader takes the
// leftover as evidence that darwin was considered — when what it records is a
// decision about a package nobody ships.
func TestEveryExclusionExcludesSomething(t *testing.T) {
	core := map[string]struct{}{}
	for _, n := range ImageCoreNames {
		core[n] = struct{}{}
	}
	for _, group := range []struct {
		name  string
		names []string
	}{
		{"FloorExcludedUnbuildable", FloorExcludedUnbuildable},
		{"FloorExcludedPolicy", FloorExcludedPolicy},
	} {
		for _, n := range group.names {
			if _, ok := core[n]; !ok {
				t.Errorf("%s names %q, which the image core does not bake — the entry "+
					"excludes nothing, and leaving it in place claims a decision was made "+
					"about a package yolo does not ship", group.name, n)
			}
		}
	}
}

// TestFlakeBuildsTheNoncontainerProfileFromTheFloor pins the CALL SITE, not the
// list: the floor has to reach the profile a launch actually realizes.
//
// The lists above can be perfect while `yoloNoncontainerProfile`'s `paths` no
// longer mentions the floor at all, which is a jail with no floor and a green
// suite — the shape AGENTS.md names ("does it fail if I delete the call site?").
// Deleting `noncontainerFloorPackages ++` from that buildEnv fails here.
func TestFlakeBuildsTheNoncontainerProfileFromTheFloor(t *testing.T) {
	flake := stripNixComments(readFlake(t))

	i := strings.Index(flake, "packages."+FloorProfileAttr+" = ")
	if i < 0 {
		t.Fatalf("flake.nix has no `packages.%s = ` binding — Materialize realizes "+
			"`.#packages.<system>.%s` and nix would fail at run time with a missing "+
			"attribute", FloorProfileAttr, FloorProfileAttr)
	}
	body := flake[i:]
	if j := strings.Index(body, "};"); j >= 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "noncontainerFloorPackages") {
		t.Errorf("flake.nix's `packages.%s` no longer builds from "+
			"`noncontainerFloorPackages` — a notch with no image would launch with "+
			"whatever the user declared and nothing else, which is the state "+
			"docs/design/macos-user-provisioning.md exists to end:\n%s",
			FloorProfileAttr, body)
	}

	// And the OTHER profile must NOT carry it. The container path's store delivery
	// writes that closure into /run/yolo/packages/bin, which sits AHEAD of /bin on
	// PATH — so a floor there would silently reroute 27 names the image already
	// bakes through a boot-written farm (AGENTS.md, on the invariant that inverts
	// for every name leaving /bin).
	k := strings.Index(flake, "packages."+ProfileAttr+" = ")
	if k < 0 {
		t.Fatalf("flake.nix has no `packages.%s = ` binding", ProfileAttr)
	}
	other := flake[k:]
	if j := strings.Index(other, "};"); j >= 0 {
		other = other[:j]
	}
	if strings.Contains(other, "noncontainerFloorPackages") {
		t.Errorf("flake.nix's `packages.%s` now carries the floor. That attr is the "+
			"container path's store-delivery closure (YOLO_STORE_PACKAGES=1), and its "+
			"farm outranks /bin — the floor belongs in %s:\n%s",
			ProfileAttr, FloorProfileAttr, other)
	}
}
