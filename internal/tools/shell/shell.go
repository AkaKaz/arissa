// Package shell implements the shell_exec tool.
//
// arissa delegates access control to the OS: the arissa service
// user's groups and sudoers configuration decide what commands can
// actually succeed. The system prompt and skills guide Claude on
// what to run.
package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool returns the shell_exec tool definition for the Claude API.
func Tool() anthropic.BetaToolUnionParam {
	return anthropic.BetaToolUnionParam{
		OfTool: &anthropic.BetaToolParam{
			Name: "shell_exec",
			Description: anthropic.String(
				"Execute a shell command on this host. " +
					"The command runs as the arissa service user. " +
					"What you can do is determined by OS-level permissions (groups, sudoers). " +
					"For destructive operations, the operator will be asked to approve via Slack.",
			),
			InputSchema: anthropic.BetaToolInputSchemaParam{
				Properties: map[string]any{
					"command": map[string]string{
						"type":        "string",
						"description": "The shell command to execute.",
					},
					"reason": map[string]string{
						"type": "string",
						"description": "One short sentence explaining why this command is needed. " +
							"Shown to the operator in the approval prompt for mutating commands.",
					},
				},
				Required: []string{"command", "reason"},
			},
		},
	}
}

// Result is the raw outcome of a shell invocation.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

const (
	defaultTimeout = 30 * time.Second
	maxOutputBytes = 4000
)

// Exec runs cmd via `sh -c` with a default 30s timeout. The
// command inherits the parent's environment plus LANG=C.UTF-8.
func Exec(ctx context.Context, command string) (Result, error) {
	return ExecWithTimeout(ctx, command, defaultTimeout)
}

// ExecWithTimeout is Exec with a caller-supplied timeout.
func ExecWithTimeout(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), "LANG=C.UTF-8")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	// A non-zero exit is represented by ExitCode alone; don't
	// escalate it into a Go error. Return runtime errors (e.g.
	// couldn't fork) as-is.
	if err != nil && cmd.ProcessState == nil {
		return res, err
	}
	return res, nil
}

// Format renders a Result as a human-readable block for Claude.
func Format(r Result) string {
	return fmt.Sprintf(
		"exit=%d\n--- stdout ---\n%s\n--- stderr ---\n%s",
		r.ExitCode,
		truncate(r.Stdout),
		truncate(r.Stderr),
	)
}

func truncate(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + fmt.Sprintf("\n...[truncated %d bytes]", len(s)-maxOutputBytes)
}
