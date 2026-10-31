package packload

// defaultnames.go is the warning that keeps a converted config's skill names from changing in
// silence (docs/design/pack-naming.md §7, review finding M5). Main's migrator wrote `[pack_sources]`
// keys and `local/<pack>` entries with no `bare`, which was all a config could say before pack
// naming; under pack naming such an entry gets its default namespace, so every skill it delivered
// as `review` is now `<namespace>-review`. The current migrator writes `bare = true`, so only a
// config the old one converted meets this.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
)

// ConvertedConfigBackups are the files the config migrator keeps beside a user config it
// converted (configmigrate's Target.Backup): their presence says the config is a converted one.
var ConvertedConfigBackups = []string{"config.jsonc.pre-migrate", "config.json.pre-migrate"}

// ConvertedConfigIn reports whether the user config dir holds a migrator backup.
func ConvertedConfigIn(dir string) bool {
	for _, name := range ConvertedConfigBackups {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// DefaultNamespaceRenames is one warning per selected pack whose DEFAULT namespace (one neither
// its entry nor its source chose: Pack.KeepNamesStep) renames a skill it ships or moves its files
// landing or /ctx directory, naming each move old → new, the one step that keeps the old names and
// the one that keeps the new ones. Nil unless converted: a config written since pack naming chose
// its names knowingly.
func DefaultNamespaceRenames(packs []*Pack, converted bool) []string {
	if !converted {
		return nil
	}
	var out []string
	for _, p := range packs {
		if p == nil || p.Namespace == "" || p.KeepNamesStep == "" || p.Decl == nil {
			continue
		}
		var moved []string
		for _, name := range SkillNames(p) {
			if to := packname.Delivered(p.Namespace, name); to != name {
				moved = append(moved, "skill "+name+" → "+to)
			}
		}
		// Bare, the pack landed under its own name and its /ctx grants under its staged
		// directory (landingName and CtxName with no Landing); now both are its Landing.
		files, ctx := false, false
		for _, c := range p.Decl.Contributions() {
			files = files || c.Kind == packdecl.KindFiles
			ctx = ctx || c.Kind == packdecl.KindReadsHost
		}
		bare := *p
		bare.Landing = ""
		if from := bare.landingName(); files && p.Landing != "" && from != p.Landing {
			moved = append(moved, "files landing "+from+" → "+p.Landing)
		}
		if from := bare.CtxName(); ctx && p.Landing != "" && from != p.Landing {
			moved = append(moved, "/ctx/host-"+from+"/ → /ctx/host-"+p.Landing+"/")
		}
		if len(moved) == 0 {
			continue
		}
		accept := p.AcceptNamesStep
		if accept == "" {
			accept = "`bare = false` on that entry or source"
		}
		out = append(out, fmt.Sprintf("pack %s is now named in the namespace %q, which its config entry "+
			"and source did not choose (a config converted before pack naming): %s. Keep the old names "+
			"with %s; or keep the new ones and silence this with %s. Either way, then run `yolo check`",
			p.Name, p.Namespace, strings.Join(moved, ", "), p.KeepNamesStep, accept))
	}
	return out
}

// SkillNames is the sorted names, as the author wrote them, of the skills p delivers: each
// directory holding a SKILL.md under a skills source the delivery reads (SkillsSources), the
// declared contributions and the conventional skills/ tree alike, with a manifest or without.
func SkillNames(p *Pack) []string {
	seen := map[string]bool{}
	if p == nil {
		return nil
	}
	sources, _ := p.SkillsSources()
	for _, source := range sources {
		src := source.Dir
		entries, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(src, e.Name(), "SKILL.md")); err == nil {
				seen[e.Name()] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
