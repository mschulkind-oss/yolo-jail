package mcpauthdaemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

// The one-sign-in tier's poll (MA-D10, MA-D17, docs/design/mcp-auth-sharing.md §8.4): every
// jail's broker passes over the share's sources on this interval, which is far inside §8.4's
// 10-minute refresh margin, so a due refresh is never missed by more than one interval.
var onceInterval = 30 * time.Second

// sourceLockWait bounds a write-back's wait for the agent's own lock; a write-back that loses
// is retried on the next pass, so it never needs to wait long.
var sourceLockWait = 2 * time.Second

// newOnceTicks and afterOncePass are the poll's clock and a test's view of each finished pass.
var (
	newOnceTicks = func() (<-chan time.Time, func()) {
		ticker := time.NewTicker(onceInterval)
		return ticker.C, ticker.Stop
	}
	afterOncePass = func() {}
)

func setOnceSeams(ticks <-chan time.Time, passed func()) func() {
	oldTicks, oldPassed := newOnceTicks, afterOncePass
	newOnceTicks = func() (<-chan time.Time, func()) { return ticks, func() {} }
	afterOncePass = passed
	return func() { newOnceTicks, afterOncePass = oldTicks, oldPassed }
}

func (b *broker) onceStore() (*mcpauth.CanonicalStore, error) {
	return mcpauth.NewOnceStore(filepath.Join(b.canonical.Dir, "once"))
}

// onceSources builds each source with its pack's match function, from the derive pinned at
// startup.
func (b *broker) onceSources() []mcpauth.OnceSource {
	sources := make([]mcpauth.OnceSource, 0, len(b.share.Sources))
	for _, source := range b.share.Sources {
		script := b.scripts[source.Pack]
		pack := source.Pack
		sources = append(sources, mcpauth.OnceSource{Pack: pack, Store: source.SourceStore(sourceLockWait), Fields: source.Decl.Fields,
			Match: func(key string, raw json.RawMessage) (string, string, bool) {
				var entry map[string]any
				if json.Unmarshal(raw, &entry) != nil || entry == nil || script == "" {
					return "", "", false
				}
				ref, err := luahook.MCPAuthMatch(script, pack, key, entry)
				if err != nil || ref == nil {
					return "", "", false
				}
				return ref.URL, ref.Issuer, true
			}})
	}
	return sources
}

// runOnce passes once at start and on every tick until stop closes. It runs after readiness,
// so a slow or unreachable authorization server never delays a launch.
func (b *broker) runOnce(stop <-chan struct{}) {
	if len(b.share.Once) == 0 || len(b.share.Sources) == 0 {
		return
	}
	store, err := b.onceStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: one-sign-in state is unavailable; repair host state permissions and relaunch:", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	once := &mcpauth.OnceBroker{Canonical: store,
		Audit: func(e mcpauth.Event) { b.auditEvents([]mcpauth.Event{e}) },
		Warn:  func(line string) { fmt.Fprintln(os.Stderr, "mcp-auth-broker:", line) }}
	sources := b.onceSources()
	ticks, stopTicks := newOnceTicks()
	defer stopTicks()
	for {
		for _, source := range sources {
			if err := once.Pass(ctx, source, b.share.Once); err != nil {
				fmt.Fprintln(os.Stderr, "mcp-auth-broker: one-sign-in pass:", err)
			}
		}
		afterOncePass()
		select {
		case <-stop:
			return
		case <-ticks:
		}
	}
}

// onceStatus is the status rows for the one-sign-in servers: never a token.
func (b *broker) onceStatus() ([]map[string]any, error) {
	rows := make([]map[string]any, 0, len(b.share.Once))
	if len(b.share.Once) == 0 || len(b.share.Sources) == 0 {
		return rows, nil
	}
	store, err := b.onceStore()
	if err != nil {
		return nil, err
	}
	// Status never creates or repairs state (ReadStatus); no lock yet means nothing adopted yet.
	var file mcpauth.CanonicalFile
	if _, statErr := os.Lstat(filepath.Join(store.Dir, store.LockName)); statErr == nil {
		if file, err = store.ReadStatus(); err != nil {
			return nil, err
		}
	}
	hints := make([]string, 0, len(b.share.Sources))
	for _, source := range b.share.Sources {
		hints = append(hints, source.Decl.LoginHint)
	}
	for _, server := range b.share.Once {
		row := map[string]any{"name": server.Name, "scope": "user", "sign_in": "once", "state": "not-signed-in",
			"next_step": strings.ReplaceAll(strings.Join(hints, "; or "), "{name}", server.Name)}
		for _, record := range file.Records {
			if record.Server == server.Name && record.Identity == server.Identity {
				row["state"], row["generation"], row["expires_at_ms"], row["adopted_from"] = record.State, int64(record.Generation), record.ExpiresAtMS, record.AdoptedFrom
				if record.State == "live" {
					row["next_step"] = ""
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// anyRows hands rows to the session's JSON writer, which encodes []any and map[string]any but
// not a typed slice of maps.
func anyRows(rows []map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out
}
