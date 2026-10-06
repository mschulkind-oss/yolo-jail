package packload

// agentfiles.go is the composition half of AGENT FILES (a term coined in
// docs/design/model-lists-and-pickers.md MM-D33; packdecl's agentfiles.go carries the definition):
// the files an agent's env derive composes under a variable its program declares in
// `agent_files`, which a jail's per-agent env writer puts beside the agent's env file and points
// the variable at.
//
// THE CONTENT NEVER JOINS THE ENVIRONMENT. AgentEnv takes each declared variable out of the
// derive's output before the output becomes shape vars, so no vehicle that serializes an
// environment (the shared file, the macos-user session env, the host exec) can hand a document
// to a program as a value. A notch that writes agent files collects them (WithAgentFiles), and
// its writer turns each into a file and a path; every other notch withholds them.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// AgentFile is one agent file a launch composed for an agent.
type AgentFile struct {
	// Var is the variable the program reads the file's path from, a key of its `agent_files`.
	Var string
	// Name is the file's declared name; the writer puts it beside the agent's env file as
	// `<agent>.<Name>`.
	Name string
	// Content is the file's bytes: the derive's string as it is, or its table as JSON.
	Content []byte
}

// WithAgentFiles tells the env derive this notch writes agent files (ctx.agent_files) and
// collects the ones it composes into *dst, sorted by variable. Without it the derive is told the
// notch writes none, and a value it composes under a declared variable anyway is withheld.
func WithAgentFiles(dst *[]AgentFile) AgentEnvOption {
	return func(o *agentEnvOpts) { o.files = dst }
}

// takeAgentFiles removes owner's declared agent-file variables for agent from composed, the env
// derive's output, and, when dst is set, appends each one composed as an AgentFile. A value that
// is neither a string nor a table is a broken producer, refused for envVarsOf's reason: this
// composition IS the delivery.
func takeAgentFiles(composed map[string]any, owner *Pack, agent string, dst *[]AgentFile) error {
	declared := owner.Decl.AgentFiles(agent)
	if len(declared) == 0 {
		return nil
	}
	vars := make([]string, 0, len(declared))
	for v := range declared {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	for _, v := range vars {
		val, ok := composed[v]
		if !ok {
			continue
		}
		delete(composed, v)
		if dst == nil || val == nil {
			continue
		}
		content, err := agentFileContent(val)
		if err != nil {
			return fmt.Errorf("pack %s: %s's env derive set the agent file %s (%s): %w",
				owner.Name, agent, v, declared[v], err)
		}
		if len(content) == 0 {
			continue
		}
		*dst = append(*dst, AgentFile{Var: v, Name: declared[v], Content: content})
	}
	return nil
}

// agentFileContent is an agent file's bytes: a string as it is, a table as indented JSON with a
// trailing newline. A Go map marshals with its keys sorted, so the bytes do not move between
// launches.
func agentFileContent(val any) ([]byte, error) {
	switch v := val.(type) {
	case string:
		return []byte(v), nil
	case map[string]any, []any:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	default:
		return nil, fmt.Errorf("a %T, want a string or a table", val)
	}
}

// AgentFiles is the delivery's agent files, nil for a nil delivery (an agent the gate composed
// nothing for).
func (d *AgentDelivery) AgentFiles() []AgentFile {
	if d == nil {
		return nil
	}
	return d.Files
}
