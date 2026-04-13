// Package agent drives the Claude tool-use loop.
//
// Each Slack user gets a Session with its own rolling history of
// MessageParams. The Session asks Claude for a response, resolves
// any tool_use blocks by calling the registered ToolHandler, feeds
// the results back, and loops until Claude produces a final text
// response (or the iteration cap is hit).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"

	"arissa/internal/config"
)

const defaultMaxToolIterations = 10
const maxRollingTurns = 20
const maxTokens = 4096

// Context describes who is talking and where. Passed to ToolHandler
// so tool implementations (like the approval flow) can reach the
// same Slack thread.
type Context struct {
	UserID    string
	ChannelID string
	ThreadTS  string
}

// ToolResult is what a ToolHandler returns. Content is fed back to
// Claude as the tool_result; IsError marks the block with is_error
// so Claude can react to failures.
type ToolResult struct {
	Content string
	IsError bool
}

// ToolHandler resolves one tool_use invocation from Claude.
type ToolHandler func(ctx context.Context, name string, input json.RawMessage, ac Context) (ToolResult, error)

// Deps bundles the collaborators that every Session shares.
type Deps struct {
	Client       *anthropic.Client
	Cfg          *config.Config
	SystemPrompt string
	Tools        []anthropic.ToolUnionParam
	Handle       ToolHandler
}

// Session is one user's rolling conversation with Claude.
type Session struct {
	deps    *Deps
	history []anthropic.MessageParam
}

// NewSession creates an empty session tied to a set of Deps.
func NewSession(deps *Deps) *Session {
	return &Session{deps: deps}
}

// Send pushes the user text into the session and runs the tool-use
// loop until Claude finishes. The returned string is the final
// assistant message.
func (s *Session) Send(ctx context.Context, userText string, ac Context) (string, error) {
	s.history = append(s.history, anthropic.NewUserMessage(anthropic.NewTextBlock(userText)))
	s.trim()

	maxIter := s.deps.Cfg.Agent.MaxToolIterations
	if maxIter <= 0 {
		maxIter = defaultMaxToolIterations
	}

	for i := 0; i < maxIter; i++ {
		resp, err := s.deps.Client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(s.deps.Cfg.Anthropic.Model),
			MaxTokens: maxTokens,
			System: []anthropic.TextBlockParam{
				{Text: s.deps.SystemPrompt},
			},
			Tools:    s.deps.Tools,
			Messages: s.history,
		})
		if err != nil {
			return "", fmt.Errorf("claude: %w", err)
		}

		s.history = append(s.history, resp.ToParam())

		if resp.StopReason != anthropic.StopReasonToolUse {
			text := collectText(resp.Content)
			s.trim()
			if text == "" {
				return "(no response)", nil
			}
			return text, nil
		}

		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			tu, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}
			out, herr := s.deps.Handle(ctx, tu.Name, tu.Input, ac)
			content := out.Content
			if herr != nil {
				content = fmt.Sprintf("tool error: %v", herr)
				results = append(results, anthropic.NewToolResultBlock(tu.ID, content, true))
				continue
			}
			results = append(results, anthropic.NewToolResultBlock(tu.ID, content, out.IsError))
		}
		s.history = append(s.history, anthropic.NewUserMessage(results...))
	}

	return "(aborted: tool-use loop exceeded iteration cap)", nil
}

func collectText(blocks []anthropic.ContentBlockUnion) string {
	var parts []string
	for _, b := range blocks {
		if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
			if tb.Text != "" {
				parts = append(parts, tb.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// trim keeps the rolling window bounded and ensures the window does
// not start with a dangling tool_result block.
func (s *Session) trim() {
	for len(s.history) > maxRollingTurns*2 {
		s.history = s.history[1:]
	}
	for len(s.history) > 0 && hasToolResult(s.history[0]) {
		s.history = s.history[1:]
	}
}

func hasToolResult(m anthropic.MessageParam) bool {
	if m.Role != anthropic.MessageParamRoleUser {
		return false
	}
	for _, b := range m.Content {
		if b.OfToolResult != nil {
			return true
		}
	}
	return false
}

// Registry keeps one Session per user id.
type Registry struct {
	mu       sync.Mutex
	deps     *Deps
	sessions map[string]*Session
}

// NewRegistry returns an empty Registry.
func NewRegistry(deps *Deps) *Registry {
	return &Registry{deps: deps, sessions: map[string]*Session{}}
}

// For returns the Session for userID, creating one if needed.
func (r *Registry) For(userID string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[userID]
	if !ok {
		s = NewSession(r.deps)
		r.sessions[userID] = s
	}
	return s
}

// Reset drops the Session and its persisted turns for userID.
func (r *Registry) Reset(userID string) {
	r.mu.Lock()
	delete(r.sessions, userID)
	r.mu.Unlock()
}
