package packdecl

// fork.go is the vocabulary of a FORK: a `program` delivered `via: "source"`, whose bytes come
// from a pinned source address and one build command instead of a registry or a vendor
// installer (docs/design/forked-programs-as-packs.md, OQ-FP3 and FP-D5).
//
// A fork is declared as a fork OF a base pack and claims no name of its own (§4.1, OQ-FP5): the
// base keeps the bin, and the selection rewrites a copy of the base's program with the fork's
// delivery (packload.ApplyForks). So this file's rules are about the manifest shape alone. Which
// fields a fork may carry is FP-D6's split, read one field at a time: the fields that say HOW THE
// BYTES ARRIVE are the fork's, and the fields that say WHAT THE PROGRAM DOES once it is there stay
// the base's, where a fork cannot restate them.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The mechanism's three names, spelled once (the plan's house style): the manifest's
// `via: "source"`, the Install.Kind the jail's installer switches on, and the receipt kind a
// build's store entry is recorded under (entrypoint.ReceiptKindBuild).
const (
	// ViaSource is the `via` a fork declares.
	ViaSource = "source"
	// InstallKindSource is Install.Kind for a program a fork's build delivers.
	InstallKindSource = "source"
)

// IsFork reports whether c is a fork's own program contribution: `kind: "program"` with a
// `fork_of`. Such a contribution installs nothing by itself (InstallContributions skips it) and
// claims no agent name; its delivery reaches the base pack's program through the selection's
// rewrite, which leaves `fork_of` off the copy it makes.
func (c Contribution) IsFork() bool {
	return c.Kind == KindProgram && c.ForkOf != ""
}

// forkFields are the four fields a fork adds, for the placement refusal.
func forkFields(c Contribution) []struct {
	name string
	set  bool
} {
	return []struct {
		name string
		set  bool
	}{
		{"fork_of", c.ForkOf != ""},
		{"source", c.Source != ""},
		{"build", c.Build != ""},
		{"produces", c.Produces != nil},
	}
}

// forkFieldPlacementProblems refuses the four fork fields anywhere but a `program` delivered
// `via: "source"`, in `update`'s position and for its reason: on any other kind, or beside any
// other via, no consumer reads them, so accepting them would be a declaration that silently
// does nothing. The reverse — `via: "source"` without them — is forkProblems' required-field
// check.
func forkFieldPlacementProblems(label string, c Contribution) []string {
	if c.Kind == KindProgram && c.Via == ViaSource {
		return nil
	}
	var problems []string
	for _, f := range forkFields(c) {
		if !f.set {
			continue
		}
		if c.Kind == KindProgram {
			problems = append(problems, fmt.Sprintf(
				"%s: only a \"program\" with via %q takes %q — it is part of a FORK's delivery "+
					"(a pinned source address built in a capture jail); this program is delivered "+
					"via %q, which reads none of it", label, ViaSource, f.name, c.Via))
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s: kind %q does not take %q — it is part of a FORK's delivery, which only a "+
				"\"program\" with via %q declares; no consumer reads it on this kind",
			label, c.Kind, f.name, ViaSource))
	}
	return problems
}

// forkBaseField is one program field a fork may not carry, with why it is the base's.
type forkBaseField struct {
	name, why string
	set       bool
}

// forkBaseFields are the program fields refused on a fork (FP-D6). Each is either another via's
// delivery field, which a source build has no use for, or a fact about what the program DOES,
// which the base pack declares and keeps: restating it on the fork would give one program two
// declarations of it, and the rewrite keeps the base's either way.
func forkBaseFields(c Contribution) []forkBaseField {
	const base = "it is the base pack's to declare — the fork supplies the program's bytes and " +
		"the base keeps everything else (FP-D6)"
	return []forkBaseField{
		{"package", "it names an npm package, and a fork's bytes come from its build", c.Package != ""},
		{"url", "it names a vendor installer, and a fork's bytes come from its build", c.URL != ""},
		{"flags", "they are npm install flags, and a fork is not installed by npm", c.Flags != nil},
		{"update", "a self-update verb would replace the pinned build with the vendor's release, and " +
			"a fork that drifts from its lock is reported, never silently refreshed (§12)", c.Update != nil},
		{"versions_dir", "it is where a vendor installer keeps its releases, which a build has none of",
			c.VersionsDir != ""},
		{"install_hints", "a host package manager's package is the UPSTREAM program, which a " +
			"`yolo host check-deps` remedy would then install in the fork's place", c.InstallHints != nil},
		{"model_catalog", "its entries name files inside an npm package's directory, and a fork " +
			"installs no npm package", c.ModelCatalog != nil},
		{"refresh", base, c.Refresh != nil},
		{"provider_sets", base, c.ProviderSets},
		{"capabilities", base, c.Capabilities != nil},
		{"protocols", base, c.Protocols != nil},
		{"platform_switches", base, c.PlatformSwitches != nil},
		{"platform_regions", base, c.PlatformRegions != nil},
		{"unlisted_background_models", base, c.UnlistedBackgroundModels},
	}
}

// forkProblems validates a `program` delivered `via: "source"`: the four fork fields required and
// well-formed, and every program field that is not the fork's refused by name. `platforms` (where
// the fork builds) and `node_floor` (the Node its entrypoint needs) are the two it may add.
func forkProblems(label string, c Contribution) []string {
	var problems []string
	for _, f := range forkBaseFields(c) {
		if f.set {
			problems = append(problems, fmt.Sprintf("%s: a fork (via %q) does not take %q — %s",
				label, ViaSource, f.name, f.why))
		}
	}
	switch {
	case c.ForkOf == "":
		problems = append(problems, label+": a fork (via \"source\") needs \"fork_of\" — the pack "+
			"whose program it builds; the base keeps the name, and the fork supplies the bytes")
	case !ValidPackName(c.ForkOf):
		problems = append(problems, fmt.Sprintf("%s.fork_of: %q is not a pack name — a bare name, "+
			"with no \"/\", \":\", \"=\" or \"..\"", label, c.ForkOf))
	}
	if c.Source == "" {
		problems = append(problems, label+": a fork (via \"source\") needs \"source\" — a git "+
			"address pinned by ref, e.g. git+https://github.com/you/fork?ref=main")
	} else if why := ForkSourceProblem(c.Source); why != "" {
		problems = append(problems, fmt.Sprintf("%s.source %q: %s", label, c.Source, why))
	}
	switch {
	case strings.TrimSpace(c.Build) == "":
		problems = append(problems, label+": a fork (via \"source\") needs \"build\" — one command "+
			"line, run by bash in the checked-out source with the capture jail's home as HOME")
	case strings.ContainsAny(c.Build, "\r\n\x00"):
		problems = append(problems, label+".build: must be ONE command line — a newline makes it "+
			"a script, which belongs in the fork's repository where the command can call it")
	}
	problems = append(problems, producesProblems(label, c.Bin, c.Produces)...)
	return problems
}

// ForkSourceProblem says why a fork's `source` cannot key a build, or "" when it can: it must
// parse as a pack source address (packsrc.Parse, so the grammar is the one `packs` entries use)
// AND be a git transport. A `file://` directory is refused because a directory has no revision,
// and the store keys a fork entry on the revision it was built at (§6) — `git+file://` names a
// local REPOSITORY, which does have one.
func ForkSourceProblem(source string) string {
	a, err := packsrc.Parse(source)
	if err != nil {
		return err.Error()
	}
	if a.Kind != packsrc.KindGit {
		return "a fork builds a REVISION, and a file:// directory has none to pin or key a build " +
			"on — name a git repository (git+https://…, git+ssh://…, or git+file:// for a local " +
			"one) with ?ref="
	}
	return ""
}

// producesProblems validates `produces`: at least one entry, each a clean home-relative path
// strictly inside one of the program surfaces a capture walks (paths.InstalledProgramSurfaces),
// no duplicates, and one of them the program itself at `<surface>/bin/<bin>` on a surface whose
// bin directory is on PATH (paths.HomeSurfaces).
func producesProblems(label, bin string, produces []string) []string {
	field := label + ".produces"
	if len(produces) == 0 {
		return []string{label + ": a fork (via \"source\") needs \"produces\" — the home-relative " +
			"paths its build must leave, one of them the program at " + forkProgramExample(bin)}
	}
	var problems []string
	seen := map[string]int{}
	for i, p := range produces {
		entry := fmt.Sprintf("%s[%d]", field, i)
		before := len(problems)
		problems = appendPathProblems(problems, entry, p)
		if len(problems) > before {
			continue
		}
		if p == "" || path.Clean(p) != p || p == "." {
			problems = append(problems, fmt.Sprintf("%s: %q must be a clean home-relative path "+
				"(no ./, //, or trailing /)", entry, p))
			continue
		}
		if first, dup := seen[p]; dup {
			problems = append(problems, fmt.Sprintf("%s: %q is already listed at [%d]", entry, p, first))
			continue
		}
		seen[p] = i
		if why := underProgramSurface(p); why != "" {
			problems = append(problems, fmt.Sprintf("%s: %q %s", entry, p, why))
		}
	}
	if len(problems) == 0 && ForkProgramPath(bin, produces) == "" {
		problems = append(problems, fmt.Sprintf("%s: none of the entries is the program itself — "+
			"one must be %s, a surface's bin directory on PATH, or the build leaves nothing a "+
			"launcher can run", field, forkProgramExample(bin)))
	}
	return problems
}

// underProgramSurface says why p is not strictly inside a program surface, or "".
//
// THE SURFACES ARE CAPTURE'S (paths.InstalledProgramSurfaces), because a build's result is what
// the capture driver moves out of them: a path anywhere else is not in the delta, so a build that
// left it would be refused as missing its outputs every time. A surface's ROOT is refused too —
// it exists before any build runs, so it is never in the delta either.
func underProgramSurface(p string) string {
	var roots []string
	for _, s := range paths.InstalledProgramSurfaces() {
		rel := filepath.ToSlash(s.HomeRel)
		roots = append(roots, rel)
		if p == rel {
			return "names a whole program surface, which exists before the build runs and is " +
				"never in its result — name what the build puts inside it"
		}
		if strings.HasPrefix(p, rel+"/") {
			return ""
		}
	}
	return "is not inside a program surface (" + strings.Join(roots, ", ") + ") — a build's " +
		"result is what it leaves there, and nothing outside them is captured"
}

// ForkProgramPath returns the entry of produces that is the program itself — `<surface>/bin/<bin>`
// on a surface whose bin directory is on the jail's PATH — or "" when none is. The source
// launcher execs it and the catalog accounts for it, so both read it from here.
func ForkProgramPath(bin string, produces []string) string {
	if bin == "" {
		return ""
	}
	for _, p := range produces {
		for _, s := range paths.HomeSurfaces() {
			if p == filepath.ToSlash(filepath.Join(s.HomeRel, "bin", bin)) {
				return p
			}
		}
	}
	return ""
}

// ProgramPath is ForkProgramPath for this Install: the home-relative path of the program a fork's
// build leaves, "" for any program that is not a fork's.
func (in Install) ProgramPath() string {
	if in.Kind != InstallKindSource {
		return ""
	}
	return ForkProgramPath(in.Bin, in.Produces)
}

// ForkRecipe is a fork build's RECIPE HASH (docs/design/forked-programs-as-packs.md §6, FP-D8): the
// sha256, in hex, of a canonical form of everything besides the revision that could change the
// bytes a build leaves — its command line, the paths it must leave, and the source subdirectory it
// runs in. Editing any of them is a different recipe, so an entry built from the old one is a miss.
//
// THE CANONICAL FORM is the JSON array [build, sorted produces, subdir]: JSON so no separator can
// be spelled inside a value, and produces sorted because their order changes nothing the build does.
// The toolchain is deliberately not in it (OQ-FP2): it is recorded on the receipt instead.
func ForkRecipe(build string, produces []string, subdir string) string {
	sorted := append([]string(nil), produces...)
	sort.Strings(sorted)
	canonical, _ := json.Marshal([]any{build, sorted, subdir})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// forkProgramExample renders the accepted program paths for a message.
func forkProgramExample(bin string) string {
	if bin == "" {
		bin = "<bin>"
	}
	var alts []string
	for _, s := range paths.HomeSurfaces() {
		alts = append(alts, filepath.ToSlash(filepath.Join(s.HomeRel, "bin", bin)))
	}
	return strings.Join(alts, " or ")
}
