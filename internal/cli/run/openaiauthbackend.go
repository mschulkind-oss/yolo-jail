package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const (
	openAIAuthBrokerName = "openai-auth-broker"
	// The PACK that ships the loophole above, which is a different name from the loophole
	// and is the axis the disclosure/inert split below scopes on: a report about packs
	// takes a pack name, a spawn allow-list takes a loophole name.
	openAIAuthPackName             = "openai-auth"
	openAIAuthMountSentinelName    = ".mount-sentinel"
	openAIAuthMountSentinelContent = "yolo-openai-auth-mount-v1\n"
)

func openAIAuthLoopholeActive(cfg *jsonx.OrderedMap) bool {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	lp, ok := set.Lookup(openAIAuthBrokerName)
	return ok && lp.Active() && set.MayRunHostCode(lp)
}

// THE SUBSET SPAWN PATH IS GONE — retired 2026-09-17, and what it proved is kept.
//
// `startOpenAIAuth` and `startOpenAIAuthDisclosed` lived here because the macos-user arm
// returned from Run above `startLoopholesDisclosed` and could start exactly one host service:
// the OpenAI credential broker. The wrapper existed to make that one spawn's disclosure
// inseparable from it, after the arm had been shipping a SILENT spawn — pack-declared code on
// the user's real machine with no container anywhere, while "This launch runs pack code on
// your machine" never printed. AGENTS.md: the read/exec banners ARE the trust boundary today
// (OQ-TP9 deleted the approval gate and KEPT this banner as the compensating disclosure), so
// that silence had not weakened the boundary for that backend, it had removed it.
//
// The arm now routes through `startLoopholesDisclosed` like every other backend, so both
// functions lost their last production caller and were deleted rather than left behind as a
// callee nothing calls — the shape AGENTS.md names as "a test that pins the CALLEE while the
// CALL SITE is unpinned is not a test", which this repo has shipped five times.
//
// WHAT INVERTED WITH THEM, and it is the part worth reading before re-adding anything here:
// the subset SCOPING was correct and is now an underclaim. `startOpenAIAuthDisclosed` handed
// `notePackHostExec` only the openai-auth pack, because announcing daemon argvs that path
// never started would have been OQ-10's overclaim. The arm starts every admitted loophole
// now, so the whole pack set is exactly what it must announce, and `notePackLoopholesInert`
// takes the whole set too — they stay complements by construction instead of by a pair of
// hand-maintained filters. `partitionOpenAIAuthPack`/`withoutOpenAIAuthPack` are KEPT: the
// container branch still uses them.

// prepareOpenAIAuthMountSentinel creates the inert file named by the shipped
// loophole's state_files list before container argv assembly. A nonempty list is
// the fail-closed boundary that keeps credentials.json out of the jail; the marker
// gives that boundary one harmless, always-present source to mount without the
// runtime's missing-source warning.
//
// Replace the path atomically on every launch. Besides avoiding torn content, the
// rename replaces a pre-existing symlink rather than following it into some other
// host file that the bind mount would then expose.
func (o *Options) prepareOpenAIAuthMountSentinel(cfg *jsonx.OrderedMap) {
	if !openAIAuthLoopholeActive(cfg) {
		return
	}
	if err := writeOpenAIAuthMountSentinel(); err != nil {
		o.pr(o.Stderr).print("[yellow]Warning: could not prepare the OpenAI authentication mount marker: " +
			err.Error() + "[/yellow]")
	}
}

func writeOpenAIAuthMountSentinel() error {
	dir := loopholes.StateDirFor(openAIAuthBrokerName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".mount-sentinel.*")
	if err != nil {
		return fmt.Errorf("create temporary marker: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary marker: %w", err)
	}
	if _, err := tmp.WriteString(openAIAuthMountSentinelContent); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary marker: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary marker: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, openAIAuthMountSentinelName)); err != nil {
		return fmt.Errorf("replace marker: %w", err)
	}
	return nil
}

// partitionOpenAIAuthPack splits the loaded packs into the one pack whose host service a
// SUBSET backend really starts and every other pack, which such a backend leaves inert.
//
// ONE function returning BOTH halves, rather than two independent filters, because both halves
// print in the SAME launch — the exec disclosure above and notePackLoopholesInert — and the two
// lines contradict each other the moment the halves overlap (a pack announced as running AND
// reported inert) or leave a gap (a pack neither line mentions). Complementary by construction
// is cheaper than complementary by review; TestOpenAIAuthPackFiltersPartitionThePackSet pins it
// anyway, since a later edit could still split them.
//
// PER PACK, not per loophole, and the imprecision is deliberate and bounded: the openai-auth
// pack's whole content is the broker loophole, so its exec claims and the subset a macos-user
// launch starts are the same set. If it ever ships a second loophole the two split — and the
// inert report splits with it in the same direction, since it drops the pack whole.
func partitionOpenAIAuthPack(packs []*packload.Pack) (spawning, inert []*packload.Pack) {
	for _, p := range packs {
		if p.Name == openAIAuthPackName {
			spawning = append(spawning, p)
			continue
		}
		inert = append(inert, p)
	}
	return spawning, inert
}

func withoutOpenAIAuthPack(packs []*packload.Pack) []*packload.Pack {
	_, inert := partitionOpenAIAuthPack(packs)
	return inert
}
