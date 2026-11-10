// Package s3fixture is a test and demo fixture: a path-style subset of S3 served over
// plain HTTP on 127.0.0.1, and a fake host `aws` command that speaks to it. It is
// never shipped, never imported by shipped code, and never contacts AWS: every
// credential here is a synthetic literal and every endpoint is the loopback.
//
// It deliberately imports neither internal/callbroker nor internal/s3classify, so the
// code that mediates a call and the service the call lands on cannot share a bug.
package s3fixture

import (
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // S3's ETag is an MD5; this is not a security use.
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// The synthetic credentials and region every demo and test uses. They are not, and
// must never be replaced by, a real credential.
const (
	AccessKeyID     = "AKIDSYNTHETICFIXTURE"
	SecretAccessKey = "synthetic-secret-fixture-only-not-a-real-key"
	Region          = "us-east-1"
)

// maxBody caps a request body the fixture will read.
const maxBody = 8 << 20

// Options configures a Server.
type Options struct {
	// Credentials is the only credential the server accepts.
	Credentials sigv4.Credentials
	// Region is the signing region the server expects; empty means Region.
	Region string
	// Buckets seeds bucket -> key -> bytes. The map is copied.
	Buckets map[string]map[string][]byte
	// Now is the clock for LastModified; nil means time.Now.
	Now func() time.Time
	// OnRequest, if set, is called with each request after it is logged (the demo
	// prints it). It must not call back into the Server.
	OnRequest func(LoggedRequest)
}

// LoggedRequest is one request as the server saw it. It carries no header, body or
// credential.
type LoggedRequest struct {
	Method     string
	Path       string
	Query      string
	Authorized bool
	Status     int
}

type object struct {
	data []byte
	mod  time.Time
}

// Server is the fake S3. Its zero value is not usable; call NewServer.
type Server struct {
	creds  sigv4.Credentials
	region string
	now    func() time.Time
	onReq  func(LoggedRequest)

	mu      sync.Mutex
	buckets map[string]map[string]object
	log     []LoggedRequest
	writes  int

	srv *http.Server
	ln  net.Listener
}

// NewServer builds a Server that is not yet listening. Use it as an http.Handler, or
// call Start.
func NewServer(o Options) *Server {
	s := &Server{creds: o.Credentials, region: o.Region, now: o.Now, onReq: o.OnRequest, buckets: map[string]map[string]object{}}
	if s.region == "" {
		s.region = Region
	}
	if s.now == nil {
		s.now = time.Now
	}
	for b, keys := range o.Buckets {
		s.buckets[b] = map[string]object{}
		for k, v := range keys {
			s.buckets[b][k] = object{data: append([]byte(nil), v...), mod: s.now().UTC()}
		}
	}
	return s
}

// Start listens on 127.0.0.1 on a free port and serves in the background.
func (s *Server) Start() error { return s.StartAddr("127.0.0.1:0") }

// StartAddr is Start on addr, which must be a loopback address.
func (s *Server) StartAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("s3fixture: %q is not a loopback address; the fixture serves 127.0.0.1 only", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// URL is the base URL of a started server.
func (s *Server) URL() string {
	if s.ln == nil {
		return ""
	}
	return "http://" + s.ln.Addr().String()
}

// Close stops a started server.
func (s *Server) Close() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

// TB is the part of testing.TB MustStart needs; it keeps package testing out of the
// non-test build.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// MustStart starts s and closes it when the test ends.
func MustStart(t TB, s *Server) {
	t.Helper()
	if err := s.Start(); err != nil {
		t.Fatalf("s3fixture: start: %v", err)
	}
	t.Cleanup(s.Close)
}

// Object returns a copy of the stored object.
func (s *Server) Object(bucket, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.buckets[bucket][key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), o.data...), true
}

// Keys lists a bucket's keys, sorted.
func (s *Server) Keys(bucket string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedKeys(bucket)
}

func (s *Server) sortedKeys(bucket string) []string {
	var keys []string
	for k := range s.buckets[bucket] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RequestLog is a copy of every request seen, in arrival order.
func (s *Server) RequestLog() []LoggedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LoggedRequest(nil), s.log...)
}

// Writes counts the mutating requests (PUT, DELETE) that were authorized and accepted.
func (s *Server) Writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

type s3Error struct {
	Status  int
	Code    string
	Message string
}

func (s *Server) record(r *http.Request, authorized bool, status int, wrote bool) {
	entry := LoggedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Authorized: authorized, Status: status}
	s.mu.Lock()
	s.log = append(s.log, entry)
	if wrote && status/100 == 2 {
		s.writes++
	}
	s.mu.Unlock()
	if s.onReq != nil {
		s.onReq(entry)
	}
}

// ServeHTTP is the S3 subset.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		s.fail(w, r, false, s3Error{400, "InvalidRequest", "request body unreadable or too large"})
		return
	}
	if !s.verify(r, body) {
		s.fail(w, r, false, s3Error{403, "AccessDenied", "Access Denied"})
		return
	}
	bucket, key := splitPath(r.URL.Path)
	mutating := r.Method == http.MethodPut || r.Method == http.MethodDelete
	status, e := s.handle(w, r, bucket, key, body)
	if e != nil {
		s.fail(w, r, true, *e)
		return
	}
	s.record(r, true, status, mutating)
}

func splitPath(p string) (bucket, key string) {
	p = strings.TrimPrefix(p, "/")
	bucket, key, _ = strings.Cut(p, "/")
	return bucket, key
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, authorized bool, e s3Error) {
	s.record(r, authorized, e.Status, false)
	if r.Method == http.MethodHead {
		w.WriteHeader(e.Status)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(e.Status)
	type errBody struct {
		XMLName xml.Name `xml:"Error"`
		Code    string
		Message string
	}
	out, _ := xml.Marshal(errBody{Code: e.Code, Message: e.Message})
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

func etag(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // see import
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// handle runs an authorized request and returns the status it wrote, or an error for
// the caller to render.
func (s *Server) handle(w http.ResponseWriter, r *http.Request, bucket, key string, body []byte) (int, *s3Error) {
	if bucket == "" {
		return 0, &s3Error{501, "NotImplemented", "only path-style bucket and object operations are implemented"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	objs, bucketExists := s.buckets[bucket]
	if !bucketExists {
		return 0, &s3Error{404, "NoSuchBucket", "The specified bucket does not exist"}
	}
	switch {
	case key == "" && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		return s.list(w, bucket, r.URL.Query()), nil
	case key == "":
		return 0, &s3Error{501, "NotImplemented", "only ListObjectsV2 is implemented on a bucket"}
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		o, ok := objs[key]
		if !ok {
			return 0, &s3Error{404, "NoSuchKey", "The specified key does not exist"}
		}
		h := w.Header()
		h.Set("ETag", etag(o.data))
		h.Set("Last-Modified", o.mod.UTC().Format(http.TimeFormat))
		h.Set("Content-Type", "binary/octet-stream")
		h.Set("Content-Length", fmt.Sprint(len(o.data)))
		w.WriteHeader(200)
		if r.Method == http.MethodGet {
			_, _ = w.Write(o.data)
		}
		return 200, nil
	case r.Method == http.MethodPut:
		objs[key] = object{data: append([]byte(nil), body...), mod: s.now().UTC()}
		w.Header().Set("ETag", etag(body))
		w.WriteHeader(200)
		return 200, nil
	case r.Method == http.MethodDelete:
		delete(objs, key)
		w.WriteHeader(204)
		return 204, nil
	}
	return 0, &s3Error{405, "MethodNotAllowed", "the method is not implemented"}
}

type listContents struct {
	Key          string
	LastModified string
	ETag         string
	Size         int
	StorageClass string
}

type listPrefix struct {
	Prefix string
}

func (s *Server) list(w http.ResponseWriter, bucket string, q url.Values) int {
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	var contents []listContents
	var prefixes []listPrefix
	seen := map[string]bool{}
	for _, k := range s.sortedKeys(bucket) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if delim != "" {
			if i := strings.Index(k[len(prefix):], delim); i >= 0 {
				cp := k[:len(prefix)+i+len(delim)]
				if !seen[cp] {
					seen[cp] = true
					prefixes = append(prefixes, listPrefix{cp})
				}
				continue
			}
		}
		o := s.buckets[bucket][k]
		contents = append(contents, listContents{k, o.mod.UTC().Format("2006-01-02T15:04:05.000Z"), etag(o.data), len(o.data), "STANDARD"})
	}
	type result struct {
		XMLName        xml.Name `xml:"ListBucketResult"`
		Name           string
		Prefix         string
		KeyCount       int
		MaxKeys        int
		IsTruncated    bool
		Contents       []listContents
		CommonPrefixes []listPrefix
	}
	out, _ := xml.Marshal(result{Name: bucket, Prefix: prefix, KeyCount: len(contents) + len(prefixes), MaxKeys: 1000, Contents: contents, CommonPrefixes: prefixes})
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
	return 200
}

// verify checks the SigV4 Authorization header by re-signing a clone of the request
// with the same X-Amz-Date and body, and the x-amz-content-sha256 header against the
// body.
func (s *Server) verify(r *http.Request, body []byte) bool {
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !ok {
		return false
	}
	fields := map[string]string{}
	for _, part := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		fields[k] = v
	}
	cred := strings.Split(fields["Credential"], "/")
	if len(cred) != 5 || cred[0] != s.creds.AccessKeyID || cred[2] != s.region || cred[3] != "s3" || cred[4] != "aws4_request" {
		return false
	}
	amzDate := r.Header.Get("X-Amz-Date")
	when, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil || when.UTC().Format("20060102") != cred[1] {
		return false
	}
	sum := sha256.Sum256(body)
	if !hmac.Equal([]byte(r.Header.Get("X-Amz-Content-Sha256")), []byte(hex.EncodeToString(sum[:]))) {
		return false
	}
	clone := &http.Request{
		Method: r.Method,
		URL:    &url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery},
		Host:   r.Host,
		Header: http.Header{},
	}
	for _, name := range strings.Split(fields["SignedHeaders"], ";") {
		switch name {
		case "host", "x-amz-date", "x-amz-content-sha256", "x-amz-security-token", "":
		default:
			for _, v := range r.Header.Values(name) {
				clone.Header.Add(name, v)
			}
		}
	}
	err = sigv4.Sign(clone, body, s.creds, sigv4.Options{Region: s.region, Service: "s3", SinglePathEncoding: true, ContentSHA256Header: true, Time: when})
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(clone.Header.Get("Authorization")), []byte(auth))
}
