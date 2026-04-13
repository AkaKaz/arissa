// Package slack is arissa's Socket Mode gateway.
//
// It owns the *slack.Client / *socketmode.Client pair, dispatches
// incoming events (app mentions, DMs, block actions) to the agent
// registry or the approval broker, and exposes Broadcast for
// startup/shutdown status messages.
package slack

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"arissa/internal/agent"
	"arissa/internal/config"
	"arissa/internal/tools/approval"
)

const chunkMaxBytes = 3900

// Gateway ties the Slack client to arissa's session registry and
// approval broker.
type Gateway struct {
	cfg      *config.Config
	api      *slack.Client
	sm       *socketmode.Client
	registry *agent.Registry
	broker   *approval.Broker
}

// API returns the underlying Slack Web API client, used by the
// approval flow to post and update messages directly.
func (g *Gateway) API() *slack.Client {
	return g.api
}

// New builds a Gateway. Wire the agent registry in with SetRegistry
// after construction — the tool handler closes over the gateway,
// so the dependency is cyclic at boot time.
func New(cfg *config.Config, broker *approval.Broker) *Gateway {
	api := slack.New(
		cfg.Slack.BotToken,
		slack.OptionAppLevelToken(cfg.Slack.AppToken),
	)
	sm := socketmode.New(api)
	return &Gateway{
		cfg:    cfg,
		api:    api,
		sm:     sm,
		broker: broker,
	}
}

// SetRegistry wires the agent registry into the gateway.
func (g *Gateway) SetRegistry(r *agent.Registry) {
	g.registry = r
}

// Broadcast posts a status message to every allowed channel. Used
// for startup and shutdown notifications.
func (g *Gateway) Broadcast(ctx context.Context, text string) {
	for ch := range g.cfg.Slack.AllowedChannelIDs {
		if _, _, err := g.api.PostMessageContext(ctx, ch, slack.MsgOptionText(text, false)); err != nil {
			fmt.Printf("[slack] broadcast to %s failed: %v\n", ch, err)
		}
	}
}

// Run blocks until ctx is cancelled. It drives the Socket Mode
// event loop and dispatches events to the registry / broker.
func (g *Gateway) Run(ctx context.Context) error {
	go g.dispatch(ctx)
	return g.sm.RunContext(ctx)
}

func (g *Gateway) dispatch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-g.sm.Events:
			if !ok {
				return
			}
			g.handleEvent(ctx, evt)
		}
	}
}

func (g *Gateway) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPI, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		if evt.Request != nil {
			g.sm.Ack(*evt.Request)
		}
		if eventsAPI.Type != slackevents.CallbackEvent {
			return
		}
		switch ev := eventsAPI.InnerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			g.onMention(ctx, ev)
		case *slackevents.MessageEvent:
			g.onMessage(ctx, ev)
		}

	case socketmode.EventTypeInteractive:
		cb, ok := evt.Data.(slack.InteractionCallback)
		if !ok {
			return
		}
		if evt.Request != nil {
			g.sm.Ack(*evt.Request)
		}
		if cb.Type != slack.InteractionTypeBlockActions {
			return
		}
		for _, a := range cb.ActionCallback.BlockActions {
			if !g.broker.HasAction(a.ActionID) {
				continue
			}
			g.broker.HandleAction(approval.Decision{
				ActionID: a.ActionID,
				UserID:   cb.User.ID,
			})
		}
	}
}

func (g *Gateway) onMention(ctx context.Context, ev *slackevents.AppMentionEvent) {
	if ev.User == "" {
		return
	}
	go func() {
		_ = g.api.AddReactionContext(ctx, "thumbsup", slack.ItemRef{
			Channel:   ev.Channel,
			Timestamp: ev.TimeStamp,
		})
	}()
	g.handleMessage(ctx, ev.User, ev.Text, ev.Channel, ev.TimeStamp)
}

func (g *Gateway) onMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	// Only direct messages, and only genuine user input (drop bot
	// echoes and subtype variants like edits/joins).
	if ev.ChannelType != "im" {
		return
	}
	if ev.BotID != "" || ev.SubType != "" {
		return
	}
	if ev.User == "" {
		return
	}
	go func() {
		_ = g.api.AddReactionContext(ctx, "thumbsup", slack.ItemRef{
			Channel:   ev.Channel,
			Timestamp: ev.TimeStamp,
		})
	}()
	g.handleMessage(ctx, ev.User, ev.Text, ev.Channel, ev.TimeStamp)
}

func (g *Gateway) handleMessage(ctx context.Context, userID, rawText, channelID, threadTS string) {
	if g.registry == nil {
		return
	}
	if len(g.cfg.Slack.AllowedChannelIDs) > 0 {
		if _, ok := g.cfg.Slack.AllowedChannelIDs[channelID]; !ok {
			return
		}
	}
	if len(g.cfg.Slack.AllowedUserIDs) > 0 {
		if _, ok := g.cfg.Slack.AllowedUserIDs[userID]; !ok {
			g.reply(ctx, channelID, threadTS, "Sorry, you are not on this bot's allowlist.")
			return
		}
	}

	content := strings.TrimSpace(stripMention(rawText))
	if content == "" {
		return
	}

	if content == "!reset" {
		g.registry.Reset(userID)
		g.reply(ctx, channelID, threadTS, "ok")
		return
	}

	session := g.registry.For(userID)
	reply, err := session.Send(ctx, content, agent.Context{
		UserID:    userID,
		ChannelID: channelID,
		ThreadTS:  threadTS,
	})
	if err != nil {
		g.reply(ctx, channelID, threadTS, fmt.Sprintf("internal error: %v", err))
		return
	}
	for _, chunk := range chunkForSlack(reply) {
		g.reply(ctx, channelID, threadTS, chunk)
	}
}

func (g *Gateway) reply(ctx context.Context, channelID, threadTS, text string) {
	_, _, err := g.api.PostMessageContext(ctx, channelID,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		fmt.Printf("[slack] reply failed: %v\n", err)
	}
}

var mentionRE = regexp.MustCompile(`<@[A-Z0-9]+>`)

func stripMention(s string) string {
	return mentionRE.ReplaceAllString(s, "")
}

func chunkForSlack(text string) []string {
	if len(text) <= chunkMaxBytes {
		return []string{text}
	}
	var out []string
	for len(text) > chunkMaxBytes {
		cut := strings.LastIndex(text[:chunkMaxBytes], "\n")
		if cut < chunkMaxBytes/2 {
			cut = chunkMaxBytes
		}
		out = append(out, text[:cut])
		text = text[cut:]
	}
	if len(text) > 0 {
		out = append(out, text)
	}
	return out
}
