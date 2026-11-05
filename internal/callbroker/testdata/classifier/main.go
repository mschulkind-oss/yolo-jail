package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type input struct {
	Version     string            `json:"version"`
	Argv        []string          `json:"argv"`
	StdinSHA256 string            `json:"stdin_sha256"`
	Facts       []json.RawMessage `json:"facts"`
}

type resource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type classification struct {
	Argv        []string       `json:"argv"`
	StdinSHA256 string         `json:"stdin_sha256"`
	Reads       [][]resource   `json:"reads,omitempty"`
	Writes      []writeRequest `json:"writes,omitempty"`
}

type writeRequest struct {
	Cell        string     `json:"cell"`
	Chain       []resource `json:"chain"`
	Description string     `json:"description"`
}

type lookupRequest struct {
	Name      string   `json:"name"`
	Arguments []string `json:"arguments"`
}

type refusal struct {
	Class    string `json:"class"`
	Reason   string `json:"reason"`
	NextStep string `json:"next_step"`
}

type answer struct {
	Version        string          `json:"version"`
	Classification *classification `json:"classification,omitempty"`
	Lookups        []lookupRequest `json:"lookups,omitempty"`
	Refused        *refusal        `json:"refused,omitempty"`
}

func main() {
	var req input
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil || len(req.Argv) == 0 {
		os.Exit(2)
	}
	if req.Argv[0] == "classifier-error" {
		_, _ = fmt.Fprintln(os.Stdout, "not-json")
		return
	}
	out := answer{Version: req.Version}
	read := [][]resource{{{Type: "project", ID: "team:alpha"}}}
	switch req.Argv[0] {
	case "lookup", "read", "error":
		out.Classification = &classification{Argv: req.Argv, StdinSHA256: req.StdinSHA256, Reads: read}
	case "update-pr":
		out.Classification = &classification{Argv: req.Argv, StdinSHA256: req.StdinSHA256,
			Writes: []writeRequest{{Cell: "change", Chain: []resource{{Type: "pr", ID: "#1"}, {Type: "project", ID: "team:alpha"}}, Description: "update test PR"}}}
	case "update":
		if len(req.Facts) == 0 {
			out.Lookups = []lookupRequest{{Name: "task-project", Arguments: []string{"a1"}}}
		} else {
			writes := make([]writeRequest, 0, len(req.Argv)-1)
			for _, item := range req.Argv[1:] {
				project, cell := "team:alpha", "change"
				if item == "b1" {
					project, cell = "team:beta", "blocked"
				}
				writes = append(writes, writeRequest{Cell: cell, Chain: []resource{{Type: "task", ID: item}, {Type: "project", ID: project}}, Description: "update task"})
			}
			out.Classification = &classification{Argv: req.Argv, StdinSHA256: req.StdinSHA256, Writes: writes}
		}
	default:
		out.Refused = &refusal{Class: "unknown", Reason: "unknown command", NextStep: "Use a named task command"}
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
