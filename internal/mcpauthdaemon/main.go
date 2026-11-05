// Package mcpauthdaemon owns one launch's host-only MCP replica seed and exit harvest.
package mcpauthdaemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

var lockWait = 10 * time.Second

// Startup has one absolute budget across every replica's adoption and seed.
// The selected host service allows 45 seconds before declaring readiness failure.
var startupBudget = 40 * time.Second

type broker struct {
	share     mcpauth.Share
	canonical *mcpauth.CanonicalStore
	audit     *brokeraudit.Log
	// The seed's generation fences an exit sign-out against another jail's newer sign-in.
	seeded map[string]uint64
	// Match the seed's native entry too: sign-out then sign-in can reuse generation 1.
	seededEntry map[string]json.RawMessage
	// Immutable selected pack scripts are read before the jail/pack tree can retire.
	scripts         map[string]string
	startupDeadline time.Time
}

func Main(args []string) int {
	flags := flag.NewFlagSet("mcp-auth-broker", flag.ContinueOnError)
	socket := flags.String("socket", "", "fronted Unix socket")
	state := flags.String("state-dir", "", "private broker state directory")
	sharePath := flags.String("share", "", "host-private launch input")
	check := flags.Bool("self-check", false, "check canonical state")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *state == "" || !filepath.IsAbs(*state) {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: --state-dir must be absolute; retry with a valid host state directory")
		return 2
	}
	canonical, err := mcpauth.NewCanonicalStore(*state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: cannot open host state; repair its directory permissions and retry:", err)
		return 1
	}
	if *check {
		entries, err := os.ReadDir(*state)
		if err != nil {
			fmt.Fprintln(os.Stdout, "FAIL: mcp-auth host state is unreadable; repair its directory permissions and rerun yolo check:", err)
			return 1
		}
		recordCount := 0
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			pack := strings.TrimSuffix(entry.Name(), ".json")
			store := *canonical
			store.Filename, store.LockName = entry.Name(), pack+".lock"
			file, err := store.Read()
			if err != nil {
				fmt.Fprintf(os.Stdout, "FAIL: mcp-auth canonical state cannot be read; restore a private backup of %s and rerun yolo check: %v\n", filepath.Join(*state, entry.Name()), err)
				return 1
			}
			recordCount += len(file.Records)
		}
		if recordCount == 0 {
			fmt.Fprintln(os.Stdout, "NOTE: no MCP sign-ins have been shared yet; sign in to a user-level server with your selected agent, then relaunch its jail")
			return 0
		}
		fmt.Fprintf(os.Stdout, "OK: mcp-auth canonical state has %d records\n", recordCount)
		return 0
	}
	if *socket == "" || *sharePath == "" || filepath.Clean(*sharePath) != filepath.Join(*state, "replicas", filepath.Base(*sharePath)) {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: --socket and a private --share inside state/replicas are required; retry the launch")
		return 2
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	replicas, err := paths.OpenStateDirRoot(filepath.Join(*state, "replicas"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: cannot open private launch directory; repair host state permissions and retry:", err)
		return 1
	}
	data, err := paths.ReadRegularFileBeneath(replicas, filepath.Base(*sharePath))
	replicas.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: cannot read private launch input; retry the launch:", err)
		return 1
	}
	share, err := mcpauth.DecodeShare(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: invalid private launch input; retry the launch:", err)
		return 1
	}
	if share.Jail+".json" != filepath.Base(*sharePath) {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: launch identity differs from private share filename; retry the launch")
		return 1
	}
	// Keep this file's lock until the FINAL harvest and socket shutdown. The keeper
	// removes the input only after the supervisor observes our real process exit.
	lockRoot, err := paths.OpenStateDirRoot(filepath.Join(*state, "replicas"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: cannot lock private launch input; retry:", err)
		return 1
	}
	lockName := share.Jail + ".lock"
	lockFile, err := paths.OpenLockFileBeneath(lockRoot, lockName)
	if err != nil {
		lockRoot.Close()
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: cannot lock launch replica; repair host state permissions and retry:", err)
		return 1
	}
	if err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		lockRoot.Close()
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: replica already has a live owner; retry after the existing jail exits")
		return 1
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		_ = lockRoot.Close()
	}()
	b := broker{share: share, canonical: canonical, audit: brokeraudit.Open(filepath.Join(paths.BrokerDir(), "audit.jsonl"), func(message string) { fmt.Fprintln(os.Stderr, "mcp-auth-broker: audit warning:", message) }), seeded: map[string]uint64{}}
	b.startupDeadline = time.Now().Add(startupBudget)
	if err := b.loadScripts(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: selected pack script is unavailable; retry with a readable staged pack:", err)
		return 1
	}
	if err = b.adoptAtStart(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: existing sign-in could not be adopted before readiness; repair the named workspace store or lock and retry:", err)
		return 1
	}
	if err = b.seed(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: seed failed before readiness; repair the named workspace store or lock and retry:", err)
		return 1
	}
	if err := b.startupRemaining(); err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: startup budget exhausted before readiness; inspect the workspace store and retry:", err)
		return 1
	}
	b.startupDeadline = time.Time{}
	stop := make(chan struct{})
	select {
	case <-signals:
		if err := b.harvest(); err != nil {
			fmt.Fprintln(os.Stderr, "mcp-auth-broker: final harvest failed; repair the workspace store before retrying:", err)
			return 1
		}
		return 0
	default:
	}
	finished := make(chan struct{})
	go func() {
		select {
		case <-signals:
			close(stop)
		case <-finished:
		}
	}()
	err = hostservice.ServeFrontedUnix(b.handleStatus, *socket, stop)
	close(finished)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: fronted socket stopped; retry the launch:", err)
	}
	if harvestErr := b.harvest(); harvestErr != nil {
		fmt.Fprintln(os.Stderr, "mcp-auth-broker: final harvest failed; the canonical record was not replaced by an unreadable replica. Repair its store or lock before retrying:", harvestErr)
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func (b *broker) handleStatus(s *hostservice.Session) {
	op, _ := s.Get("op")
	if op != "status" || s.JailID != b.share.Jail {
		s.Stderr("mcp-auth: unsupported request or jail; inspect the selected pack and retry\n")
		s.Exit(2)
		return
	}
	agents := make([]map[string]any, 0, len(b.share.Replicas))
	for _, replica := range b.share.Replicas {
		file, readErr := b.canonicalFor(replica).ReadStatus()
		if readErr != nil {
			s.Stderr("mcp-auth: canonical state unavailable; repair host state and retry\n")
			s.Exit(1)
			return
		}
		servers := make([]map[string]any, 0, len(replica.Shared))
		for _, shared := range replica.Shared {
			row := map[string]any{"name": shared.Name, "scope": "user", "state": "not-signed-in", "next_step": strings.ReplaceAll(replica.Decl.LoginHint, "{name}", shared.Name)}
			for _, record := range file.Records {
				if record.Server == shared.Name && record.Identity == shared.Identity {
					row["state"], row["generation"], row["expires_at_ms"] = record.State, record.Generation, record.ExpiresAtMS
					if record.State == "live" {
						row["next_step"] = ""
					}
					break
				}
			}
			servers = append(servers, row)
		}
		agents = append(agents, map[string]any{"pack": replica.Pack, "sharing": "launch-boundary", "servers": servers})
	}
	_ = s.JSON(map[string]any{"schema": "yolo.mcp-auth.status/1", "agents": agents})
	s.Exit(0)
}

func (b *broker) auditEvents(events []mcpauth.Event) {
	if b.audit == nil {
		return
	}
	for _, e := range events {
		b.audit.Append(brokeraudit.Event{Service: "mcp-auth", Event: e.Action, Server: e.Server, Generation: int(e.Generation), Jail: b.share.Jail, Workspace: b.share.Workspace})
	}
}

func (b *broker) root(replica mcpauth.Replica) (*os.Root, error) {
	home, err := paths.OpenWorkspaceStateSubdir(b.share.Workspace, replica.Root[0])
	if err != nil {
		return nil, err
	}
	defer home.Close()
	return paths.OpenStateSubdirRoot(home, replica.Root[1], filepath.Join(paths.WorkspaceStateDir(b.share.Workspace), replica.Root[0], replica.Root[1]))
}

func (b *broker) prepareLockParent(root *os.Root, replica mcpauth.Replica) error {
	parent := root
	var opened []*os.Root
	defer func() {
		for _, dir := range opened {
			_ = dir.Close()
		}
	}()
	components := strings.Split(filepath.ToSlash(filepath.Dir(replica.Lock.Path)), "/")
	for _, component := range components {
		if component == "." {
			continue
		}
		if err := parent.Mkdir(component, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		next, err := paths.OpenStateSubdirRoot(parent, component, filepath.Join(b.share.Workspace, ".yolo", "home", replica.Root[1], component))
		if err != nil {
			return err
		}
		opened = append(opened, next)
		parent = next
	}
	return nil
}

func (b *broker) loadScripts() error {
	b.scripts = make(map[string]string)
	for _, replica := range b.share.Replicas {
		if len(replica.Shared) == 0 {
			continue
		}
		script, err := os.ReadFile(replica.DeriveFile)
		if err != nil {
			return fmt.Errorf("%s: cannot read selected pack derive", replica.Pack)
		}
		b.scripts[replica.Pack] = string(script)
	}
	return nil
}

func (b *broker) matchSharedEntry(replica mcpauth.Replica, server mcpauth.SharedServer, raw json.RawMessage) error {
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil || entry == nil {
		return fmt.Errorf("mcp-auth: %s: shared entry is malformed; repair the agent's store and retry", server.Server)
	}
	script, ok := b.scripts[replica.Pack]
	if !ok || script == "" {
		return fmt.Errorf("mcp-auth: %s: selected pack derive was not pinned at startup; retry with a readable staged pack", replica.Pack)
	}
	matched, err := luahook.MCPAuthMatch(script, replica.Pack, server.Key, entry)
	if err != nil || matched == nil || matched.Name != strings.TrimPrefix(server.Server, "local:") || matched.URL != server.URL {
		return fmt.Errorf("mcp-auth: %s: native store key does not match its composed server URL; repair the selected pack's match function or remove the stale entry, then retry", server.Server)
	}
	if server.ClientID != "" {
		var native struct {
			ClientID string `json:"client_id"`
		}
		if json.Unmarshal(raw, &native) != nil || native.ClientID != server.ClientID {
			return fmt.Errorf("mcp-auth: %s: native OAuth client ID differs from approved identity; leave the native entry untouched and retry with matching settings", server.Server)
		}
	}
	return nil
}

func (b *broker) startupRemaining() error {
	if !b.startupDeadline.IsZero() && !time.Now().Before(b.startupDeadline) {
		return fmt.Errorf("mcp-auth: startup exceeded %s; release the store lock and retry", startupBudget)
	}
	return nil
}

func (b *broker) waitForStore() (time.Duration, error) {
	if err := b.startupRemaining(); err != nil {
		return 0, err
	}
	if b.startupDeadline.IsZero() {
		return lockWait, nil
	}
	return min(lockWait, time.Until(b.startupDeadline)), nil
}

func (b *broker) canonicalFor(replica mcpauth.Replica) *mcpauth.CanonicalStore {
	store := *b.canonical
	store.Filename = replica.Pack + ".json"
	store.LockName = replica.Pack + ".lock"
	if !b.startupDeadline.IsZero() {
		store.Wait = min(store.Wait, max(time.Until(b.startupDeadline), time.Nanosecond))
	}
	return &store
}

func (b *broker) seed() error {
	for _, replica := range b.share.Replicas {
		if len(replica.Shared) == 0 {
			continue
		}
		if err := b.startupRemaining(); err != nil {
			return err
		}
		if err := b.canonicalFor(replica).WithReadLock(func(file *mcpauth.CanonicalFile) error { return b.seedReplica(file, replica) }); err != nil {
			return err
		}
	}
	return nil
}

func (b *broker) seedReplica(file *mcpauth.CanonicalFile, replica mcpauth.Replica) error {
	if err := b.startupRemaining(); err != nil {
		return err
	}
	if b.seeded == nil {
		b.seeded = map[string]uint64{}
	}
	if b.seededEntry == nil {
		b.seededEntry = map[string]json.RawMessage{}
	}

	root, err := b.root(replica)
	if err != nil {
		return err
	}
	if err = b.prepareLockParent(root, replica); err != nil {
		root.Close()
		return fmt.Errorf("%s: cannot prepare the declared store lock; retry after repairing the workspace: %w", replica.Pack, err)
	}
	adapter, err := mcpauth.AdapterFor(&replica.Decl)
	if err == nil {
		wait, waitErr := b.waitForStore()
		if waitErr != nil {
			root.Close()
			return waitErr
		}
		var events []mcpauth.Event
		var managedUpdates = map[string]json.RawMessage{}
		seededKeys := map[string]bool{}
		err = adapter.SeedFile(root, replica.Store, wait, true, func(store mcpauth.Store) (mcpauth.Store, error) {
			var eligible []mcpauth.SharedServer
			for _, server := range replica.Servers() {
				var record *mcpauth.Record
				for i := range file.Records {
					if file.Records[i].Server == server.Server && file.Records[i].Identity == server.Identity {
						record = &file.Records[i]
						if record.URL != server.URL {
							return store, fmt.Errorf("mcp-auth: %s: canonical identity URL changed; inspect the selected server and retry", server.Server)
						}
						if err := b.matchSharedEntry(replica, server, record.Entry); err != nil {
							return store, err
						}
					}
				}
				current, exists := store.Entries[server.Key]
				managed, err := b.readManaged(replica, server)
				if err != nil {
					return store, err
				}
				if managed != nil && managed.Identity != server.Identity {
					// A scoped ledger associates this native key with the old identity
					// whether or not the new identity has canonical credentials.
					// Retire only the exact old copy; changed bytes remain unproved.
					if exists {
						if !sameNativeJSON(current, managed.Entry) {
							return store, fmt.Errorf("mcp-auth: %s: old managed identity has changed native bytes; leave the unknown entry untouched and repair this workspace's native sign-in before relaunching", server.Server)
						}
						if err := b.matchSharedEntry(replica, server, current); err != nil {
							return store, err
						}
					}
				}
				// A name/URL/key match is not proof of a native login's headers.
				// Only an absent entry or an exact previously broker-written copy
				// may be replaced or removed.
				if exists && (managed == nil || !sameNativeJSON(current, managed.Entry)) {
					fmt.Fprintf(os.Stderr, "mcp-auth-broker: server %q has an unproved native sign-in; it was not seeded from canonical or shared. Keep this workspace's sign-in private; repair the native entry before relaunching if the user-level credential is needed.\n", server.Server)
					continue
				}
				if exists {
					if err := b.matchSharedEntry(replica, server, current); err != nil {
						return store, err
					}
				}
				if record != nil || managed != nil {
					eligible = append(eligible, server)
				}
			}
			next, wrote := mcpauth.PlanSeed(file.Records, store, eligible)
			events = wrote
			for _, server := range eligible {
				found := false
				for _, record := range file.Records {
					if record.Server == server.Server && record.Identity == server.Identity {
						found = true
						break
					}
				}
				if !found {
					if _, exists := next.Entries[server.Key]; exists {
						delete(next.Entries, server.Key)
						events = append(events, mcpauth.Event{Action: "seed-remove", Server: server.Server})
					}
				}
			}
			if len(next.Entries) == 0 && next.EmptyStore == "delete" {
				next.Deleted = true
			}
			for _, server := range eligible {
				current, exists := next.Entries[server.Key]
				if exists {
					seededKeys[server.Key] = true
				}
				if !exists {
					managedUpdates[server.Key] = nil
				} else if _, before := store.Entries[server.Key]; !before || !sameNativeJSON(store.Entries[server.Key], current) {
					managedUpdates[server.Key] = append(json.RawMessage(nil), current...)
				}
			}
			return next, b.startupRemaining()
		})
		if err == nil {
			for _, server := range replica.Servers() {
				if entry, update := managedUpdates[server.Key]; update {
					if err = b.writeManaged(replica, server, entry); err != nil {
						break
					}
				}
			}
		}
		if err == nil {
			for _, record := range file.Records {
				for _, server := range replica.Servers() {
					if record.Server != server.Server || record.Identity != server.Identity || !seededKeys[server.Key] {
						continue
					}
					managed, readErr := b.readManaged(replica, server)
					if readErr != nil {
						err = readErr
						break
					}
					if managed != nil {
						key := replica.Pack + "|" + record.Server + "|" + record.Identity
						b.seeded[key] = record.Generation
						b.seededEntry[key] = append(json.RawMessage(nil), record.Entry...)
					}
				}
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			b.auditEvents(events)
		}
	}
	root.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", replica.Pack, err)
	}
	return nil
}

func (b *broker) harvest() error      { return b.observe(true) }
func (b *broker) adoptAtStart() error { return b.observe(false) }

func (b *broker) observe(allowSignOut bool) error {
	var problems []error
	for _, replica := range b.share.Replicas {
		if len(replica.Shared) == 0 {
			continue
		}
		root, err := b.root(replica)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !allowSignOut {
			if err = b.prepareLockParent(root, replica); err != nil {
				root.Close()
				problems = append(problems, fmt.Errorf("%s: cannot prepare native store lock: %w", replica.Pack, err))
				continue
			}
		}
		adapter, err := mcpauth.AdapterFor(&replica.Decl)
		if err != nil {
			root.Close()
			problems = append(problems, err)
			continue
		}
		var events []mcpauth.Event
		// Observe beneath the native lock. Canonical is committed before any replica
		// refresh token is stripped. A failed canonical write leaves its original intact.
		var adopted map[string]json.RawMessage
		err = b.canonicalFor(replica).WithLock(func(file *mcpauth.CanonicalFile) error {
			wait, waitErr := b.waitForStore()
			if waitErr != nil {
				return waitErr
			}
			return adapter.InspectFile(root, replica.Store, wait, func(store mcpauth.Store) error {
				shared := make([]mcpauth.SharedServer, 0, len(replica.Shared))
				for _, server := range replica.Servers() {
					managed, readErr := b.readManaged(replica, server)
					if readErr != nil {
						return readErr
					}
					if raw, present := store.Entries[server.Key]; present {
						// The native entry does not attest the sign-in headers. A
						// changed token is not proof it retained the seed identity.
						if managed != nil && managed.Identity == server.Identity && sameNativeJSON(raw, managed.Entry) {
							if err := b.matchSharedEntry(replica, server, raw); err != nil {
								return err
							}
						}
						continue
					}
					if !allowSignOut || managed == nil || managed.Identity != server.Identity {
						continue
					}
					key := replica.Pack + "|" + server.Server + "|" + server.Identity
					generation, seeded := b.seeded[key]
					current := uint64(0)
					var currentEntry json.RawMessage
					for _, r := range file.Records {
						if r.Server == server.Server && r.Identity == server.Identity {
							current, currentEntry = r.Generation, r.Entry
						}
					}
					if seeded && generation == current && bytes.Equal(b.seededEntry[key], currentEntry) {
						shared = append(shared, server)
					}
				}
				next, _, planned := mcpauth.PlanAdopt(file.Records, store, true, shared, b.share.Workspace)
				file.Records = next
				events = planned
				adopted = map[string]json.RawMessage{}
				for _, e := range events {
					if e.Action == "adopt" {
						for _, server := range shared {
							if server.Server == e.Server {
								adopted[server.Key] = append(json.RawMessage(nil), store.Entries[server.Key]...)
							}
						}
					}
				}
				return b.startupRemaining()
			})
		})
		if err == nil {
			b.auditEvents(events)
			// If an agent wrote a *different* sign-in while canonical committed, do not
			// strip that new token: it will be harvested on the next launch.
			if len(adopted) > 0 {
				err = adapter.UpdateFile(root, replica.Store, lockWait, true, func(store mcpauth.Store) (mcpauth.Store, error) {
					for _, server := range replica.Servers() {
						old, ok := adopted[server.Key]
						if !ok || !bytes.Equal(old, store.Entries[server.Key]) {
							continue
						}
						fresh, stripErr := mcpauth.StripRefresh(old, server.RefreshTokenPath)
						if stripErr != nil {
							return store, stripErr
						}
						store.Entries[server.Key] = fresh
					}
					return store, nil
				})
			}
			if err == nil {
				for _, event := range events {
					if allowSignOut && (event.Action == "adopt" || event.Action == "sign-out") {
						b.auditEvents([]mcpauth.Event{{Action: "harvest", Server: event.Server, Generation: event.Generation}})
					}
				}
			}
		}
		root.Close()
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", replica.Pack, err))
		}
	}
	return errors.Join(problems...)
}
