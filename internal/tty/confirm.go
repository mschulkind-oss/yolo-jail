package tty

import (
	"bufio"
	"io"
	"strings"
)

// Confirm writes prompt to out and reads one line of answer from in: the one yes/no reader
// every yolo prompt shares, so `[y/N]` and `[Y/n]` mean the same thing wherever they appear.
//
// "y" and "yes" (any case) answer yes, and "n" and "no" answer no. An EMPTY line — the user
// pressed Enter — takes defaultYes, which is the capital letter in the prompt's brackets.
// Anything else answers no, since a prompt that guesses at a typo would be deciding for the
// user. END OF INPUT answers no whatever the default: a closed or empty stdin is not a person
// pressing Enter, and a default-yes prompt that read EOF as consent would act for a caller who
// never saw the question. A nil in is the same "no one can answer" and returns false.
//
// Whether to prompt at all is the caller's decision, made first with IsTerminal on the
// streams it will use: this reads whatever it is handed.
func Confirm(out io.Writer, in io.Reader, prompt string, defaultYes bool) bool {
	if in == nil {
		return false
	}
	_, _ = io.WriteString(out, prompt)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "":
		return defaultYes
	case "y", "yes":
		return true
	default:
		return false
	}
}
