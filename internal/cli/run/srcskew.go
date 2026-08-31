package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// AllowSourceSkewEnv opts back into launching with a host binary older than the
// tree the image is about to be built from. Named on the refusal, in the house
// style of YOLO_ALLOW_STALE_IMAGE and YOLO_ALLOW_UNREACHABLE_SERVICES: a fatal
// witness always says how to overrule it.
const AllowSourceSkewEnv = "YOLO_ALLOW_SOURCE_SKEW"

// refuseOnSourceSkew reports whether this launch must stop because the source tree
// at repoRoot has moved past the binary running this function, through paths the
// jail image is built from (version.SourceSkew).
//
// # Why it refuses instead of warning
//
// The launch is already doomed at this point — it just fails later, more expensively,
// and somewhere that names neither half of the cause. AutoLoadImage will nix-build and
// stream a multi-gigabyte image from the NEW source, hand it a launcher argv from the
// OLD source, and the mismatch surfaces (if it surfaces at all) as an entrypoint error
// about a path nobody asked about. Refusing costs the user one `just install`; not
// refusing costs a build, a stream, and a debugging session that starts in the wrong
// package. Cheap, early and named beats late, expensive and anonymous.
//
// It is deliberately CONTAINER-ONLY, called under the same `rt != "macos-user"` guard
// as the repo-root gate. macos-user has no image and no second binary: the host yolo
// provisions the sandbox in-process, so there are not two halves to skew.
func (o *Options) refuseOnSourceSkew(repoRoot string) bool {
	if o.Getenv(AllowSourceSkewEnv) != "" {
		return false
	}
	skew := version.SourceSkew(repoRoot)
	if skew == nil {
		return false
	}
	o.pr(o.Stderr).print(
		"[bold red]Refusing to launch: this yolo is older than the source tree it would " +
			"build the jail from.[/bold red]\n\n" +
			"  this yolo   was built from  " + short(skew.BinaryCommit) + "\n" +
			"  " + repoRoot + "  is at  " + short(skew.TreeCommit) + "\n" +
			"  they differ in  " + strings.Join(skew.Changed, ", ") + "\n\n" +
			"The jail IMAGE is rebuilt from the tree on every launch. The yolo you just ran\n" +
			"changes only when you run `just install`. Launching now would pair a launcher\n" +
			"and a yolo-entrypoint built from different source — which fails deep inside the\n" +
			"boot, naming neither half (the last one refused with\n" +
			"`mkdir /home/agent/.yolo: read-only file system`), and only after building and\n" +
			"streaming the whole image.\n\n" +
			"[bold]Fix:[/bold]  (cd " + repoRoot + " && just install)\n\n" +
			"To launch anyway and own the mismatch: [bold]" + AllowSourceSkewEnv + "=1[/bold]")
	return true
}

// short abbreviates a full sha for the report. Not git's `--short` (which is
// length-adaptive): this is display only, and the refusal prints the repo root
// beside it, so a fixed 8 is unambiguous enough and stable to assert on.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
