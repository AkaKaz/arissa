// Package approval implements the Slack button-driven approval
// flow used before running any shell_exec command.
//
// The Broker is shared between the Slack gateway (which forwards
// incoming block_actions) and the tool handler (which calls
// Request and blocks until a decision is made or the timer fires).
package approval

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// DefaultTimeout is how long Request waits before giving up.
const DefaultTimeout = 5 * time.Minute

// Request describes an approval round.
type Request struct {
	Command        string
	Reason         string
	RequesterID    string
	ChannelID      string
	ThreadTS       string
	AllowedUserIDs map[string]struct{}
	Timeout        time.Duration
}

// Result is what Request returns once a decision is made or the
// timer fires.
type Result struct {
	Approved  bool
	DecidedBy string
	Reason    string
}

// Decision is the action a Slack user took on one approval message,
// fed into the Broker by the Slack gateway.
type Decision struct {
	ActionID string
	UserID   string
}

// Broker tracks outstanding approval rounds by action_id and wakes
// the waiting goroutine when a Decision comes in.
type Broker struct {
	mu      sync.Mutex
	pending map[string]*entry // keyed by both approve and deny action ids
}

type entry struct {
	approveID      string
	denyID         string
	requesterID    string
	allowedUserIDs map[string]struct{}
	done           chan Decision
}

// NewBroker returns an empty Broker.
func NewBroker() *Broker {
	return &Broker{pending: map[string]*entry{}}
}

// Request posts the approval prompt to Slack and blocks until a
// Decision arrives or Timeout elapses.
func (b *Broker) Request(ctx context.Context, api *slack.Client, req Request) (Result, error) {
	approveID, denyID := genIDs()

	text := fmt.Sprintf(
		"*Approval required* -- <@%s>\nCommand:\n```\n%s\n```\nReason: _%s_",
		req.RequesterID, req.Command, req.Reason,
	)
	blocks := buildPromptBlocks(req.Command, req.Reason, req.RequesterID, approveID, denyID)

	channelID, messageTS, err := api.PostMessageContext(
		ctx, req.ChannelID,
		slack.MsgOptionText(text, false),
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionTS(req.ThreadTS),
	)
	if err != nil {
		return Result{}, fmt.Errorf("post approval: %w", err)
	}

	ent := &entry{
		approveID:      approveID,
		denyID:         denyID,
		requesterID:    req.RequesterID,
		allowedUserIDs: req.AllowedUserIDs,
		done:           make(chan Decision, 1),
	}
	b.mu.Lock()
	b.pending[approveID] = ent
	b.pending[denyID] = ent
	b.mu.Unlock()
	defer b.remove(approveID, denyID)

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	select {
	case d := <-ent.done:
		approved := d.ActionID == approveID
		updateApprovalMessage(ctx, api, channelID, messageTS, req.Command, approved, d.UserID)
		return Result{Approved: approved, DecidedBy: d.UserID}, nil

	case <-time.After(timeout):
		updateTimeoutMessage(ctx, api, channelID, messageTS, req.Command)
		return Result{Approved: false, Reason: "approval timed out"}, nil

	case <-ctx.Done():
		return Result{Approved: false, Reason: ctx.Err().Error()}, ctx.Err()
	}
}

// HandleAction is called by the Slack gateway for every block
// action. It validates the decider against the approval's own
// allowlist/requester and dispatches to the waiting Request call.
// Unknown or unauthorized actions are silently dropped.
func (b *Broker) HandleAction(d Decision) {
	b.mu.Lock()
	ent, ok := b.pending[d.ActionID]
	b.mu.Unlock()
	if !ok {
		return
	}
	if !isAuthorized(d.UserID, ent.requesterID, ent.allowedUserIDs) {
		return
	}
	select {
	case ent.done <- d:
	default:
	}
}

// HasAction answers "does this action_id belong to an outstanding
// approval round?" so the Slack gateway can ignore unrelated block
// actions.
func (b *Broker) HasAction(actionID string) bool {
	b.mu.Lock()
	_, ok := b.pending[actionID]
	b.mu.Unlock()
	return ok
}

func (b *Broker) remove(ids ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		delete(b.pending, id)
	}
}

func genIDs() (string, string) {
	approve := fmt.Sprintf("arissa_approve_%d_%s", time.Now().UnixNano(), randSuffix())
	deny := strings.Replace(approve, "approve", "deny", 1)
	return approve, deny
}

func randSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func isAuthorized(userID, requesterID string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return userID == requesterID
	}
	_, ok := allowed[userID]
	return ok
}

func buildPromptBlocks(command, reason, requesterID, approveID, denyID string) []slack.Block {
	bodyText := fmt.Sprintf(
		"*Approval required* -- <@%s>\nCommand:\n```\n%s\n```\nReason: _%s_",
		requesterID, command, reason,
	)
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, bodyText, false, false),
			nil, nil,
		),
		slack.NewActionBlock("",
			&slack.ButtonBlockElement{
				Type:     slack.METButton,
				ActionID: approveID,
				Text:     slack.NewTextBlockObject(slack.PlainTextType, "Approve", false, false),
				Style:    slack.StyleDanger,
			},
			&slack.ButtonBlockElement{
				Type:     slack.METButton,
				ActionID: denyID,
				Text:     slack.NewTextBlockObject(slack.PlainTextType, "Deny", false, false),
			},
		),
	}
}

func updateApprovalMessage(ctx context.Context, api *slack.Client, channelID, messageTS, command string, approved bool, userID string) {
	var body string
	if approved {
		body = fmt.Sprintf(
			":white_check_mark: *Approved* by <@%s>\n```\n%s\n```",
			userID, command,
		)
	} else {
		body = fmt.Sprintf(
			":x: *Denied* by <@%s>\n```\n%s\n```",
			userID, command,
		)
	}
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, body, false, false),
			nil, nil,
		),
	}
	_, _, _, _ = api.UpdateMessageContext(ctx, channelID, messageTS,
		slack.MsgOptionText(body, false),
		slack.MsgOptionBlocks(blocks...),
	)
}

func updateTimeoutMessage(ctx context.Context, api *slack.Client, channelID, messageTS, command string) {
	body := fmt.Sprintf(":hourglass: *Approval timed out.*\n```\n%s\n```", command)
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, body, false, false),
			nil, nil,
		),
	}
	_, _, _, _ = api.UpdateMessageContext(ctx, channelID, messageTS,
		slack.MsgOptionText(body, false),
		slack.MsgOptionBlocks(blocks...),
	)
}
