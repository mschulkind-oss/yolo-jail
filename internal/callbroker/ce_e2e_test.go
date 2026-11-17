package callbroker

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/s3fixture"
)

const cePeriod = "Start=2026-01-01,End=2026-02-01"

func (f *s3E2E) roots(roots ...string) {
	f.t.Helper()
	scope, err := brokerscope.ReadFile(f.h.scopePath)
	if err != nil {
		f.t.Fatal(err)
	}
	scope.Repos = roots
	if _, err := brokerscope.Write(scope); err != nil {
		f.t.Fatal(err)
	}
}
func (f *s3E2E) invocations() [][]string {
	f.t.Helper()
	file, err := os.Open(filepath.Join(f.home, s3fixture.InvocationLog))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	out := [][]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var args []string
		if err := json.Unmarshal(scanner.Bytes(), &args); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, args)
	}
	if err := scanner.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *s3E2E) ceDenied(args ...string) {
	f.t.Helper()
	before, invoked := f.requests(), len(f.invocations())
	rc, out := f.aws("", args...)
	if rc != 64 || out == "" {
		f.t.Fatalf("denial rc=%d out=%q args=%q", rc, out, args)
	}
	if f.requests() != before || len(f.invocations()) != invoked {
		f.t.Fatalf("refused invocation reached host AWS: %q", args)
	}
	if len(f.pending()) != 0 {
		f.t.Fatal("CE refusal filed approval")
	}
}

func TestCEReadThroughShippedAWSMediation(t *testing.T) {
	f := newS3E2E(t)
	f.roots(s3Root, "ce://host-login")
	before := f.requests()
	rc, out := f.aws("", "ce", "get-tags", "--time-period", cePeriod)
	if rc != 0 || out != "{\"Tags\":[\"fixture\"]}\n" {
		t.Fatalf("CE mediation rc=%d output=%q", rc, out)
	}
	if f.requests() != before+1 || len(f.invocations()) != 1 {
		t.Fatal("controlled fake did not make one request in one invocation")
	}
	if len(f.pending()) != 0 {
		t.Fatal("read filed an approval request")
	}
}

func TestCEShippedRowsCanonicalExecutionAndOwnRoot(t *testing.T) {
	f := newS3E2E(t)
	rows := []struct {
		op              string
		args, canonical []string
		out             string
	}{
		{"get-cost-and-usage", []string{"--metrics", "UsageQuantity", "UnblendedCost", "--granularity=MONTHLY", "--time-period", "End=2026-02-01,Start=2026-01-01"}, []string{"--time-period", cePeriod, "--granularity", "MONTHLY", "--metrics", "UnblendedCost", "UsageQuantity"}, "{\"ResultsByTime\":[{\"Total\":{\"UnblendedCost\":{\"Amount\":\"12.34\",\"Unit\":\"USD\"}}}]}\n"},
		{"get-cost-forecast", []string{"--metric", "UNBLENDED_COST", "--time-period", cePeriod, "--granularity", "DAILY", "--prediction-interval-level", "99"}, []string{"--time-period", cePeriod, "--granularity", "DAILY", "--metric", "UNBLENDED_COST", "--prediction-interval-level", "99"}, "{\"Total\":{\"Amount\":\"23.45\",\"Unit\":\"USD\"}}\n"},
		{"get-dimension-values", []string{"--dimension", "SERVICE", "--time-period", `{"End":"2026-02-01","Start":"2026-01-01"}`, "--max-results=1000", "--context=COST_AND_USAGE"}, []string{"--time-period", cePeriod, "--dimension", "SERVICE", "--max-results", "1000", "--context", "COST_AND_USAGE"}, "{\"DimensionValues\":[{\"Value\":\"Synthetic Service\"}]}\n"},
		{"get-tags", []string{"--time-period=" + cePeriod, "--tag-key=team"}, []string{"--time-period", cePeriod, "--tag-key", "team"}, "{\"Tags\":[\"fixture\"]}\n"},
	}
	for _, row := range rows {
		argv := append([]string{"ce", row.op}, row.args...)
		f.ceDenied(argv...)
		f.roots(s3Root, "ce://host-login")
		before := f.requests()
		invoked := len(f.invocations())
		rc, out := f.aws("", argv...)
		if rc != 0 || out != row.out {
			t.Fatalf("%s rc=%d out=%q", row.op, rc, out)
		}
		canonical := append(append([]string{"ce", row.op}, row.canonical...), "--output", "json", "--no-paginate")
		if f.requests() != before+1 || len(f.invocations()) != invoked+1 || !reflect.DeepEqual(f.invocations()[invoked], canonical) {
			t.Fatalf("not one canonical invocation: %q", f.invocations())
		}
		if !reflect.DeepEqual(f.server.CEArgv()[len(f.server.CEArgv())-1], canonical) {
			t.Fatal("host and service argv differ")
		}
		if len(f.pending()) != 0 {
			t.Fatal("standing read asked")
		}
		f.roots(s3Root)
		f.ceDenied(argv...)
	}
	f.roots("ce://host-login")
	f.ceDenied("s3", "ls", s3Root)
}

func TestCEEscapesDeniedBeforeHostExecutionAndJSONDataPreserved(t *testing.T) {
	f := newS3E2E(t)
	f.roots(s3Root, "ce://host-login")
	for _, flag := range []string{"profile", "endpoint-url", "region", "debug", "no-sign-request", "output", "query", "no-paginate", "cli-input-json", "cli-input-yaml", "billing-view-arn", "sort-by"} {
		f.ceDenied("ce", "get-tags", "--time-period", cePeriod, "--"+flag+"=file://secret")
	}
	for _, flag := range []string{"filter", "tag-key", "search-string", "next-page-token"} {
		for _, v := range []string{"file://secret", "fileb://secret", "https://secret", "-x"} {
			f.ceDenied("ce", "get-tags", "--time-period", cePeriod, "--"+flag, v)
		}
	}
	for _, flag := range []string{"metrics", "group-by"} {
		f.ceDenied("ce", "get-cost-and-usage", "--time-period", cePeriod, "--granularity", "DAILY", "--metrics", "UnblendedCost", "--"+flag, "file://secret")
	}
	for _, service := range []string{"ec2", "sts", "future"} {
		f.ceDenied(service, "get-anything")
	}
	for _, op := range []string{"GetAnything", "get-cost-and-usage-with-resources", "get-usage-forecast", "create-cost-category-definition"} {
		f.ceDenied("ce", op, "--time-period", cePeriod)
	}
	filter := `{"Tags":{"Values":["file://x","-x","https://x"],"Key":"-x"}}`
	canonical := `{"Tags":{"Key":"-x","Values":["file://x","-x","https://x"]}}`
	for _, groups := range [][]string{{"Type=TAG,Key=file://x", "Type=COST_CATEGORY,Key=-x"}, {`[{"Key":"file://x","Type":"TAG"},{"Key":"-x","Type":"COST_CATEGORY"}]`}} {
		argv := []string{"ce", "get-cost-and-usage", "--time-period", cePeriod, "--granularity", "DAILY", "--metrics", "UnblendedCost", "--filter", filter, "--group-by"}
		argv = append(argv, groups...)
		rc, out := f.aws("", argv...)
		if rc != 0 {
			t.Fatalf("safe nested data rc=%d out=%q", rc, out)
		}
		got := f.invocations()[len(f.invocations())-1]
		want := []string{"ce", "get-cost-and-usage", "--time-period", cePeriod, "--granularity", "DAILY", "--metrics", "UnblendedCost", "--group-by", `[{"Type":"TAG","Key":"file://x"},{"Type":"COST_CATEGORY","Key":"-x"}]`, "--filter", canonical, "--output", "json", "--no-paginate"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("nested canonical=%q want=%q", got, want)
		}
	}
	// Jail HOME, login variables, credentials and endpoint cannot replace pinned host env.
	cmd := exec.Command(f.shim, "ce", "get-tags", "--time-period", cePeriod)
	cmd.Env = []string{f.envKey + "=" + f.endpoint, "PATH=" + filepath.Dir(f.yolo) + ":/usr/bin:/bin", "HOME=" + filepath.Join(f.home, "jail-home"), "AWS_ENDPOINT_URL=http://192.0.2.1:1", "AWS_PROFILE=jail", "AWS_REGION=jail", "AWS_ACCESS_KEY_ID=jail", "AWS_SECRET_ACCESS_KEY=jail", "AWS_CONFIG_FILE=file://jail"}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "{\"Tags\":[\"fixture\"]}\n" {
		t.Fatalf("jail env replaced host execution: %v %s", err, out)
	}
	f.roots(s3Root)
	deniedCmd := exec.Command(f.shim, "ce", "get-tags", "--time-period", cePeriod)
	deniedCmd.Env = cmd.Env
	out, err = deniedCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "ce://host-login") {
		t.Fatalf("jail env widened scope: %v %s", err, out)
	}
}

func TestCEPaginationHostErrorsRedactionAndAudit(t *testing.T) {
	f := newS3E2E(t)
	f.roots(s3Root, "ce://host-login")
	call := func(args ...string) (int, string) {
		return f.aws("", append([]string{"ce", "get-tags", "--time-period", cePeriod}, args...)...)
	}
	rc, out := call("--search-string", "fixture-pages")
	if rc != 0 || out != "{\"Tags\":[\"first-page\"],\"NextPageToken\":\"fixture-next\"}\n" || f.requests() != 1 || len(f.invocations()) != 1 {
		t.Fatalf("first page: %d %q", rc, out)
	}
	rc, out = call("--next-page-token=fixture-next")
	if rc != 0 || out != "{\"Tags\":[\"second-page\"]}\n" || f.requests() != 2 || len(f.invocations()) != 2 {
		t.Fatalf("explicit next page: %d %q", rc, out)
	}
	rc, out = call("--search-string", "fixture-error")
	if rc != 254 || !strings.Contains(out, "synthetic CE service error; retry") {
		t.Fatalf("service error: %d %q", rc, out)
	}
	rc, out = call("--search-string", "fixture-redaction")
	if rc != 0 || strings.Contains(out, s3fixture.SecretAccessKey) || strings.Contains(out, s3fixture.AccessKeyID) {
		t.Fatalf("redaction: %d %q", rc, out)
	}
	events, skipped, err := brokeraudit.Read(filepath.Join(f.home, "audit.jsonl"))
	if err != nil || skipped != 0 || len(events) < 4 {
		t.Fatalf("audit %v %d %+v", err, skipped, events)
	}
	last := events[len(events)-1]
	if last.Service != "aws" || last.Set != "read" || last.Outcome != "ran" || last.CommandDigest == "" || len(last.Argv) != 0 {
		t.Fatalf("audit fields: %+v", last)
	}
	// Protocol and all retained fixture records are credential-free, not just stdout.
	for _, p := range []string{filepath.Join(f.home, "selected.json"), filepath.Join(f.home, "audit.jsonl"), filepath.Join(f.home, s3fixture.InvocationLog)} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), s3fixture.SecretAccessKey) || strings.Contains(string(b), s3fixture.AccessKeyID) {
			t.Fatalf("secret in %s", p)
		}
	}
}

func TestAWSReadWithdrawalAndLatestApprovedScopeAreAuthoritative(t *testing.T) {
	f := newS3E2E(t)
	f.roots(s3Root, "ce://host-login")
	args := []string{"ce", "get-tags", "--time-period", cePeriod}
	if rc, out := f.aws("", args...); rc != 0 {
		t.Fatalf("initial read %d %q", rc, out)
	}
	_, old, err := loopholegrant.Read(f.h.input.GrantFile)
	if err != nil || old == nil {
		t.Fatal(err)
	}
	next := *old
	next.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.h.input.GrantFile, *old, next); err != nil {
		t.Fatal(err)
	}
	f.ceDenied(args...)
	f.ceDenied("s3", "ls", s3Root)
	if err := loopholegrant.Replace(context.Background(), f.h.input.GrantFile, next, *old); err != nil {
		t.Fatal(err)
	}
	// A jail-side edit does not change the approved root file.
	if err := os.WriteFile(filepath.Join(f.home, ".yolo.toml"), []byte("[brokered.aws]\nroots=['s3://bkt/team/']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if rc, out := f.aws("", args...); rc != 0 {
		t.Fatalf("unapproved config changed live scope %d %q", rc, out)
	}
	f.roots(s3Root)
	f.ceDenied(args...)
	// The host read ceiling is independently revocable for both services.
	if err := os.WriteFile(filepath.Join(f.home, ".config", "yolo-jail", "config.toml"), []byte("packs=['aws']\n[allow.aws]\nalways=[]\non_request=[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.ceDenied("s3", "ls", s3Root)
}

func TestAWSNeverConsultsRetiredSourceRequestsOrAnswers(t *testing.T) {
	f := newS3E2E(t)
	args := []string{"s3", "cp", "-", s3Root + "old.txt"}
	if rc, out := f.aws("old body", args...); rc != 75 {
		t.Fatalf("new write did not ask: %d %q", rc, out)
	}
	pending := f.pending()
	if len(pending) != 1 {
		t.Fatal(pending)
	}
	old := &brokerstore.Store{Dir: paths.BrokerSourceDir("aws-s3"), Source: "aws-s3", PublishRecords: true,
		Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	if err := os.MkdirAll(old.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	q := pending[0]
	q.ID = ""
	q.Set = "aws-s3"
	q.Repo = "aws-s3"
	retired, err := old.File(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Decide(retired.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	resources := pending[0].CallWrites[0].Chain
	if _, _, err := old.AddWorking(brokerstore.WorkingEntry{Kind: brokerstore.WorkingCallPrefix + "put_objects", Workspace: f.home, Repo: "aws", Resource: brokerstore.CallResourceKey(resources), By: "synthetic-retired-answer", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if rc, out := f.aws("old body", args...); rc != 75 || f.server.Writes() != 0 {
		t.Fatalf("old answer authorized new source: %d %q", rc, out)
	}
	view, err := f.host("approve", "--json")
	if err != nil {
		t.Fatalf("approve list %v %s", err, view)
	}
	var inventory struct {
		Sources  []struct{ Source, Status, Authority string }
		Requests []struct{ ID, Source string }
	}
	start := strings.Index(view, `{"schema":`)
	if start < 0 || json.Unmarshal([]byte(view[start:]), &inventory) != nil {
		t.Fatalf("invalid approval inventory: %s", view)
	}
	selected, removed := false, false
	for _, s := range inventory.Sources {
		selected = selected || s.Source == "aws" && s.Status == "selected" && s.Authority == "core"
		removed = removed || s.Source == "aws-s3" && s.Status == "removed" && s.Authority == "deny-only"
	}
	if !selected || !removed {
		t.Fatalf("selection/history authority changed: %+v", inventory.Sources)
	}
	for _, r := range inventory.Requests {
		if r.Source != "aws" || r.ID == retired.ID {
			t.Fatalf("old request entered active inventory: %+v", r)
		}
	}
	if out, err := f.host("approve", retired.ID); err == nil || out == "" {
		t.Fatalf("retired source answer activated: %v %s", err, out)
	}
	after, err := old.Snapshot()
	if err != nil || len(after.Grants) != 1 || after.Grants[0].Uses != 1 {
		t.Fatalf("old grant moved/spent: %+v %v", after, err)
	}
}
