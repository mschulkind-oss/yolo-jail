package entrypoint

// requires.go is the jail half of the `requires` kind: a pack declares a binary that must
// EXIST, and yolo asserts it at boot instead of installing it.
//
// Why it is not just `program` with the install left out: `program` means "yolo installs
// this and owns a launcher path for it", and a pack wanting a tool the IMAGE already bakes
// (fd, fzf) or the user already provides had to either lie — declaring an npm install for a
// baked binary — or say nothing at all, which is what the fzf example pack did, losing its
// host-notch install_hints in the process. Those are different claims and now have
// different kinds.
//
// So this generates NOTHING. No launcher, no shim, no file: a `requires` contribution
// cannot shadow anything, because it puts nothing on PATH. Its only jail-side effect is the
// report below.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AssertRequiredBins probes the jail's PATH for every `requires` binary the loaded packs
// declare and warns, BY NAME, about each one that is absent.
//
// A WARNING, not an A12 boot failure, and the split matters. A12 makes a generator failure
// fatal because a half-written config file hands the agent a broken home. This is the
// opposite situation: nothing is half-written — a pack asserted something about the image
// and the image disagrees. Escalating that to fatal would mean a pack declaring one tool
// the image does not bake stops the jail from STARTING, so the user could not launch the
// jail to fix the pack inside it. The pack's other contributions are still perfectly good.
//
// It is never silent, which is the actual requirement: the whole point of the kind over
// staying quiet (the fzf pack's old workaround) is that an absent dependency is REPORTED
// rather than discovered later as a tool that mysteriously does nothing.
func AssertRequiredBins(e *Env) {
	packs, err := LoadJailPacks(e)
	if err != nil {
		// The pack load failure is already fatal via load_packs in the boot path; nothing
		// to add here.
		return
	}
	path := BootPath(e)
	for _, p := range packs {
		for _, req := range p.Decl.RequiredBins() {
			if lookPathIn(path, req.Bin) != "" {
				continue
			}
			msg := "warning: pack " + p.Name + " requires " + req.Bin +
				", which is not on PATH in this jail (yolo does not install a `requires` " +
				"binary — add it to `packages` in yolo-jail.jsonc, or declare it as `program`)"
			if len(req.Hints) > 0 {
				msg += " [host hints: " + strings.Join(sortedHintKeys(req.Hints), ", ") + "]"
			}
			e.warn(msg)
		}
	}
}

// lookPathIn resolves bin against an explicit colon-separated PATH, returning the first
// executable match or "".
//
// It takes the PATH explicitly rather than using exec.LookPath because at the point this
// runs the PROCESS's PATH is still the container's default — execBash sets BootPath only at
// the very end of the boot. Probing os.Getenv("PATH") here would answer a question nobody
// asked: whether the tool is visible to the entrypoint, not whether it will be visible to
// the agent.
func lookPathIn(path, bin string) string {
	if bin == "" {
		return ""
	}
	// A name with a separator is a path, not a PATH lookup — the manifest path guards
	// already reject those, so this is belt-and-braces.
	if strings.Contains(bin, "/") {
		if isExecutableFile(bin) {
			return bin
		}
		return ""
	}
	for _, dir := range strings.Split(path, ":") {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, bin)
		if isExecutableFile(cand) {
			return cand
		}
	}
	return ""
}

// isExecutableFile reports whether p is a regular file (or a symlink to one) with any
// execute bit set.
func isExecutableFile(p string) bool {
	fi, err := os.Stat(p) // Stat, not Lstat: /bin/<tool> is a symlink into the nix store
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// sortedHintKeys returns an install_hints map's keys sorted, so the warning is
// deterministic (Go's map order is not, and this line ends up in boot logs).
func sortedHintKeys(hints map[string]string) []string {
	out := make([]string, 0, len(hints))
	for k := range hints {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
