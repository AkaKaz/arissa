// Package memory wires arissa's filesystem-backed memory Store
// behind the Anthropic built-in memory tool (memory_20250818).
//
// The Claude API emits tool_use blocks whose input follows
// BetaMemoryTool20250818CommandUnion. Dispatch reads the Command
// discriminator and forwards to the Store. The strings we return
// mirror the upstream Python reference implementation so Claude's
// built-in prompt matches our outputs.
package memory

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"

	"arissa/internal/memory"
)

// Tool returns the Beta tool declaration. arissa calls it into
// BetaMessageNewParams.Tools alongside the shell_exec tool.
func Tool() anthropic.BetaToolUnionParam {
	return anthropic.BetaToolUnionParam{
		OfMemoryTool20250818: &anthropic.BetaMemoryTool20250818Param{},
	}
}

// Dispatch unmarshals the Claude-side memory command JSON and
// runs it against store. The returned string is suitable for
// placing into a tool_result content block. If ok is false the
// block should be marked is_error=true.
func Dispatch(store *memory.Store, input json.RawMessage) (result string, ok bool) {
	var cmd anthropic.BetaMemoryTool20250818CommandUnion
	if err := json.Unmarshal(input, &cmd); err != nil {
		return fmt.Sprintf("Error: invalid memory command: %v", err), false
	}

	out, err := run(store, cmd)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return out, true
}

func run(store *memory.Store, cmd anthropic.BetaMemoryTool20250818CommandUnion) (string, error) {
	switch cmd.Command {
	case "view":
		return store.View(cmd.Path, cmd.ViewRange)
	case "create":
		return store.Create(cmd.Path, cmd.FileText)
	case "str_replace":
		return store.StrReplace(cmd.Path, cmd.OldStr, cmd.NewStr)
	case "insert":
		return store.Insert(cmd.Path, cmd.InsertLine, cmd.InsertText)
	case "delete":
		return store.Delete(cmd.Path)
	case "rename":
		return store.Rename(cmd.OldPath, cmd.NewPath)
	default:
		return "", fmt.Errorf("unknown memory command %q", cmd.Command)
	}
}
