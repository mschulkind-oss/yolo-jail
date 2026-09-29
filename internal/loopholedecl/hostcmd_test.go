package loopholedecl_test

// hostcmd_test.go pins `jail_daemon.host_cmd` (JailDaemon.HostCmd): the argv that opens a
// credential DOORWAY on the host, as a launch-owned listener, for a launch whose agent shares the
// host's loopback (docs/design/host-notch-services.md HS-D15). It is carried raw, with {listen} as
// its one token, and refused where it could open a doorway nobody's clients reach or that answers
// anyone.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func doorwayDaemon(extra map[string]any) map[string]any {
	d := map[string]any{
		"cmd":          []any{"yolo-jaild", "adapter", "--listen", "{listen}"},
		"listen":       "127.0.0.1:1460",
		"caller_token": true,
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func TestADoorwayDeclaresItsHostArgvRaw(t *testing.T) {
	m, err := decodeMap(t, "adapter", map[string]any{
		"name": "adapter", "description": "x",
		"jail_daemon": doorwayDaemon(map[string]any{
			"host_cmd": []any{"yolo", "internal", "daemon", "adapter", "--listen", "{listen}"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.JailDaemon.HostCmd, " ")
	if got != "yolo internal daemon adapter --listen "+loopholedecl.TokenListen {
		t.Errorf("HostCmd = %q, want the declared argv with its token unresolved", got)
	}
}

func TestAJailDaemonWithoutAHostArgvHasNone(t *testing.T) {
	m, err := decodeMap(t, "adapter", map[string]any{
		"name": "adapter", "description": "x", "jail_daemon": doorwayDaemon(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.JailDaemon.HostCmd != nil {
		t.Errorf("HostCmd = %v with no host_cmd declared", m.JailDaemon.HostCmd)
	}
}

func TestAHostArgvIsRefusedWhereItsDoorwayCouldNotServe(t *testing.T) {
	for name, tc := range map[string]struct {
		daemon map[string]any
		want   string
	}{
		"not a list": {doorwayDaemon(map[string]any{"host_cmd": "yolo internal daemon adapter"}),
			"must be a non-empty list of strings"},
		"empty": {doorwayDaemon(map[string]any{"host_cmd": []any{}}),
			"must be a non-empty list of strings"},
		"no caller token": {map[string]any{
			"cmd": []any{"d", "{listen}"}, "listen": "127.0.0.1:1460",
			"host_cmd": []any{"yolo", "d", "{listen}"}}, "'jail_daemon.caller_token' is not true"},
		"no listen": {map[string]any{"cmd": []any{"d"}, "caller_token": true,
			"host_cmd": []any{"yolo", "d"}}, "'jail_daemon.listen' is not"},
		"listen never named": {doorwayDaemon(map[string]any{"host_cmd": []any{"yolo", "d", "127.0.0.1:1460"}}),
			"never names '{listen}'"},
		"a jail-side token": {doorwayDaemon(map[string]any{
			"host_cmd": []any{"yolo", "d", "{listen}", "{jail_loophole_dir}/x"}}), "nothing resolves there"},
		"a host daemon's token": {doorwayDaemon(map[string]any{
			"host_cmd": []any{"yolo", "d", "{listen}", "{socket}"}}), "nothing resolves there"},
		"a control character": {doorwayDaemon(map[string]any{
			"host_cmd": []any{"yolo", "d\n", "{listen}"}}), "host_cmd"},
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
