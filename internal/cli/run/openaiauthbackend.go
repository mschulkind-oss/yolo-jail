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
	openAIAuthBrokerName           = "openai-auth-broker"
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

func withoutOpenAIAuthPack(packs []*packload.Pack) []*packload.Pack {
	out := make([]*packload.Pack, 0, len(packs))
	for _, p := range packs {
		if p.Name != "openai-auth" {
			out = append(out, p)
		}
	}
	return out
}
