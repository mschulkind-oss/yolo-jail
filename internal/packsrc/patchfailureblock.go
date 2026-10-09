package packsrc

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// patchfailureblock.go is PF-D81's ERROR BLOCK (docs/design/patched-forks.md §8): the one text every
// actor prints for a patch series that does not apply — a jail launch, `yolo host`, the host floor,
// `yolo pack update`, `yolo capture`, cached-good recovery — so the copyable shape is written once.
// It names the subject, the upstream target, the patch and its conflict or cause, that the operation
// stopped, the repair, and the ONE bypass that works on this machine (PatchBypass, PF-D83), and is
// plain text: conspicuous without color.

// PatchBypass is what the block's Bypass line may offer, decided by the caller from what is on this
// machine (PF-D83): the bypass is named only when it will work.
type PatchBypass struct {
	// Command is the operation the bypass variable is put in front of: "yolo", "yolo host -- pi",
	// "yolo pack update".
	Command string
	// Runs is the label of the intact admitted build paths.AllowPatchFailuresEnv runs in the failed
	// series' place; "" when there is none, and then that variable is not offered.
	Runs string
	// CachedGood is the owner key whose older recorded good build YOLO_USE_CACHED_GOOD runs for one
	// fresh jail launch (PF-D82), with CachedGoodLabel naming it; "" when there is none or Runs is set.
	CachedGood, CachedGoodLabel string
	// Missing says YOLO_ALLOW_MISSING_PROGRAMS starts the operation without the program (a jail
	// launch with no build of it to run, PF-D83).
	Missing bool
	// Unneeded says the operation goes on without stopping for it: a patched extension no selected
	// agent pack loads where this launch runs.
	Unneeded bool
}

// CachedGoodEnv is the jail launch's cached-good recovery selector (internal/cli/run's
// CachedGoodEnv, PF-D82), spelled here so the block can offer it.
const CachedGoodEnv = "YOLO_USE_CACHED_GOOD"

// lines is the Bypass line and the line under it saying what it runs or leaves out.
func (b PatchBypass) lines() []string {
	cmd := b.Command
	if cmd == "" {
		cmd = "yolo"
	}
	switch {
	case b.Unneeded:
		return []string{"Bypass: none needed — no selected agent pack loads it here, so this launch goes on"}
	case b.Runs != "":
		return []string{"Bypass: " + paths.AllowPatchFailuresEnv + "=1 " + cmd,
			"  (runs the intact admitted build " + b.Runs + " for this one run, skipping this update; " +
				"it does not repair the series)"}
	case b.CachedGood != "":
		return []string{"Bypass: " + CachedGoodEnv + "=" + b.CachedGood + " " + cmd,
			"  (runs the older admitted build " + b.CachedGoodLabel + " for this one fresh launch; it lacks the current " +
				"series and build line, and no intact build of the current series is on this machine for " +
				paths.AllowPatchFailuresEnv + "=1 to run)"}
	case b.Missing:
		return []string{"Bypass: " + paths.AllowMissingProgramsEnv + "=1 " + cmd,
			"  (starts without it: no intact admitted build of it is on this machine for " +
				paths.AllowPatchFailuresEnv + "=1 to run)"}
	}
	return []string{"Bypass: none on this machine — no intact admitted build of it is here for " +
		paths.AllowPatchFailuresEnv + "=1 to run; repair the series, or drop its pack"}
}

// Block is the error block for f: label names the subject ("fork pi-mine/pi", or an owner key),
// owner is the owner key the repair's rebase takes, and bypass what the Bypass line offers.
func (f *PatchFailure) Block(label, owner string, bypass PatchBypass) string {
	if f == nil {
		return ""
	}
	var b strings.Builder
	target := f.Target.Tag
	if target == "" {
		target = f.Target.Commit
	}
	fmt.Fprintf(&b, "ERROR: %s: patch application failed at upstream %s (%s)\n", label, target, f.Target.Commit)
	if f.Member != "" {
		fmt.Fprintf(&b, "  Patch: %s\n", PrintableFailureText(f.Member))
	}
	switch f.Kind {
	case "conflict":
		if len(f.Paths) > 0 {
			fmt.Fprintf(&b, "  Conflict: %s\n", PrintableFailureText(strings.Join(f.Paths, ", ")))
		} else {
			b.WriteString("  Conflict: the patch could not be merged\n")
		}
	case "base":
		fmt.Fprintf(&b, "  Base rejection: %s\n", PrintableFailureText(f.Detail))
	default:
		fmt.Fprintf(&b, "  Application command: %s\n", PrintableFailureText(f.Detail))
	}
	b.WriteString("  Operation stopped; no older fit or base will be built.\n")
	if f.Kind == "base" {
		fmt.Fprintf(&b, "  Repair: re-export the series with git format-patch --base=%s\n", f.Target.Commit)
	} else {
		fmt.Fprintf(&b, "  Repair: yolo pack rebase %s --onto %s\n", owner, f.Target.Commit)
	}
	for _, l := range bypass.lines() {
		b.WriteString("  " + l + "\n")
	}
	if f.Log != "" {
		if st, err := os.Stat(f.Log); err == nil && !st.IsDir() {
			fmt.Fprintf(&b, "  Log: %s\n", PrintableFailureText(f.Log))
		}
	}
	return b.String()
}

// PrintableFailureText escapes control characters a patch name, path or git detail could carry, so
// the block stays one copyable text on any terminal.
func PrintableFailureText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\x%02x", r)
		}
	}
	return b.String()
}
