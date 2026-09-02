package entrypoint

// packrender_test_support.go provides ConfigurePackByName — the one entry point that
// renders a named pack's surfaces and runs its hooks, without the caller supplying a
// loaded pack.
//
// It exists for the CALLERS THAT ARE NOT THE BOOT PATH: the existing per-agent tests
// (prism_claude_test.go and friends) and `yolo check`'s dry-run generator probe. Both used
// to call ConfigureClaudePrism / ConfigureCopilotPrism / … directly, and those six
// functions are gone — their bodies were per-agent data now living in the packs.
//
// Keeping the tests pointed here rather than deleting them is the point: they were written
// as parity proofs against the pre-prism bespoke writers (a dropped MCP server does not
// resurrect, a captured user edit survives, an OAuth token is not wiped), and running them
// through the DECLARATIVE path is how we know the pack declarations reproduce what the Go
// functions did. A test suite that only exercised the new mechanism would prove the
// mechanism works, not that it does the same thing.
//
// NOT used by the boot path, which loads packs from the mounted tree (LoadJailPacks) and
// renders every one. This reaches the embedded packs by name instead.

import (
	"fmt"
	"os"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// ConfigurePackByName renders every surface the named EMBEDDED pack declares, then runs
// its hooks. Errors are returned rather than collected, since a caller asking for one pack
// by name wants to know whether that pack worked.
func ConfigurePackByName(e *Env, name string) error {
	p, err := embeddedPack(name)
	if err != nil {
		return err
	}
	// The WHOLE embedded set, not just the pack asked for: the selection a derive sees
	// (surfaceSelection) resolves across packs, because the pack that declares a
	// `provider` for a profile usually installs no CLI — packs/zai is the shipped case,
	// and the surface it speaks to is claude's. Resolving against one pack would answer
	// with the profile's bare name wherever the provider differs from it, and `yolo
	// check` would then validate a render the boot never produces. The embedded set is
	// all this entry has — a CONFIGURED pack's declaration stays outside its reach, the
	// same reach the overlays below are bounded by, and for the opposite reason: there
	// the entry must not RENDER a pack nobody asked for, here it simply cannot see one.
	set, err := embeddedPackSet()
	if err != nil {
		return err
	}
	tables := liveTables(e)
	// The autonomy policy reads off the target's profile, exactly as the boot loop does
	// (ConfigurePackSurfaces) — this entry has to agree with it or the parity proofs above
	// would be measuring a posture the boot path never renders.
	autonomy := e.renderTarget().Profile().AgentAutonomy
	// The profile table resolved the same way the boot loop resolves it — this entry must
	// gate on the same selection, or the parity proofs above measure a render the boot
	// never produces.
	profiles := packload.ProfileTable(e.LoadUseProfiles())
	surfaces, problems, notes := p.SurfacesForReport(autonomy)
	if len(problems) > 0 {
		return fmt.Errorf("pack %s: %s", name, problems[0])
	}
	// A config patch that named no surface of its own pack merged into nothing. This entry
	// is `yolo check`'s dry-run probe, which makes it the FIRST place an authoring mistake
	// of that shape can be seen — a pack is checked before any jail renders it — so the
	// note is not optional here. Same ruling as the boot loop's: a warning, never an error.
	for _, n := range notes {
		e.warnOnce(n.String())
	}
	deriveScript := packload.DeriveScript(p)
	// Overlays over the ONE pack asked for, so a pack that overlays a surface it owns
	// itself still renders. A cross-pack overlay cannot resolve from a single-pack view
	// and is reported ownerless (R2) — correct here rather than a limitation, since this
	// entry means "render this pack" and the boot loop is what sees the whole set. The
	// profile table is the one resolved above, so this entry gates a `profile`-scoped
	// overlay on the same selection the boot loop's gate reads.
	overlays := packoverlay.Collect([]*packload.Pack{p}, autonomy, profiles)
	for _, s := range surfaces {
		if err := renderDeclaredSurface(e, s, tables, deriveScript,
			surfaceSelectionFor(set, profiles, s),
			overlays.For(s.Agent, s.Name)); err != nil {
			return err
		}
	}
	for _, h := range p.Decl.HookContributions() {
		if err := runPackHook(e, p, h); err != nil {
			return err
		}
	}
	return nil
}

// embeddedPack materializes the embedded official packs once and returns the one named.
//
// Materialized once per process (not per call) because ConfigurePackByName is called
// repeatedly across a test's boots, and re-copying the tree each time would be wasted work
// on the path that is meant to mirror what the boot loop does with a mounted tree.
func embeddedPack(name string) (*packload.Pack, error) {
	packs, err := embeddedPackSet()
	if err != nil {
		return nil, err
	}
	for _, p := range packs {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no embedded pack named %q", name)
}

// embeddedPackSet materializes the embedded official packs once and returns all of them.
// Split out of embeddedPack for the one caller that needs the SET rather than a member —
// the selection a surface derive sees resolves across packs (ConfigurePackByName).
func embeddedPackSet() ([]*packload.Pack, error) {
	embeddedOnce.Do(func() {
		packs, problems := packload.MaterializeEmbedded(officialpacks.FS, embeddedDir())
		if len(problems) > 0 {
			embeddedErr = fmt.Errorf("%s", problems[0])
			return
		}
		embeddedPacks = packs
	})
	if embeddedErr != nil {
		return nil, embeddedErr
	}
	return embeddedPacks, nil
}

var (
	embeddedOnce    sync.Once
	embeddedPacks   []*packload.Pack
	embeddedErr     error
	embeddedRoot    string
	embeddedDirOnce sync.Once
)

// embeddedDir is one temp dir per process for the materialized embedded packs, so the
// several entry points here share one copy rather than each making their own.
func embeddedDir() string {
	embeddedDirOnce.Do(func() {
		dir, err := os.MkdirTemp("", "yolo-embedded-packs-")
		if err == nil {
			embeddedRoot = dir
		}
	})
	return embeddedRoot
}

// EmbeddedPackNames lists the embedded official packs, in a deterministic order.
func EmbeddedPackNames() []string {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, embeddedDir())
	if len(problems) > 0 {
		return nil
	}
	names := make([]string, 0, len(packs))
	for _, p := range packs {
		names = append(names, p.Name)
	}
	return names
}

// ProbeSurface is one declared surface reduced to what a dry-run validator needs: where
// the file is, how to parse it, and whether yolo writes it at all.
type ProbeSurface struct {
	Label      string
	Path       string
	Codec      string
	Unrendered bool
}

// EmbeddedPackSurfaces lists every embedded pack's surfaces with their in-jail paths
// resolved against the Env's home, so `yolo check` can validate what was just rendered
// without knowing any tool's name.
func EmbeddedPackSurfaces(e *Env) []ProbeSurface {
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, embeddedDir())
	if len(problems) > 0 {
		return nil
	}
	var out []ProbeSurface
	for _, p := range packs {
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			continue
		}
		for _, s := range surfaces {
			out = append(out, ProbeSurface{
				Label:      s.Agent + "/" + s.Name,
				Path:       expandHomePath(e, s.Path),
				Codec:      s.Codec,
				Unrendered: s.ResolvedMode() == manifest.ModeUnrendered,
			})
		}
	}
	return out
}
