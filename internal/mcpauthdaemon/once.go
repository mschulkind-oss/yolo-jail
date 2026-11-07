package mcpauthdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

// onceWarn is where the one-sign-in tier's token-free lines go: the daemon's stderr, which is
// its log, or the sink of the launcher that runs the tier itself (BoundaryPass, ServeOnce).
func (b *broker) onceWarn(line string) {
	if b.warn != nil {
		b.warn(line)
		return
	}
	fmt.Fprintln(os.Stderr, "mcp-auth-broker:", line)
}

func (b *broker) newOnceBroker() (*mcpauth.OnceBroker, error) {
	store, err := b.onceStore()
	if err != nil {
		return nil, err
	}
	return &mcpauth.OnceBroker{Canonical: store,
		Audit: func(e mcpauth.Event) { b.auditEvents([]mcpauth.Event{e}) },
		Warn:  b.onceWarn}, nil
}

// passAll is one pass over every source, each source's problems joined.
func (b *broker) passAll(ctx context.Context, once *mcpauth.OnceBroker, sources []mcpauth.OnceSource) error {
	var problems []error
	for _, source := range sources {
		if err := once.Pass(ctx, source, b.share.Once); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// runOnce passes once at start and on every tick until stop closes. It runs after readiness,
// so a slow or unreachable authorization server never delays a launch.
func (b *broker) runOnce(stop <-chan struct{}) {
	if len(b.share.Once) == 0 || len(b.share.Sources) == 0 {
		return
	}
	once, err := b.newOnceBroker()
	if err != nil {
		b.onceWarn("one-sign-in state is unavailable; repair host state permissions and relaunch: " + err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	sources := b.onceSources()
	ticks, stopTicks := newOnceTicks()
	defer stopTicks()
	for {
		if err := b.passAll(ctx, once, sources); err != nil {
			b.onceWarn("one-sign-in pass: " + err.Error())
		}
		afterOncePass()
		select {
		case <-stop:
			return
		case <-ticks:
		}
	}
}

// launcherBroker is the one-sign-in tier's broker for a launcher that runs the tier itself,
// where no mcp-auth-broker daemon runs for the launch (ID-19): the same canonical store, audit
// log and pinned pack scripts the daemon uses, with its lines going to warn.
func launcherBroker(stateDir string, share mcpauth.Share, warn func(string)) (*broker, error) {
	if err := share.Validate(); err != nil {
		return nil, err
	}
	if len(share.Replicas) != 0 {
		return nil, errors.New("mcp-auth: a launcher runs only the one-sign-in tier; its share names no replica")
	}
	canonical, err := mcpauth.NewCanonicalStore(stateDir)
	if err != nil {
		return nil, err
	}
	b := &broker{share: share, canonical: canonical, warn: warn, seeded: map[string]uint64{},
		audit: brokeraudit.Open(filepath.Join(paths.BrokerDir(), "audit.jsonl"), func(message string) {
			if warn != nil {
				warn("audit warning: " + message)
			}
		})}
	if err := b.loadScripts(); err != nil {
		return nil, err
	}
	return b, nil
}

// BoundaryPass is the one-sign-in tier at a launch boundary (docs/design/mcp-auth-sharing.md
// §8.5, ID-19): ONE pass over the share's sources, run by the launcher before the agent starts
// and after it has ended, on a backend where no broker runs while the jail does. It adopts a
// sign-in the agent left with its refresh token, refreshes a record that is due and writes the
// access token back, exactly as the daemon's poll does; ctx bounds it.
func BoundaryPass(ctx context.Context, stateDir string, share mcpauth.Share, warn func(string)) error {
	if len(share.Once) == 0 || len(share.Sources) == 0 {
		return nil
	}
	b, err := launcherBroker(stateDir, share, warn)
	if err != nil {
		return err
	}
	once, err := b.newOnceBroker()
	if err != nil {
		return err
	}
	return b.passAll(ctx, once, b.onceSources())
}

// ServeOnce is the daemon's poll, run by a launcher that lives exactly as long as its session
// (a macos-user session, ID-20): a pass at once and on every tick until stop closes. It returns
// once the poll has stopped, so nothing of it outlives the session.
func ServeOnce(stateDir string, share mcpauth.Share, warn func(string), stop <-chan struct{}) error {
	if len(share.Once) == 0 || len(share.Sources) == 0 {
		return nil
	}
	b, err := launcherBroker(stateDir, share, warn)
	if err != nil {
		return err
	}
	b.runOnce(stop)
	return nil
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
