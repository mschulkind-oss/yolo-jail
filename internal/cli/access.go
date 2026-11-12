package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitremote"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const accessUsage = `Usage: yolo access [--json] [github|git] [-- <command>]
       yolo access [--json] --ask -- <command>

Read this jail's GitHub or Git broker standing, or predict a brokered command.
For git push, specify a remote and explicit source:destination refspecs.
Use --ask to file an approval request without running the command.

Examples:
  yolo access --json                     # read this jail's GitHub broker standing
  yolo access --json git                 # read this jail's Git broker standing
  yolo access -- gh pr merge 40          # predict before a write
  yolo access -- git push origin HEAD:refs/heads/main
  yolo access --ask -- gh pr merge 40    # file an approval without merging`

type accessCallFunc func(op string, args []string, stdin io.Reader) ([]byte, int)

func runAccessVerb(args []string) int { return runAccess(args[1:]) }

func runAccess(args []string) int {
	return runAccessWith(args, func(op string, call []string, stdin io.Reader) ([]byte, int) {
		return ghbroker.ForwardAccess(op, call, ghbroker.ForwardEnv{
			Getenv: os.Getenv, Stdin: stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		})
	}, os.Getenv, os.Stdin, os.Stdout, os.Stderr)
}

func runAccessWith(args []string, call accessCallFunc, getenv func(string) string, stdin io.Reader, stdout, stderr *os.File) int {
	jsonMode, ask := false, false
	source := ""
	var command []string
	beforeDash := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		if beforeDash && a == "--" {
			beforeDash = false
			command = append(command, args[i+1:]...)
			break
		}
		if beforeDash {
			switch a {
			case "--json":
				jsonMode = true
			case "--ask":
				ask = true
			case "--help", "-h":
				fmt.Fprintln(stdout, accessUsage)
				return 0
			default:
				if strings.HasPrefix(a, "-") || source != "" {
					fmt.Fprintln(stderr, accessUsage)
					return ghbroker.ExitUsage
				}
				source = a
			}
			continue
		}
	}
	if ask && len(command) == 0 {
		fmt.Fprintln(stderr, "yolo access: --ask requires a command after --")
		return ghbroker.ExitUsage
	}
	if source == "" {
		source = "github"
		if len(command) > 0 && command[0] == "git" {
			source = "git"
		}
	}
	if source == "git" {
		if getenv(gitremote.EndpointEnv) == "" {
			if getenv("YOLO_HOST_DIR") == "" {
				fmt.Fprintln(stderr, "yolo access: no broker serves this shell; run `yolo approve --json` on the host for the host-wide view")
			} else {
				fmt.Fprintln(stderr, "yolo access: no git-broker endpoint serves this jail; ask the human to run `yolo check` on the host, then relaunch")
			}
			return ghbroker.ExitUnavailable
		}
		op := "status"
		if len(command) > 0 {
			op = "evaluate"
			if ask {
				op = "ask"
			}
		}
		body, code := gitremote.ForwardAccess(op, command, gitremote.Env{Getenv: getenv, Stderr: stderr})
		if code != 0 {
			return code
		}
		if jsonMode {
			_, _ = stdout.Write(body)
		} else if op == "status" {
			renderAccessStatus(stdout, body)
		} else {
			renderAccessPrediction(stdout, body)
			if op == "evaluate" {
				renderGitPreAsk(stdout, body, command)
			}
		}
		return 0
	}
	if source != "github" {
		fmt.Fprintf(stderr, "yolo access: %s status is not available in this jail; use `yolo check` on the host to inspect selected broker packs\n", termsafe.Visible(source))
		return ghbroker.ExitUnavailable
	}
	if getenv(ghbroker.EndpointEnv) == "" {
		if getenv("YOLO_HOST_DIR") != "" {
			fmt.Fprintln(stderr, "yolo access: no github-broker endpoint serves this jail; if GitHub access is intended, ask the human to run `yolo pack add github` on the host")
		} else {
			fmt.Fprintln(stderr, "yolo access: no broker serves this shell; run `yolo approve --json` on the host for the host-wide view")
		}
		return ghbroker.ExitUnavailable
	}
	op := "status"
	if len(command) > 0 {
		op = "evaluate"
		if ask {
			op = "ask"
		}
	}
	body, code := call(op, command, stdin)
	if code != 0 {
		return code
	}
	if jsonMode {
		_, _ = stdout.Write(body)
		return 0
	}
	if op == "status" {
		renderAccessStatus(stdout, body)
	} else {
		renderAccessPrediction(stdout, body)
	}
	return 0
}

func renderAccessStatus(out *os.File, body []byte) {
	var r struct {
		Source       string   `json:"source"`
		State        string   `json:"state"`
		Read         string   `json:"read"`
		Repositories []string `json:"repositories"`
		ScopeGrants  []struct {
			Kind        string `json:"kind"`
			Repo        string `json:"repo"`
			Name        string `json:"name"`
			RepoPattern bool   `json:"repo_pattern"`
		} `json:"scope_grants"`
		Requests []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Expires string `json:"expires"`
		} `json:"requests"`
		Grants []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Expires string `json:"expires"`
		} `json:"grants"`
		Working []struct {
			ID       string `json:"id"`
			Repo     string `json:"repo"`
			Resource string `json:"resource"`
		} `json:"working_set"`
		Limits struct {
			Pending       int    `json:"pending"`
			PendingCap    int    `json:"pending_cap"`
			CooldownUntil string `json:"cooldown_until"`
		} `json:"limits"`
		Next string `json:"next"`
	}
	if json.Unmarshal(body, &r) != nil {
		fmt.Fprintln(out, "Access readout unavailable: unsupported response")
		return
	}
	fmt.Fprintf(out, "%s broker: %s; read: %s\n", termsafe.Visible(r.Source), termsafe.Visible(r.State), termsafe.Visible(r.Read))
	for _, repo := range r.Repositories {
		fmt.Fprintf(out, "  repository: %s\n", termsafe.Visible(repo))
	}
	for _, grant := range r.ScopeGrants {
		// A grant approved as a pattern is spelled as the gate spells it (brokerscope.Grant.String),
		// so it never reads like the inert grant an older build recorded on the same repository.
		repo := termsafe.Visible(grant.Repo)
		if grant.RepoPattern {
			repo = brokerscope.PatternRepoPrefix + repo
		}
		fmt.Fprintf(out, "  approved %s grant for %s %s\n", termsafe.Visible(grant.Kind), repo, termsafe.Visible(grant.Name))
	}
	for _, g := range r.Grants {
		fmt.Fprintf(out, "  grant %s: %s, expires %s\n", termsafe.Visible(g.ID), termsafe.Visible(g.Kind), termsafe.Visible(g.Expires))
	}
	for _, w := range r.Working {
		fmt.Fprintf(out, "  working: %s %s %s\n", termsafe.Visible(w.ID), termsafe.Visible(w.Repo), termsafe.Visible(w.Resource))
	}
	for _, q := range r.Requests {
		fmt.Fprintf(out, "  request %s: %s, expires %s\n", termsafe.Visible(q.ID), termsafe.Visible(q.State), termsafe.Visible(q.Expires))
	}
	fmt.Fprintf(out, "  pending requests: %d/%d\n", r.Limits.Pending, r.Limits.PendingCap)
	if r.Limits.CooldownUntil != "" {
		fmt.Fprintf(out, "  denied-command cooldown until %s\n", termsafe.Visible(r.Limits.CooldownUntil))
	}
	if r.Next != "" {
		fmt.Fprintf(out, "  next: %s\n", termsafe.Visible(r.Next))
	}
}

func renderAccessPrediction(out *os.File, body []byte) {
	var r struct {
		Source          string `json:"source"`
		CallUnavailable string `json:"call_unavailable"`
		Call            struct {
			Source    string   `json:"source"`
			Canonical []string `json:"canonical"`
		} `json:"call"`
		Canonical  []string `json:"canonical"`
		Classified []struct {
			Ref       string `json:"ref"`
			CoveredBy string `json:"covered_by"`
		} `json:"classified"`
		CoveredBy     string                                     `json:"covered_by"`
		ReliesOnUntil *string                                    `json:"relies_on_until"`
		InputsDigest  string                                     `json:"inputs_digest"`
		Verdict       string                                     `json:"verdict"`
		Rule          string                                     `json:"rule"`
		RequestID     string                                     `json:"request_id"`
		Next          []struct{ Who, Where, Run, Detail string } `json:"next"`
	}
	if json.Unmarshal(body, &r) != nil {
		fmt.Fprintln(out, "Access prediction unavailable: unsupported response")
		return
	}
	argv := r.Call.Canonical
	if argv == nil {
		argv = r.Canonical
	}
	quoted := termsafe.Visible(shquote.Join(argv))
	if r.Call.Source == "git" || r.Source == "git" {
		quoted = "git push (broker-selected refs)"
		if r.CallUnavailable != "" {
			quoted = "git push (selection unavailable)"
		}
	}
	fmt.Fprintf(out, "%s → %s\n", quoted, termsafe.Visible(r.Verdict))
	if r.Rule != "" {
		fmt.Fprintf(out, "  rule: %s\n", termsafe.Visible(r.Rule))
	}
	if r.CallUnavailable != "" {
		fmt.Fprintf(out, "  call: %s\n", termsafe.Visible(r.CallUnavailable))
	}
	if r.Call.Source == "git" || r.Source == "git" {
		for _, ref := range r.Classified {
			line := "requires approval"
			if ref.CoveredBy != "" {
				line = "covered by " + termsafe.Visible(ref.CoveredBy)
			}
			fmt.Fprintf(out, "  ref %s: %s\n", termsafe.Visible(ref.Ref), line)
		}
		if r.CoveredBy != "" {
			fmt.Fprintf(out, "  covered by: %s\n", termsafe.Visible(r.CoveredBy))
		}
		if r.ReliesOnUntil != nil {
			fmt.Fprintf(out, "  latest reliance: %s\n", termsafe.Visible(*r.ReliesOnUntil))
		}
		fmt.Fprintln(out, "  decision only: a real push rechecks the host and remote; no ref updates sent")
	}
	if r.RequestID != "" {
		fmt.Fprintf(out, "  request: %s (pending; nothing ran)\n", termsafe.Visible(r.RequestID))
	}
	for _, n := range r.Next {
		if n.Run != "" {
			fmt.Fprintf(out, "  next (%s, %s): %s\n", termsafe.Visible(n.Who), termsafe.Visible(n.Where), termsafe.Visible(n.Run))
		} else if n.Detail != "" {
			fmt.Fprintf(out, "  next (%s, %s): %s\n", termsafe.Visible(n.Who), termsafe.Visible(n.Where), termsafe.Visible(n.Detail))
		}
	}
}

func renderGitPreAsk(out *os.File, body []byte, command []string) {
	var prediction struct {
		Verdict string `json:"verdict"`
	}
	if json.Unmarshal(body, &prediction) != nil || prediction.Verdict != "asks" {
		return
	}
	if accessArgvRoundTripsTerminal(command) {
		fmt.Fprintf(out, "  file this exact request without pushing: %s\n", shquote.Join(append([]string{"yolo", "access", "--ask", "--"}, command...)))
	} else {
		fmt.Fprintln(out, "  exact pre-ask command unavailable: an argument contains invisible or terminal-control bytes; rerun with the original argv and --ask, or ask the human to push from the host")
	}
}

// An exact copyable terminal command must not turn control bytes into different bytes
// after quoting. The original argv still goes to Git and the host unchanged.
func accessArgvRoundTripsTerminal(argv []string) bool {
	for _, arg := range argv {
		if termsafe.HasUnsafe(arg) {
			return false
		}
	}
	return true
}
