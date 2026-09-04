package capture

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ManifestName is the delta manifest's filename inside an ENTRY-SHAPED directory — the
// admitted `entries/<key>/`, or the proto-entry the driver builds under `staging/<id>/`.
//
// It sits BESIDE `tree/`, never inside it, and that is the whole reason the entry is one
// level deeper than packsrc's `trees/<sha>/` (slice 1's second correction): materialize
// hardlinks `tree/` WHOLESALE, so yolo's own metadata must not be in the thing being copied
// or every workspace would grow a stray file the vendor never installed.
const ManifestName = "capture-manifest.json"

// ManifestSchema is the version of the on-disk manifest format written by this yolo.
//
// IT IS A VERSION BOUNDARY, and ReadManifest refuses anything HIGHER — the same discipline
// as the sibling pack lockfile (`packsrc/lock.go:30`), for the same reason: a newer yolo may
// add a field whose absence changes what the file means, and a reader that shrugged at the
// version would materialize a tree it only half understood. A LOWER version is not refused
// here because there is no lower version yet; the first one added must decide what it means.
const ManifestSchema = 1

// Entry kinds, as they appear in a manifest. Deliberately the three treedigest distinguishes
// (`d`/`f`/`l`), spelled out — a manifest is read by humans as often as by yolo.
const (
	KindDir     = "dir"
	KindFile    = "file"
	KindSymlink = "symlink"
)

// RefSymlinkTarget is the one kind of absolute reference this slice gathers: a symlink whose
// target points into the capture-time HOME. It is a named constant rather than a bare string
// because file CONTENT references are the obvious second kind — claude's launcher shim embeds
// its own path — and slice 6 (macos-user relocation) is where the cost of a full read pass to
// find them is worth paying. Nothing here scans file bytes, and the manifest says so by
// carrying only this kind.
const RefSymlinkTarget = "symlink-target"

// Manifest is the FILE MANIFEST member of §6.3's receipt tuple: what the installer left
// behind, as paths relative to the jail HOME, plus what a relocation would have to rewrite.
//
// It is deliberately NOT a second receipt. The receipt (`.yolo/receipts.jsonl`) records the
// act — declaration, installer URL, capture hash, platform, time; this records the CONTENT,
// and it lives with the content rather than with the workspace that caused it. Nothing about
// the run that produced it is in here, so two byte-identical captures produce two
// byte-identical manifests.
type Manifest struct {
	// Schema is ManifestSchema. See its doc: higher is refused, not tolerated.
	Schema int `json:"schema"`
	// Home is the HOME the capture ran against — the STAGING PREFIX on macos-user, and
	// plain /home/agent on the container backends. Recorded because it is the string
	// AbsoluteRefs are references TO: a relocating materialize needs to know what to
	// rewrite them from, and it cannot re-derive it from a tree it did not capture.
	Home string `json:"home"`
	// Platform is "<GOOS>/<GOARCH>" as observed by the DRIVER, which is the only party
	// that can observe it: a capture made from a Mac through podman runs in a Linux jail,
	// so the host's platform is the wrong answer and the jail's is not derivable from
	// outside. §6.3's "captures are per-platform (and only for platforms we can run)" has
	// nowhere else to be written down — the store key is content-addressed, so two
	// platforms produce two keys but neither key says which.
	Platform string `json:"platform"`
	// Surfaces are the home-relative roots that were walked, in walk order
	// (paths.HomeSurfaces). A delta is only ever honest about the surfaces it looked at,
	// so the manifest says which those were rather than leaving a reader to assume.
	Surfaces []string `json:"surfaces"`
	// Excluded are the home-relative subtrees the delta never contains even though a
	// surface covers them — DefaultExcludes(), which is yolo's own state dir.
	//
	// Recorded for the same reason Surfaces is: a delta is honest only about what it
	// looked at, and "the capture surfaces MINUS these" is the whole of what it looked
	// at. A reader who finds no `.local/share/yolo-jail` in a tree should be able to see
	// that it was subtracted rather than merely absent.
	Excluded []string `json:"excluded"`
	// Entries is the delta, sorted by Path: every directory, file and symlink now in
	// tree/. Sorted so the file is diffable and a re-capture of an identical install
	// produces an identical manifest.
	Entries []ManifestEntry `json:"entries"`
	// AbsoluteRefs are the places in the tree that name Home absolutely. Empty on the
	// container backends by construction (capture home and materialize home are both
	// /home/agent), and the input to slice 6's relocate-or-refuse decision everywhere
	// else. Gathered now because gathering them during the walk the manifest already
	// does is nearly free, and re-walking a 1.2 GB tree later is not.
	AbsoluteRefs []AbsoluteRef `json:"absoluteRefs"`
}

// ManifestEntry is one path in the captured tree.
//
// The fields are the file manifest and nothing else — no provenance, no per-entry flags.
// Adding one is a schema decision, not an implementation detail: install-capture.md's
// Blockers say to stop and ask before adding per-entry metadata a later yolo must parse,
// and Schema above is the boundary that makes the asking enforceable.
type ManifestEntry struct {
	// Path is home-relative and slash-separated (".local/bin/claude"), which is also its
	// path inside tree/. Slash-separated on every platform: a manifest is compared
	// against manifests, and a captured path is a jail path.
	Path string `json:"path"`
	// Kind is KindDir, KindFile or KindSymlink.
	Kind string `json:"kind"`
	// Mode is the permission bits as four octal digits ("0755"). Empty for a symlink,
	// whose own mode is meaningless (lchmod is not portable and nothing reads it).
	Mode string `json:"mode,omitempty"`
	// Size is the file's byte count; omitted for directories and symlinks.
	Size int64 `json:"size,omitempty"`
	// Target is a symlink's target, verbatim from readlink and NEVER followed — the same
	// rule treedigest states, and for the second of its two reasons: an installer's tree
	// is full of self-references, and following them would record whatever else happens
	// to be installed on the capture machine.
	Target string `json:"target,omitempty"`
}

// AbsoluteRef is one reference in the captured tree that names the capture-time HOME
// absolutely — the thing that makes a tree non-relocatable until it is rewritten.
type AbsoluteRef struct {
	// Path is the home-relative path of the entry CARRYING the reference.
	Path string `json:"path"`
	// Kind is RefSymlinkTarget.
	Kind string `json:"kind"`
	// Value is the reference verbatim, so a rewrite can be a prefix substitution on a
	// string yolo actually saw rather than one it reconstructed.
	Value string `json:"value"`
}

// TreeDir is the captured tree inside an entry-shaped directory — an admitted
// `entries/<key>/` or the proto-entry the driver builds under `staging/<id>/`.
//
// The two are the same shape on purpose: the driver produces exactly what Admit consumes,
// so admission is `Admit(TreeDir(out))` plus moving the manifest up beside it, with nothing
// in between that could file a manifest against a tree it does not describe.
func TreeDir(root string) string { return filepath.Join(root, treeLeaf) }

// ManifestPath is the manifest inside an entry-shaped directory.
func ManifestPath(root string) string { return filepath.Join(root, ManifestName) }

// ReceiptsName is the capture receipt log inside an ADMITTED entry — the same JSONL
// schema, and the same filename, as the per-workspace `.yolo/receipts.jsonl` a generated
// installer appends to.
//
// BESIDE THE ENTRY, not in a workspace, because program-delivery.md §6.3 says so in as
// many words ("a machine-local receipt beside the CAS entry, not a repo-committed
// lockfile") and because the thing recorded is machine-wide: the capture jail's workspace
// is thrown away with the staging dir, and the invoking workspace merely happened to be
// where a human stood. Same schema, same name, different scope — not a second record.
const ReceiptsName = "receipts.jsonl"

// ReceiptsPath is the receipt log inside an entry-shaped directory.
func ReceiptsPath(root string) string { return filepath.Join(root, ReceiptsName) }

// TotalBytes is the sum of the manifest's regular-file sizes — what a capture COSTS, and
// the number the whole subsystem exists to stop paying per workspace.
func (m *Manifest) TotalBytes() int64 {
	var n int64
	for _, e := range m.Entries {
		if e.Kind == KindFile {
			n += e.Size
		}
	}
	return n
}

// WriteManifest writes m to ManifestPath(root), pretty-printed and newline-terminated.
//
// Indented because this file is read by humans during exactly the debugging session where
// a capture went wrong, and it is kilobytes against a tree of gigabytes.
func WriteManifest(root string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ManifestPath(root), append(b, '\n'), 0o644)
}

// ReadManifest reads the manifest from an entry-shaped directory.
//
// A manifest written by a NEWER yolo is a hard error naming the upgrade, never a
// best-effort parse: see ManifestSchema.
func ReadManifest(root string) (*Manifest, error) {
	b, err := os.ReadFile(ManifestPath(root))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("capture manifest %s: %w", ManifestPath(root), err)
	}
	if m.Schema > ManifestSchema {
		return nil, fmt.Errorf("capture manifest %s is schema %d and this yolo understands "+
			"%d — upgrade yolo rather than reading it partially",
			ManifestPath(root), m.Schema, ManifestSchema)
	}
	return &m, nil
}

// describeTree walks a captured tree and reports what is in it, plus every absolute
// reference into home.
//
// It walks the RESULT rather than accumulating as the move goes, so the manifest describes
// what is actually on disk — including the ancestor directories the move had to create,
// which no delta record knows about. A manifest built from intentions would be the first
// thing to disagree with the tree it names.
func describeTree(tree, home string) ([]ManifestEntry, []AbsoluteRef, error) {
	var entries []ManifestEntry
	var refs []AbsoluteRef
	err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(tree, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil // the tree root is not an entry; it IS the home
		}
		rel = filepath.ToSlash(rel)
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			entries = append(entries, ManifestEntry{Path: rel, Kind: KindSymlink, Target: target})
			if underPrefix(home, target) {
				refs = append(refs, AbsoluteRef{Path: rel, Kind: RefSymlinkTarget, Value: target})
			}
		case d.IsDir():
			entries = append(entries, ManifestEntry{Path: rel, Kind: KindDir, Mode: octal(info)})
		default:
			entries = append(entries, ManifestEntry{
				Path: rel, Kind: KindFile, Mode: octal(info), Size: info.Size(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	return entries, refs, nil
}

// octal renders the permission bits as four octal digits.
func octal(info fs.FileInfo) string {
	return "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
}

// underPrefix reports whether an ABSOLUTE reference names prefix or something beneath it.
//
// Lexical and path-component-aware: "/home/agentx" is not under "/home/agent". A relative
// target is never a reference to the prefix — it is the relocatable case, and reporting it
// would hand slice 6 a rewrite it must not make.
func underPrefix(prefix, ref string) bool {
	if prefix == "" || !filepath.IsAbs(ref) {
		return false
	}
	prefix = filepath.Clean(prefix)
	clean := filepath.Clean(ref)
	return clean == prefix || strings.HasPrefix(clean, prefix+string(filepath.Separator))
}
