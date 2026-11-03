package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	defaultTimeout    = time.Hour
	defaultPoll       = 15 * time.Second
	defaultRequestMax = 30 * time.Second
	maxTimeout        = 24 * time.Hour
	maxPoll           = 10 * time.Minute
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

func main() { os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr)) }

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "darwin-floor" {
		return runFloorCLI(args[1:], stdout, stderr, nil)
	}
	flags := flag.NewFlagSet("release-gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", "", "GitHub repository in OWNER/REPO form")
	sha := flags.String("sha", "", "full 40-character commit SHA")
	timeout := flags.Duration("timeout", defaultTimeout, "total wait deadline (default 1h)")
	poll := flags.Duration("poll-interval", defaultPoll, "poll interval (default 15s)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !repositoryPattern.MatchString(*repo) || !shaPattern.MatchString(*sha) ||
		*timeout <= 0 || *timeout > maxTimeout || *poll <= 0 || *poll > maxPoll {
		fmt.Fprintln(stderr, "release-gate: invalid arguments; provide --repo OWNER/REPO and --sha FULL_SHA, with positive bounded durations")
		return 2
	}
	if strings.Contains(*repo, "..") {
		fmt.Fprintln(stderr, "release-gate: invalid repository name")
		return 2
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	client := &http.Client{}
	g := gate{
		repo: *repo, sha: strings.ToLower(*sha), token: token, client: client,
		baseURL: "https://api.github.com", now: time.Now, sleep: sleepContext,
		timeout: *timeout, pollEvery: *poll, requestMax: defaultRequestMax,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "Waiting for %s CI push proof at %s (timeout %s)\n", workflowPath, g.sha, g.timeout)
	result, err := g.wait(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "release-gate: CI proof unavailable: %s. Nothing was tagged; rerun the release request after resolving CI or API access.\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "release-gate: could not write proof JSON")
		return 1
	}
	return 0
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type pathList []string

func (p *pathList) String() string { return strings.Join(*p, ",") }
func (p *pathList) Set(value string) error {
	*p = append(*p, value)
	return nil
}

// runFloorCLI is `release-gate darwin-floor`: wait until every --path is in the
// Cachix cache, polling while a darwin-floor-cache.yml run for one of the
// --wait-shas commits is pending, and otherwise dispatching one run that builds
// --sha and requiring it to succeed and the cache then to serve every path.
// The token needs actions: write for that dispatch. configure, when non-nil, adjusts the gate
// before it runs (tests point it at stub servers).
func runFloorCLI(args []string, stdout, stderr io.Writer, configure func(*floorGate)) int {
	flags := flag.NewFlagSet("release-gate darwin-floor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", "", "GitHub repository in OWNER/REPO form")
	cache := flags.String("cache", "", "Cachix cache name")
	var paths pathList
	flags.Var(&paths, "path", "store path that must be in the cache (repeatable)")
	target := flags.String("sha", "", "the full commit SHA being released")
	waitFile := flags.String("wait-shas", "", "file of main commits, one per line, whose darwin-floor-cache runs are worth waiting for")
	timeout := flags.Duration("timeout", defaultTimeout, "total wait deadline (default 1h)")
	poll := flags.Duration("poll-interval", defaultPoll, "poll interval (default 15s)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || !repositoryPattern.MatchString(*repo) || strings.Contains(*repo, "..") ||
		!cacheNamePattern.MatchString(*cache) || len(paths) == 0 || *waitFile == "" ||
		!shaPattern.MatchString(*target) ||
		*timeout <= 0 || *timeout > maxTimeout || *poll <= 0 || *poll > maxPoll {
		fmt.Fprintln(stderr, "release-gate darwin-floor: invalid arguments; provide --repo OWNER/REPO, --cache NAME, --sha FULL_SHA, one or more --path /nix/store/..., and --wait-shas FILE, with positive bounded durations")
		return 2
	}
	for _, path := range paths {
		if !storePathPattern.MatchString(path) {
			fmt.Fprintf(stderr, "release-gate darwin-floor: %q is not a store path\n", path)
			return 2
		}
	}
	file, err := os.Open(*waitFile)
	if err != nil {
		fmt.Fprintln(stderr, "release-gate darwin-floor: cannot read the wait list")
		return 2
	}
	waitSHAs, err := parseWaitSHAs(file)
	file.Close()
	if err != nil {
		fmt.Fprintf(stderr, "release-gate darwin-floor: %s\n", err)
		return 2
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	f := floorGate{
		api: gate{
			repo: *repo, token: token, client: &http.Client{}, baseURL: "https://api.github.com",
			now: time.Now, sleep: sleepContext, timeout: *timeout, pollEvery: *poll, requestMax: defaultRequestMax,
		},
		cache: *cache, cacheURL: "https://" + *cache + ".cachix.org", upstreamURLs: []string{"https://cache.nixos.org"}, paths: paths, waitSHAs: waitSHAs, progress: stderr,
		targetSHA: strings.ToLower(*target),
	}
	if configure != nil {
		configure(&f)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "Checking https://%s.cachix.org for the darwin floor profiles %s (timeout %s)\n", f.cache, strings.Join(paths, ", "), f.api.timeout)
	result, err := f.wait(ctx)
	if err != nil {
		var refusal floorRefusal
		if errors.As(err, &refusal) {
			fmt.Fprintf(stderr, "release-gate darwin-floor: %s.\n", err)
		} else {
			fmt.Fprintf(stderr, "release-gate darwin-floor: the cache check failed: %s. Nothing was tagged; request this release again once the cache and the Actions API answer.\n", err)
		}
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "release-gate darwin-floor: could not write proof JSON")
		return 1
	}
	return 0
}
