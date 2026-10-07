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
