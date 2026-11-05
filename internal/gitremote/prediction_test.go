package gitremote

import (
	"strings"
	"testing"
)

func TestValidGitPredictionRequiresTypedBoundedReply(t *testing.T) {
	old, newID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	base := `{"schema":"yolo.access.prediction/1","source":"git","source_status":"verified","scope_source":"live-host","scope_verified":true,"eligibility_status":"not-covered","coverage_status":"needs-approval","call":{"source":"git","canonical":["git-receive-pack","` + old + ` ` + newID + ` refs/heads/main"],"digest":"abc"},"command_digest":"abc","classified":[{"ref":"refs/heads/main","old":"` + old + `","new":"` + newID + `"}],"verdict":"asks","rule":"approval needed","inputs_digest":"def","next":[{"who":"human","where":"host","run":"yolo approve r-123"}]}`
	if !validGitPrediction([]byte(base)) {
		t.Fatal("valid host reply rejected")
	}
	missingCall := strings.Replace(base, `"call":{"source":"git","canonical":["git-receive-pack","`+old+` `+newID+` refs/heads/main"],"digest":"abc"},`, "", 1)
	if validGitPrediction([]byte(missingCall)) {
		t.Fatal("missing verified command accepted")
	}
	refused := strings.Replace(missingCall, `"source_status":"verified"`, `"source_status":"not-read"`, 1)
	refused = strings.Replace(refused, `"verdict":"asks"`, `"verdict":"refused"`, 1)
	refused = strings.Replace(refused, `"classified":[{"ref":"refs/heads/main","old":"`+old+`","new":"`+newID+`"}],`, "", 1)
	refused = strings.Replace(refused, `"coverage_status":"needs-approval",`, "", 1)
	refused = strings.Replace(refused, `"eligibility_status":"not-covered",`, "", 1)
	refused = strings.Replace(refused, `"command_digest":"abc",`, "", 1)
	refused = strings.Replace(refused, `"source":"git",`, `"source":"git","call_unavailable":"no verified Git command; retry the preflight",`, 1)
	if !validGitPrediction([]byte(refused)) {
		t.Fatal("honest unavailable command rejected")
	}
	for _, fragment := range []string{`"verdict":"ask"`, `"source_status":null`, `"rule":null`, `"inputs_digest":null`, `"who":"other"`, `"where":"jail","run":"confirm request"`, `"run":"yolo approve r-123","detail":"also prose"`, `"next":null`, `"classified":null`, `"request_id":"r-<>"`, `"source_error":1`, `"unknown":1`, `"source_status":"not-read"`, `"scope_verified":false`, `"scope_verified":null`, `"coverage_status":"covered"`, `"ref":"refs/heads/other"`, `"old":"bad"`, `"command_digest":"wrong"`} {
		wire := base
		switch fragment {
		case `"verdict":"ask"`:
			wire = strings.Replace(wire, `"verdict":"asks"`, fragment, 1)
		case `"source_status":null`, `"source_status":"not-read"`:
			wire = strings.Replace(wire, `"source_status":"verified"`, fragment, 1)
		case `"scope_verified":false`, `"scope_verified":null`:
			wire = strings.Replace(wire, `"scope_verified":true`, fragment, 1)
		case `"coverage_status":"covered"`:
			wire = strings.Replace(wire, `"coverage_status":"needs-approval"`, fragment, 1)
		case `"rule":null`:
			wire = strings.Replace(wire, `"rule":"approval needed"`, fragment, 1)
		case `"inputs_digest":null`:
			wire = strings.Replace(wire, `"inputs_digest":"def"`, fragment, 1)
		case `"who":"other"`:
			wire = strings.Replace(wire, `"who":"human"`, fragment, 1)
		case `"where":"jail","run":"confirm request"`:
			wire = strings.Replace(wire, `"where":"host","run":"yolo approve r-123"`, fragment, 1)
		case `"run":"yolo approve r-123","detail":"also prose"`:
			wire = strings.Replace(wire, `"run":"yolo approve r-123"`, fragment, 1)
		case `"next":null`:
			wire = strings.Replace(wire, `"next":[{"who":"human","where":"host","run":"yolo approve r-123"}]`, fragment, 1)
		case `"classified":null`:
			wire = strings.Replace(wire, `"classified":[{"ref":"refs/heads/main","old":"`+old+`","new":"`+newID+`"}]`, fragment, 1)
		case `"ref":"refs/heads/other"`:
			wire = strings.Replace(wire, `"ref":"refs/heads/main"`, fragment, 1)
		case `"old":"bad"`:
			wire = strings.Replace(wire, `"old":"`+old+`"`, fragment, 1)
		case `"command_digest":"wrong"`:
			wire = strings.Replace(wire, `"command_digest":"abc"`, fragment, 1)
		case `"request_id":"r-<>"`, `"source_error":1`, `"unknown":1`:
			wire = strings.TrimSuffix(wire, "}") + `,` + fragment + `}`
		}
		if validGitPrediction([]byte(wire)) {
			t.Errorf("malformed reply accepted: %s", fragment)
		}
	}
}
