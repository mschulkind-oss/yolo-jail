package agents

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/agents/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// gatedSkills are built-in skills staged only when the workspace is the
// yolo-jail source tree (includeDev). The keys are top-level dir names in
// builtinskills.FS.
var gatedSkills = map[string]bool{
	"developing-yolo-jail": true,
}

// writeBuiltinSkills copies the embedded built-in skill trees into dst,
// skipping gated skills unless includeDev is true. dst is an agent's already-
// cleared skills-staging dir; existing entries are not removed here (the caller
// clears inside dst first, preserving its inode for the live bind mount).
//
// NOTE: returning nil for a directory in fs.WalkDir does NOT prune it — a gated
// subtree must be skipped with fs.SkipDir.
func writeBuiltinSkills(dst string, includeDev bool) error {
	return fs.WalkDir(builtinskills.FS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		// Top-level dir == a skill name; gate the whole subtree.
		if d.IsDir() && !filepath.IsAbs(p) && !containsSep(p) {
			if gatedSkills[p] && !includeDev {
				return fs.SkipDir
			}
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

// containsSep reports whether p contains a path separator (embed.FS always uses
// "/"), i.e. p is nested rather than a top-level skill dir.
func containsSep(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			return true
		}
	}
	return false
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

// ReadProvisioningFailed reports whether workspace/.yolo/startup.log exists and
// contains "PROVISIONING FAILED". A read error → false.
func ReadProvisioningFailed(workspace string) bool {
	data, err := os.ReadFile(filepath.Join(workspace, ".yolo", "startup.log"))
	if err != nil {
		return false
	}
	return containsSub(string(data), "PROVISIONING FAILED")
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
