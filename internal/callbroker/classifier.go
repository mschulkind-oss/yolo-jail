// Package callbroker validates selected pack classifiers before any call can become
// a decision. This package does not authorize or execute service calls.
package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const classifierLimit = 64 << 10

// classifierDeadline bounds ONE classifier process: the checksum check, the
// exec and its answer. It does not cover the core-run lookups between rounds,
// the authority checks they make, or the host program a lookup executes, so a
// slow service or a busy host does not turn a legitimate call into a refusal.
const classifierDeadline = 2 * time.Second

// classificationBudget bounds a whole multi-round classification, lookups
// included, for a caller that brings no deadline of its own (a revalidation
// under the store's transaction); it is the CLI handler's per-call budget.
const classificationBudget = cliHandlerCallBudget
const maxLookupRounds = 3

// CLIInput is all the information supplied to a classifier: never a credential,
// host environment, endpoint, user config path or original stdin bytes.
type CLIInput struct {
	Version     string       `json:"version"`
	Front       string       `json:"front"`
	Argv        []string     `json:"argv"`
	StdinSHA256 string       `json:"stdin_sha256"`
	Roots       []string     `json:"roots"`
	Facts       []LookupFact `json:"facts,omitempty"`
}

type LookupFact struct {
	Name      string          `json:"name"`
	Arguments []string        `json:"arguments"`
	Value     json.RawMessage `json:"value"`
}

type LookupRequest struct {
	Name      string   `json:"name"`
	Arguments []string `json:"arguments"`
}

type Resource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Write struct {
	Cell        string     `json:"cell"`
	Chain       []Resource `json:"chain"`
	Description string     `json:"description"`
}

type Classification struct {
	Argv        []string     `json:"argv"`
	StdinSHA256 string       `json:"stdin_sha256"`
	Reads       [][]Resource `json:"reads,omitempty"`
	Writes      []Write      `json:"writes,omitempty"`
}

type Refusal struct {
	Class    string `json:"class"`
	Reason   string `json:"reason"`
	NextStep string `json:"next_step"`
}

type Answer struct {
	Version        string          `json:"version"`
	Classification *Classification `json:"classification,omitempty"`
	Lookups        []LookupRequest `json:"lookups,omitempty"`
	Refused        *Refusal        `json:"refused,omitempty"`
	Local          bool            `json:"local,omitempty"`
}

// Classifier is the selected staged declaration and gate-approved scope, never a
// jail-supplied executable path. No service call is executed by ClassifyCLI.
type Classifier struct {
	Source       string
	Argv         []string
	Identities   map[string]loopholedecl.CallBrokerProgramIdentity
	Roots        []string
	Resources    []loopholedecl.CallBrokerResource
	Cells        []loopholedecl.CallBrokerCell
	Lookups      []loopholedecl.CallBrokerLookup
	ReadOverPost []string

	// deadline bounds one classifier process; zero means classifierDeadline.
	// Tests set it to exceed or stay within the bound deterministically.
	deadline time.Duration
}

func (c Classifier) invokeDeadline() time.Duration {
	if c.deadline > 0 {
		return c.deadline
	}
	return classifierDeadline
}

// LookupReader is supplied by core only after it has checked a read grant and
// checked that the lookup's own classification is a standing read. Nil refuses
// every lookup, rather than turning a classifier's demand into authority.
type LookupReader func(context.Context, loopholedecl.CallBrokerLookup, []string) (json.RawMessage, error)

func (c Classifier) ClassifyCLI(ctx context.Context, argv []string, stdin []byte, read LookupReader) (Answer, error) {
	return c.ClassifyCLIDigest(ctx, argv, fmt.Sprintf("%x", sha256.Sum256(stdin)), read)
}

// ClassifyCLIDigest replays the classifier from the command's recorded stdin hash.
// The classifier protocol receives only the hash, never stdin bytes; the actual
// invocation hashes its own original stdin before dispatch.
func (c Classifier) ClassifyCLIDigest(ctx context.Context, argv []string, stdinSHA256 string, read LookupReader) (Answer, error) {
	if len(stdinSHA256) != 64 {
		return Answer{}, errors.New("call broker stdin digest is invalid; retry the original call")
	}
	for _, digit := range stdinSHA256 {
		if !strings.ContainsRune("0123456789abcdef", digit) {
			return Answer{}, errors.New("call broker stdin digest is invalid; retry the original call")
		}
	}
	if c.Source == "" || len(c.Argv) == 0 || len(argv) == 0 {
		return Answer{}, errors.New("call broker classifier has no selected declaration or call; retry with its pack selected")
	}
	ctx, cancel := context.WithTimeout(ctx, classificationBudget)
	defer cancel()
	input := CLIInput{Version: "yolo.call-broker-classifier/1", Front: "cli", Argv: append([]string(nil), argv...),
		StdinSHA256: stdinSHA256, Roots: append([]string(nil), c.Roots...)}
	for round := 0; round <= maxLookupRounds; round++ {
		answer, err := c.invoke(ctx, input)
		if err != nil {
			return Answer{}, fmt.Errorf("call broker %s classifier failed: %w; this is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author", c.Source, err)
		}
		if err := c.validate(answer, input); err != nil {
			return Answer{}, fmt.Errorf("call broker %s classifier failed: %w; this is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author", c.Source, err)
		}
		if len(answer.Lookups) == 0 {
			return answer, nil
		}
		if round == maxLookupRounds {
			return Answer{}, fmt.Errorf("call broker %s classifier exceeded three lookup rounds; this is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author", c.Source)
		}
		if read == nil {
			return Answer{}, fmt.Errorf("call broker %s lookup requires a core-approved read grant; request read access before retrying", c.Source)
		}
		for _, lookup := range answer.Lookups {
			var declared *loopholedecl.CallBrokerLookup
			for i := range c.Lookups {
				if c.Lookups[i].Name == lookup.Name {
					declared = &c.Lookups[i]
					break
				}
			}
			if declared == nil {
				return Answer{}, fmt.Errorf("call broker %s classifier requested an undeclared lookup; this is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author", c.Source)
			}
			fact, err := read(ctx, *declared, lookup.Arguments)
			if err != nil {
				return Answer{}, fmt.Errorf("call broker %s lookup failed: %w; check read access and retry", c.Source, err)
			}
			if !json.Valid(fact) || len(fact) > classifierLimit {
				return Answer{}, fmt.Errorf("call broker %s lookup returned invalid or oversized facts; this is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author", c.Source)
			}
			input.Facts = append(input.Facts, LookupFact{Name: lookup.Name, Arguments: lookup.Arguments, Value: fact})
		}
	}
	return Answer{}, errors.New("call broker classifier exceeded lookup rounds; this is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author")
}

func (c Classifier) invoke(ctx context.Context, input CLIInput) (Answer, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return Answer{}, err
	}
	if len(payload) > classifierLimit {
		return Answer{}, errors.New("classifier input exceeds 64 KiB")
	}
	raw, err := c.invokePayload(ctx, payload)
	if err != nil {
		return Answer{}, err
	}
	var answer Answer
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return Answer{}, fmt.Errorf("classifier answer is not strict JSON: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Answer{}, errors.New("classifier answer has trailing data")
	}
	return answer, nil
}

func (c Classifier) invokePayload(ctx context.Context, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.invokeDeadline())
	defer cancel()
	binary := c.Argv[0]
	if strings.HasPrefix(binary, "{program:") && strings.HasSuffix(binary, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(binary, "{program:"), "}")
		identity, ok := c.Identities[name]
		if !ok || identity.Bin != name || identity.Path == "" || identity.SHA256 == "" || !filepath.IsAbs(identity.Path) {
			return nil, errors.New("selected classifier identity is unavailable")
		}
		resolved, err := filepath.EvalSymlinks(identity.Path)
		if err != nil || resolved != identity.Path {
			return nil, errors.New("selected classifier path changed")
		}
		file, err := os.Open(identity.Path)
		if err != nil {
			return nil, err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			file.Close()
			return nil, errors.New("selected classifier is not a regular executable")
		}
		h := sha256.New()
		_, err = io.Copy(h, file)
		closeErr := file.Close()
		if err != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != identity.SHA256 {
			return nil, errors.New("selected classifier checksum changed")
		}
		binary = identity.Path
	} else if binary == "yolo" {
		var err error
		binary, err = os.Executable()
		if err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("classifier must be selected staged program or yolo")
	}
	cmd := exec.Command(binary, c.Argv[1:]...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdin = bytes.NewReader(payload)
	var stdout boundedAnswer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := runOwnedGroup(ctx, cmd); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, fmt.Errorf("classifier lifecycle failed: %w", err)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("classifier timed out or was cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("classifier exited unsuccessfully: %w", err)
	}
	if stdout.tooLarge {
		return nil, errors.New("classifier answer exceeds 64 KiB")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

type boundedAnswer struct {
	buffer   bytes.Buffer
	tooLarge bool
}

func (b *boundedAnswer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedAnswer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > classifierLimit {
		b.tooLarge = true
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func (c Classifier) validate(a Answer, input CLIInput) error {
	if a.Version != input.Version {
		return errors.New("unknown classifier protocol version")
	}
	kinds := 0
	if a.Classification != nil {
		kinds++
	}
	if len(a.Lookups) != 0 {
		kinds++
	}
	if a.Refused != nil {
		kinds++
	}
	if a.Local {
		kinds++
	}
	if kinds != 1 {
		return errors.New("classifier must answer exactly one kind")
	}
	if a.Refused != nil && (a.Refused.Class == "" || a.Refused.Reason == "" || a.Refused.NextStep == "" ||
		len(a.Refused.Reason) > 512 || len(a.Refused.NextStep) > 512 ||
		termsafe.HasUnsafe(a.Refused.Reason) || termsafe.HasUnsafe(a.Refused.NextStep)) {
		return errors.New("classifier refusal is missing, unsafe or oversized")
	}
	if a.Local && !(len(input.Argv) == 1 && (input.Argv[0] == "--help" || input.Argv[0] == "--version" || input.Argv[0] == "version")) {
		return errors.New("classifier declared a non-local command local")
	}
	if len(a.Lookups) > 0 {
		for _, lookup := range a.Lookups {
			found := false
			for _, named := range c.Lookups {
				if named.Name == lookup.Name {
					found = true
					break
				}
			}
			if !found {
				return errors.New("unknown lookup")
			}
		}
	}
	if a.Classification == nil {
		return nil
	}
	call := a.Classification
	if len(call.Argv) == 0 || call.StdinSHA256 != input.StdinSHA256 || len(call.Reads)+len(call.Writes) == 0 {
		return errors.New("canonical call is empty or changed its stdin")
	}
	for _, word := range call.Argv {
		if strings.ContainsRune(word, 0) {
			return errors.New("canonical argv contains NUL")
		}
	}
	for _, chain := range call.Reads {
		if err := c.validateChain(chain); err != nil {
			return err
		}
	}
	for _, write := range call.Writes {
		found := false
		for _, cell := range c.Cells {
			if cell.Name == write.Cell {
				found = true
				break
			}
		}
		if !found || write.Cell == "read" || write.Description == "" || len(write.Description) > 512 || termsafe.HasUnsafe(write.Description) {
			return errors.New("unknown write cell or invalid description")
		}
		if err := c.validateChain(write.Chain); err != nil {
			return err
		}
	}
	return nil
}

func (c Classifier) validateChain(chain []Resource) error {
	if len(chain) == 0 || len(chain) > len(c.Resources) {
		return errors.New("missing or cyclic resource chain")
	}
	parentOf := map[string]string{}
	for _, r := range c.Resources {
		parentOf[r.Type] = r.Parent
	}
	seen := map[string]bool{}
	for i, r := range chain {
		parent, ok := parentOf[r.Type]
		if !ok || r.ID == "" || seen[r.Type] || strings.ContainsAny(r.ID, "\x00\r\n") {
			return errors.New("undeclared resource or invalid resource chain")
		}
		seen[r.Type] = true
		if i+1 < len(chain) {
			if parent != chain[i+1].Type {
				return errors.New("resource parent chain changed")
			}
		} else if parent != "" {
			return errors.New("resource chain has no scope root")
		}
	}
	for _, root := range c.Roots {
		if root == chain[len(chain)-1].ID {
			return nil
		}
	}
	return errors.New("resource is outside gate-approved scope")
}
