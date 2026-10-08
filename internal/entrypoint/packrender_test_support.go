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

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// ConfigurePackByName renders every surface the named EMBEDDED pack declares, then runs
// its hooks. Errors are returned rather than collected, since a caller asking for one pack
// by name wants to know whether that pack worked.
func ConfigurePackByName(e *Env, name string) error {
	p, err := embeddedPack(name)
	if err != nil {
		return err
	}
	return configureOnePack(e, p)
}

// configureOnePack is ConfigurePackByName's body over a pack already in hand: every surface p
// declares, rendered from a single-pack view, then its hooks.
//
// Split from the lookup so the render is reachable with a fixture pack. ConfigurePackByName
// only ever resolves an EMBEDDED pack, and no shipped pack carries every declaration this
// body gates (posture lists, today), so a gate here — the autonomy bit handed to
// packoverlay.Collect below — could otherwise be inverted with every test green
// (configureonepack_test.go).
func configureOnePack(e *Env, p *packload.Pack) error {
	single := []*packload.Pack{p}
	// THE ONE LOOP (surfaceloop.go) over a single-pack view, with this entry's failure
	// disposition: stop at the first error, since a caller asking for one pack by name wants
	// to know whether that pack worked. Everything else — the posture from the target's
	// profile, the profile table, the resolved selection, the per-agent MCP table — is the
	// boot's own, so the parity proofs this entry serves measure the render the boot produces.
	if err := renderPackSet(e, single, func(autonomy bool, activeProfiles map[string][]string) *packoverlay.OverlaySet {
		// Overlays over the ONE pack asked for, so a pack that overlays a surface it owns
		// itself still renders. A cross-pack overlay cannot resolve from a single-pack view
		// and is reported ownerless (R2) — correct here rather than a limitation, since this
		// entry means "render this pack" and the boot loop is what sees the whole set. The
		// selection's BUILT-IN capability half reads the same single-pack view: it answers
		// for a surface whose agent this pack installs — every shipped one — and nothing for
		// a surface an agent pack elsewhere would speak for.
		return packoverlay.Collect(single, autonomy, activeProfiles)
	}, func(_ string, run func() error) error {
		return run()
	}); err != nil {
		return err
	}
	for _, h := range p.Decl.HookContributions() {
		if err := runPackHook(e, p, h); err != nil {
			return err
		}
	}
	return nil
}

// embeddedPack returns the one embedded pack named, out of the process's materialized set.
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

// embeddedPackSet is every embedded official pack. Split out of embeddedPack for the one
// caller that needs the SET rather than a member — the selection a surface derive sees
// resolves across packs (ConfigurePackByName).
//
// packload.Embedded, which is the process's ONE loaded copy (the build's shared tree).
// These three entry points each used to run their own MaterializeEmbedded — into a shared
// `yolo-embedded-packs-` temp dir that nothing removed, so `yolo check` re-extracted the
// ~30-file tree three times per run and left the directory behind. The set is not
// selection-gated and must not become so, because of what it is for: `yolo check`'s dry-run
// probe renders EVERY pack yolo ships, whatever this machine's config selects.
func embeddedPackSet() ([]*packload.Pack, error) {
	packs := packload.Embedded()
	if problems := packload.EmbeddedProblems(); len(problems) > 0 {
		return nil, fmt.Errorf("%s", problems[0])
	}
	return packs, nil
}

// EmbeddedPackNames lists the embedded official packs, in a deterministic order.
func EmbeddedPackNames() []string {
	packs, err := embeddedPackSet()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(packs))
	for _, p := range packs {
		names = append(names, p.Name)
	}
	return names
}

// ProbeSurface is one declared surface reduced to what a dry-run validator needs: where
// the file is, how to parse it, and whether yolo writes it at all. Unrendered covers both a
// surface declared `unrendered` and one the render loop's `whenListed` gate skipped on this
// Env (Env.listSkipped), so it is meaningful only after the render ran on e.
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
	packs, err := embeddedPackSet()
	if err != nil {
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
				Unrendered: s.ResolvedMode() == manifest.ModeUnrendered || e.listSkipped[s.Key()],
			})
		}
	}
	return out
}
