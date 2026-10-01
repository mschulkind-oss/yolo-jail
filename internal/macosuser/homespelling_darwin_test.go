//go:build darwin

package macosuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homespelling_darwin_test.go is the half of homespelling_test.go only a Mac can say: that the
// facts macOSHomes asserts are true of the running machine, and that the check refuses every one
// of those spellings of a REAL home after the launch's own resolution (resolvePathAbs, which on
// darwin is the platform's filepath.EvalSymlinks) has had its turn at it. It reads the filesystem
// and writes nothing.

// The firmlink is a hard fact of every macOS since 10.15, so an alias that is not the users root
// on this machine is a layout that refuses the wrong directory and misses the right one.
func TestMacOSHomesAliasesAreTheUsersRootOnThisMac(t *testing.T) {
	root, err := os.Stat(macOSHomes.usersRoot)
	if err != nil {
		t.Fatalf("stat %s: %v", macOSHomes.usersRoot, err)
	}
	for _, alias := range macOSHomes.aliases {
		fi, err := os.Stat(alias)
		if err != nil {
			t.Errorf("stat %s: %v — the alias does not exist on this Mac", alias, err)
			continue
		}
		if !os.SameFile(root, fi) {
			t.Errorf("%s is not the same directory as %s on this Mac", alias, macOSHomes.usersRoot)
		}
	}
	// Folding is assumed rather than probed (macOSHomes says why), so a case-sensitive volume is
	// not a failure — it is the one-way error the assumption accepts. Say which this machine is.
	if upper, err := os.Stat(strings.ToUpper(macOSHomes.usersRoot)); err == nil && os.SameFile(root, upper) {
		t.Logf("this Mac's volume folds case: %s is %s", strings.ToUpper(macOSHomes.usersRoot), macOSHomes.usersRoot)
	} else {
		t.Logf("this Mac's volume does not fold case; the folded match over-refuses here, which is the safe direction")
	}
}

// THE END-TO-END HALF: every spelling that reaches this user's real home on this machine is
// refused by the precondition list the launch and `yolo check` both walk, which resolves the
// workspace itself. Fails if the facts are wrong, if resolvePathAbs turns out to canonicalize
// a spelling into one the check misses, or if the precondition stops asking.
func TestEverySpellingOfARealHomeIsRefusedOnThisMac(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(home) != macOSHomes.usersRoot || filepath.Base(home) == sharedHomeName {
		t.Skipf("HOME %s is not a home under %s, so there is no real home to spell", home, macOSHomes.usersRoot)
	}
	homeInfo, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(home)
	reached := 0
	for _, spelling := range []string{
		home,
		strings.ToUpper(macOSHomes.usersRoot) + "/" + name,
		strings.ToLower(macOSHomes.usersRoot) + "/" + name,
		"/System/Volumes/Data/Users/" + name,
		"/system/volumes/data/users/" + name,
	} {
		fi, err := os.Stat(spelling)
		if err != nil || !os.SameFile(homeInfo, fi) {
			t.Logf("%s does not reach %s on this Mac", spelling, home)
			continue
		}
		reached++
		for _, r := range CheckLaunchPreconditions(mockDeps(nil).launchProbes(), spelling) {
			if r.ID == PreconditionNeutralGround && (!r.Checked || r.Held) {
				t.Errorf("%s reaches %s on this Mac, and the location check (on %s) did not refuse it",
					spelling, home, r.Workspace)
			}
		}
	}
	// The home itself and its firmlink spelling reach it on every macOS since 10.15, so fewer
	// means the loop above tested nothing.
	if reached < 2 {
		t.Errorf("only %d spellings reached %s; want at least the home and its firmlink", reached, home)
	}
}
