package packdecl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ViaPack is the `via` a program built from its pack's OWN tree declares (the design
// docs/design/pack-buildable-programs.md, BG-D1): the pack ships a directory of source, an
// optional one-line build runs in it, and the program is the file `run` names afterwards. It is
// not a relaxed `via: "source"`, which keeps its fork meaning: a fork's bytes come from a pinned
// address in another repository, and a pack-tree program's bytes are the pack's own.
//
// Its delivery reaches the launch through the source launcher, so it projects to the
// InstallKindSource install kind (contributes.go, knownVias). Nothing here builds, fetches or
// installs anything: this file is the vocabulary and its refusals.
const ViaPack = "pack"

// PrebuiltBuild is one platform's download of a pack-tree program, the value of its `prebuilt`
// map keyed by `<goos>/<goarch>`. It follows `binaries`' rules exactly (loopholedecl's
// DownloadURLRefusal and IsSHA256Digest): an https URL with no credentials, and a sha256 the
// download is verified against before anything runs.
//
// `prebuilt` is not `fallback`: that key is taken by an unmodified extension's list entry
// (Contribution.Fallback), and an object under it would not decode.
type PrebuiltBuild struct {
	// URL is where the file is fetched from: https, no credentials in it.
	URL string `json:"url"`
	// SHA256 is the file's digest, 64 lowercase hex digits. It is the pin.
	SHA256 string `json:"sha256"`
}

// packTreeBaseField is one delivery field of another via, which a pack-tree program does not take.
type packTreeBaseField struct {
	name, why string
	set       bool
}

// packTreeBaseFields are the delivery fields of the other vias, which a pack-tree program does not
// take: each names a mechanism the pack-tree program does not use. The fork fields are refused by
// forkFieldPlacementProblems, which runs beside this for every program.
func packTreeBaseFields(c Contribution) []packTreeBaseField {
	return []packTreeBaseField{
		{"package", "it names an npm package, and a pack-tree program's bytes come from its own `from` tree", c.Package != ""},
		{"url", "it names a vendor installer, and a pack-tree program's bytes come from its own `from` tree", c.URL != ""},
		{"flags", "they are npm install flags, and a pack-tree program is not installed by npm", c.Flags != nil},
		{"update", "a self-update verb would replace the pack's program with a vendor's release, which the pack does not pin", c.Update != nil},
		{"versions_dir", "it is where a vendor installer keeps its releases, which a pack-tree program has none of", c.VersionsDir != ""},
		{"installer_env", "it is the environment of a vendor installer, and a pack-tree program has none", c.InstallerEnv != nil},
	}
}

// packTreeProblems validates a `program` delivered `via: "pack"`: `from` and `run` required and
// well-formed, `build` one command line when present, `prebuilt` well-formed when present, and
// every delivery field of another via refused by name. `platforms` and `node_floor` are allowed,
// as on any program, and so is every behavior field: the pack owns this program.
func packTreeProblems(label string, c Contribution) []string {
	var problems []string
	for _, f := range packTreeBaseFields(c) {
		if f.set {
			problems = append(problems, fmt.Sprintf("%s: a program delivered via %q does not take %q — %s",
				label, ViaPack, f.name, f.why))
		}
	}
	if c.From == "" {
		problems = append(problems, label+": a program delivered via \"pack\" needs \"from\" — the "+
			"pack-relative directory holding its source, such as \"tools/board-cli\" (\".\" is the pack root)")
	} else {
		problems = appendPathProblems(problems, label+".from", c.From)
		problems = appendCleanPathProblem(problems, label+".from", c.From)
	}
	if c.Run == "" {
		problems = append(problems, label+": a program delivered via \"pack\" needs \"run\" — the file "+
			"the program is, relative to \"from\", such as \"dist/board.js\"")
	} else {
		problems = appendPathProblems(problems, label+".run", c.Run)
		problems = appendCleanPathProblem(problems, label+".run", c.Run)
	}
	if strings.ContainsAny(c.Build, "\r\n\x00") {
		problems = append(problems, label+".build: must be one command line — a newline makes it a "+
			"script, which belongs in the pack's source tree where the command can call it")
	}
	if c.Prebuilt != nil {
		problems = append(problems, prebuiltProblems(label, c.Prebuilt)...)
	}
	return problems
}

// appendCleanPathProblem refuses a relative path that path.Clean would change ("a//b", "a/",
// "./a"), so the manifest says the path it means. "." is the pack root and is clean. Absolute and
// ".." paths are refused by appendPathProblems, so they are passed over here.
func appendCleanPathProblem(problems []string, field, p string) []string {
	if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return problems
	}
	if clean := path.Clean(p); clean != p {
		return append(problems, fmt.Sprintf("%s: %q is not a cleaned relative path — write %q", field, p, clean))
	}
	return problems
}

// prebuiltProblems validates a program's `prebuilt` map: at least one platform, every key a
// <goos>/<goarch> pair Go spells, and every entry an https URL with no credentials and a sha256.
func prebuiltProblems(label string, prebuilt map[string]PrebuiltBuild) []string {
	if len(prebuilt) == 0 {
		return []string{label + ": \"prebuilt\" declares no platform — name at least one, e.g. " +
			"\"linux/amd64\", or drop \"prebuilt\" and build from \"from\""}
	}
	platforms := make([]string, 0, len(prebuilt))
	for platform := range prebuilt {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	var problems []string
	for _, platform := range platforms {
		field := fmt.Sprintf("%s.prebuilt[%q]", label, platform)
		goos, goarch, hasArch := strings.Cut(platform, "/")
		if !hasArch || goarch == "" || strings.Contains(goarch, "/") ||
			!loopholedecl.IsKnownGOOS(goos) || !loopholedecl.IsKnownGOARCH(goarch) {
			problems = append(problems, fmt.Sprintf("%s: the key must be a goos/goarch pair as Go "+
				"spells it, e.g. \"linux/amd64\"", field))
			continue
		}
		b := prebuilt[platform]
		switch loopholedecl.DownloadURLRefusal(b.URL) {
		case loopholedecl.URLNotAbsolute:
			problems = append(problems, fmt.Sprintf("%s.url %q is not an absolute URL", field, b.URL))
		case loopholedecl.URLNotHTTPS:
			problems = append(problems, fmt.Sprintf("%s.url %q must be https — the sha256 pins the bytes, "+
				"and https keeps what was asked for private on the way", field, b.URL))
		case loopholedecl.URLCredentials:
			problems = append(problems, fmt.Sprintf("%s.url carries credentials — every footprint of this "+
				"pack prints the URL, so a credential in it is published; serve the file without one", field))
		}
		switch {
		case b.SHA256 == "":
			problems = append(problems, fmt.Sprintf("%s.sha256 is required — a download without a digest "+
				"leaves the program unpinned (a release asset can be re-uploaded under the same URL); "+
				"write the file's sha256", field))
		case !loopholedecl.IsSHA256Digest(b.SHA256):
			problems = append(problems, fmt.Sprintf("%s.sha256 %q is not a sha256 digest — 64 lowercase "+
				"hex digits, as `sha256sum` prints it", field, b.SHA256))
		}
	}
	return problems
}

// packTreeFieldPlacementProblems refuses `from`, `run` and `prebuilt` where no via reads them: `from`
// on a program delivered any other way, and `run` and `prebuilt` on any other program or kind.
// `from` is a content kind's field too (skills, files, briefing), so it is refused on a program
// only; the content kinds keep their own rules.
func packTreeFieldPlacementProblems(label string, c Contribution) []string {
	var problems []string
	if c.Kind == KindProgram && c.Via != ViaPack && c.From != "" {
		problems = append(problems, fmt.Sprintf("%s: a program delivered via %q does not take \"from\" — "+
			"it is the pack-relative source directory of a program built from its own tree, which only "+
			"a \"program\" with via %q reads; drop \"from\", or deliver it with via %q", label, c.Via, ViaPack, ViaPack))
	}
	if c.Kind != KindProgram || c.Via != ViaPack {
		if c.Run != "" {
			problems = append(problems, fmt.Sprintf("%s: kind %q does not take \"run\" — it names the file a "+
				"program built from its pack tree runs, which only a \"program\" with via %q reads", label, c.Kind, ViaPack))
		}
		if c.Prebuilt != nil {
			problems = append(problems, fmt.Sprintf("%s: kind %q does not take \"prebuilt\" — it names the "+
				"downloads of a program built from its pack tree, which only a \"program\" with via %q reads", label, c.Kind, ViaPack))
		}
	}
	return problems
}

// PackTreeWarnings is what `yolo pack lint` warns about for the pack-tree programs of a manifest
// (BG-D16): a program whose `from` directory holds no staged file and that declares no `prebuilt`
// has nothing to build or run, and a launch would refuse it. present reports whether a
// pack-relative directory holds any staged file; "." is the pack root and always present. A
// warning, not a refusal: an absent directory is legal in the manifest, and a program delivered
// some other way may still be the pack's intent.
func PackTreeWarnings(contributions []Contribution, present func(dir string) bool) []string {
	var out []string
	for _, c := range contributions {
		if c.Kind != KindProgram || c.Via != ViaPack || c.Prebuilt != nil {
			continue
		}
		if path.Clean(c.From) == "." || present(path.Clean(c.From)) {
			continue
		}
		out = append(out, fmt.Sprintf("program %q: \"from\" %q holds no file in this pack, and no \"prebuilt\" "+
			"is declared, so there is nothing to build or run — add the source directory, or declare a "+
			"\"prebuilt\" download for each platform it serves", c.Bin, c.From))
	}
	return out
}

// PackTreeLayoutVersion is the version of the placement layout a pack-tree build leaves
// (PackTreeProduces): the program's directory under ~/.local/lib/yolo-programs and its link in
// ~/.local/bin. It is in the recipe (PackTreeRecipe), so a change to the layout is a new build and
// never a hit on an entry laid out the old way.
const PackTreeLayoutVersion = 1

// packTreeRecipeTag opens a pack-tree recipe's canonical form, so it never equals a fork's recipe
// (ForkRecipe) or a tree's for the same strings: those canonical forms are JSON arrays too, and an
// untagged one could coincide with them (BG-D3).
const packTreeRecipeTag = "pack-tree"

// PackTreeRecipe is a pack-tree program's RECIPE HASH (docs/design/pack-buildable-programs.md
// §6.1, BG-D3): the sha256, in hex, of the JSON array ["pack-tree", layout version, from, build,
// run]. Everything besides the source directory's content that could change the bytes a build
// leaves is in it; the content is the build's revision (the tree digest), and the toolchain is
// recorded on the receipt, not keyed (OQ-FP2, as for a fork).
func PackTreeRecipe(from, build, run string) string {
	canonical, _ := json.Marshal([]any{packTreeRecipeTag, PackTreeLayoutVersion, from, build, run})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// PackTreeBuildSource is a pack-tree build's SOURCE string, the first part of its build identity
// and the key the capture store selects its entries under: "pack:<pack>/<from>". The pack's name is
// in it, so two packs that ship the same tree build apart. It is a record, not an address: no
// fork's source (a packsrc address, which never starts "pack:") can equal it.
func PackTreeBuildSource(pack, from string) string {
	return "pack:" + pack + "/" + from
}

// PackTreeCheckoutBuildSource is the SOURCE string of a pack-tree program built from the user's own
// checkout at dir in place of its pack's `from` (`programs.overrides.<bin>.path`, BG-D19):
// "checkout:<pack>:<dir>". It never equals a pack's own build source ("pack:…") nor a fork's (a
// packsrc address), so a checkout's builds are kept, selected and reused apart from the pack's.
func PackTreeCheckoutBuildSource(pack, dir string) string {
	return "checkout:" + pack + ":" + dir
}

// PackTreeProgramDir is the home-relative directory a pack-tree program lives in: its source
// directory copied there and built in place (BG-D4), so every path the build embeds is under the
// home, where the capture's relocation records it.
func PackTreeProgramDir(bin string) string {
	return ".local/lib/yolo-programs/" + bin
}

// PackTreeProgramLink is the home-relative path of a pack-tree program's link on PATH,
// ~/.local/bin/<bin>, which points at `run` inside PackTreeProgramDir by a relative target.
func PackTreeProgramLink(bin string) string {
	return ".local/bin/" + bin
}

// PackTreeLinkTarget is the relative target of PackTreeProgramLink: from ~/.local/bin to `run`
// inside the program's directory, "../lib/yolo-programs/<bin>/<run>".
func PackTreeLinkTarget(bin, run string) string {
	return "../lib/yolo-programs/" + bin + "/" + run
}

// PackTreeProduces are the home-relative paths a pack-tree program's store entry holds, and the
// only ones (BG-D4): the link first, which is the program path the source launcher execs
// (ForkProgramPath finds it), then the program's directory. The source launcher clears both before
// it materializes a new key, which is what replaces the directory whole (BG-D12).
func PackTreeProduces(bin string) []string {
	return []string{PackTreeProgramLink(bin), PackTreeProgramDir(bin)}
}

// IsPackTree reports whether this Install is a PACK-TREE PROGRAM's delivery (`via: "pack"`): kind
// "source", with the program file it runs. Such a program has no fork pin, no source address and
// no fork recipe, so every reader of those asks this first.
func (in Install) IsPackTree() bool {
	return in.Kind == InstallKindSource && in.PackRun != ""
}

// IsForkBuild reports whether this Install is a FORK's delivery: kind "source" and not a pack-tree
// program's. It is the predicate a reader of a fork's pin, source address or recipe asks, so a
// pack-tree program, the install kind's other member, never reaches a fork's path.
func (in Install) IsForkBuild() bool {
	return in.Kind == InstallKindSource && !in.IsPackTree()
}

// PackTreeRecipe is PackTreeRecipe for this Install's pack-tree delivery, "" for any other program.
func (in Install) PackTreeRecipe() string {
	if !in.IsPackTree() {
		return ""
	}
	return PackTreeRecipe(in.PackFrom, in.Build, in.PackRun)
}

// packTreePrebuiltRecipeTag opens a PREBUILT artifact's recipe, so it never equals a build's
// (PackTreeRecipe) for the same program: the two are different bytes for one bin, and a hit on one
// must never serve the other.
const packTreePrebuiltRecipeTag = "pack-tree-prebuilt"

// PackTreePrebuiltRecipe is the RECIPE HASH of a pack-tree program delivered from its `prebuilt`
// artifact (docs/design/pack-buildable-programs.md §7): the sha256, in hex, of the JSON array
// ["pack-tree-prebuilt", layout version, bin]. The artifact's own sha256 is the entry's revision,
// so the recipe carries only what lays the file out: it lands as PackTreePrebuiltRun(bin) inside
// the program's directory, linked the way a build's `run` is.
func PackTreePrebuiltRecipe(bin string) string {
	canonical, _ := json.Marshal([]any{packTreePrebuiltRecipeTag, PackTreeLayoutVersion, bin})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// PackTreePrebuiltRun is where a prebuilt artifact lands inside its program's directory, relative
// to it: the file under the program's own name, ~/.local/lib/yolo-programs/<bin>/<bin> (§7).
func PackTreePrebuiltRun(bin string) string { return bin }

// PrebuiltPlatforms are the platforms a `prebuilt` map declares, sorted; nil for none.
func PrebuiltPlatforms(prebuilt map[string]PrebuiltBuild) []string {
	var out []string
	for p := range prebuilt {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// PrebuiltOptInSetting is the user-scope setting that opts one program into its pack's prebuilt
// artifact even when the pack ships its source (docs/design/pack-buildable-programs.md §7, OQ-BG2,
// BG-D18), as the command that writes it into the user config, in backquotes: the readable key
// `programs.overrides.<bin>.prebuilt`, which `yolo config set` writes in either config format.
func PrebuiltOptInSetting(bin string) string {
	return "`yolo config set " + shquote.Quote("programs.overrides."+bin+".prebuilt") + " true --user`"
}

// PrebuiltOptInStep is the clause a refusal of a pack-tree BUILD that cannot run here adds when the
// pack declares a prebuilt artifact for platform (OQ-BG2): the explicit opt-in that runs it instead.
// "" when it declares none, since then there is nothing to opt into. Never a substitution: yolo uses
// the artifact for a present source only when the user sets this.
func PrebuiltOptInStep(pack, bin, platform string, prebuilt map[string]PrebuiltBuild) string {
	if _, ok := prebuilt[platform]; !ok {
		return ""
	}
	return "or, to run pack " + pack + "'s prebuilt artifact for " + platform + " instead of building it, run " +
		PrebuiltOptInSetting(bin)
}
