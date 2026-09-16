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

func (o *Options) startOpenAIAuth(cname, rt string, cfg *jsonx.OrderedMap) []loopholeDaemon {
	return o.startLoopholesMatching(cname, rt, cfg, func(name string) bool {
		return name == openAIAuthBrokerName
	})
}

// startOpenAIAuthDisclosed is the SPAWN BOUNDARY of the SUBSET path, and it is a wrapper for
// the same reason startLoopholesDisclosed is one (packloopholes.go): the disclosure has to be
// inseparable from the spawn rather than a statement above it that a later edit can move or a
// backend arm can forget.
//
// IT WAS FORGOTTEN, which is why this function exists. The macos-user arm returns from Run
// above startLoopholesDisclosed and reached the broker through startOpenAIAuth directly, so
// the only call site of notePackHostExec sat on a path that arm never takes: the broker's
// `host_daemon` — pack-declared code, on the user's real machine, with no container anywhere
// — spawned while "This launch runs pack code on your machine" never printed. AGENTS.md:
// the read/exec banners ARE the trust boundary today (OQ-TP9 deleted the approval gate and
// KEPT this banner as the compensating disclosure), so a silent spawn did not weaken the
// boundary for that backend, it removed it. The arm's own comment recorded the gap as open;
// this is the close.
//
// SCOPED TO THE openai-auth PACK — the complement of the scoping withoutOpenAIAuthPack does
// for the inert report printed on the same arm. Handing notePackHostExec the whole pack set
// would announce daemon argvs this path never starts, which is the overclaim OQ-10 rules out
// (a disclosure that names what is not happening is the silent skip wearing a badge), and the
// inert report a few lines later would then contradict it pack for pack.
func (o *Options) startOpenAIAuthDisclosed(cname, rt string, cfg *jsonx.OrderedMap,
	packs []*packload.Pack) []loopholeDaemon {
	spawning, _ := partitionOpenAIAuthPack(packs)
	o.notePackHostExec(spawning)
	return o.startOpenAIAuth(cname, rt, cfg)
}

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
