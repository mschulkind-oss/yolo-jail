package config

import "testing"

// V1: the reserved-destination guard and the STAGING it provisions must agree about
// `.config/yolo-home/`.
//
// A home-root destination (`~/.npmrc`) cannot be written into the `:ro` home base, so
// the CLI stages a symlink there pointing at `SymlinkTarget()` — a private subtree of
// the writable `~/.config` overlay, keyed by the entry's slug. That subtree is yolo
// infrastructure the user never named, so a SECOND entry declaring a path inside it is
// two entries writing one file: the alias spelling (`~/.npmrc`) and the target spelling
// (`~/.config/yolo-home/.npmrc`) are the same inode, exactly as `~/.gitconfig` and
// `~/.config/git/config` are.
//
// SymlinkTarget's own doc claimed the subtree "can never collide"; it could not collide
// with a REAL ~/.config entry a tool owns, which is what that sentence was about, but it
// could collide with another host_files entry — the one writer the reservation exists to
// exclude.
func TestHostFileReservedDestsCoverStagingTargets(t *testing.T) {
	reserved := hostFileReservedDests()
	// The exact target a `~/.npmrc` entry stages: the alias/target pair for the SAME file.
	alias := HostFileEntry{Path: ".npmrc"}
	if alias.StagingFor() != HostFileStagingSymlink {
		t.Fatalf("fixture: ~/.npmrc must be a symlink-staged destination, got %v", alias.StagingFor())
	}
	target := alias.SymlinkTarget()
	if _, why := checkHostFileDest(target, reserved); why == "" {
		t.Errorf("checkHostFileDest(%q) accepted the staging target of ~/.npmrc — "+
			"two entries would compose the same file, and the second one silently wins", target)
	}
	// Not only the same-slug spelling: the WHOLE subtree is yolo's, so an arbitrary path
	// inside it is refused too. Reserving only the computed targets would leave a user
	// free to plant a file among yolo's staged ones.
	for _, dest := range []string{
		".config/yolo-home/anything.json",
		".config/yolo-home/nested/deep.toml",
		".config/yolo-home", // the dir itself
	} {
		if _, why := checkHostFileDest(dest, reserved); why == "" {
			t.Errorf("checkHostFileDest(%q) accepted a path in yolo's own staging subtree", dest)
		}
	}
	// And the guard must stay NARROW: `.config/yolo-homework/x` merely shares a prefix
	// with the reserved segment and is an ordinary user destination.
	for _, dest := range []string{
		".config/yolo-homework/x.json",
		".config/yolo-home-ish.json",
		".config/mytool/config.json",
	} {
		if _, why := checkHostFileDest(dest, reserved); why != "" {
			t.Errorf("checkHostFileDest(%q) rejected an ordinary destination: %s", dest, why)
		}
	}
}

// The guard is LEXICAL on purpose, and this pins that rather than leaving it as an
// accident of implementation.
//
// A reserved-destination check must not resolve its argument through the filesystem.
// filepath.EvalSymlinks errors on a path that does not exist — which is the NORMAL state
// of a host_files destination, since the whole point is to bring the file into being — so
// a resolving guard would fall back to the lexical answer for every new destination and
// only differ for one that already exists. That difference is the wrong way round: an
// existing `~/.gitconfig` symlink resolves AWAY from the reserved name into whatever it
// points at, so resolution would turn a rejected spelling into an accepted one. A guard
// that passes because the path is not there yet is worse than one that rejects a spelling.
//
// So both spellings of a reserved file are listed as literals (reservedHomeFiles' A2
// entries) and matched with path.Clean. This test asserts the property that makes that
// safe: the verdict is a pure function of the string, identical whether or not the path
// exists on the machine running the check.
func TestHostFileDestGuardIsPurelyLexical(t *testing.T) {
	reserved := hostFileReservedDests()
	// Materialize the alias as a real symlink pointing somewhere unreserved, in a temp
	// home. If the guard resolved, it would follow this and accept the destination.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	for _, dest := range []string{".gitconfig", ".config/git/config", ".claude/claude.json"} {
		if _, why := checkHostFileDest(dest, reserved); why == "" {
			t.Errorf("checkHostFileDest(%q) must reject a reserved destination whether or not "+
				"it exists on disk", dest)
		}
	}
}
