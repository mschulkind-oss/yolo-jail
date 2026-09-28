package loopholedecl_test

// listen_test.go pins `jail_daemon.listen` and its {listen} token (docs/plans/notch-convergence.md
// NC-D41): the address is declared once, as a loopback host:port, the argv takes it as the token,
// and the two are refused apart — as is the token in a field that runs on the host, where nothing
// substitutes it.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestAJailDaemonDeclaresItsListenAddressOnce(t *testing.T) {
	m, err := decodeMap(t, "adapter", map[string]any{
		"name": "adapter", "description": "x",
		"jail_daemon": map[string]any{
			"cmd": []any{"yolo-jaild", "adapter", "--listen", "{listen}"}, "listen": "127.0.0.1:1460",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.JailDaemon.Listen != "127.0.0.1:1460" {
		t.Errorf("Listen = %q, want the declared 127.0.0.1:1460", m.JailDaemon.Listen)
	}
	if m.JailDaemon.Cmd[3] != loopholedecl.TokenListen {
		t.Errorf("the argv resolved the token at decode: %v; the launch resolves it", m.JailDaemon.Cmd)
	}
}

func TestAListenDeclarationIsRefusedApartFromItsToken(t *testing.T) {
	for name, tc := range map[string]struct {
		daemon map[string]any
		want   string
	}{
		"token without listen": {map[string]any{"cmd": []any{"d", "--listen", "{listen}"}},
			"'jail_daemon.listen' is not declared"},
		"listen without token": {map[string]any{"cmd": []any{"d", "--listen", "127.0.0.1:1460"},
			"listen": "127.0.0.1:1460"}, "never names '{listen}'"},
		"not loopback": {map[string]any{"cmd": []any{"d", "{listen}"}, "listen": "0.0.0.0:1460"},
			"loopback IP literal"},
		"hostname": {map[string]any{"cmd": []any{"d", "{listen}"}, "listen": "localhost:1460"},
			"loopback IP literal"},
		"no port": {map[string]any{"cmd": []any{"d", "{listen}"}, "listen": "127.0.0.1"},
			"loopback host:port"},
		"port out of range": {map[string]any{"cmd": []any{"d", "{listen}"}, "listen": "127.0.0.1:70000"},
			"between 1 and 65535"},
		"not a string": {map[string]any{"cmd": []any{"d", "{listen}"}, "listen": 1460},
			"must be a string"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeMap(t, "adapter", map[string]any{
				"name": "adapter", "description": "x", "jail_daemon": tc.daemon,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one saying %q", err, tc.want)
			}
		})
	}
}

func TestTheListenTokenIsRefusedWhereTheHostResolvesTheField(t *testing.T) {
	for name, fields := range map[string]map[string]any{
		"host_daemon.cmd": {"host_daemon": map[string]any{"cmd": []any{"d", "{socket}", "{listen}"}}},
		"doctor_cmd":      {"doctor_cmd": []any{"d", "{listen}"}},
	} {
		t.Run(name, func(t *testing.T) {
			manifest := map[string]any{"name": "adapter", "description": "x"}
			for k, v := range fields {
				manifest[k] = v
			}
			_, err := decodeMap(t, "adapter", manifest)
			if err == nil || !strings.Contains(err.Error(), "only 'jail_daemon.cmd' takes it") {
				t.Errorf("err = %v, want the host-field refusal", err)
			}
		})
	}
}
