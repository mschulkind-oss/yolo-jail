package entrypoint

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// buildreceipt.go is the receipt of a FORK'S BUILD (docs/design/forked-programs-as-packs.md
// FP-D8): the `record` line written beside a fork's store entry, and the `materialize` line a jail
// appends when it puts that entry into its home.
//
// A KIND OF ITS OWN, `build`, and that is the compatibility property rather than a label. The one
// capture receipt reader keeps only kind `capture` (ReadCaptureReceipts), so a yolo that predates
// forks can never select a fork's entry for an installer query of the same bin — §9's "never serve
// a near-miss". It is the third name of the mechanism: manifest `via: "source"` → Install.Kind
// "source" → receipt `kind: "build"`.
//
// Same schema as every other receipt (receiptPrefix, parseReceiptLine), with three fields only this
// kind writes: `revision` (the full commit), `recipe` (packdecl.ForkRecipe, the sha256 of the build
// command, the outputs and the source subdirectory) and `toolchain` (the capture jail's image
// identity — OQ-FP2's record, which is on the receipt and never in the capture manifest, whose
// invariant is that nothing about the producing run is in it). A PATCHED fork's build adds five
// more, additive as FP-D8 lets the schema grow: `fork`, `series`, `tree`, `tag` and `version`
// (docs/design/patched-forks.md §6.3).

// ReceiptKindBuild is a fork build's receipt kind.
const ReceiptKindBuild = "build"

// BuildReceipt is one build receipt.
type BuildReceipt struct {
	// Bin is the program's bin.
	Bin string
	// Source is the fork's source address as declared, which is the receipt's `declared`: the
	// component selection keys a fork entry on beside the bin and the platform.
	Source string
	// Key is the store key, Digest the full sha256 of the canonical file manifest, Bytes the tree's
	// size and Path the entry root — CaptureReceipt's fields, in its places.
	Key    string
	Digest string
	Bytes  int64
	Path   string
	// Platform is "<GOOS>/<GOARCH>" as the build jail observed it.
	Platform string
	// Revision is the full commit the source was built at.
	Revision string
	// Recipe is the recipe hash (packdecl.ForkRecipe).
	Recipe string
	// Toolchain is the image identity of the jail the build ran in, "" when it could not be read.
	Toolchain string
	// Fork, Series, Tree, Tag and Version are a PATCHED fork's build's (docs/design/patched-forks.md
	// §6.3, PF-D7), and a plain fork's build writes none of them: the fork key ("<pack>/<bin>"),
	// which a patched build's selection keys on in place of its source, so no plain fork's query
	// and no other fork's selects it; the series digest; the PATCHED TREE (the git tree object
	// copied into the build's src/, §5.3); and the version tag of the upstream commit with its
	// version (PF-D36), so a good build recovered from the store knows the version it runs.
	Fork    string
	Series  string
	Tree    string
	Tag     string
	Version string
	// Act is ReceiptActRecord beside the entry, ReceiptActMaterialize in a workspace's log.
	Act string
	// Time is the moment recorded.
	Time time.Time
}

// Line renders the receipt as one JSON line, without the trailing newline, in CaptureReceipt's
// field order with the three build fields after `platform`.
func (r BuildReceipt) Line() string {
	var b strings.Builder
	b.WriteString(receiptPrefix(ReceiptKindBuild, r.Bin, r.Source))
	for _, f := range []struct{ name, val string }{{"resolved", r.Key}, {"sha256", r.Digest}} {
		if f.val != "" {
			b.WriteString(`,"` + f.name + `":` + jsonStringLiteral(f.val))
		}
	}
	if r.Bytes >= 0 {
		fmt.Fprintf(&b, `,"bytes":%d`, r.Bytes)
	}
	for _, f := range []struct{ name, val string }{
		{"path", r.Path}, {"platform", r.Platform}, {"revision", r.Revision},
		{"recipe", r.Recipe}, {"toolchain", r.Toolchain},
		{"fork", r.Fork}, {"series", r.Series}, {"tree", r.Tree}, {"tag", r.Tag}, {"version", r.Version},
	} {
		if f.val != "" {
			b.WriteString(`,"` + f.name + `":` + jsonStringLiteral(f.val))
		}
	}
	act := r.Act
	if act == "" {
		act = ReceiptActRecord
	}
	b.WriteString(`,"act":` + jsonStringLiteral(act))
	b.WriteString(`,"time":` + jsonStringLiteral(r.Time.UTC().Format(receiptTimeLayout)))
	b.WriteString("}")
	return b.String()
}

// ReadBuildReceipts parses a JSONL receipt log and returns its `kind:"build"` lines, in file order:
// ReadCaptureReceipts' twin, through the same parseReceiptLine and with its tolerance (an absent
// file is empty, an unparseable line is skipped).
func ReadBuildReceipts(path string) ([]BuildReceipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BuildReceipt
	for _, line := range splitLines(string(data)) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r, err := parseReceiptLine(line)
		if err != nil || r.Kind != ReceiptKindBuild {
			continue
		}
		br := BuildReceipt{
			Bin: r.Bin, Source: r.Declared, Key: r.Resolved, Digest: r.SHA256, Bytes: r.Bytes,
			Path: r.Path, Platform: r.Platform, Revision: r.Revision, Recipe: r.Recipe,
			Toolchain: r.Toolchain, Fork: r.Fork, Series: r.Series, Tree: r.Tree, Tag: r.Tag,
			Version: r.Version, Act: r.Act,
		}
		if t, terr := time.Parse(receiptTimeLayout, r.Time); terr == nil {
			br.Time = t
		}
		out = append(out, br)
	}
	return out, nil
}
