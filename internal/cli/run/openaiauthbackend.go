package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

const (
	openAIAuthBrokerName = "openai-auth-broker"
	// The PACK that ships the loophole above, which is a different name from the loophole:
	// a report about packs takes a pack name, a spawn allow-list takes a loophole name.
	//
	// It scopes NO report any more — that was the disclosure/inert split deleted below — and
	// what still reads it is the fixtures that name a realistic pack. Kept because the
	// distinction it records is the one a future per-pack scoping would get wrong again.
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
// hand-maintained filters. `partitionOpenAIAuthPack`/`withoutOpenAIAuthPack` were kept for the
// container branch, and that branch is gone as well (see below).

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

// THE SUBSET PARTITION IS GONE TOO — deleted 2026-09-18, and what it was wrong about is the
// record worth keeping.
//
// `partitionOpenAIAuthPack` and `withoutOpenAIAuthPack` split the loaded packs into "the one
// pack whose host service a SUBSET backend really starts" and the rest. The macos-user half of
// that job disappeared with the subset spawn path above; the last caller was the
// `rt == "container"` branch in startLoopholesDisclosed, which handed the inert report every
// pack EXCEPT this one — so on Apple Container the credential service was the single pack the
// launch said nothing about, while its measured state is that the daemon starts and the jail
// cannot reach it.
//
// The argument for the exemption was that a pack "announced as running AND reported inert" is
// an untruth. Measured, it is not, twice over: the exec disclosure is CLAIM-shaped (it names
// what a pack DECLARES it runs), so claude's broker is announced and reported inert in the same
// AC launch and always was; and on this backend the two statements — the daemon started, the
// jail cannot reach it — are both true, with the second being the one a user can act on.
// packloopholes.go carries the whole reasoning at the line that used to hold the branch.
//
// WHAT WAS DELIBERATELY NOT DONE WITH IT, so the next reader does not take it for an
// oversight: openai-auth-broker-plan.md's step 10 also proposes WITHHOLDING
// `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT` on Apple Container. That is not done here, and
// the reason is not scope:
//
//   - the measurement is per BACKEND, not per service. Nothing crosses container→host on
//     `container` 1.1.0, so EVERY loopback-TLS endpoint variable this backend emits is equally
//     unreachable. Withholding one service's on that evidence is a per-service patch to a
//     per-backend fact, and it would leave the next service to rediscover it.
//   - it is not the shape brokerEndpointIsUnpublishable (assemble_parts.go) uses for the Claude
//     broker, whatever the symmetry suggests. That one is withheld because NOTHING PUBLISHES —
//     no singleton is ensured on AC and the allow list admits this service alone — so the
//     variable names a file that never appears. Here the file does appear: the daemon runs and
//     writes it. "Published but unreachable" and "never published" are different faults, and
//     the witness classes them differently (OQ-R4's faultUnreachable vs faultUnpublished).
//   - it has a cost the plan does not price. The jail-side adapter is still in this launch's
//     YOLO_JAIL_DAEMONS payload; withholding the variable it reads turns "an adapter that
//     cannot reach its front" into "an adapter with no front named at all", which on today's
//     supervisor is an empty log and an endless respawn (roadmap 📦 row 2). A launch that says
//     what is wrong beats a launch that removes the pointer and keeps the process.
//
// So the disclosure is the fix, and the transport is upstream.
