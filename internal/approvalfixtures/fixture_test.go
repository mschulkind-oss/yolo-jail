package approvalfixtures

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every committed fixture parses, validates and is named after its id.
func TestEveryFixtureIsValid(t *testing.T) {
	fs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) == 0 {
		t.Fatal("no fixtures")
	}
}

// valid is a minimal fixture every rule accepts; each case below breaks one rule.
func valid() Fixture {
	return Fixture{
		Schema: Schema, ID: "x", Title: "t", Family: "git", Support: "synthetic", SupportNote: "n",
		Prompt: "request", State: "pending", Story: "s", Origin: "hand-written",
		Request: json.RawMessage(`{"schema":1,"id":"r-1","source":"git","workspace":"/home/dev/code/app","repo":"example-org/app","state":"pending","headline":"h"}`),
		HookEnv: map[string]string{"YOLO_REQUEST_ID": "r-1"},
		Evidence: []Evidence{
			{Platform: "linux", Kind: "missing", Renderer: "r", Note: "not yet"},
			{Platform: "macos", Kind: "missing", Renderer: "r", Note: "not yet"},
		},
	}
}

func TestValidateRefusesEachBrokenRule(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("the valid fixture: %v", err)
	}
	native := Evidence{Platform: "linux", Kind: "native-capture", Renderer: "r", File: "captures/linux/a.png", Tested: "visual",
		Provenance: map[string]string{"os": "o", "daemon": "d", "daemon_version": "1", "theme": "t", "scale": "1", "captured_at": "c", "commit": "c"}}
	cases := []struct {
		name, want string
		mut        func(*Fixture)
	}{
		{"a real request written by hand", "never hand-written", func(f *Fixture) { f.Support = "real" }},
		{"a synthetic request claimed captured", "hand-written", func(f *Fixture) { f.Origin = "captured"; f.CapturedWith = "yolo approve --json" }},
		{"a captured request naming no command", "names the command", func(f *Fixture) { f.Support = "real"; f.Origin = "captured" }},
		{"no macos evidence", "no macos evidence", func(f *Fixture) { f.Evidence = f.Evidence[:1] }},
		{"a native capture without provenance", `provenance "daemon"`, func(f *Fixture) {
			n := native
			n.Provenance = map[string]string{"os": "o"}
			f.Evidence[0] = n
		}},
		{"a browser tile claimed native", "never a native capture", func(f *Fixture) { n := native; n.Platform = "browser"; f.Evidence = append(f.Evidence, n) }},
		{"a placeholder file on missing evidence", "missing evidence has no file", func(f *Fixture) { f.Evidence[0].File = "placeholder.png" }},
		{"missing evidence with no reason", "says why", func(f *Fixture) { f.Evidence[0].Note = "" }},
		{"a preview claiming an action test", "tests no action", func(f *Fixture) {
			f.Evidence = append(f.Evidence, Evidence{Platform: "browser", Kind: "preview", Renderer: "r", Tested: "action"})
		}},
		{"a prompt where policy needs none", "carries no request", func(f *Fixture) { f.Prompt = "none"; f.State = "no-prompt" }},
		{"an unsanitized workspace", "sanitized /home/dev", func(f *Fixture) {
			f.Request = json.RawMessage(`{"schema":1,"id":"r-1","source":"git","workspace":"/home/someone/work","state":"pending","headline":"h"}`)
		}},
		{"an unsanitized repository", "sanitized example-org", func(f *Fixture) {
			f.Request = json.RawMessage(`{"schema":1,"id":"r-1","source":"git","workspace":"/home/dev/code/app","repo":"acme/secret","state":"pending","headline":"h"}`)
		}},
		{"hook env for another request", "not the request's id", func(f *Fixture) { f.HookEnv["YOLO_REQUEST_ID"] = "r-2" }},
		{"an unknown state", "state", func(f *Fixture) { f.State = "approved" }},
		{"a decision that is not approve", `starts with "approve"`, func(f *Fixture) { f.Decision = &Decision{Argv: []string{"audit"}} }},
		{"a native capture outside its platform's directory", "is under captures/linux/", func(f *Fixture) {
			n := native
			n.File = "captures/macos/a.png"
			f.Evidence[0] = n
		}},
		{"one renderer both missing and captured", "both missing and captured", func(f *Fixture) { f.Evidence = append(f.Evidence, native) }},
		{"a hook env key that is not a request field", "not a YOLO_REQUEST_ variable", func(f *Fixture) { f.HookEnv["LD_PRELOAD"] = "x" }},
		{"a store state the request does not have", `the request's state is "pending"`, func(f *Fixture) { f.State = "denied" }},
		{"decided elsewhere without exit 3", "exited 3", func(f *Fixture) { f.State = "decided-elsewhere" }},
		{"stale without exit 6", "exited 6", func(f *Fixture) { f.State = "stale"; f.Decision = &Decision{Argv: []string{"approve"}, Exit: 0} }},
		{"a file escaping the gallery", "not a relative path", func(f *Fixture) {
			n := native
			n.File = "../../secret.png"
			f.Evidence[0] = n
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := valid()
			f.HookEnv = map[string]string{"YOLO_REQUEST_ID": "r-1"}
			f.Evidence = append([]Evidence(nil), f.Evidence...)
			c.mut(&f)
			err := f.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// Parse refuses a field the format does not define, so a typo cannot pass as a label.
func TestParseRefusesUnknownFields(t *testing.T) {
	b, _ := json.Marshal(valid())
	b = []byte(strings.Replace(string(b), `"schema":1,`, `"schema":1,"suport":"real",`, 1))
	if _, err := Parse(b); err == nil || !strings.Contains(err.Error(), "suport") {
		t.Fatalf("got %v", err)
	}
}
