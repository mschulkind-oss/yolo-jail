// Package s3classify implements the built-in AWS S3 call-broker classifier.
//
// It is deny-by-default: only the five argv shapes in the aws-s3 design's table are
// classified, everything else is refused before anything runs. It never sees or builds a
// credential, an endpoint, a profile or a region; any such option in the jail's argv is a
// refusal, so nothing from the jail shapes the host invocation beyond the table.
package s3classify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

const protocolVersion = "yolo.call-broker-classifier/1"

const maxKeyBytes = 1024

const keyAlphabet = "A-Za-z0-9._=/-"

var (
	bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	dottedQuad = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)
	keyChars   = regexp.MustCompile(`^[A-Za-z0-9._=/-]*$`)
)

// commandFlags are `--` words that belong to some aws s3/s3api command rather than to
// the CLI's global options. A row that does not take one refuses it as unsupported; any
// other `--` word anywhere in argv is treated as a global option and refused as a
// credential-route change.
var commandFlags = map[string]bool{
	"recursive": true, "include": true, "exclude": true, "bucket": true, "key": true,
	"dryrun": true, "delete": true, "acl": true, "quiet": true, "only-show-errors": true,
	"storage-class": true, "sse": true, "content-type": true, "version-id": true,
	"metadata": true, "expires": true, "follow-symlinks": true, "page-size": true,
	"human-readable": true, "summarize": true, "request-payer": true, "exact-timestamps": true,
}

// Run handles `yolo internal call-broker-classify aws-s3`. It reads only the versioned
// classifier protocol and writes one answer; it never runs AWS.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "aws-s3" {
		fmt.Fprintln(stderr, "use the selected aws-s3 pack's classifier dispatch")
		return 64
	}
	var input callbroker.CLIInput
	decoder := json.NewDecoder(io.LimitReader(stdin, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fmt.Fprintln(stderr, "S3 classifier input is invalid; retry the selected aws command")
		return 2
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fmt.Fprintln(stderr, "S3 classifier input has trailing data; retry the selected aws command")
		return 2
	}
	if input.Version != protocolVersion || input.Front != "cli" || len(input.Argv) == 0 {
		fmt.Fprintln(stderr, "S3 classifier protocol is unsupported; rebuild yolo and retry")
		return 2
	}
	answer := Classify(input)
	if err := json.NewEncoder(stdout).Encode(answer); err != nil {
		fmt.Fprintln(stderr, "S3 classifier could not write its answer")
		return 2
	}
	return 0
}

// Classify maps the jail's argv to one canonical host call and its resource chain, or
// refuses it. It is a pure function of its input.
func Classify(input callbroker.CLIInput) callbroker.Answer {
	answer := callbroker.Answer{Version: input.Version}
	if input.Version != protocolVersion || input.Front != "cli" || len(input.Argv) == 0 {
		return refused(answer, "unsupported", "unsupported S3 classifier input", "Use a selected yolo build and retry the aws command")
	}
	argv := input.Argv
	if len(argv) == 1 && argv[0] == "--version" {
		// Never Local: core's CLI front turns a local answer into a refusal and the pack
		// owns the jail's `aws`, so there is no local one to run.
		return refused(answer, "unsupported", "aws --version is not run through the S3 broker",
			"Run `aws --version` on the host")
	}
	for _, arg := range argv {
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, _, _ := strings.Cut(arg[2:], "=")
		if !commandFlags[name] {
			return refused(answer, "credential",
				"the option "+safe(name)+" is not accepted from the jail",
				"Set it in the host's AWS configuration or environment; the broker never takes an endpoint, profile, region or signing option from the jail")
		}
	}
	roots := parseRoots(input.Roots)
	switch argv[0] {
	case "s3":
		return classifyS3(answer, input, roots)
	case "s3api":
		return classifyS3API(answer, input, roots)
	}
	return unsupported(answer, "this AWS service is not enabled by the aws-s3 pack")
}

func classifyS3(answer callbroker.Answer, input callbroker.CLIInput, roots []root) callbroker.Answer {
	argv := input.Argv
	if len(argv) < 2 {
		return unsupported(answer, "an s3 command is required")
	}
	switch argv[1] {
	case "ls":
		recursive, urls, ok := splitLs(argv[2:])
		if !ok || len(urls) != 1 {
			return unsupported(answer, "s3 ls takes one s3:// URL and optionally --recursive")
		}
		bucket, prefix, err := parseURL(urls[0], true)
		if err != nil {
			return unsupported(answer, err.Error())
		}
		loc, ok := cover(roots, bucket, prefix)
		if !ok {
			return outOfScope(answer)
		}
		canon := []string{"s3", "ls", "s3://" + bucket + "/" + prefix}
		if recursive {
			canon = append(canon, "--recursive")
		}
		return read(answer, input, canon, "prefix", "s3://"+bucket+"/"+prefix, loc)
	case "cp":
		args := argv[2:]
		switch {
		case len(args) == 2 && args[1] == "-" && args[0] != "-":
			bucket, key, err := parseURL(args[0], false)
			if err != nil {
				return unsupported(answer, err.Error())
			}
			loc, ok := cover(roots, bucket, key)
			if !ok {
				return outOfScope(answer)
			}
			return read(answer, input, []string{"s3", "cp", args[0], "-"}, "object", "s3://"+bucket+"/"+key, loc)
		case len(args) == 2 && args[0] == "-" && args[1] != "-":
			bucket, key, err := parseURL(args[1], false)
			if err != nil {
				return unsupported(answer, err.Error())
			}
			loc, ok := cover(roots, bucket, key)
			if !ok {
				return outOfScope(answer)
			}
			id := "s3://" + bucket + "/" + key
			return write(answer, input, []string{"s3", "cp", "-", id}, "put_objects", id,
				"create or replace object "+id+" from standard input", loc)
		}
		return unsupported(answer, "s3 cp is supported only between one s3:// object and standard input or output (`-`)")
	case "rm":
		args := argv[2:]
		if len(args) != 1 {
			return unsupported(answer, "s3 rm takes exactly one s3:// object URL and no options")
		}
		bucket, key, err := parseURL(args[0], false)
		if err != nil {
			return unsupported(answer, err.Error())
		}
		loc, ok := cover(roots, bucket, key)
		if !ok {
			return outOfScope(answer)
		}
		id := "s3://" + bucket + "/" + key
		return write(answer, input, []string{"s3", "rm", id}, "delete_objects", id, "delete object "+id, loc)
	}
	return unsupported(answer, "this s3 command is not enabled by the aws-s3 pack")
}

func classifyS3API(answer callbroker.Answer, input callbroker.CLIInput, roots []root) callbroker.Answer {
	argv := input.Argv
	if len(argv) < 2 || argv[1] != "head-object" {
		return unsupported(answer, "only s3api head-object is enabled by the aws-s3 pack")
	}
	var bucket, key string
	seen := map[string]bool{}
	args := argv[2:]
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--bucket" && name != "--key" {
			return unsupported(answer, "s3api head-object takes only --bucket and --key")
		}
		if seen[name] {
			return unsupported(answer, "repeated "+name+" is not supported")
		}
		seen[name] = true
		if !hasValue {
			if i+1 == len(args) {
				return unsupported(answer, name+" requires a value")
			}
			i++
			value = args[i]
		}
		if strings.HasPrefix(value, "-") {
			// The host aws would read such a value as an option, not as the bucket or key.
			return unsupported(answer, name+" takes a value that does not start with -")
		}
		if name == "--bucket" {
			bucket = value
		} else {
			key = value
		}
	}
	if !seen["--bucket"] || !seen["--key"] {
		return unsupported(answer, "s3api head-object requires --bucket and --key")
	}
	if err := checkBucket(bucket); err != nil {
		return unsupported(answer, err.Error())
	}
	if err := checkKey(key, false); err != nil {
		return unsupported(answer, err.Error())
	}
	loc, ok := cover(roots, bucket, key)
	if !ok {
		return outOfScope(answer)
	}
	return read(answer, input, []string{"s3api", "head-object", "--bucket", bucket, "--key", key},
		"object", "s3://"+bucket+"/"+key, loc)
}

// splitLs separates `s3 ls` arguments into the one allowed flag and the positionals.
func splitLs(args []string) (recursive bool, urls []string, ok bool) {
	for _, arg := range args {
		switch {
		case arg == "--recursive":
			if recursive {
				return false, nil, false
			}
			recursive = true
		case strings.HasPrefix(arg, "-") && arg != "-":
			return false, nil, false
		default:
			urls = append(urls, arg)
		}
	}
	return recursive, urls, true
}

// parseURL splits s3://bucket[/rest]. For a listing, rest may be empty and may end in
// "/"; for an object it is a key.
func parseURL(raw string, listing bool) (bucket, rest string, err error) {
	body, found := strings.CutPrefix(raw, "s3://")
	if !found {
		return "", "", errors.New("expected an s3://<bucket>/<key> URL")
	}
	bucket, rest, _ = strings.Cut(body, "/")
	if err := checkBucket(bucket); err != nil {
		return "", "", err
	}
	if err := checkKey(rest, listing); err != nil {
		return "", "", err
	}
	return bucket, rest, nil
}

func checkBucket(b string) error {
	if !bucketName.MatchString(b) || strings.Contains(b, "..") || dottedQuad.MatchString(b) {
		return errors.New("the bucket name must be a plain lowercase S3 bucket name (not an ARN or an IP address)")
	}
	return nil
}

// checkKey enforces the key alphabet and shape. A listing prefix may be empty and may
// end in a single "/"; an object key may be neither.
func checkKey(k string, listing bool) error {
	bad := fmt.Errorf("the key must be 1-%d bytes of %s, with no leading or trailing slash and no empty, . or .. segment", maxKeyBytes, keyAlphabet)
	if listing {
		bad = fmt.Errorf("the prefix must be at most %d bytes of %s, with no leading slash and no empty, . or .. segment", maxKeyBytes, keyAlphabet)
	}
	if k == "" && listing {
		return nil
	}
	if len(k) == 0 || len(k) > maxKeyBytes || !keyChars.MatchString(k) {
		return bad
	}
	body := k
	if listing {
		body = strings.TrimSuffix(k, "/")
	}
	for _, seg := range strings.Split(body, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return bad
		}
	}
	return nil
}

type root struct {
	id     string
	bucket string
	prefix string
}

// parseRoots keeps only well-formed roots (`s3://<bucket>` or `s3://<bucket>/<prefix>/`).
// A root that does not parse is dropped, never widened.
func parseRoots(raw []string) []root {
	var out []root
	for _, r := range raw {
		body, ok := strings.CutPrefix(r, "s3://")
		if !ok {
			continue
		}
		bucket, prefix, hasSlash := strings.Cut(body, "/")
		if checkBucket(bucket) != nil {
			continue
		}
		if hasSlash {
			if prefix == "" || !strings.HasSuffix(prefix, "/") || checkKey(prefix, true) != nil {
				continue
			}
		}
		out = append(out, root{id: r, bucket: bucket, prefix: prefix})
	}
	return out
}

// cover returns the longest root string that covers the path. Coverage is a string
// prefix test, so `s3://b/p` is not covered by root `s3://b/p/`.
func cover(roots []root, bucket, path string) (string, bool) {
	best := ""
	found := false
	for _, r := range roots {
		if r.bucket != bucket || !strings.HasPrefix(path, r.prefix) {
			continue
		}
		if !found || len(r.id) > len(best) {
			best, found = r.id, true
		}
	}
	return best, found
}

func read(answer callbroker.Answer, input callbroker.CLIInput, argv []string, typ, id, loc string) callbroker.Answer {
	answer.Classification = &callbroker.Classification{
		Argv:        argv,
		StdinSHA256: input.StdinSHA256,
		Reads:       [][]callbroker.Resource{{{Type: typ, ID: id}, {Type: "location", ID: loc}}},
	}
	return answer
}

func write(answer callbroker.Answer, input callbroker.CLIInput, argv []string, cell, id, description, loc string) callbroker.Answer {
	answer.Classification = &callbroker.Classification{
		Argv:        argv,
		StdinSHA256: input.StdinSHA256,
		Writes: []callbroker.Write{{
			Cell:        cell,
			Chain:       []callbroker.Resource{{Type: "object", ID: id}, {Type: "location", ID: loc}},
			Description: description,
		}},
	}
	return answer
}

func outOfScope(answer callbroker.Answer) callbroker.Answer {
	return refused(answer, "scope", "no approved S3 root covers that bucket and key",
		"Add the root to `brokered.aws-s3.roots` in this workspace's config, then run `yolo config apply` on the host")
}

func unsupported(answer callbroker.Answer, reason string) callbroker.Answer {
	return refused(answer, "unsupported", reason,
		"Use `aws s3 ls s3://<bucket>[/<prefix>] [--recursive]`, `aws s3 cp s3://<bucket>/<key> -`, `aws s3 cp - s3://<bucket>/<key>`, `aws s3 rm s3://<bucket>/<key>` or `aws s3api head-object --bucket <bucket> --key <key>`, or run the command on the host")
}

func refused(answer callbroker.Answer, class, reason, next string) callbroker.Answer {
	answer.Refused = &callbroker.Refusal{Class: class, Reason: reason, NextStep: next}
	return answer
}

// optionName is the only shape of a jail-supplied option name echoed back in a refusal.
var optionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// safe names a jail-supplied option in a refusal only when it is a plain option name;
// anything else (control, format or bidi characters, overlong words) is not echoed.
func safe(name string) string {
	if optionName.MatchString(name) {
		return "--" + name
	}
	return "given"
}
