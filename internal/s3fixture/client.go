package s3fixture

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// Exit codes mirror the real aws CLI: 252 for a command line it cannot parse, 254 for
// a service error, 255 for a configuration problem.
const (
	exitUsage   = 252
	exitService = 254
	exitConfig  = 255
)

var (
	bucketRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	keyRE    = regexp.MustCompile(`^[A-Za-z0-9._=/-]+$`)
)

type command struct {
	kind      string // version, ls, get, head, put, rm
	bucket    string
	key       string // object key, or listing prefix for ls
	recursive bool
}

// RunAWS is a fake host `aws` that implements exactly the canonical argv grammar of
// the S3 mediation slice and refuses everything else with exit 252, before any
// network or environment access: it trusts nothing it is handed.
func RunAWS(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "aws (fixture): %v\n", err)
		return exitUsage
	}
	if cmd.kind == "version" {
		fmt.Fprintln(stdout, "aws-cli/2.0.0-fixture (synthetic; never contacts AWS)")
		return 0
	}

	endpoint := getenv("AWS_ENDPOINT_URL_S3")
	if endpoint == "" {
		endpoint = getenv("AWS_ENDPOINT_URL")
	}
	if endpoint == "" {
		fmt.Fprintln(stderr, "aws (fixture): AWS_ENDPOINT_URL_S3 or AWS_ENDPOINT_URL is required")
		return exitConfig
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		fmt.Fprintln(stderr, "aws (fixture): the endpoint must be an http(s) URL with no path")
		return exitConfig
	}
	creds := sigv4.Credentials{
		AccessKeyID:     getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    getenv("AWS_SESSION_TOKEN"),
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		fmt.Fprintln(stderr, "aws (fixture): AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required")
		return exitConfig
	}
	region := firstNonEmpty(getenv("AWS_REGION"), getenv("AWS_DEFAULT_REGION"), "us-east-1")
	c := &client{base: u.Scheme + "://" + u.Host, creds: creds, region: region, stdout: stdout, stderr: stderr,
		http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	switch cmd.kind {
	case "ls":
		return c.ls(cmd)
	case "get":
		return c.get(cmd)
	case "head":
		return c.head(cmd)
	case "put":
		var data []byte
		if stdin != nil {
			data, err = io.ReadAll(stdin)
			if err != nil {
				fmt.Fprintln(stderr, "aws (fixture): reading standard input failed")
				return exitUsage
			}
		}
		return c.put(cmd, data)
	case "rm":
		return c.rm(cmd)
	}
	return exitUsage
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func parse(args []string) (command, error) {
	bad := func(format string, a ...any) (command, error) {
		return command{}, fmt.Errorf("unsupported command line: "+format, a...)
	}
	if len(args) == 1 && args[0] == "--version" {
		return command{kind: "version"}, nil
	}
	if len(args) < 2 {
		return bad("expected `s3 <verb> ...` or `s3api head-object ...`")
	}
	switch {
	case args[0] == "s3" && args[1] == "ls":
		var url string
		c := command{kind: "ls"}
		for _, a := range args[2:] {
			switch {
			case a == "--recursive" && !c.recursive:
				c.recursive = true
			case strings.HasPrefix(a, "-") || url != "":
				return bad("s3 ls takes one s3:// URL and optionally --recursive")
			default:
				url = a
			}
		}
		b, p, err := parseS3URL(url, true)
		if err != nil {
			return bad("%v", err)
		}
		c.bucket, c.key = b, p
		return c, nil
	case args[0] == "s3" && args[1] == "cp" && len(args) == 4 && args[3] == "-":
		b, k, err := parseS3URL(args[2], false)
		if err != nil {
			return bad("%v", err)
		}
		return command{kind: "get", bucket: b, key: k}, nil
	case args[0] == "s3" && args[1] == "cp" && len(args) == 4 && args[2] == "-":
		b, k, err := parseS3URL(args[3], false)
		if err != nil {
			return bad("%v", err)
		}
		return command{kind: "put", bucket: b, key: k}, nil
	case args[0] == "s3" && args[1] == "rm" && len(args) == 3:
		b, k, err := parseS3URL(args[2], false)
		if err != nil {
			return bad("%v", err)
		}
		return command{kind: "rm", bucket: b, key: k}, nil
	case args[0] == "s3api" && args[1] == "head-object":
		vals := map[string]string{}
		rest := args[2:]
		for i := 0; i < len(rest); i++ {
			name, val, hasEq := strings.Cut(rest[i], "=")
			if name != "--bucket" && name != "--key" {
				return bad("s3api head-object takes only --bucket and --key")
			}
			if !hasEq {
				i++
				if i >= len(rest) {
					return bad("%s needs a value", name)
				}
				val = rest[i]
			}
			if _, dup := vals[name]; dup {
				return bad("%s given twice", name)
			}
			vals[name] = val
		}
		b, k := vals["--bucket"], vals["--key"]
		if !bucketRE.MatchString(b) || strings.Contains(b, "..") || !validKey(k) {
			return bad("bad bucket or key")
		}
		return command{kind: "head", bucket: b, key: k}, nil
	}
	return bad("not one of the supported forms")
}

func validKey(k string) bool {
	if k == "" || len(k) > 1024 || !keyRE.MatchString(k) || strings.HasPrefix(k, "/") || strings.HasSuffix(k, "/") {
		return false
	}
	for _, seg := range strings.Split(k, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// parseS3URL splits s3://B/K. A listing may omit the key or end it in "/".
func parseS3URL(s string, listing bool) (bucket, key string, err error) {
	rest, ok := strings.CutPrefix(s, "s3://")
	if !ok {
		return "", "", fmt.Errorf("%q is not an s3:// URL", s)
	}
	bucket, key, _ = strings.Cut(rest, "/")
	if !bucketRE.MatchString(bucket) || strings.Contains(bucket, "..") {
		return "", "", fmt.Errorf("bad bucket name")
	}
	if listing {
		if key != "" && !keyRE.MatchString(key) {
			return "", "", fmt.Errorf("bad key prefix")
		}
		for _, seg := range strings.Split(strings.TrimSuffix(key, "/"), "/") {
			if key != "" && (seg == "" || seg == "." || seg == "..") {
				return "", "", fmt.Errorf("bad key prefix")
			}
		}
		return bucket, key, nil
	}
	if !validKey(key) {
		return "", "", fmt.Errorf("bad object key")
	}
	return bucket, key, nil
}

type client struct {
	base           string
	creds          sigv4.Credentials
	region         string
	stdout, stderr io.Writer
	http           *http.Client
}

// do sends one signed request. On a non-2xx answer it prints the aws-style error and
// returns that exit code.
func (c *client) do(method, path, rawQuery, op string, body []byte) (*http.Response, []byte, int) {
	u := c.base + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(c.stderr, "aws (fixture): cannot build the request")
		return nil, nil, exitConfig
	}
	if err := sigv4.Sign(req, body, c.creds, sigv4.Options{Region: c.region, Service: "s3", SinglePathEncoding: true, ContentSHA256Header: true}); err != nil {
		fmt.Fprintln(c.stderr, "aws (fixture): signing failed")
		return nil, nil, exitConfig
	}
	resp, err := c.http.Do(req)
	if err != nil {
		fmt.Fprintf(c.stderr, "Could not connect to the endpoint URL: %q\n", c.base)
		return nil, nil, exitService
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		if xml.Unmarshal(data, &e) != nil || e.Code == "" {
			e.Code, e.Message = fmt.Sprint(resp.StatusCode), http.StatusText(resp.StatusCode)
		}
		fmt.Fprintf(c.stderr, "An error occurred (%s) when calling the %s operation: %s\n", e.Code, op, e.Message)
		return nil, nil, exitService
	}
	return resp, data, 0
}

func objPath(bucket, key string) string { return "/" + bucket + "/" + key }

func (c *client) ls(cmd command) int {
	q := url.Values{"list-type": {"2"}}
	if cmd.key != "" {
		q.Set("prefix", cmd.key)
	}
	if !cmd.recursive {
		q.Set("delimiter", "/")
	}
	_, data, code := c.do(http.MethodGet, "/"+cmd.bucket, q.Encode(), "ListObjectsV2", nil)
	if code != 0 {
		return code
	}
	var res struct {
		Contents []struct {
			Key          string
			LastModified string
			Size         int64
		}
		CommonPrefixes []struct{ Prefix string }
	}
	if err := xml.Unmarshal(data, &res); err != nil {
		fmt.Fprintln(c.stderr, "aws (fixture): the listing could not be parsed")
		return exitService
	}
	// Like aws, a non-recursive listing shows names relative to the prefix's directory.
	dir := ""
	if !cmd.recursive {
		if i := strings.LastIndex(cmd.key, "/"); i >= 0 {
			dir = cmd.key[:i+1]
		}
	}
	for _, p := range res.CommonPrefixes {
		fmt.Fprintf(c.stdout, "%27sPRE %s\n", "", strings.TrimPrefix(p.Prefix, dir))
	}
	for _, o := range res.Contents {
		when := o.LastModified
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", o.LastModified); err == nil {
			when = t.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(c.stdout, "%s %10d %s\n", when, o.Size, strings.TrimPrefix(o.Key, dir))
	}
	return 0
}

func (c *client) get(cmd command) int {
	_, data, code := c.do(http.MethodGet, objPath(cmd.bucket, cmd.key), "", "GetObject", nil)
	if code != 0 {
		return code
	}
	_, _ = c.stdout.Write(data)
	return 0
}

func (c *client) head(cmd command) int {
	resp, _, code := c.do(http.MethodHead, objPath(cmd.bucket, cmd.key), "", "HeadObject", nil)
	if code != 0 {
		return code
	}
	lm := resp.Header.Get("Last-Modified")
	if t, err := http.ParseTime(lm); err == nil {
		lm = t.UTC().Format("2006-01-02T15:04:05+00:00")
	}
	out := struct {
		AcceptRanges  string
		LastModified  string
		ContentLength int64
		ETag          string
		ContentType   string
	}{"bytes", lm, resp.ContentLength, resp.Header.Get("ETag"), resp.Header.Get("Content-Type")}
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "    ")
	_ = enc.Encode(out)
	return 0
}

func (c *client) put(cmd command, data []byte) int {
	if _, _, code := c.do(http.MethodPut, objPath(cmd.bucket, cmd.key), "", "PutObject", data); code != 0 {
		return code
	}
	fmt.Fprintf(c.stdout, "upload: - to s3://%s/%s\n", cmd.bucket, cmd.key)
	return 0
}

func (c *client) rm(cmd command) int {
	if _, _, code := c.do(http.MethodDelete, objPath(cmd.bucket, cmd.key), "", "DeleteObject", nil); code != 0 {
		return code
	}
	fmt.Fprintf(c.stdout, "delete: s3://%s/%s\n", cmd.bucket, cmd.key)
	return 0
}
