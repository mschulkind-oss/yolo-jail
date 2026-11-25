package check

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// reportDetachedConfig warns when the user config at path is a regular file with a
// <path>.pre-migrate symlink beside it: the shape `yolo config migrate` leaves for a config that
// was a link (RC-D41), which no longer follows the link's target. It only reads the link text
// (Readlink); it never resolves the target and never moves anything. A warning, not a failure:
// the config works, it may just be stale against what the link pointed at.
func (o *Options) reportDetachedConfig(r *reporter, path string) {
	cfg, err := os.Lstat(path)
	if err != nil || !cfg.Mode().IsRegular() {
		return
	}
	kept := path + ".pre-migrate"
	text, err := os.Readlink(kept)
	if err != nil {
		return
	}
	detached := path + ".detached"
	r.warn(termsafe.Visible(path)+" is a regular file, no longer the link it was (kept at "+
		termsafe.Visible(kept)+", pointing at "+termsafe.Visible(text)+"); edits to that link's target do not reach it",
		"To follow the link's target again, first copy any changes you want from "+termsafe.Visible(path)+
			" into the target, then run these two commands, one at a time:\n"+
			"  "+shquote.Join([]string{"mv", path, detached})+"\n"+
			"  "+shquote.Join([]string{"mv", kept, path}))
}
