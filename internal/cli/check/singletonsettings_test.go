package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func settingsSleeperChildMain(socketPath string) int {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return 1
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return 0
		}
		_ = c.Close()
	}
}

// TestCheckReportsARunningDaemonWhoseSettingsDifferFromConfig is `yolo check`'s half of
// host-daemon-ownership.md HD-D2: a host-wide daemon that is running settings the config no
// longer says is a [WARN] naming the KEY, never the value; a current one is a pass; a
// daemon that is not running has nothing to compare and gets no row.
//
// The daemon is REAL — spawned through broker.EnsureSingleton, which records what it was
// handed — so the row is graded against the record a launch actually leaves.
func TestCheckReportsARunningDaemonWhoseSettingsDifferFromConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process on a host-wide singleton socket")
	}
	t.Setenv("HOME", t.TempDir())
	stateRoot := t.TempDir()
	realStateDir := loopholes.StateDirFor
	loopholes.StateDirFor = func(n string) string { return filepath.Join(stateRoot, n) }
	t.Cleanup(func() { loopholes.StateDirFor = realStateDir })

	name := "yjtest-check-settings"
	settingsPath := loopholes.SettingsFileFor(name)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"profile":"started-secret"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := []string{os.Args[0], "-settings-sleeper-child", paths.HostSingletonSocket(name), settingsPath}
	lp := &loopholes.Loophole{
		Name:       name,
		Settings:   []loopholes.Setting{{Key: "profile", Type: loopholes.SettingTypeString, Default: ""}},
		HostDaemon: &loopholes.HostDaemon{Scope: loopholes.ScopeHost, Cmd: argv},
	}
	configWith := func(profile string) *jsonx.OrderedMap {
		settings := jsonx.NewOrderedMap()
		settings.Set("profile", profile)
		entry := jsonx.NewOrderedMap()
		entry.Set("settings", settings)
		block := jsonx.NewOrderedMap()
		block.Set(name, entry)
		return block
	}
	grade := func(profile string) string {
		var buf strings.Builder
		reportSingletonSettings(&reporter{w: &buf}, lp, configWith(profile))
		return buf.String()
	}

	if got := grade("anything"); got != "" {
		t.Errorf("a daemon that is not running got a row: %q", got)
	}

	deps := broker.SingletonDeps(name, argv)
	deps.Out = nil
	t.Cleanup(func() { broker.BrokerKill(deps, syscall.SIGTERM, 2*time.Second) })
	broker.EnsureSingleton(deps)
	if !broker.BrokerIsAlive(deps) {
		t.Fatal("the fake daemon did not come up")
	}

	if got := grade("started-secret"); !strings.Contains(got, "[PASS]") ||
		!strings.Contains(got, "settings match config") {
		t.Errorf("a daemon running the configured settings is not a pass: %q", got)
	}
	got := grade("configured-secret")
	if !strings.Contains(got, "[WARN]") || !strings.Contains(got, "differ from config (profile)") {
		t.Errorf("a daemon running other settings is not a [WARN] naming the key: %q", got)
	}
	for _, value := range []string{"started-secret", "configured-secret"} {
		if strings.Contains(got, value) {
			t.Errorf("the row prints the setting VALUE %q: %q", value, got)
		}
	}
}

// TestCheckLoopholesGradesSingletonSettings pins the call site: the row above is reached
// only through checkLoopholes, ahead of the doctor_cmd gate so a daemon that declares no
// self-check is still graded.
func TestCheckLoopholesGradesSingletonSettings(t *testing.T) {
	if !callsIn(t, "sections_loopholes.go", "checkLoopholes")["reportSingletonSettings"] {
		t.Error("checkLoopholes no longer calls reportSingletonSettings")
	}
}

// callsIn returns the names called inside the top-level function fn of file.
func callsIn(t *testing.T, file, fn string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch f := call.Fun.(type) {
				case *ast.Ident:
					out[f.Name] = true
				case *ast.SelectorExpr:
					out[f.Sel.Name] = true
				}
			}
			return true
		})
		return out
	}
	t.Fatalf("%s has no function %s", file, fn)
	return nil
}
