package gitbroker

import (
	"context"
	"slices"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// menu.go collects, while one push's refs are judged, the lasting paths that would cover the
// refused ones — each ref's project request, and the user-config ceiling it still needs — so
// the push's refusal renders them in one menu (approvalwait.Menu) beside its `yolo approve`
// answers, each command alone on its own line, instead of inside a sentence per ref.

// pushMenu is one push's collected paths. A coverer may run on another goroutine than the
// refusal that reads it, so it is locked.
type pushMenu struct {
	mu      sync.Mutex
	project []string
	ceiling []string
	listed  bool
}

func (m *pushMenu) addProject(command string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.Contains(m.project, command) {
		m.project = append(m.project, command)
	}
}

// addCeiling records the host step that lets the user config allow kind: `yolo allow`, behind
// `yolo config migrate` for a user config still in legacy JSONC (allowAdvice's two steps).
func (m *pushMenu) addCeiling(kind string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	steps := []string{AllowOnRequestCommand(kind)}
	if _, toml := config.UserConfigFile(); !toml {
		steps = []string{"yolo config migrate", AllowOnRequestCommand(kind)}
	}
	for _, s := range steps {
		if !slices.Contains(m.ceiling, s) {
			m.ceiling = append(m.ceiling, s)
		}
	}
}

// hasPaths reports whether m collected a lasting path that no pending request's menu has
// listed yet: the push's refusal then lists them itself.
func (m *pushMenu) hasPaths() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.listed && (len(m.project) > 0 || len(m.ceiling) > 0)
}

// markListed records that a pending request's menu lists m's paths.
func (m *pushMenu) markListed() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listed = true
}

type pushMenuKey struct{}

// withPushMenu carries m to the push's refusal through ctx.
func withPushMenu(ctx context.Context, m *pushMenu) context.Context {
	return context.WithValue(ctx, pushMenuKey{}, m)
}

func pushMenuFrom(ctx context.Context) *pushMenu {
	m, _ := ctx.Value(pushMenuKey{}).(*pushMenu)
	return m
}

// hostPushCommand is the command that does a refused push on the host instead.
const hostPushCommand = "git push"

// retryPush is what the agent runs again once a push's request is allowed.
const retryPush = "run the same push again"

// menu is the push's paths for request id ("" when none was filed). Its lasting paths are the
// project's requests alone: `yolo approve`'s own lasting answers, `--add` and `--remember`, are
// removed (git-broker.md GB-D28), so the menu offers Allow once, `--for 15m`, the project
// request, and the push from the host.
func (m *pushMenu) menu(id string) approvalwait.Menu {
	out := approvalwait.Menu{ID: id, Retry: retryPush, Fallback: hostPushCommand}
	if m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		out.Ceiling = slices.Clone(m.ceiling)
		for _, c := range m.project {
			out.Project = append(out.Project, approvalwait.ProjectStep{Command: c})
		}
	}
	return out
}
