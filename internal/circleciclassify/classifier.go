// Package circleciclassify implements the built-in CircleCI call-broker classifier.
package circleciclassify

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

var circleCIProjectSlug = regexp.MustCompile(`^gh/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)

// Run handles `yolo internal call-broker-classify circleci`. It reads only the
// versioned classifier protocol and writes one answer; it never runs CircleCI.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "circleci" {
		fmt.Fprintln(stderr, "use the selected circleci pack's classifier dispatch")
		return 64
	}
	var input callbroker.CLIInput
	decoder := json.NewDecoder(io.LimitReader(stdin, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fmt.Fprintln(stderr, "CircleCI classifier input is invalid; retry the selected circleci command")
		return 2
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fmt.Fprintln(stderr, "CircleCI classifier input has trailing data; retry the selected circleci command")
		return 2
	}
	if input.Version != protocolVersion || input.Front != "cli" || len(input.Argv) == 0 {
		fmt.Fprintln(stderr, "CircleCI classifier protocol is unsupported; rebuild yolo and retry")
		return 2
	}
	answer := Classify(input)
	if err := json.NewEncoder(stdout).Encode(answer); err != nil {
		fmt.Fprintln(stderr, "CircleCI classifier could not write its answer")
		return 2
	}
	return 0
}

// Classify allows only an explicit, read-only project run listing whose CircleCI
// project slug is one of the gate-approved workspace roots. Other upstream verbs
// remain refused until their resource lookups and canonical calls are implemented.
func Classify(input callbroker.CLIInput) callbroker.Answer {
	answer := callbroker.Answer{Version: input.Version}
	if input.Version != protocolVersion || input.Front != "cli" || len(input.Argv) == 0 {
		return refused(answer, "unsupported", "unsupported CircleCI classifier input", "Use a selected yolo build and retry the CircleCI command")
	}
	if len(input.Argv) == 1 && (input.Argv[0] == "--help" || input.Argv[0] == "--version" || input.Argv[0] == "version") {
		answer.Local = true
		return answer
	}
	argv := input.Argv
	if len(argv) < 2 || argv[0] != "run" || argv[1] != "list" {
		return refused(answer, "unsupported", "this CircleCI verb is not enabled by the scoped pack", "Use `circleci run list --project gh/<org>/<repo>` for scoped reads, or run the command on the host")
	}
	project, err := parseRunList(argv[2:])
	if err != nil {
		return refused(answer, "unsupported", err.Error(), "Use `circleci run list --project gh/<org>/<repo>` with only the supported listing flags")
	}
	if !circleCIProjectSlug.MatchString(project) {
		return refused(answer, "scope", "only a GitHub CircleCI project slug (`gh/<org>/<repo>`) can be scoped by this pack", "Use the GitHub project slug for a repository approved at this workspace's CircleCI scope gate")
	}
	found := false
	for _, root := range input.Roots {
		if project == root {
			found = true
			break
		}
	}
	if !found {
		return refused(answer, "scope", "the CircleCI project is not a gate-approved workspace root", "Add the repository to this workspace's approved scope with `yolo check`, then retry")
	}
	answer.Classification = &callbroker.Classification{
		Argv:        append([]string(nil), argv...),
		StdinSHA256: input.StdinSHA256,
		Reads:       [][]callbroker.Resource{{{Type: "project", ID: project}}},
	}
	return answer
}

func parseRunList(args []string) (string, error) {
	project := ""
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--project":
			if seen[name] {
				return "", errors.New("repeated --project is not supported")
			}
			seen[name] = true
			if !hasValue {
				if i+1 == len(args) {
					return "", errors.New("--project requires a CircleCI project slug")
				}
				i++
				value = args[i]
			}
			if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
				return "", errors.New("--project requires a literal CircleCI project slug")
			}
			project = value
		case "--branch", "-b":
			if seen["--branch"] {
				return "", errors.New("repeated --branch is not supported")
			}
			seen["--branch"] = true
			if !hasValue {
				if i+1 == len(args) {
					return "", errors.New(name + " requires a branch name")
				}
				i++
				value = args[i]
			}
			if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
				return "", errors.New(name + " requires a literal branch name")
			}
		case "--json":
			if hasValue || seen[name] {
				return "", errors.New("--json takes no value and may appear only once")
			}
			seen[name] = true
		default:
			return "", errors.New("unknown CircleCI run-list argument or flag")
		}
	}
	if project == "" {
		return "", errors.New("run list requires --project because host git-remote discovery is not used by the broker")
	}
	return project, nil
}

func refused(answer callbroker.Answer, class, reason, next string) callbroker.Answer {
	answer.Refused = &callbroker.Refusal{Class: class, Reason: reason, NextStep: next}
	return answer
}
