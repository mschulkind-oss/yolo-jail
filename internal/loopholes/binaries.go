package loopholes

// binaries.go is where a manifest's `binaries` (internal/loopholedecl's binaries.go) meets THIS
// MACHINE: which build each reference needs here, where the cache keeps it, whether it is
// there, and what to say when it is not (docs/design/broker-as-a-pack.md BP-D1).
//
// Two answers, on two axes, because they have different fixes:
//
//   - NO BUILD for the platform a reference runs on is SupportedHere's answer, on the
//     platform axis (load.go): nothing is missing and nothing can be installed.
//   - A build that is declared and NOT IN THE CACHE is BinariesFetched's, a probe with one
//     fix. A launch never fetches (§3.1: "at `pack install`, never at launch"), so the fix is
//     `yolo pack install`, and the launch says so (BinaryInertNotes).

import (
	"runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// BinaryCacheDir is the pack-binary cache every record resolves against. A package var so a
// test can point it at a temp dir instead of the real home's.
var BinaryCacheDir = func() string { return paths.PackBinariesDir() }

// packInstallFix is the command every not-fetched message names.
const packInstallFix = "`yolo pack install` downloads and verifies it"

// JailBinaryPath is what `{jail_binary:<name>}` resolves to for a loophole: the container path
// its jail build is mounted at.
func JailBinaryPath(loophole, name string) string {
	return loopholedecl.JailBinaryPath(loophole, name)
}

// BinaryNeed is one build this machine needs to run a loophole, with where the cache keeps it.
type BinaryNeed struct {
	loopholedecl.BinaryNeed
	// Path is the cached build's host path (packbin.Path), "" when HasBuild is false.
	Path string
}

// Fetched reports whether the need's build is in the cache.
func (n BinaryNeed) Fetched() bool { return n.HasBuild && packbin.Present(n.Path) }

// BinaryNeeds is every build this machine needs to run the loophole, host references first.
func (l *Loophole) BinaryNeeds() []BinaryNeed {
	return l.binaryNeedsOn(runtime.GOOS, runtime.GOARCH)
}

func (l *Loophole) binaryNeedsOn(goos, goarch string) []BinaryNeed {
	return binaryNeedsOn(l.Binaries, l.BinaryRefs, goos, goarch)
}

func binaryNeedsOn(binaries []Binary, refs BinaryRefs, goos, goarch string) []BinaryNeed {
	decl := loopholedecl.BinaryNeedsFor(binaries, refs, goos, goarch)
	if len(decl) == 0 {
		return nil
	}
	dir := BinaryCacheDir()
	out := make([]BinaryNeed, 0, len(decl))
	for _, d := range decl {
		n := BinaryNeed{BinaryNeed: d}
		if d.HasBuild {
			n.Path = packbin.Path(dir, d.Build.SHA256, d.Binary)
		}
		out = append(out, n)
	}
	return out
}

// hostBinaryPaths answers resolve's `{binary:<name>}` substitution: the cached path of each host
// reference's build for this machine, and false for a name with no build here.
func hostBinaryPaths(binaries []Binary, refs BinaryRefs) func(string) (string, bool) {
	paths := map[string]string{}
	for _, n := range binaryNeedsOn(binaries, refs, runtime.GOOS, runtime.GOARCH) {
		if !n.Jail && n.HasBuild {
			paths[n.Binary] = n.Path
		}
	}
	return func(name string) (string, bool) {
		p, ok := paths[name]
		return p, ok
	}
}

// binaryUnsupportedReason is the platform-axis message for the first reference with no build
// for where it runs on a goos/goarch machine, or ("", false). It says, like
// PlatformsUnsupportedReason, that nothing is missing.
func (l *Loophole) binaryUnsupportedReason(goos, goarch string) (string, bool) {
	byName := map[string]Binary{}
	for _, b := range l.Binaries {
		byName[b.Name] = b
	}
	for _, n := range loopholedecl.BinaryNeedsFor(l.Binaries, l.BinaryRefs, goos, goarch) {
		if n.HasBuild {
			continue
		}
		return "unsupported on " + n.Platform + " — the binary " + n.Binary + ", which it runs " +
			n.Where() + ", has no build for " + n.Platform + " (it has builds for " +
			strings.Join(byName[n.Binary].BuildPlatforms(), ", ") + "). Nothing is missing on " +
			"this machine and nothing can be installed to fix it", true
	}
	return "", false
}

// mountedJailBinary is where an OUTER launch mounted a jail build: the container path
// `{jail_binary:<name>}` resolved to. Inside a jail it is the copy this jail already runs, which a
// nested launch binds on when its own cache lacks the build. A package var so a test can point it
// at a file; production reads JailBinaryPath.
var mountedJailBinary = JailBinaryPath

// jailBinarySource is the host file a launch binds for a jail need: the cached build, or, inside a
// jail whose own cache lacks it, the copy the outer launch mounted. "" when neither is there.
func (l *Loophole) jailBinarySource(n BinaryNeed) string {
	if n.Fetched() {
		return n.Path
	}
	if inJail() {
		if p := mountedJailBinary(l.Name, n.Binary); isFile(p) {
			return p
		}
	}
	return ""
}

// BinariesFetched reports whether every build the loophole runs on this machine is in the cache.
// A need with no build is SupportedHere's to report and is not counted here.
//
// INSIDE A JAIL, presence decides, as it does for bind mounts (inJailActive): a jail daemon's
// build counts when the outer launch mounted it at its container path, whether or not this
// jail's own cache holds a copy — a nested launch binds that copy on (jailBinarySource) — and a
// host reference is the host's business, not this jail's.
func (l *Loophole) BinariesFetched() bool {
	_, missing := l.UnfetchedBinaryReason()
	return !missing
}

// UnfetchedBinaryReason is BinariesFetched's message: the first build that is declared for where
// it runs and is not in the cache, and the command that fetches it. ("", false) when every build
// is there.
func (l *Loophole) UnfetchedBinaryReason() (string, bool) {
	for _, n := range l.BinaryNeeds() {
		if !n.HasBuild || n.Fetched() {
			continue
		}
		if inJail() && (!n.Jail || l.jailBinarySource(n) != "") {
			continue
		}
		return "waiting for its binary " + n.Binary + " (" + n.Platform + ", run " + n.Where() +
			"), which is not fetched yet — " + packInstallFix, true
	}
	return "", false
}

// hostBinariesUnready is why a HOST reference's build cannot run here, or ("", false): the
// doctor's gate, since doctor_cmd is a host field and runs from `yolo check` and `yolo
// loopholes status` whatever the loophole's state.
func (l *Loophole) hostBinariesUnready() (string, bool) {
	for _, n := range l.BinaryNeeds() {
		if n.Jail {
			continue
		}
		if !n.HasBuild {
			return "the binary " + n.Binary + " has no build for " + n.Platform +
				", so there is nothing here to run", true
		}
		if !n.Fetched() {
			return "the binary " + n.Binary + " (" + n.Platform + ") is not fetched yet — " +
				packInstallFix, true
		}
	}
	return "", false
}

// BinaryInertNotes returns one note per loophole whose declared builds are not fetched, on the
// binary axis: ENABLED and SUPPORTED here (a disabled one is inert for the user's reason, and an
// unsupported one is PlatformInertNotes' to report), and not superseded. Once per name, for
// PlatformInertNotes' reason.
func BinaryInertNotes(lps []*Loophole) []InertNote {
	var out []InertNote
	seen := map[string]bool{}
	for _, lp := range lps {
		if lp == nil || !lp.Enabled || seen[lp.Name] || lp.Superseded() || !lp.SupportedHere() {
			continue
		}
		reason, missing := lp.UnfetchedBinaryReason()
		if !missing {
			continue
		}
		seen[lp.Name] = true
		out = append(out, InertNote{Name: lp.Name, Axis: AxisBinary, Reason: reason})
	}
	return out
}
