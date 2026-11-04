package check

// offswitchserved_test.go pins `yolo check` keeping a legacy user `enabled: false` on an
// off-switch loophole off the way a launch does (docs/design/legacy-config-compat.md LC-D3): the
// workspace cannot turn it back on, by `enabled: true` or by a null that resets the block or the
// entry, so predictedServed must not predict a daemon the launch will not serve. It goes
// through mergeScopes, the merge check.go runs, so deleting the keep-off step from
// config.MergeConfigWithSources, or merging check's scopes some other way, fails it.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestCheckDoesNotPredictAUserOffSwitchServed(t *testing.T) {
	bundled := isolatedModuleDir(t)
	// A synthetic aws-auth with a jail daemon, so "served" is observable in the prediction;
	// the off switch is keyed by the loophole's name (loopholedecl.HasOffSwitch).
	writeLoopholeManifest(t, bundled, "aws-auth",
		`"name":"aws-auth","description":"d","transport":"none",`+
			`"jail_daemon":{"cmd":["yolo-jaild","aws-auth","--listen","{listen}"],"listen":"127.0.0.1:1997"}`)
	parse := func(s string) *jsonx.OrderedMap {
		t.Helper()
		v, err := jsonx.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return v.(*jsonx.OrderedMap)
	}
	const userOn = `{"loopholes": {"aws-auth": {"enabled": true}}}`
	const userOff = `{"loopholes": {"aws-auth": {"enabled": false}}}`
	for _, tc := range []struct {
		name, user, ws string
		want           bool
	}{
		{"control: user on", userOn, `{}`, true},
		{"workspace enabled true", userOff, `{"loopholes": {"aws-auth": {"enabled": true}}}`, false},
		{"workspace loopholes null", userOff, `{"loopholes": null}`, false},
		{"workspace entry null", userOff, `{"loopholes": {"aws-auth": null}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := mergeScopes(parse(tc.user), parse(tc.ws), nil, t.TempDir())
			merged.Set("runtime", "podman")
			got := (&Options{}).predictedServed(merged, nil).Serves("aws-auth")
			if got != tc.want {
				t.Errorf("predicted served = %v, want %v (merged %v)", got, tc.want,
					merged)
			}
		})
	}
}
