package claudesecurity

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestViewServiceUsesClaudeNFCPathHash(t *testing.T) {
	const ascii = "/Users/matt/.local/share/yolo-jail/host-agents/claude"
	if got := ViewService(ascii); got != "Claude Code-credentials-5d0efea8" {
		t.Fatalf("ViewService(%q) = %q", ascii, got)
	}
	composed := "/tmp/Jos\u00e9/store"
	decomposed := "/tmp/Jose\u0301/store"
	if a, b := ViewService(composed), ViewService(decomposed); a != b {
		t.Fatalf("NFC-equivalent paths yielded distinct services: %q != %q", a, b)
	}
}

func TestAccountModeRefusesOnlyPasswordAddsAndPreservesGlobalOptions(t *testing.T) {
	called := false
	runner := func(_ string, args []string, stdin io.Reader, _, _ io.Writer) int {
		called = true
		if len(args) > 0 && args[0] == "-i" {
			_, _ = io.Copy(io.Discard, stdin)
		}
		return 7
	}
	for _, args := range [][]string{
		{"-q", "add-generic-password", "-a", "account", "-s", "secret service", "-X", "opaque"},
		{"-i", "add-generic-password", "-a", "account", "-s", "secret service", "-X", "opaque"},
	} {
		var errout bytes.Buffer
		if got := Run(AccountMode, "", args, strings.NewReader(""), io.Discard, &errout, "/fake/security", runner); got != 50 || called {
			t.Fatalf("account add returned %d, delegated=%v, stderr=%q", got, called, errout.String())
		}
		if strings.Contains(errout.String(), "opaque") || strings.Contains(errout.String(), "secret service") {
			t.Fatalf("refusal exposed command arguments: %q", errout.String())
		}
	}
	for _, args := range [][]string{{"-q", "find-generic-password", "-s", "secret"}, {"-i"}} {
		called = false
		var errout bytes.Buffer
		if got := Run(AccountMode, "", args, strings.NewReader("find-generic-password -s 'a secret'\n"), io.Discard, &errout, "/fake/security", runner); got != 7 || !called {
			t.Fatalf("account read/unknown returned %d, delegated=%v", got, called)
		}
	}
}

func TestViewModeParsesCommandPositionAndEffectiveServiceOption(t *testing.T) {
	const service = "Claude Code-credentials-5d0efea8"
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{name: "unknown global option value", args: []string{"--unknown", "add-generic-password", "-s", service}, want: 7},
		{name: "unknown command operand", args: []string{"find-certificate", "-c", "add-generic-password", "-s", service}, want: 7},
		{name: "password value contains service flag", args: []string{"add-generic-password", "-X", "-s", service, "-s", "Other Service"}, want: 7},
		{name: "write password value contains service flag", args: []string{"add-generic-password", "-w", "-s", service, "-s", "Other Service"}, want: 7},
		{name: "account value contains service flag", args: []string{"add-generic-password", "-a", "-s", service, "-s", "Other Service"}, want: 7},
		{name: "last service wins", args: []string{"find-generic-password", "-s", service, "-s", "Other Service"}, want: 7},
		{name: "last service targets view", args: []string{"find-generic-password", "-s", "Other Service", "-s", service}, want: 44},
		{name: "exact add targets view", args: []string{"add-generic-password", "-a", "account", "-s", service, "-X", "secret"}, want: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			var gotArgs []string
			runner := func(_ string, args []string, _ io.Reader, _, _ io.Writer) int {
				called = true
				gotArgs = append([]string(nil), args...)
				return 7
			}
			var errout bytes.Buffer
			got := Run(ViewMode, service, tc.args, strings.NewReader(""), io.Discard, &errout, "/fake/security", runner)
			if got != tc.want {
				t.Fatalf("Run returned %d, want %d (called=%v, stderr=%q)", got, tc.want, called, errout.String())
			}
			if tc.want == 7 && (!called || strings.Join(gotArgs, "\x00") != strings.Join(tc.args, "\x00")) {
				t.Fatalf("passthrough called=%v args=%q, want unchanged %q", called, gotArgs, tc.args)
			}
			if tc.want != 7 && called {
				t.Fatalf("scoped operation reached security: args=%q", gotArgs)
			}
		})
	}
}

func TestViewModeRefusesOnlyExactManagedService(t *testing.T) {
	const service = "Claude Code-credentials-5d0efea8"
	for _, tc := range []struct {
		line string
		code int
	}{
		{"-q find-generic-password -s 'Claude Code-credentials-5d0efea8'\n", 44},
		{"delete-generic-password -s Claude\\ Code-credentials-5d0efea8\n", 44},
		{"add-generic-password -s 'Claude Code-credentials-5d0efea8' -X hidden\n", 50},
	} {
		var delegated bytes.Buffer
		runner := func(_ string, _ []string, stdin io.Reader, _, _ io.Writer) int {
			b, _ := io.ReadAll(stdin)
			delegated.Write(b)
			return 0
		}
		var errout bytes.Buffer
		if got := Run(ViewMode, service, []string{"-i"}, strings.NewReader(tc.line), io.Discard, &errout, "/fake/security", runner); got != tc.code {
			t.Fatalf("%q returned %d, want %d", tc.line, got, tc.code)
		}
		if delegated.Len() != 0 {
			t.Fatalf("intercepted command reached security: %q", delegated.String())
		}
	}
	for _, line := range []string{
		"unknown-command add-generic-password -s 'Claude Code-credentials-5d0efea8'\n",
		"add-generic-password -X '-s' 'Claude Code-credentials-5d0efea8' -s 'Other Service'\n",
		"add-generic-password -w '-s' 'Claude Code-credentials-5d0efea8' -s 'Other Service'\n",
		"add-generic-password -a '-s' 'Claude Code-credentials-5d0efea8' -s 'Other Service'\n",
		"find-generic-password -s 'Claude Code-credentials-5d0efea8' -s 'Other Service'\n",
		"find-generic-password -s 'Claude Code-credentials'\n",          // bare service entry stays usable
		"find-generic-password -s 'Claude Code-credentials-deadbeef'\n", // another hashed service stays usable
		"find-generic-password -s 'Claude Code-5d0efea8'\n",             // API-key service stays usable
		"delete-generic-password -s 'Claude Code-credentials-deadbeef'\n",
		"add-generic-password -s 'Claude Code-5d0efea8'\n",
		"find-generic-password -s Claude Code-credentials-5d0efea8\n", // unquoted, not the exact service
		"find-generic-password -s Claude Code-credentials\n",
		"unlock-keychain\n",
	} {
		if line == "" {
			continue
		}
		input := line
		var forwarded bytes.Buffer
		runner := func(_ string, _ []string, stdin io.Reader, _, _ io.Writer) int {
			_, _ = io.Copy(&forwarded, stdin)
			return 6
		}
		if got := Run(ViewMode, service, []string{"-i"}, strings.NewReader(input), io.Discard, io.Discard, "/fake/security", runner); got != 6 {
			t.Fatalf("passthrough for %q returned %d", input, got)
		}
		if forwarded.String() != input {
			t.Fatalf("passthrough changed bytes: got %q want %q", forwarded.String(), input)
		}
	}
	for _, serviceArg := range []string{"Claude Code-credentials", "Claude Code-credentials-deadbeef", "Claude Code-5d0efea8"} {
		var called bool
		runner := func(_ string, _ []string, _ io.Reader, _, _ io.Writer) int { called = true; return 0 }
		if got := Run(ViewMode, service, []string{"find-generic-password", "-s", serviceArg}, strings.NewReader(""), io.Discard, io.Discard, "/fake/security", runner); got != 0 || !called {
			t.Fatalf("service %q was not passed through", serviceArg)
		}
	}
}

func TestInteractiveStopsForwardingWhenChildExitsWithOpenInput(t *testing.T) {
	stdin, input := io.Pipe()
	defer stdin.Close()
	defer input.Close()
	finished := make(chan int, 1)
	runner := func(_ string, _ []string, childStdin io.Reader, _, _ io.Writer) int {
		line, err := bufio.NewReader(childStdin).ReadString('\n')
		if err != nil || line != "find-generic-password -s Other\n" {
			t.Errorf("runner read %q, %v", line, err)
		}
		return 17
	}
	go func() {
		finished <- Run(ViewMode, "Claude Code-credentials-5d0efea8", []string{"-i"}, stdin,
			io.Discard, io.Discard, "/fake/security", runner)
	}()
	if _, err := io.WriteString(input, "find-generic-password -s Other\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-finished:
		if code != 17 {
			t.Fatalf("interactive status = %d, want child status 17", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interactive filter waited for upstream EOF after the child exited")
	}
}

func TestInteractiveProductionRunnerStopsAfterChildExitWithOpenInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "security")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nIFS= read -r line\nprintf R\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	ready := make(chan error, 1)
	go func() {
		var marker [1]byte
		_, err := io.ReadFull(readyRead, marker[:])
		ready <- err
	}()
	stdin, input := io.Pipe()
	defer stdin.Close()
	defer input.Close()
	finished := make(chan int, 1)
	go func() {
		finished <- Run(ViewMode, "Claude Code-credentials-5d0efea8", []string{"-i"}, stdin,
			readyWrite, io.Discard, path, runSecurity)
	}()
	if _, err := io.WriteString(input, "find-generic-password -s Other\n"); err != nil {
		t.Fatal(err)
	}
	// Start the exit bound only after the actual child has started and consumed
	// its command. Otherwise cold exec startup under whole-suite load can spend
	// the entire two seconds before this test's EOF-handling assertion begins.
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("child readiness: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the production child did not start and read its command")
	}
	select {
	case code := <-finished:
		if code != 23 {
			t.Fatalf("interactive production status = %d, want 23", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production exec runner waited for upstream EOF after its child exited")
	}
}

func TestInteractiveFiltersMatchedLineAndForwardsOtherBytes(t *testing.T) {
	const service = "Claude Code-credentials-5d0efea8"
	input := "unknown-command --literal='a b'\r\nfind-generic-password -s '" + service + "'\nadd-generic-password -s 'Other Service'\n"
	var forwarded, errout bytes.Buffer
	runner := func(_ string, args []string, stdin io.Reader, _, _ io.Writer) int {
		if strings.Join(args, " ") != "-i" {
			t.Errorf("runner args = %v", args)
		}
		_, _ = io.Copy(&forwarded, stdin)
		return 9
	}
	if got := Run(ViewMode, service, []string{"-i"}, strings.NewReader(input), io.Discard, &errout, "/fake/security", runner); got != 9 {
		t.Fatalf("mixed interactive status = %d, want the last forwarded command's status", got)
	}
	want := "unknown-command --literal='a b'\r\nadd-generic-password -s 'Other Service'\n"
	if forwarded.String() != want {
		t.Fatalf("forwarded bytes = %q, want %q", forwarded.String(), want)
	}
	if strings.Contains(errout.String(), service) || strings.Contains(errout.String(), "Other Service") {
		t.Fatalf("stderr exposed input: %q", errout.String())
	}
}

func TestInteractiveBlockedOnlyDoesNotStartSecurityAndHandlesEOFBoundaries(t *testing.T) {
	const service = "Claude Code-credentials-5d0efea8"
	for _, input := range []string{
		// strings.NewReader supplies EOF after these bytes; they are a complete command without a final newline.
		"find-generic-password -s 'Other Service' -s '" + service + "'",
		"find-generic-password -s '" + service + "'\n",
	} {
		t.Run(strings.ReplaceAll(input, "\n", "newline"), func(t *testing.T) {
			called := false
			runner := func(_ string, _ []string, stdin io.Reader, _, _ io.Writer) int {
				called = true
				_, _ = io.Copy(io.Discard, stdin)
				return 9
			}
			var errout bytes.Buffer
			got := Run(ViewMode, service, []string{"-i"}, strings.NewReader(input), io.Discard, &errout, "/fake/security", runner)
			if got != 44 || called {
				t.Fatalf("blocked-only input returned %d, security started=%v", got, called)
			}
			if strings.Contains(errout.String(), service) {
				t.Fatalf("refusal exposed the managed service: %q", errout.String())
			}
		})
	}
}

type refusalSignal chan string

func (w refusalSignal) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestInteractiveRefusesCompletedLineBeforeEOFThenWaitsForNormalEOF(t *testing.T) {
	const service = "Claude Code-credentials-5d0efea8"
	stdin, input := io.Pipe()
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = input.Close()
	})
	refusals := make(refusalSignal, 1)
	started := make(chan struct{}, 1)
	finished := make(chan int, 1)
	runner := func(_ string, _ []string, _ io.Reader, _, _ io.Writer) int {
		started <- struct{}{}
		return 9
	}
	go func() {
		finished <- Run(ViewMode, service, []string{"-i"}, stdin, io.Discard, refusals, "/fake/security", runner)
	}()
	line := "add-generic-password -s '" + service + "' -X hidden\n"
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(input, line)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write complete interactive command: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = input.Close()
		t.Fatal("newline-terminated command was not consumed promptly")
	}
	select {
	case message := <-refusals:
		if !strings.Contains(message, "default keychain could not be found") || strings.Contains(message, service) {
			t.Fatalf("unexpected or over-disclosing refusal: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newline-terminated refusal did not arrive before stdin EOF")
	}
	select {
	case <-started:
		t.Fatal("blocked interactive command started security")
	default:
	}
	select {
	case code := <-finished:
		t.Fatalf("interactive filter ended before normal EOF with status %d", code)
	default:
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-finished:
		if code != 50 {
			t.Fatalf("blocked interactive command returned %d, want nontransient 50", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interactive filter did not finish after normal EOF")
	}
	select {
	case <-started:
		t.Fatal("blocked interactive command started security")
	default:
	}
}

func TestRunSecurityPreservesNormalExitAndSignalStatus(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		script string
		want   int
	}{
		{name: "normal exit 130", script: "#!/bin/sh\nexit 130\n", want: 130},
		{name: "signal termination", script: "#!/bin/sh\nkill -TERM $$\n", want: 128 + int(syscall.SIGTERM)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-"))
			if err := os.WriteFile(path, []byte(tc.script), 0o700); err != nil {
				t.Fatal(err)
			}
			if got := runSecurity(path, nil, strings.NewReader(""), io.Discard, io.Discard); got != tc.want {
				t.Fatalf("runSecurity returned %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseWordsQuotesEscapesAndRejectsMalformedInput(t *testing.T) {
	got, ok := parseWords([]byte(`add-generic-password -s "Claude Code-credentials-5d0efea8" -X 'secret value'`))
	if !ok || strings.Join(got, "|") != "add-generic-password|-s|Claude Code-credentials-5d0efea8|-X|secret value" {
		t.Fatalf("parseWords = %#v, %v", got, ok)
	}
	if _, ok := parseWords([]byte(`add-generic-password -s 'unterminated`)); ok {
		t.Fatal("accepted malformed quoted input")
	}
}

func TestShimContentQuotesCarrierValues(t *testing.T) {
	body := ShimContent("/path with spaces/yolo", ViewMode, "service ' value")
	if !strings.Contains(body, `'/path with spaces/yolo'`) || !strings.Contains(body, `"$@"`) ||
		strings.Contains(body, "eval") {
		t.Fatalf("unsafe or unquoted adapter carrier:\n%s", body)
	}
}
