package macosuser

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// SandboxTmpdirValidationContext is the host-resolved contract for one native launch's allowed
// temp roots. It is serialized into the macos-user session environment and baked into generated
// program launchers; runtime shell variables cannot add roots to it.
type SandboxTmpdirValidationContext struct {
	Mode              Mode   `json:"mode"`
	Workspace         string `json:"workspace"`
	Home              string `json:"home"`
	DarwinUserTempDir string `json:"darwin_user_temp_dir,omitempty"`
	SharedTmp         bool   `json:"shared_tmp,omitempty"`
}

// MarshalSandboxTmpdirValidationContext writes the context as JSON for the protected session
// carrier and generated launchers.
func MarshalSandboxTmpdirValidationContext(ctx SandboxTmpdirValidationContext) (string, error) {
	if err := ctx.validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(ctx)
	if err != nil {
		return "", fmt.Errorf("encode native temp-validation context: %w", err)
	}
	return string(b), nil
}

// ParseSandboxTmpdirValidationContext reads the launcher-baked context passed to the hidden
// runtime validator. Invalid or incomplete metadata fails closed rather than guessing roots.
func ParseSandboxTmpdirValidationContext(raw string) (SandboxTmpdirValidationContext, error) {
	var ctx SandboxTmpdirValidationContext
	if raw == "" {
		return ctx, fmt.Errorf("native temp-validation context is missing")
	}
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return ctx, fmt.Errorf("native temp-validation context is malformed")
	}
	if err := ctx.validate(); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func (ctx SandboxTmpdirValidationContext) validate() error {
	if ctx.Mode != ModeAccount && ctx.Mode != ModeSelf {
		return fmt.Errorf("native temp-validation context has an unknown run-as mode")
	}
	if ctx.Workspace == "" || ctx.Home == "" || !filepath.IsAbs(ctx.Workspace) || !filepath.IsAbs(ctx.Home) {
		return fmt.Errorf("native temp-validation context lacks absolute workspace and sandbox-home roots")
	}
	if ctx.DarwinUserTempDir != "" && !filepath.IsAbs(ctx.DarwinUserTempDir) {
		return fmt.Errorf("native temp-validation context has a non-absolute user-temp root")
	}
	return nil
}

// SandboxTmpdirValidationProblem is the single policy predicate used by the direct run plan and
// nested generated-program launches, in both modes: the deny-by-default profile closes the shared
// /tmp in the account mode too (MMP-D29), so a TMPDIR or program-specific override outside the
// roots the context names would fail every temporary file there as well (MMP-D36).
func SandboxTmpdirValidationProblem(ctx SandboxTmpdirValidationContext, variable, value string) string {
	if value == "" {
		return ""
	}
	return composedTmpdirEnvProblem(variable, value,
		[]string{ctx.Workspace, ctx.Home, ctx.DarwinUserTempDir}, ctx.SharedTmp)
}

// ValidateSandboxTmpdirEnv applies the direct run plan's same temp-root predicate to the final
// TMPDIR and every selected program-declared follower before its generated launcher execs the
// program. Only the names and values come from that invocation; roots are host-resolved context.
func ValidateSandboxTmpdirEnv(ctx SandboxTmpdirValidationContext, names []string, getenv func(string) string) error {
	if err := ctx.validate(); err != nil {
		return err
	}
	if getenv == nil {
		return fmt.Errorf("native temp-validation environment is unavailable")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !packdecl.ValidEnvName(name) {
			return fmt.Errorf("native temp-validation received an invalid environment variable name")
		}
		if seen[name] {
			return fmt.Errorf("native temp-validation received a duplicate environment variable name")
		}
		seen[name] = true
	}
	for _, variable := range append([]string{"TMPDIR"}, names...) {
		value := getenv(variable)
		if problem := SandboxTmpdirValidationProblem(ctx, variable, value); problem != "" {
			return fmt.Errorf("%s", problem)
		}
	}
	return nil
}
