package ghbroker

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Authority is host-owned launch scope and fresh broker identity. Its reads are
// non-locking: store callbacks already hold the shared source transaction.
type Authority struct {
	path  string
	scope brokerscope.File
	start string
	lease *os.File
}

// BindAuthority binds this broker lifetime (start) to the launch's scope file under the core's
// lifetime lease (brokercore.BindLease).
func BindAuthority(path string, sf brokerscope.File, start string) (*Authority, error) {
	f, lease, err := brokercore.BindLease(Source, path, sf, start)
	if err != nil {
		return nil, err
	}
	return &Authority{path: path, scope: f, start: start, lease: lease}, nil
}

// Close serializes lifetime invalidation with authorization (brokercore.CloseLease).
func (a *Authority) Close() error {
	if a == nil {
		return nil
	}
	return brokercore.CloseLease(Source, a.lease)
}
func (a *Authority) live() (brokerscope.File, error) {
	if a == nil {
		return brokerscope.File{}, errors.New("host authority unavailable; relaunch the jail")
	}
	return brokercore.Live(Source, a.path, a.scope, a.start)
}

// Classify re-reads current host sets and current approved launch scope. Unknown
// or unsupported policy is an error, never a fallback to the default write set.
func (a *Authority) Classify(argv []string, repo string) (Decision, brokerstore.Policy, error) {
	f, err := a.live()
	if err != nil {
		return Decision{}, brokerstore.Policy{}, err
	}
	d := Classify(argv, repo, NewScope(append(append([]string(nil), f.Repos...), f.Widened...)))
	if d.Outcome != OutcomeStanding && d.Outcome != OutcomeWindowed {
		return d, brokerstore.Policy{}, errors.New(d.Reason)
	}
	// READ IS THE FLOOR here too, against the same fresh read (BB-D108): an approved write
	// revalidates through this (Revalidate), so a read grant withdrawn by a `yolo config apply`
	// during the approval wait refuses the write rather than letting it run.
	if r, missing := scopeOfFile(f).unread(d.Repos); missing {
		return d, brokerstore.Policy{}, errors.New(config.BrokerNoReadRefusal(Source, ReadCapability, r, f.LiveScope))
	}
	d = Classify(d.Argv, repo, NewScope(append(append([]string(nil), f.Repos...), f.Widened...)))
	for _, r := range d.Repos {
		if err = brokerscope.ValidateCurrent(a.path, a.scope, r); err != nil {
			return d, brokerstore.Policy{}, err
		}
	}
	sets, err := config.CurrentBrokeredSets(Source)
	if err != nil {
		return d, brokerstore.Policy{}, err
	}
	if sets == nil {
		sets = []config.BrokeredSet{{Name: SetReadOnly, Mode: "standing"}, {Name: SetReadWrite, Mode: "windowed"}}
	}
	// Explicit admit replacement versus augmentation is not yet ruled (§5.7).
	// Refuse the entire policy rather than silently applying a broader default.
	for _, s := range sets {
		if s.Admit != nil || (s.Name != SetReadOnly && s.Name != SetReadWrite) {
			return d, brokerstore.Policy{}, fmt.Errorf("permission set %s has unsupported explicit rules; use default set rules on the host", s.Name)
		}
	}
	base := d.Set
	// A standing set that covers the command takes precedence over a windowed one.
	for _, mode := range []string{"standing", "windowed"} {
		for _, s := range sets {
			if s.Mode != mode || (s.Name != base && s.Name != SetReadWrite) {
				continue
			}
			d.Set = s.Name
			if mode == "standing" {
				if base == SetReadWrite && s.Name == SetReadWrite {
					return d, brokerstore.Policy{}, errors.New("allow.github.sets in your user config makes GitHub writes standing, which is refused. Next: remove its read-write entry, or run `yolo config unset allow.github.sets --user` on the host to go back to the default sets (reads standing, writes asked), then run the command again")
				}
				d.Outcome = OutcomeStanding
				return d, brokerstore.Policy{}, nil
			}
			p := brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 12 * time.Hour, Session: true, Uses: 25}
			if s.Window != "" {
				p.Window, err = time.ParseDuration(s.Window)
				if err != nil {
					return d, p, err
				}
			}
			if s.MaxWindow != "" && s.MaxWindow != "session" {
				p.MaxWindow, err = time.ParseDuration(s.MaxWindow)
				p.Session = false
				if err != nil {
					return d, p, err
				}
			}
			if s.Uses != 0 {
				p.Uses = s.Uses
			}
			if p.Window <= 0 || p.Window > p.MaxWindow || p.Uses < 1 {
				return d, p, errors.New("invalid host set bounds; repair the host policy")
			}
			d.Outcome = OutcomeWindowed
			return d, p, nil
		}
	}
	return d, brokerstore.Policy{}, errors.New("no host permission set admits this command; configure its set on the host")
}
func (a *Authority) Revalidate(q brokerstore.Request) (brokerstore.Policy, error) {
	if a == nil || q.Start != a.start || q.LaunchID != a.scope.LaunchID || q.Workspace != a.scope.Workspace || q.Jail != paths.JailShortHash(a.scope.Container) {
		return brokerstore.Policy{}, errors.New("request holder is not this live host launch")
	}
	d, p, err := a.Classify(q.Argv, q.Repo)
	if err != nil {
		return p, err
	}
	if d.Outcome != OutcomeWindowed || d.Set != q.Set || !slices.Equal(d.Argv, q.Argv) || strings.Join(d.Repos, ",") != q.Repo {
		return p, errors.New("current classification differs; refile the command")
	}
	return p, nil
}

// ApprovalAuthority is the standalone host frontend adapter. It derives the
// scope path from a validated launch id, not from a caller-provided path.
func ApprovalAuthority(q brokerstore.Request) (brokerstore.Policy, error) {
	if q.LaunchID == "" || strings.ContainsAny(q.LaunchID, "/\\.") {
		return brokerstore.Policy{}, errors.New("request has no valid host launch identity")
	}
	path := paths.BrokerScopeFile(Source, q.LaunchID)
	f, err := brokerscope.ReadFile(path)
	if err != nil {
		return brokerstore.Policy{}, err
	}
	a := &Authority{path: path, scope: f, start: q.Start}
	return a.Revalidate(q)
}
