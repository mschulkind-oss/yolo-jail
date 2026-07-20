package checkcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// checkBrokerCredsFreshness ports _check_broker_creds_freshness with an INJECTED
// CLOCK (Options.Now). Grades the shared Claude credentials' expiry against
// wall-clock now, using the file mtime as a "time since last refresh" proxy.
func (o *Options) checkBrokerCredsFreshness(r *reporter) {
	credsPath := filepath.Join(paths.GlobalHome(), ".claude-shared-credentials", ".credentials.json")
	info, err := os.Stat(credsPath)
	if err != nil {
		return // first /login hasn't happened — nothing to grade
	}
	if info.Size() == 0 {
		return // documented pre-login placeholder
	}
	data, err := os.ReadFile(credsPath)
	if err != nil {
		r.warn(fmt.Sprintf("shared creds %s: unreadable", credsPath),
			"OSError: "+err.Error())
		return
	}
	expiresAtMS, ok := parseCredsExpiresAt(data)
	if !ok {
		r.warn(fmt.Sprintf("shared creds %s: unreadable", credsPath),
			"KeyError: 'claudeAiOauth'")
		return
	}

	now := o.Now()
	nowMS := now.UnixMilli()
	remainingS := int((expiresAtMS - nowMS) / 1000)
	mtimeAgeS := int(now.Sub(info.ModTime()).Seconds())
	if mtimeAgeS < 0 {
		mtimeAgeS = 0
	}

	lastWrite := ""
	if mtimeAgeS >= 0 {
		lastWrite = "last write " + nixdiag.FmtDuration(mtimeAgeS) + " ago"
	}

	switch {
	case remainingS < 0:
		msg := "shared creds expired " + nixdiag.FmtDuration(-remainingS) + " ago"
		if lastWrite != "" {
			msg += " (" + lastWrite + ")"
		}
		r.fail(msg,
			"Refreshes are not landing.  Run /login from inside a "+
				"jail to recover; check broker log at "+
				"~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log")
	case remainingS < 3600:
		msg := "shared creds expire in " + nixdiag.FmtDuration(remainingS)
		if lastWrite != "" {
			msg += " (" + lastWrite + ")"
		}
		r.warn(msg,
			"Approaching expiry without a refresh having landed.  "+
				"Healthy cadence keeps this above 1h.")
	default:
		suffix := ""
		if lastWrite != "" {
			suffix = ", " + lastWrite
		}
		r.ok("shared creds valid for " + nixdiag.FmtDuration(remainingS) + suffix)
	}
}

// parseCredsExpiresAt extracts int(data["claudeAiOauth"]["expiresAt"]) in ms.
func parseCredsExpiresAt(data []byte) (int64, bool) {
	decoded, err := jsonx.Decode(data)
	if err != nil {
		return 0, false
	}
	obj, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return 0, false
	}
	oauthV, _ := obj.Get("claudeAiOauth")
	oauth, ok := oauthV.(*jsonx.OrderedMap)
	if !ok {
		return 0, false
	}
	expV, ok := oauth.Get("expiresAt")
	if !ok {
		return 0, false
	}
	if n, ok := jsonx.AsInt(expV); ok {
		return n, true
	}
	if f, ok := expV.(float64); ok {
		return int64(f), true
	}
	return 0, false
}

// checkDiskUsage ports _check_disk_usage: surface yolo-jail's total on-disk
// footprint and nudge toward `yolo prune` over threshold. Never a fail.
func (o *Options) checkDiskUsage(r *reporter, config *jsonx.OrderedMap) {
	if o.inJail() {
		r.ok("Inside jail — disk-usage check skipped (runs host-side)")
		return
	}
	thresholdGB := 15.0
	if config != nil {
		if pruneV, _ := config.Get("prune"); pruneV != nil {
			if pruneCfg, ok := pruneV.(*jsonx.OrderedMap); ok {
				raw, _ := pruneCfg.Get("warn_threshold_gb")
				if f, ok := numberFloat(raw); ok && f > 0 {
					thresholdGB = f
				}
			}
		}
	}

	rt := o.detectRuntime()
	workspaces := o.findYoloWorkspaces(rt)
	total := diskUsageTotal(workspaces, paths.GlobalStorage())
	totalGB := float64(total) / (1024 * 1024 * 1024)
	human := prune.FmtBytes(total)
	if totalGB >= thresholdGB {
		r.warn(fmt.Sprintf("yolo-jail disk usage: %s (over %.0f GiB threshold)", human, thresholdGB),
			"Run `yolo prune` to see reclaim candidates, `yolo prune --apply` to execute")
	} else {
		r.ok(fmt.Sprintf("yolo-jail disk usage: %s (threshold %.0f GiB)", human, thresholdGB))
	}
}

// findYoloWorkspaces ports prune._find_yolo_workspaces: resolved workspace paths
// for every yolo-* container the runtime knows about (running or stopped). Any
// probe error → empty (check() swallows it).
func (o *Options) findYoloWorkspaces(rt string) []string {
	res := o.Exec([]string{rt, "ps", "-a", "--format", "{{.Names}}"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return nil
	}
	var names []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		n := strings.TrimSpace(line)
		if strings.HasPrefix(n, "yolo-") {
			names = append(names, n)
		}
	}
	var found []string
	seen := map[string]struct{}{}
	for _, name := range names {
		ws, ok := o.inspectWorkspaceMount(rt, name)
		if !ok {
			continue
		}
		resolved := ws
		if r, err := filepath.EvalSymlinks(ws); err == nil {
			resolved = r
		} else if abs, err := filepath.Abs(ws); err == nil {
			resolved = abs
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		found = append(found, resolved)
	}
	return found
}

// inspectWorkspaceMount ports prune._inspect_workspace_mount: the host path
// bound into /workspace for name.
func (o *Options) inspectWorkspaceMount(rt, name string) (string, bool) {
	res := o.Exec([]string{rt, "inspect", "--format", "{{json .Mounts}}", name}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return "", false
	}
	decoded, err := jsonx.Decode([]byte(res.Stdout))
	if err != nil {
		return "", false
	}
	mounts, ok := decoded.([]any)
	if !ok {
		return "", false
	}
	for _, m := range mounts {
		mm, ok := m.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		if dst, _ := mm.Get("Destination"); asString(dst) == "/workspace" {
			if src, _ := mm.Get("Source"); asString(src) != "" {
				return asString(src), true
			}
		}
	}
	return "", false
}

// diskUsageTotal runs the `total` key of prune._disk_usage_report: bytes under
// GLOBAL_STORAGE (dirs + stray files, symlinks skipped) plus each workspace's
// .yolo/ size.
func diskUsageTotal(workspaces []string, globalStorage string) int64 {
	var gs int64
	if isDir(globalStorage) {
		entries, err := os.ReadDir(globalStorage)
		if err == nil {
			for _, e := range entries {
				child := filepath.Join(globalStorage, e.Name())
				info, err := os.Lstat(child)
				if err != nil {
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					continue
				}
				if info.IsDir() {
					gs += dirSizeBytes(child)
				} else if info.Mode().IsRegular() {
					gs += info.Size()
				}
			}
		}
	}
	var ws int64
	for _, w := range workspaces {
		ws += dirSizeBytes(filepath.Join(w, ".yolo"))
	}
	return gs + ws
}

// dirSizeBytes ports prune._dir_size_bytes: sum of regular-file lstat sizes under
// p (followlinks=false). Missing path → 0.
func dirSizeBytes(p string) int64 {
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return 0
	}
	var total int64
	_ = filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			return nil
		}
		// os.walk sums filenames' lstat sizes; symlinks are counted (lstat), not
		// followed. filepath.Walk uses Lstat, so fi is already the link's info.
		total += fi.Size()
		return nil
	})
	return total
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// numberFloat mirrors `isinstance(raw, (int, float))`: returns the float value
// for a decoded JSON int or float. A bool is NOT accepted (Python's
// isinstance(True,(int,float)) is True, but warn_threshold_gb is never a bool in
// practice; matching int/float only is faithful for real config).
func numberFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	default:
		if n, ok := jsonx.AsInt(v); ok {
			return float64(n), true
		}
		return 0, false
	}
}
