package s3fixture

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(Options{
		Credentials: sigv4.Credentials{AccessKeyID: AccessKeyID, SecretAccessKey: SecretAccessKey},
		Region:      Region,
		Buckets: map[string]map[string][]byte{
			"bkt": {"a.txt": []byte("hello world!"), "p/one": []byte("1"), "p/two/x": []byte("22"), "q": []byte("q")},
		},
		Now: func() time.Time { return fixedNow },
	})
	MustStart(t, s)
	return s
}

func envFor(s *Server) func(string) string {
	m := map[string]string{
		"AWS_ENDPOINT_URL_S3":   s.URL(),
		"AWS_ACCESS_KEY_ID":     AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": SecretAccessKey,
	}
	return func(k string) string { return m[k] }
}

func aws(t *testing.T, s *Server, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := RunAWS(args, envFor(s), strings.NewReader(stdin), &out, &errb)
	if strings.Contains(out.String()+errb.String(), SecretAccessKey) {
		t.Fatalf("secret leaked in output of %v", args)
	}
	return code, out.String(), errb.String()
}

func TestSignedRoundTripOfEveryOp(t *testing.T) {
	s := newTestServer(t)

	if code, out, _ := aws(t, s, "", "s3", "cp", "s3://bkt/a.txt", "-"); code != 0 || out != "hello world!" {
		t.Fatalf("get: %d %q", code, out)
	}
	code, out, _ := aws(t, s, "", "s3api", "head-object", "--bucket", "bkt", "--key", "a.txt")
	if code != 0 || !strings.Contains(out, `"ContentLength": 12`) || !strings.Contains(out, "ETag") || !strings.Contains(out, "LastModified") {
		t.Fatalf("head: %d %q", code, out)
	}
	if code, out, _ := aws(t, s, "new bytes", "s3", "cp", "-", "s3://bkt/n/new"); code != 0 || out != "upload: - to s3://bkt/n/new\n" {
		t.Fatalf("put: %d %q", code, out)
	}
	if got, ok := s.Object("bkt", "n/new"); !ok || string(got) != "new bytes" {
		t.Fatalf("object after put: %q %v", got, ok)
	}
	if code, out, _ := aws(t, s, "", "s3", "rm", "s3://bkt/n/new"); code != 0 || out != "delete: s3://bkt/n/new\n" {
		t.Fatalf("rm: %d %q", code, out)
	}
	if _, ok := s.Object("bkt", "n/new"); ok {
		t.Fatal("object survived rm")
	}
	if s.Writes() != 2 {
		t.Fatalf("writes = %d, want 2", s.Writes())
	}
	for _, r := range s.RequestLog() {
		if !r.Authorized {
			t.Fatalf("unauthorized request in a signed run: %+v", r)
		}
	}
}

func TestListingWithAndWithoutRecursiveAndPrefix(t *testing.T) {
	s := newTestServer(t)

	_, out, _ := aws(t, s, "", "s3", "ls", "s3://bkt")
	want := "                           PRE p/\n2026-01-01 00:00:00         12 a.txt\n2026-01-01 00:00:00          1 q\n"
	if out != want {
		t.Fatalf("ls b:\n%q\nwant\n%q", out, want)
	}
	_, out, _ = aws(t, s, "", "s3", "ls", "s3://bkt/p/")
	want = "                           PRE two/\n2026-01-01 00:00:00          1 one\n"
	if out != want {
		t.Fatalf("ls b/p/:\n%q\nwant\n%q", out, want)
	}
	_, out, _ = aws(t, s, "", "s3", "ls", "--recursive", "s3://bkt/p/")
	want = "2026-01-01 00:00:00          1 p/one\n2026-01-01 00:00:00          2 p/two/x\n"
	if out != want {
		t.Fatalf("ls -r:\n%q\nwant\n%q", out, want)
	}
	// A prefix is a string prefix, not a directory: "q" matches only key q.
	_, out, _ = aws(t, s, "", "s3", "ls", "s3://bkt/q", "--recursive")
	if out != "2026-01-01 00:00:00          1 q\n" {
		t.Fatalf("ls q: %q", out)
	}
}

func TestMissingThingsAreS3Errors(t *testing.T) {
	s := newTestServer(t)
	code, _, errs := aws(t, s, "", "s3", "cp", "s3://bkt/nope", "-")
	if code != 254 || !strings.Contains(errs, "An error occurred (NoSuchKey)") {
		t.Fatalf("missing key: %d %q", code, errs)
	}
	code, _, errs = aws(t, s, "", "s3", "ls", "s3://nobucket")
	if code != 254 || !strings.Contains(errs, "An error occurred (NoSuchBucket)") {
		t.Fatalf("missing bucket: %d %q", code, errs)
	}
	code, _, errs = aws(t, s, "", "s3api", "head-object", "--bucket=bkt", "--key=nope")
	if code != 254 || !strings.Contains(errs, "An error occurred (404)") {
		t.Fatalf("head missing: %d %q", code, errs)
	}
}

func TestUnsignedRequestIsRefusedLoggedAndChangesNothing(t *testing.T) {
	s := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPut, s.URL()+"/bkt/evil", strings.NewReader("x"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(string(body), "AccessDenied") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if _, ok := s.Object("bkt", "evil"); ok || s.Writes() != 0 {
		t.Fatal("unsigned PUT changed state")
	}
	log := s.RequestLog()
	if len(log) != 1 || log[0].Authorized || log[0].Status != 403 || log[0].Method != "PUT" || log[0].Path != "/bkt/evil" {
		t.Fatalf("log: %+v", log)
	}
}

func signedRequest(t *testing.T, s *Server, method, path, secret string, signBody, sendBody []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+path, bytes.NewReader(sendBody))
	if err != nil {
		t.Fatal(err)
	}
	creds := sigv4.Credentials{AccessKeyID: AccessKeyID, SecretAccessKey: secret}
	if err := sigv4.Sign(req, signBody, creds, sigv4.Options{Region: Region, Service: "s3", SinglePathEncoding: true, ContentSHA256Header: true, Time: fixedNow}); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestWrongSecretAndBodyMismatchAreRefused(t *testing.T) {
	s := newTestServer(t)
	good := signedRequest(t, s, http.MethodPut, "/bkt/ok", SecretAccessKey, []byte("v"), []byte("v"))
	resp, err := http.DefaultClient.Do(good)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("control: %v %v", resp, err)
	}
	resp.Body.Close()
	for name, req := range map[string]*http.Request{
		"wrong secret":  signedRequest(t, s, http.MethodPut, "/bkt/bad1", "not-the-secret", []byte("v"), []byte("v")),
		"body mismatch": signedRequest(t, s, http.MethodPut, "/bkt/bad2", SecretAccessKey, []byte("v"), []byte("tampered")),
	} {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
	}
	// A forged content hash header on a tampered body.
	forged := signedRequest(t, s, http.MethodPut, "/bkt/bad3", SecretAccessKey, []byte("v"), []byte("tampered"))
	forged.Header.Set("X-Amz-Content-Sha256", "00")
	resp, err = http.DefaultClient.Do(forged)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("forged hash: %d", resp.StatusCode)
	}
	for _, k := range []string{"bad1", "bad2", "bad3"} {
		if _, ok := s.Object("bkt", k); ok {
			t.Errorf("%s was written", k)
		}
	}
	if s.Writes() != 1 {
		t.Fatalf("writes = %d", s.Writes())
	}
}

func TestRequestLogIsACopy(t *testing.T) {
	s := newTestServer(t)
	aws(t, s, "", "s3", "ls", "s3://bkt")
	l := s.RequestLog()
	l[0].Method = "MUTATED"
	if s.RequestLog()[0].Method == "MUTATED" {
		t.Fatal("log is aliased")
	}
}

func TestClientRefusesAnythingOutsideTheGrammarWithoutARequest(t *testing.T) {
	s := newTestServer(t)
	for _, args := range [][]string{
		{"s3", "sync", ".", "s3://bkt"},
		{"--endpoint-url", "http://x", "s3", "ls", "s3://bkt"},
		{"ec2", "describe-instances"},
		{"s3", "cp", "a", "b"},
		{"s3", "cp", "s3://bkt/a", "/tmp/x"},
		{"s3", "cp", "s3://bkt/a", "s3://bkt/c"},
		{"s3", "rm", "--recursive", "s3://bkt/p/"},
		{"s3", "ls", "s3://bkt", "--profile", "x"},
		{"s3", "ls"},
		{"s3api", "get-object", "--bucket", "bkt", "--key", "a"},
		{"s3api", "head-object", "--bucket", "bkt"},
		{"s3api", "head-object", "--bucket", "bkt", "--bucket", "cbkt", "--key", "k"},
		{"s3", "cp", "s3://bkt/../x", "-"},
		{"--version", "x"},
		{},
	} {
		code, _, errs := aws(t, s, "", args...)
		if code != 252 || errs == "" {
			t.Errorf("%v: exit %d stderr %q", args, code, errs)
		}
	}
	if n := len(s.RequestLog()); n != 0 {
		t.Fatalf("a refused argv made %d request(s)", n)
	}
}

func TestVersionAndEnvironmentRefusals(t *testing.T) {
	var out, errb bytes.Buffer
	if code := RunAWS([]string{"--version"}, func(string) string { return "" }, nil, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "aws-cli/") {
		t.Fatalf("version: %d %q", code, out.String())
	}
	for name, env := range map[string]map[string]string{
		"no endpoint": {"AWS_ACCESS_KEY_ID": "a", "AWS_SECRET_ACCESS_KEY": "s"},
		"no creds":    {"AWS_ENDPOINT_URL_S3": "http://127.0.0.1:1"},
		"bad scheme":  {"AWS_ENDPOINT_URL_S3": "ftp://127.0.0.1", "AWS_ACCESS_KEY_ID": "a", "AWS_SECRET_ACCESS_KEY": "s"},
	} {
		out.Reset()
		errb.Reset()
		code := RunAWS([]string{"s3", "ls", "s3://bkt"}, func(k string) string { return env[k] }, nil, &out, &errb)
		if code == 0 || errb.Len() == 0 || out.Len() != 0 {
			t.Errorf("%s: exit %d out %q err %q", name, code, out.String(), errb.String())
		}
	}
}

func TestGenericEndpointAndRegionFallbacks(t *testing.T) {
	s := newTestServer(t)
	env := map[string]string{"AWS_ENDPOINT_URL": s.URL(), "AWS_ACCESS_KEY_ID": AccessKeyID, "AWS_SECRET_ACCESS_KEY": SecretAccessKey, "AWS_DEFAULT_REGION": Region}
	var out, errb bytes.Buffer
	if code := RunAWS([]string{"s3", "cp", "s3://bkt/q", "-"}, func(k string) string { return env[k] }, nil, &out, &errb); code != 0 || out.String() != "q" {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}
