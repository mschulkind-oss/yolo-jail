package s3fixture

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// InvocationLog is a fixture-only record of host CLI invocations; it has argv, no env.
const InvocationLog = ".aws-fixture-invocations.jsonl"

func recordInvocation(args []string, getenv func(string) string) error {
	home := getenv("HOME")
	if home == "" {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(home, InvocationLog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(args)
}

// parseCE independently accepts the canonical fixture protocol only, never raw CLI
// shorthand or global settings. The production classifier is deliberately not imported.
func parseCE(args []string) error {
	bad := func() error { return fmt.Errorf("unsupported Cost Explorer fixture argv") }
	if len(args) < 7 || args[0] != "ce" {
		return bad()
	}
	rows := map[string][]string{
		"get-cost-and-usage":   {"time-period", "granularity", "metrics", "group-by", "filter", "next-page-token"},
		"get-cost-forecast":    {"time-period", "granularity", "metric", "filter", "prediction-interval-level"},
		"get-dimension-values": {"time-period", "dimension", "search-string", "filter", "max-results", "next-page-token", "context"},
		"get-tags":             {"time-period", "tag-key", "search-string", "filter", "max-results", "next-page-token"},
	}
	order, ok := rows[args[1]]
	if !ok {
		return bad()
	}
	end := len(args) - 3
	if args[end] != "--output" || args[end+1] != "json" || args[end+2] != "--no-paginate" {
		return bad()
	}
	seen := map[string]bool{}
	last := -1
	for i := 2; i < end; {
		flag := strings.TrimPrefix(args[i], "--")
		if flag == args[i] {
			return bad()
		}
		i++
		pos := -1
		for j, v := range order {
			if flag == v {
				pos = j
			}
		}
		if pos <= last || pos < 0 || i >= end {
			return bad()
		}
		last = pos
		seen[flag] = true
		values := []string{args[i]}
		i++
		if flag == "metrics" {
			for i < end && !strings.HasPrefix(args[i], "--") {
				values = append(values, args[i])
				i++
			}
		}
		for _, v := range values {
			if strings.HasPrefix(v, "-") || regexp.MustCompile(`^[A-Za-z0-9+.-]+://`).MatchString(v) {
				return bad()
			}
		}
		v := values[0]
		switch flag {
		case "time-period":
			if !regexp.MustCompile(`^Start=20[0-9]{2}-[0-9]{2}-[0-9]{2},End=20[0-9]{2}-[0-9]{2}-[0-9]{2}$`).MatchString(v) {
				return bad()
			}
			a, err := time.Parse("2006-01-02", v[6:16])
			if err != nil {
				return bad()
			}
			b, err := time.Parse("2006-01-02", v[21:])
			if err != nil || !a.Before(b) {
				return bad()
			}
		case "granularity":
			if v != "DAILY" && v != "MONTHLY" {
				return bad()
			}
		case "context":
			if v != "COST_AND_USAGE" {
				return bad()
			}
		case "group-by":
			var groups []struct{ Type, Key string }
			if !strings.HasPrefix(v, "[") || json.Unmarshal([]byte(v), &groups) != nil || len(groups) < 1 || len(groups) > 2 {
				return bad()
			}
			for _, g := range groups {
				if g.Type != "DIMENSION" && g.Type != "TAG" && g.Type != "COST_CATEGORY" || g.Key == "" {
					return bad()
				}
			}
		case "filter":
			var filter map[string]json.RawMessage
			if !strings.HasPrefix(v, "{") || json.Unmarshal([]byte(v), &filter) != nil || len(filter) != 1 {
				return bad()
			}
			for k := range filter {
				if k != "And" && k != "Or" && k != "Not" && k != "Dimensions" && k != "Tags" && k != "CostCategories" {
					return bad()
				}
			}
		case "metrics":
			names := map[string]bool{"AmortizedCost": true, "BlendedCost": true, "NetAmortizedCost": true, "NetUnblendedCost": true, "NormalizedUsageAmount": true, "UnblendedCost": true, "UsageQuantity": true}
			if len(values) > 7 {
				return bad()
			}
			for _, v := range values {
				if !names[v] {
					return bad()
				}
				delete(names, v)
			}
		case "metric":
			if v != "AMORTIZED_COST" && v != "BLENDED_COST" && v != "NET_AMORTIZED_COST" && v != "NET_UNBLENDED_COST" && v != "UNBLENDED_COST" {
				return bad()
			}
		default:
			if v == "" {
				return bad()
			}
		}
	}
	if !seen["time-period"] {
		return bad()
	}
	switch args[1] {
	case "get-cost-and-usage":
		if !seen["granularity"] || !seen["metrics"] {
			return bad()
		}
	case "get-cost-forecast":
		if !seen["granularity"] || !seen["metric"] {
			return bad()
		}
	case "get-dimension-values":
		if !seen["dimension"] || !seen["context"] {
			return bad()
		}
	}
	return nil
}

func runCE(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if err := parseCE(args); err != nil {
		fmt.Fprintln(stderr, "aws (fixture):", err)
		return exitUsage
	}
	if err := recordInvocation(args, getenv); err != nil {
		fmt.Fprintln(stderr, "aws (fixture): invocation log failed")
		return exitConfig
	}
	u, err := url.Parse(getenv("AWS_ENDPOINT_URL"))
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		fmt.Fprintln(stderr, "aws (fixture): expected loopback HTTP endpoint")
		return exitConfig
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		fmt.Fprintln(stderr, "aws (fixture): non-loopback endpoint refused")
		return exitConfig
	}
	body, _ := json.Marshal(args)
	req, err := http.NewRequest(http.MethodPost, u.String()+"/__ce", strings.NewReader(string(body)))
	if err != nil {
		return exitConfig
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "aws (fixture): loopback CE request failed")
		return exitService
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(stderr, resp.Body)
		return exitService
	}
	_, _ = io.Copy(stdout, resp.Body)
	return 0
}
