// relocate.go is the RECORDING half of relocation: finding every absolute reference to the
// capture-time HOME that a later materialize would have to rewrite, and deciding whether the
// entry may be materialized into a different home at all.
//
// # Why this exists, and only for one backend
//
// On the container backends the capture home and the materialize home are the same string —
// /home/agent, both times — so an absolute self-reference an installer embeds is still correct
// after materialization and there is nothing to rewrite (program-delivery.md §6.3: "on the
// container backends this problem is absent by construction").
//
// macos-user has no ephemeral home to capture into: its home is one persistent, machine-constant
// /Users/_yolojail shared by every workspace and session, and splitting it is a refused design
// point (internal/cli/run/run.go:235-250). So a capture there runs against a THROWAWAY STAGING
// HOME under a narrowed Seatbelt profile (internal/macosuser.SeatbeltCaptureProfile) and the
// staging path is not the final home path. Every absolute reference the installer embedded now
// names a directory that will not exist — claude's ~/.local/bin/claude is an absolute symlink
// into its own versions dir (MEASURED in this jail via `ls -l`) — so either the manifest records
// them all and materialize rewrites them, or the entry says it cannot be moved.
//
// # What is MEASURED here and what is not
//
// Everything in this file is exercised by unit tests against real files in a temp dir on the
// machine running them: the scan, the text/binary classification, and the relocatable decision
// are MEASURED. What is NOT measured anywhere is the macOS side that makes them necessary — no
// Seatbelt profile has been loaded by a kernel, and this backend's installer pipeline is itself
// unverified on hardware (docs/design/macos-user-nix-and-features.md). See seatbeltcapture.go.
package capture

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// scanChunk is the read window for the content scan, and overlap is how much of the previous
// window is re-examined so a reference straddling two reads is still found. A needle of length
// n is found by any scanner that keeps n-1 bytes of context, so the overlap is derived from the
// prefix rather than guessed.
const scanChunk = 64 << 10

// binarySniff is how many leading bytes decide text-vs-binary, and a NUL in them is the whole
// test. This is git's heuristic (`buffer_is_binary`, 8000 bytes), adopted rather than invented:
// it is the one every developer on this repo has already internalised from `git diff` calling a
// file binary, so a capture that refuses to relocate agrees with a tool they can run themselves.
const binarySniff = 8000

// scanContentRefs finds every regular file in the tree whose BYTES contain the capture-time home
// prefix, and reports which of them could not be prefix-rewritten safely.
//
// One ref per FILE, not per occurrence: the rewrite is a byte-level substitution of one prefix
// for another across the whole file, so a second entry for the same file would be a second
// instruction to do the thing already done. AbsoluteRef.Value is the PREFIX that was found —
// the string a rewrite substitutes — which is the same shape a symlink ref carries (the
// reference verbatim, as yolo saw it) even though the carrier differs.
//
// The second return is the not-relocatable reasons: one per file that carries a reference and
// is NOT text. A prefix substitution in a Mach-O binary or a compiled cache is not a string
// edit — the path may be length-prefixed, offset-referenced, or checksummed — and Homebrew's
// answer on this exact OS was to pad the prefix to a fixed width rather than to rewrite it in
// place. yolo does not have that option (the staging path is not padded), so a reference inside
// a binary makes the whole entry non-relocatable, which is the fail-safe direction: refusing to
// materialize costs a re-capture, and a half-rewritten binary costs a corrupt program nobody
// traces back to here.
//
// Symlinks are NOT read here. describeTree already records their targets (RefSymlinkTarget) by
// readlink, and reading one as a file would follow it out of the tree.
func scanContentRefs(tree, home string) ([]AbsoluteRef, []string, error) {
	if home == "" {
		return nil, nil, nil
	}
	needle := []byte(home)
	var refs []AbsoluteRef
	var blockers []string
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		hit, binary, serr := fileHasPrefix(p, needle)
		if serr != nil {
			return serr
		}
		if !hit {
			return nil
		}
		rel, rerr := filepath.Rel(tree, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		refs = append(refs, AbsoluteRef{Path: rel, Kind: RefFileContent, Value: home})
		if binary {
			blockers = append(blockers, fmt.Sprintf(
				"%s is not text and embeds %s — a prefix substitution in a binary is not a "+
					"string edit (the path may be length-prefixed, offset-referenced or "+
					"checksummed), so this capture cannot be materialized into another home",
				rel, home))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	sort.Strings(blockers)
	return refs, blockers, nil
}

// fileHasPrefix reports whether the file's bytes contain needle, and whether the file looks
// BINARY (a NUL byte in its first binarySniff bytes).
//
// Streamed in windows with an overlap, never read whole: a captured tree is gigabytes (claude:
// 1.2 GB, measured 2026-09-03) and a scan that held one file in memory would be the second
// thing this subsystem does that costs what it exists to save. The overlap is len(needle)-1, so
// a reference split across two reads is still found — the one bug a chunked search has.
func fileHasPrefix(path string, needle []byte) (found, binary bool, err error) {
	if len(needle) == 0 {
		return false, false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer f.Close()

	overlap := len(needle) - 1
	buf := make([]byte, scanChunk+overlap)
	carried := 0
	// sniffed counts bytes of the FILE already examined for a NUL, so a short first read
	// cannot make the second window re-sniff the carried tail or overshoot the 8000-byte
	// window. It is the file offset the sniff has reached, not the offset the search has.
	var sniffed int
	for {
		n, rerr := f.Read(buf[carried:])
		if n > 0 {
			window := buf[:carried+n]
			// The NUL sniff looks only at the file's opening bytes. `carried` of this
			// window is already-sniffed tail, so skip it and cap at what is left of the
			// 8000.
			if sniffed < binarySniff {
				sniff := window[carried:]
				if lim := binarySniff - sniffed; lim < len(sniff) {
					sniff = sniff[:lim]
				}
				if bytes.IndexByte(sniff, 0) >= 0 {
					binary = true
				}
				sniffed += len(sniff)
			}
			if bytes.Contains(window, needle) {
				found = true
			}
			if found && binary {
				return true, true, nil
			}
			// Carry the tail forward so a needle straddling this boundary is seen next
			// round. Everything before it has already been searched.
			if len(window) > overlap {
				carried = copy(buf, window[len(window)-overlap:])
			} else {
				carried = len(window)
			}
		}
		if rerr == io.EOF {
			return found, binary, nil
		}
		if rerr != nil {
			return found, binary, rerr
		}
	}
}

// decideRelocatable turns a completed scan into the manifest's relocatable verdict and, when it
// is false, the reasons.
//
// THE DEFAULT IS NO. A capture is relocatable only when the FULL scan ran and found nothing it
// could not rewrite; a symlink-targets-only scan says "no absolute reference was found in a
// symlink", which is not the same claim as "there is no absolute reference". That distinction
// is the whole reason RefScan is recorded rather than inferred: an older manifest, or one from a
// container capture that never needed to look, reads back with an empty RefScan and therefore
// relocatable:false — which is exactly right, because a materialize into a DIFFERENT home is
// the only thing the flag gates and neither of those was ever meant to be moved.
func decideRelocatable(scan string, blockers []string) (bool, []string) {
	if scan != RefScanFull {
		return false, []string{"absolute references in file CONTENTS were not enumerated " +
			"(refScan=" + scanLabel(scan) + "), so this capture may only be materialized " +
			"into the home it was captured in"}
	}
	if len(blockers) > 0 {
		return false, blockers
	}
	return true, nil
}

// scanLabel renders a RefScan value for a message, naming the empty one rather than printing
// nothing — an error that reads "refScan=" sends the reader looking for a truncated string.
func scanLabel(scan string) string {
	if scan == "" {
		return "unrecorded"
	}
	return scan
}
