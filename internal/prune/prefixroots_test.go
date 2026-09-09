package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"golang.org/x/sys/unix"
)

func mkPrefixRoot(t *testing.T, dir, storePath string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, image.ImageStoreKey(storePath))
	if err := os.Symlink(storePath, link); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	tv := []unix.Timeval{unix.NsecToTimeval(when.UnixNano()), unix.NsecToTimeval(when.UnixNano())}
	if err := unix.Lutimes(link, tv); err != nil {
		t.Fatal(err)
	}
	return link
}

// TestPrefixRootsAreHeldByLivenessNotAge is the ruling, stated as the case that
// separates this pass from its neighbour: a jail that has been running for a
// MONTH keeps its prefix root, where an image root of the same age is reaped.
// Getting this backwards costs a running process the file behind its own pid1.
func TestPrefixRootsAreHeldByLivenessNotAge(t *testing.T) {
	dir := t.TempDir()
	livePath := "/nix/store/aaaa-yolo-jail-install-prefix"
	deadPath := "/nix/store/bbbb-yolo-jail-install-prefix"
	liveLink := mkPrefixRoot(t, dir, livePath, 30*24*time.Hour)
	deadLink := mkPrefixRoot(t, dir, deadPath, 30*24*time.Hour)

	sources := map[string]bool{livePath + "/" + image.JailPrefixSubdir + "/bin": true}
	reaped := PruneOrphanPrefixRoots(dir, sources, true, true, time.Now())

	if len(reaped) != 1 || reaped[0] != deadLink {
		t.Fatalf("reaped %v, want exactly the root no jail is running from (%s)", reaped, deadLink)
	}
	if _, err := os.Lstat(liveLink); err != nil {
		t.Error("a running jail's prefix root was reaped — age must NOT apply here: the jail is " +
			"executing pid1 out of that store path, so losing it is not a rebuild (OQ-BF4). " +
			"OQ-LS1's age policy is for image closures only.")
	}
}

// TestPrefixRootsDeclineWhenLivenessUnknown: this pass HAS an authority, so it
// keeps the tri-state OQ-LS1 removed from the image roots. Unknown is not
// permission when the cost of being wrong is a dead jail.
func TestPrefixRootsDeclineWhenLivenessUnknown(t *testing.T) {
	dir := t.TempDir()
	link := mkPrefixRoot(t, dir, "/nix/store/cccc-yolo-jail-install-prefix", 30*24*time.Hour)
	if reaped := PruneOrphanPrefixRoots(dir, nil, false, true, time.Now()); len(reaped) != 0 {
		t.Fatalf("reaped %v with liveness unknown, want none", reaped)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a root was removed while liveness was unknown")
	}
}

// TestPrefixRootGraceCoversTheRegistrationWindow: the root is registered in
// resolveJailPrefix, an entire image build BEFORE any container exists, so a
// concurrent pass would otherwise reap a root that is about to become live.
func TestPrefixRootGraceCoversTheRegistrationWindow(t *testing.T) {
	dir := t.TempDir()
	link := mkPrefixRoot(t, dir, "/nix/store/dddd-yolo-jail-install-prefix", time.Minute)
	if reaped := PruneOrphanPrefixRoots(dir, map[string]bool{}, true, true, time.Now()); len(reaped) != 0 {
		t.Fatalf("reaped %v — a root registered a minute ago has no container yet by construction", reaped)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("the grace window must spare a just-registered root")
	}
}

// TestPrefixStorePathOfIgnoresABundleMount: a launch running from an installed
// bundle mounts bin/linux-<arch> from the bundle, not from a store path, so
// there is no root to correlate and it must not be mistaken for one.
func TestPrefixStorePathOfIgnoresABundleMount(t *testing.T) {
	store := "/nix/store/eeee-yolo-jail-install-prefix"
	if got := PrefixStorePathOf(store + "/" + image.JailPrefixSubdir + "/bin"); got != store {
		t.Errorf("built prefix: got %q, want %q", got, store)
	}
	for _, notAPrefix := range []string{
		"/home/me/.local/share/yolo-jail/flake-bundle/bin/linux-amd64",
		"/nix/store/ffff-something-else",
		"",
	} {
		if got := PrefixStorePathOf(notAPrefix); got != "" {
			t.Errorf("PrefixStorePathOf(%q) = %q, want \"\"", notAPrefix, got)
		}
	}
}

// TestPrefixBinMountDestMatchesTheLauncher pins the one string this package
// shares with the run pipeline without importing it. If the launcher ever mounts
// the prefix somewhere else, this pass silently finds no live sources and reaps
// every prefix root — including the one the jail reading this is running from.
func TestPrefixBinMountDestMatchesTheLauncher(t *testing.T) {
	src, err := os.ReadFile("../cli/run/jailprefix.go")
	if err != nil {
		t.Fatalf("read jailprefix.go: %v", err)
	}
	want := `JailPrefixBinDir = JailPrefixDir + "/bin"`
	if !strings.Contains(string(src), want) || prefixBinMountDest != "/opt/yolo-jail/bin" {
		t.Fatalf("the prefix bin mount destination moved: prune has %q and jailprefix.go no longer "+
			"spells %s. These are two copies of one path; a silent divergence makes every prefix "+
			"root look orphaned.", prefixBinMountDest, want)
	}
}
