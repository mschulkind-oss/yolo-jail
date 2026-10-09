package cli

import (
	"fmt"
	"io"
	"os"

	runpkg "github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const allowPatchFailuresEnv = paths.AllowPatchFailuresEnv

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

// patchFailureCommand is the operation a patch failure's bypass is put in front of: `yolo host --
// <bin>` at the host, else a jail launch.
func patchFailureCommand(bin string, host bool) string {
	if host {
		return "yolo host -- " + shquote.Quote(bin)
	}
	return "yolo"
}

// writePatchFailure prints PF-D81's error block (packsrc.PatchFailure.Block) for owner, its Bypass
// line offering only what works on this machine (PF-D83), and — when the bypass is set and
// admitted names the intact build it runs — the CONTINUING line.
func writePatchFailure(w io.Writer, f *packsrc.PatchFailure, owner string, bypass packsrc.PatchBypass, admitted string) {
	if f == nil {
		return
	}
	_, _ = io.WriteString(w, f.Block(owner, owner, bypass))
	if allowPatchFailures() && admitted != "" {
		fmt.Fprintf(w, "CONTINUING: using intact admitted build %s; skips this subject's advance.\n", admitted)
	}
}

func printableFailureText(s string) string { return packsrc.PrintableFailureText(s) }
