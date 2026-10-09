package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	runpkg "github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const allowPatchFailuresEnv = "YOLO_ALLOW_PATCH_FAILURES"

var allowPatchFailures = func() bool { return os.Getenv(allowPatchFailuresEnv) == "1" }

func currentPatchFailure(f packload.Fork) *packsrc.PatchFailure {
	series, err := f.ReadSeries()
	if err != nil {
		return nil
	}
	record, err := runpkg.LoadPatchedRecord(patchedForkStore(), f, series)
	if err != nil {
		return nil
	}
	inputs, _, _, _ := f.CheckWant(series).Inputs()
	return record.CurrentPatchFailure(inputs, series.Digest)
}

func patchFailureCommand(owner, bin string, host bool) string {
	if host {
		return allowPatchFailuresEnv + "=1 yolo host -- " + shquote.Quote(bin)
	}
	return allowPatchFailuresEnv + "=1 yolo"
}

func writePatchFailure(w io.Writer, f *packsrc.PatchFailure, owner, bin string, host bool, admitted string) {
	if f == nil {
		return
	}
	target := f.Target.Tag
	if target == "" {
		target = f.Target.Commit
	}
	fmt.Fprintf(w, "ERROR: %s: patch application failed at upstream %s (%s)\n", owner, target, f.Target.Commit)
	if f.Member != "" {
		fmt.Fprintf(w, "  Patch: %s\n", printableFailureText(f.Member))
	}
	switch f.Kind {
	case "conflict":
		if len(f.Paths) > 0 {
			fmt.Fprintf(w, "  Conflict: %s\n", printableFailureText(strings.Join(f.Paths, ", ")))
		} else {
			fmt.Fprintln(w, "  Conflict: the patch could not be merged")
		}
	case "base":
		fmt.Fprintf(w, "  Base rejection: %s\n", printableFailureText(f.Detail))
	default:
		fmt.Fprintf(w, "  Application command: %s\n", printableFailureText(f.Detail))
	}
	fmt.Fprintln(w, "  Operation stopped; no older fit or base will be built.")
	if f.Kind == "base" {
		fmt.Fprintf(w, "  Repair: re-export the series with git format-patch --base=%s\n", f.Target.Commit)
	} else {
		fmt.Fprintf(w, "  Repair: yolo pack rebase %s --onto %s\n", owner, f.Target.Commit)
	}
	fmt.Fprintf(w, "  Bypass: %s\n", patchFailureCommand(owner, bin, host))
	if st, err := os.Stat(f.Log); err == nil && !st.IsDir() {
		fmt.Fprintf(w, "  Log: %s\n", printableFailureText(f.Log))
	}
	if allowPatchFailures() && admitted != "" {
		fmt.Fprintf(w, "CONTINUING: using intact admitted build %s; skips this subject's advance.\n", admitted)
	}
}

func printableFailureText(s string) string {
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
