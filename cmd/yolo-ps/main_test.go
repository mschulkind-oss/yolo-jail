package main

import (
	"strings"
	"testing"
)

// The request this client puts on the wire is now a pure statement of what was
// ASKED — a mode, and for pid mode a number. Everything below exists to pin the
// absence of the fourth field it used to carry, jail_id, because an absence has
// no other way to fail loudly: re-adding it would break no assertion that
// existed before this file, and the symptom in production would be an audit
// column quietly reverting to a value the client chose for itself.
//
// Byte-exact rather than key-wise on purpose. json.Marshal of a map emits keys
// in sorted order, so the encoding is deterministic and a whole-body comparison
// costs nothing while catching a field nobody thought to look for.

func TestRequestBodyCarriesTheModeAndNothingElse(t *testing.T) {
	cases := []struct {
		name   string
		pidSet bool
		pid    int
		tree   bool
		want   string
	}{
		{"list is the default", false, 0, false, `{"mode":"list"}`},
		{"tree", false, 0, true, `{"mode":"tree"}`},
		{"pid", true, 4242, false, `{"mode":"pid","pid":4242}`},
		// --pid 0 is a query about pid 0, not an absent flag: run() detects the
		// flag by presence (flag.Visit), so the zero value must survive here.
		{"pid 0 is a real pid", true, 0, false, `{"mode":"pid","pid":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(requestBody(buildRequest(tc.pidSet, tc.pid, tc.tree)))
			if got != tc.want {
				t.Errorf("request body = %s, want %s", got, tc.want)
			}
			if strings.Contains(got, "jail_id") {
				t.Errorf("the client named its own jail: %s\n"+
					"The host asserts the jail's identity in the connection preamble "+
					"(internal/svcendpoint/preamble.go); a client-supplied jail_id is "+
					"overridden there and must not be sent.", got)
			}
		})
	}
}

// TestPidBeatsTree pins the priority order, which is only observable when both
// selectors are set at once.
func TestPidBeatsTree(t *testing.T) {
	got := string(requestBody(buildRequest(true, 7, true)))
	if got != `{"mode":"pid","pid":7}` {
		t.Errorf("--pid --tree together = %s, want the pid query", got)
	}
}

// TestRequestReadsNoIdentityFromTheEnvironment. The two variables that used to
// feed jail_id are set here to values a leak would spell out verbatim.
//
// $YOLO_JAIL_ID is set by NOTHING in this repo, so the old code's value was
// always $HOSTNAME — which in a nested jail (forced --net=host) is the HOST's
// hostname. The field was wrong in a real configuration, not just redundant.
func TestRequestReadsNoIdentityFromTheEnvironment(t *testing.T) {
	t.Setenv("YOLO_JAIL_ID", "jail-id-from-the-environment")
	t.Setenv("HOSTNAME", "hostname-from-the-environment")

	for _, req := range []map[string]any{
		buildRequest(false, 0, false),
		buildRequest(false, 0, true),
		buildRequest(true, 1, false),
	} {
		got := string(requestBody(req))
		if strings.Contains(got, "from-the-environment") {
			t.Errorf("request body carries an environment-derived identity: %s", got)
		}
	}
}
