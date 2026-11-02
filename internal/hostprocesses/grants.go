package hostprocesses

import (
	"fmt"
	"io"
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// grants.go is the daemon's whole decision (docs/design/loophole-access-approval.md §4.3, §4.6,
// §5; plan T8, F3, PD-12).
//
// # The grant file is the allowlist
//
// The file --settings names is this loophole's grant file, and the one kind, `view`, is the
// whole decision: its granted values are the process names yolo-ps may show, and a name not
// among them is invisible in every mode. Any other standing refuses the request, exit 64
// (PD-7), with loopholegrant's §5 text naming the next step; ps never runs.
//
// The loophole is flipped (PD-12): there is no `visible` setting any more, and a file with no
// `grants` key, no file, or no --settings at all grants nothing. Every request then fails closed
// with the not_allowed text. A `visible` key an older launch left in the file is never read.
//
// # The block is re-read at each request; the settings are not
//
// The package comment's freeze stays for the settings: `fields` is read once, at startup. The
// grant block is read again on every request, because `yolo config apply` rewrites it in the
// running launch's file (loopholegrant.Replace) and the daemon takes the new standing at its
// next request, as a broker takes a scope at its next call (design §4.6). That is not the hole
// the freeze closed: the old daemon re-read a workspace file the agent could edit, and the
// grant file sits in the loophole's private state dir, outside every mount, written only from
// a gate's approved result. A block that stops reading, or vanishes, fails closed.

// The grant this daemon serves. Access and the value noun are the manifest's own words, which
// the launch writes into the grant file; these are the fallback for a file that does not carry
// them (one an older launch wrote, or none at all).
const (
	grantKind      = "view"
	grantAccess    = "see host processes"
	grantValueNoun = "process names"
	grantPack      = "host-processes"
	grantClient    = "yolo-ps"
	// exitNoGrant is a grant refusal's exit status (plan PD-7).
	exitNoGrant = loopholegrant.RefusedExit
)

// grantedNames reads the grant block at path and returns the names it grants with the refusal
// a name outside them gets, or, when it grants none, the refusal to print instead.
func grantedNames(path string) (names map[string]struct{}, notCovered, refused string) {
	var b loopholegrant.Block
	if path != "" {
		if _, blk, err := loopholegrant.Read(path); err == nil && blk != nil {
			b = *blk
		}
	}
	k, ok := b.Kinds[grantKind]
	if !ok {
		return nil, "", refusal(b, k, "", nil)
	}
	if k.Standing != loopholegrant.Granted {
		return nil, "", refusal(b, k, k.Standing, k.Values)
	}
	names = map[string]struct{}{}
	for _, v := range k.Values {
		if v != "" {
			names[v] = struct{}{}
		}
	}
	// A name outside the grant is refused without being said, since an ungranted name is
	// invisible: the step carries the noun, not the name.
	notCovered = refusal(b, k, loopholegrant.Granted, nil)
	if len(names) == 0 {
		return nil, "", notCovered
	}
	return names, notCovered, ""
}

// refusal is §5's text for a standing, in the words the grant file carries; an empty standing
// is a file that says nothing about view, which fails closed with the not_allowed text.
func refusal(b loopholegrant.Block, k loopholegrant.Kind, standing loopholegrant.Standing, values []string) string {
	pack := b.Pack
	if pack == "" {
		pack = grantPack
	}
	access := b.Access
	if access == "" {
		access = grantAccess
	}
	noun := k.ValueNoun
	if noun == "" {
		noun = grantValueNoun
	}
	return loopholegrant.Refusal{
		Client: grantClient, Pack: pack, Access: access, Kind: grantKind,
		ValueNoun: noun, Dir: b.Workspace, Values: values, Standing: standing,
	}.Text()
}

// grantBlockAt reads the grant file at path's block (nil when it has none).
func grantBlockAt(path string) (*loopholegrant.Block, error) {
	_, b, err := loopholegrant.Read(path)
	return b, err
}

// grantCheck is the self-check's report on a grant file that exists: FAIL when it does not
// read, since the daemon then refuses every request, and otherwise the view kind's standing.
func grantCheck(path string, out io.Writer) int {
	b, err := grantBlockAt(path)
	if err != nil {
		fmt.Fprintln(out, "FAIL: the grant file at "+path+" does not read, so the daemon refuses "+
			"every yolo-ps request. Restart the jail: the launch rewrites it.")
		return 1
	}
	if b == nil {
		// A file an older launch wrote, before this loophole declared its grant: the daemon
		// refuses every request until the next launch writes the block.
		fmt.Fprintln(out, "OK: the file at "+path+" carries no grant block, so yolo-ps is refused "+
			"until the next launch writes one")
		return 0
	}
	k, ok := b.Kinds[grantKind]
	switch {
	case !ok:
		fmt.Fprintln(out, "OK: grant file at "+path+" declares no "+grantKind+" kind; yolo-ps is refused")
	case k.Standing == loopholegrant.Granted:
		fmt.Fprintln(out, "OK: grant file at "+path+" grants "+grantKind+" for "+
			strconv.Itoa(len(k.Values))+" process names")
	default:
		fmt.Fprintln(out, "OK: grant file at "+path+": "+grantKind+" is "+string(k.Standing)+
			"; yolo-ps is refused naming the next step")
	}
	return 0
}
