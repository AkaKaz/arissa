// Command arissa is the entrypoint for the arissa Slack agent.
//
// It runs as a systemd Type=simple service. Config is read from
// /etc/arissa/config.toml (or ARISSA_CONFIG). If required keys are
// missing the process exits 0 so systemd does not restart-loop.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"arissa/internal/agent"
	"arissa/internal/config"
	"arissa/internal/memory"
	"arissa/internal/prompt"
	slackgw "arissa/internal/slack"
	"arissa/internal/tools/approval"
	"arissa/internal/tools/shell"
	"arissa/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "[arissa] fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Println("[arissa] required config missing -- exiting cleanly.")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := anthropic.NewClient(option.WithAPIKey(cfg.Anthropic.APIKey))
	systemPrompt := prompt.Build(cfg)

	var mem *memory.Memory
	if cfg.Memory.Enabled {
		m, err := memory.Open(cfg)
		if err != nil {
			return fmt.Errorf("open memory: %w", err)
		}
		mem = m
		defer mem.Close()
	}

	broker := approval.NewBroker()
	gw := slackgw.New(cfg, broker)

	deps := &agent.Deps{
		Client:       &client,
		Cfg:          cfg,
		SystemPrompt: systemPrompt,
		Memory:       mem,
		Tools:        []anthropic.ToolUnionParam{shell.Tool()},
		Handle:       makeToolHandler(cfg, gw, broker),
	}
	registry := agent.NewRegistry(deps)
	gw.SetRegistry(registry)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Printf("[arissa] %s received, shutting down.\n", sig)
		gw.Broadcast(ctx, "shutting down")
		cancel()
	}()

	fmt.Printf("[arissa] %s -- started (Socket Mode).\n", version.Version)
	gw.Broadcast(ctx, "online")

	if err := gw.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("slack gateway: %w", err)
	}
	return nil
}

// makeToolHandler builds the ToolHandler closure that routes the
// shell_exec tool through the Slack approval flow before calling
// shell.Exec.
func makeToolHandler(cfg *config.Config, gw *slackgw.Gateway, broker *approval.Broker) agent.ToolHandler {
	return func(ctx context.Context, name string, input json.RawMessage, ac agent.Context) (agent.ToolResult, error) {
		if name != "shell_exec" {
			return agent.ToolResult{
				Content: fmt.Sprintf("unknown tool: %s", name),
				IsError: true,
			}, nil
		}

		var args struct {
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return agent.ToolResult{
				Content: fmt.Sprintf("shell_exec: invalid input: %v", err),
				IsError: true,
			}, nil
		}
		command := strings.TrimSpace(args.Command)
		reason := args.Reason
		if reason == "" {
			reason = "(no reason)"
		}
		if command == "" {
			return agent.ToolResult{
				Content: "shell_exec: command is required",
				IsError: true,
			}, nil
		}

		req := approval.Request{
			Command:        command,
			Reason:         reason,
			RequesterID:    ac.UserID,
			ChannelID:      ac.ChannelID,
			ThreadTS:       ac.ThreadTS,
			AllowedUserIDs: cfg.Slack.AllowedUserIDs,
		}
		decision, err := broker.Request(ctx, gw.API(), req)
		if err != nil {
			return agent.ToolResult{
				Content: fmt.Sprintf("approval request failed: %v", err),
				IsError: true,
			}, nil
		}
		if !decision.Approved {
			msg := "Operator denied the command"
			if decision.Reason != "" {
				msg = fmt.Sprintf("%s (%s)", msg, decision.Reason)
			}
			msg += ". Do not retry; ask the operator what they want instead."
			return agent.ToolResult{Content: msg, IsError: false}, nil
		}

		res, err := shell.Exec(ctx, command)
		if err != nil {
			return agent.ToolResult{
				Content: fmt.Sprintf("exec failed: %v", err),
				IsError: true,
			}, nil
		}
		return agent.ToolResult{
			Content: shell.Format(res),
			IsError: res.ExitCode != 0,
		}, nil
	}
}
