package entrypoint

// orphanremove.go is OQ-PD4's REMOVAL half — the clause the catalog left unbuilt:
// "removal happens only on an explicit act; autoprune exists as an option, default off"
// (docs/design/program-delivery.md §10 step four).
//
// THE CANDIDATE SET IS THE BYTES, NEVER A RECORD, and that is the one design decision this
// file exists to make. The system already had a removal loop keyed on a record — the LSP
// bootstrap's `~/.yolo-installed-lsps` sentinel, which uninstalls exactly the entries it
// wrote — and §10 step four measured what that costs: pyright, typescript and
// typescript-language-server were installed in this jail from a since-unconfigured
// `lsp_servers` with their sentinel record LOST, so the loop that exists to remove them
// could not see them and never will. MEASURED again 2026-09-04 from inside this jail: the
// sentinel is one byte (a newline), those three packages are still under the npm prefix, and
// $GOPATH/bin still holds gopls and mcp-language-server from the same vanished declaration.
// An act whose input is InstalledOrphans — everything installed, minus everything declared —
// removes all five without consulting any record at all. A record-less orphan is therefore
// not a special case here; it is the ordinary case, and a record would only ever have been
// able to hide one.
//
// IT IS TWO FUNCTIONS BECAUSE THE PLAN IS PART OF THE CONTRACT. PlanOrphanRemovals resolves
// every path the act would unlink and measures the bytes; ApplyOrphanRemovals unlinks
// exactly the paths in the plan and nothing else. A caller that prints the plan and stops is
// the dry run, and it is the DEFAULT on both surfaces (`yolo programs remove` without
// --apply, and a boot with the option off). Nothing here decides on its own to delete
// something the plan did not name.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// orphanAutopruneEnv turns the boot's catalog into a boot that also REMOVES. It is emitted
// by the host launcher only when the user's own config asks for it (config.ProgramsAutoprune),
// so an absent variable — an older launcher, a backend that emits no env, a config that
// never mentions it — means OFF, which is the ruled default and also the safe reading of
// every one of those cases.
const orphanAutopruneEnv = "YOLO_PROGRAMS_AUTOPRUNE"

// autoprunePrefix heads the act's boot lines. It is deliberately NOT catalogPrefix: a line
// that says bytes were DELETED must not be skimmable as one of the informational lines
// above it.
const autoprunePrefix = "boot autoprune: "

// OrphanRemoval is one planned removal: the orphan, every path unlinking it takes, and the
// bytes that reclaims. Err is filled by ApplyOrphanRemovals and is nil in a plan.
type OrphanRemoval struct {
	// Orphan is what this removal is about.
	Orphan Orphan
	// Paths are the absolute paths to unlink, in order. More than one only for an npm
	// package: its directory, the global-bin symlinks that point into it, and the scope
	// directory it leaves empty behind it.
	Paths []string
	// Bytes is the disk this reclaims, measured before the act — a recursive sum for a
	// directory, st_size for a file, zero for a symlink. Best effort: an unreadable
	// subtree contributes what could be read rather than failing the plan, because a
	// number that is low by one unreadable directory is still the number a reader needs
	// to decide, and refusing to plan a removal because its size is uncertain would make
	// the act unusable on exactly the trees worth removing.
	Bytes int64
	// Err is the failure that stopped this removal, or nil. A failure is per-removal: one
	// unwritable orphan must not abandon the others.
	Err error
}

// PlanOrphanRemovals resolves what removing each orphan would unlink, and how much that
// reclaims. It reads the filesystem and changes nothing.
//
// THE PLAN IS THE ANNOUNCEMENT. A removal act must be able to say what it will remove before
// it removes it, and "the orphan's path" is not that sentence for the npm class: uninstalling
// pyright leaves `$NPM_CONFIG_PREFIX/bin/pyright` and `…/bin/pyright-langserver` dangling at
// the FRONT of the jail's PATH (BootPath puts the npm prefix second), where a broken symlink
// is a command that fails with a confusing error rather than one that is absent. So the
// symlinks are part of the plan, named in it, and removed by it.
func PlanOrphanRemovals(e *Env, orphans []Orphan) []OrphanRemoval {
	out := make([]OrphanRemoval, 0, len(orphans))
	for _, o := range orphans {
		r := OrphanRemoval{Orphan: o, Paths: []string{o.Path}, Bytes: pathBytes(o.Path)}
		if o.Class == OrphanNpm {
			r.Paths = append(r.Paths, npmBinLinksInto(e, o.Path)...)
			if scope := emptiedScopeDir(o.Path); scope != "" {
				r.Paths = append(r.Paths, scope)
			}
		}
		out = append(out, r)
	}
	return out
}

// ApplyOrphanRemovals unlinks every path in the plan, in plan order, and returns the same
// removals with Err filled in.
//
// os.RemoveAll, and it is the right verb for all three classes: an npm package is a
// directory, a native installer's program is usually a file but `mcp-wrappers` proves a
// directory lands there too, and an already-absent path is not an error (RemoveAll's
// contract), which is what makes the act idempotent when two callers race or when a plan is
// applied twice.
//
// A FAILURE IS PER-REMOVAL, and the loop does not stop. The one thing that reliably makes
// this fail is a read-only mount, and on the boot path that is a whole CLASS of orphan (the
// `:ro` `/home/agent` base home) rather than a one-off: abandoning the remaining removals
// there would make the option's behaviour depend on the alphabetical position of the first
// unwritable entry.
func ApplyOrphanRemovals(plan []OrphanRemoval) []OrphanRemoval {
	for i := range plan {
		for _, p := range plan[i].Paths {
			if err := os.RemoveAll(p); err != nil {
				plan[i].Err = err
				break
			}
		}
	}
	return plan
}

// autopruneOrphans is the boot's caller for the act, and it is OFF unless the option says
// otherwise.
//
// IT ANNOUNCES BEFORE IT ACTS, on the terminal (e.warn) rather than the boot log: the
// option's whole risk is that it deletes something the user still wanted, and a record of
// that in a file nobody opens is not a record anyone can act on. The lines name the path and
// the bytes, so a user who turned this on and regrets it can see exactly what went.
//
// It is deliberately the SAME two functions the explicit act calls. An autoprune that walked
// its own candidate set would be a second answer to "what is an orphan" — the one question
// where the two surfaces disagreeing means the boot deletes something `yolo programs remove`
// would have spared.
func autopruneOrphans(e *Env, orphans []Orphan) {
	if !autopruneEnabled(e) || len(orphans) == 0 {
		return
	}
	plan := PlanOrphanRemovals(e, orphans)
	e.warn(autoprunePrefix + "programs.autoprune is ON — removing " +
		countWord(len(plan), "orphan") + " (" + renderSize(planBytes(plan)) + ")")
	for _, r := range plan {
		e.warn(autoprunePrefix + "removing " + r.Orphan.Display + " (" +
			renderSize(r.Bytes) + "): " + strings.Join(r.Paths, " "))
	}
	for _, r := range ApplyOrphanRemovals(plan) {
		if r.Err != nil {
			e.warn(autoprunePrefix + "could not remove " + r.Orphan.Display + ": " +
				r.Err.Error())
		}
	}
}

// autopruneEnabled reads the option.
//
// The vocabulary is wider than the "1" the launcher emits because this variable is also the
// only way to turn the act on by hand (a nested launch, a test, a user debugging), and a
// knob that silently ignores `true` is a knob that reads as broken. Everything else —
// absent, empty, "0", "false", a typo — is OFF: the default is the safe one in every case
// this cannot interpret.
func autopruneEnabled(e *Env) bool {
	switch strings.ToLower(strings.TrimSpace(e.Getenv(orphanAutopruneEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// npmBinLinksInto returns every entry of $NPM_CONFIG_PREFIX/bin that is a SYMLINK resolving
// inside pkgDir.
//
// SYMLINKS ONLY, and the restriction is the safety property: npm links a package's bins into
// this directory, so a symlink pointing into the package is unambiguously that package's,
// while a regular file there was put there by something else and is nobody's to delete. The
// target is resolved against the bin directory rather than the process's cwd (npm writes
// them relative, `../lib/node_modules/…`), and an absolute target is used as-is.
func npmBinLinksInto(e *Env, pkgDir string) []string {
	binDir := filepath.Join(e.NpmPrefix, "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return nil
	}
	prefix := pkgDir + string(filepath.Separator)
	var out []string
	for _, ent := range entries {
		if ent.Type()&fs.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(binDir, ent.Name())
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(binDir, target)
		}
		if target == pkgDir || strings.HasPrefix(filepath.Clean(target), prefix) {
			out = append(out, link)
		}
	}
	return out
}

// emptiedScopeDir returns the `@scope` directory that removing pkgDir would leave empty, or
// "" when there is none.
//
// A scope is a DIRECTORY, not a package (installedNpmPackages' rule, from the other side),
// so removing `@github/copilot` leaves `@github` behind holding nothing. The catalog cannot
// see it — a scope with no children yields no entries — so nothing would ever report it and
// nothing would ever remove it. It is named in the plan rather than swept afterwards,
// because the act unlinks what the plan says and nothing else.
func emptiedScopeDir(pkgDir string) string {
	scope := filepath.Dir(pkgDir)
	if !strings.HasPrefix(filepath.Base(scope), "@") {
		return ""
	}
	entries, err := os.ReadDir(scope)
	if err != nil {
		return ""
	}
	for _, ent := range entries {
		if filepath.Join(scope, ent.Name()) != pkgDir {
			return ""
		}
	}
	return scope
}

// pathBytes sums the regular-file bytes under path: st_size for a file, a recursive walk for
// a directory, zero for a symlink or anything else.
//
// SYMLINKS ARE NOT FOLLOWED, in the walk or at the root. A global-bin symlink into a package
// would otherwise count that package's bytes a second time, and a link out of the home would
// count a file this act is not going to remove.
func pathBytes(path string) int64 {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if fi.Mode().IsRegular() {
		return fi.Size()
	}
	if !fi.IsDir() {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree contributes nothing and does not abort the walk:
			// see OrphanRemoval.Bytes for why a low number beats no number.
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// planBytes totals a plan.
func planBytes(plan []OrphanRemoval) int64 {
	var total int64
	for _, r := range plan {
		total += r.Bytes
	}
	return total
}

// countWord renders "1 orphan" / "3 orphans" so the announcement reads as a sentence.
func countWord(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(int64(n)) + " " + word + "s"
}
