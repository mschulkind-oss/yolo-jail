package jailcontent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// writeBuiltinSkills copies the embedded built-in skill trees into dst. dst is an already-
// cleared skills-staging dir; existing entries are not removed here (the caller
// clears inside dst first, preserving its inode for the live bind mount).
func writeBuiltinSkills(dst string) error {
	return fs.WalkDir(builtinskills.FS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(dst, p)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := builtinskills.FS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// WriteBriefing writes content to path, truncating in place to preserve the
// inode a running jail's bind mount captured — EXCEPT when the file is
// multi-linked (st_nlink > 1, e.g. after a `yolo prune` hardlink-dedup), in
// which case it unlinks first so a fresh inode is allocated (breaking the link
// rather than clobbering every fused sibling).
func WriteBriefing(path, content string) error {
	if fi, err := os.Lstat(path); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			_ = os.Remove(path) // best-effort: ignore removal errors
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadProvisioningFailed reports whether the workspace's provisioning log exists and
// records a failure. A read error → false.
//
// THE PATH AND THE LITERAL BOTH COME FROM THE PRODUCER'S PACKAGE, and that is the point
// of importing it for two one-line values: this reader and the stage that writes the log
// run in different processes on different backends, so nothing but a shared definition
// can make them agree. Both used to be spelled out here, beside a second spelling in
// internal/cli/run — a rename on either side would have left the briefing reporting a
// failed provision as healthy, with every test green.
func ReadProvisioningFailed(workspace string) bool {
	data, err := os.ReadFile(provision.StartupLog(workspace))
	if err != nil {
		return false
	}
	return containsSub(string(data), provision.FailedMarker)
}

func containsSub(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

// intString returns the base-10 string of v when v is an integer value (a
// jsonx-decoded int or a native Go int/int64) — used to classify a
// forward_host_ports entry.
func intString(v any) (string, bool) {
	if jsonx.IsInt(v) {
		n, _ := jsonx.AsInt(v)
		return strconv.FormatInt(n, 10), true
	}
	switch n := v.(type) {
	case int:
		return strconv.Itoa(n), true
	case int64:
		return strconv.FormatInt(n, 10), true
	}
	return "", false
}

// pyValue renders a resources map value as it appears in the briefing: strings
// verbatim; ints without ".0"; anything else via a plain format.
func pyValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if s, ok := intString(v); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
