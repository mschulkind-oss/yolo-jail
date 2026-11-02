package hostprocesses

import (
	"fmt"
	"io"
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// grants.go is the daemon's grant half (docs/design/loophole-access-approval.md §4.3, §4.6,
// §5; plan T8, PD-12).
//
// # A grant file replaces the allowlist
//
// When the file --settings names carries a `grants` block, the launch wrote it as this
// loophole's grant file, and the one kind, `view`, is the whole decision: its granted values
// are the process names yolo-ps may show, and a name not among them is invisible in every
// mode. A `visible` setting beside the block widens nothing. Any other standing refuses the
// request, exit 64 (PD-7), with loopholegrant's §5 text naming the next step; ps never runs.
//
// A file with no `grants` block keeps today's allowlist, the frozen `visible` setting, until
// the host-processes flip deletes that path (PD-12).
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

// The grant this daemon serves, as the host-processes manifest declares it (plan F3).
const (
	grantKind      = "view"
	grantAccess    = "see host processes"
	grantValueNoun = "process names"
	grantPack      = "host-processes"
	grantClient    = "yolo-ps"
	// exitNoGrant is a grant refusal's exit status (plan PD-7).
	exitNoGrant = 64
)

// grantedNames reads the grant block at path and returns the names it grants, or the refusal
// to print when it grants none.
func grantedNames(path string) (map[string]struct{}, string) {
	_, b, err := loopholegrant.Read(path)
	if err != nil || b == nil {
		return nil, refusal(loopholegrant.Block{}, "", nil)
	}
	k, ok := b.Kinds[grantKind]
	if !ok {
		return nil, refusal(*b, "", nil)
	}
	if k.Standing != loopholegrant.Granted {
		return nil, refusal(*b, k.Standing, k.Values)
	}
	names := map[string]struct{}{}
	for _, v := range k.Values {
		if v != "" {
			names[v] = struct{}{}
		}
	}
	if len(names) == 0 {
		return nil, uncovered(*b)
	}
	return names, ""
}

// refusal is §5's text for a standing; an empty standing is a file that says nothing about
// view, which fails closed with the not_allowed text.
func refusal(b loopholegrant.Block, standing loopholegrant.Standing, values []string) string {
	pack := b.Pack
	if pack == "" {
		pack = grantPack
	}
	return loopholegrant.Refusal{
		Client: grantClient, Pack: pack, Access: grantAccess, Kind: grantKind,
		ValueNoun: grantValueNoun, Dir: b.Workspace, Values: values, Standing: standing,
	}.Text()
}

// uncovered is the refusal for a call the granted names do not cover. It names no process:
// an ungranted name is invisible, so the step carries the noun, not the name.
func uncovered(b loopholegrant.Block) string {
	return refusal(b, loopholegrant.Granted, nil)
}

// grantBlockAt reads the grant file at path's block (nil when it has none).
func grantBlockAt(path string) (*loopholegrant.Block, error) {
	_, b, err := loopholegrant.Read(path)
	return b, err
}

// grantCheck is the self-check's report on a grant file: FAIL when its block does not read,
// since the daemon then refuses every request, and otherwise the view kind's standing.
func grantCheck(path string, out io.Writer) int {
	b, err := grantBlockAt(path)
	if err != nil || b == nil {
		fmt.Fprintln(out, "FAIL: the grant file at "+path+" does not read, so the daemon refuses "+
			"every yolo-ps request. Restart the jail: the launch rewrites it.")
		return 1
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
