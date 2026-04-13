// Package config loads arissa's TOML configuration.
//
// Reads /etc/arissa/config.toml (or the path in ARISSA_CONFIG). If
// required keys (Slack bot/app token, Anthropic API key) are missing
// Load returns (nil, nil) so main can exit cleanly without making
// systemd restart-loop on unconfigured hosts.
package config

import (
	"fmt"
	"os"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Config is the parsed configuration.
type Config struct {
	Slack     SlackConfig
	Anthropic AnthropicConfig
	Agent     AgentConfig
	Memory    MemoryConfig
	Prompt    PromptConfig
}

// SlackConfig holds Slack tokens and access lists.
type SlackConfig struct {
	BotToken          string
	AppToken          string
	AllowedChannelIDs map[string]struct{}
	AllowedUserIDs    map[string]struct{}
}

// AnthropicConfig holds the API key and model name.
type AnthropicConfig struct {
	APIKey string
	Model  string
}

// AgentConfig tunes the Claude tool-use loop.
type AgentConfig struct {
	Name              string
	MaxToolIterations int
}

// MemoryConfig controls the filesystem-backed memory store
// exposed to Claude via the built-in memory_20250818 tool.
type MemoryConfig struct {
	Dir string
}

// PromptConfig points at the filesystem locations that contribute
// to the system prompt.
type PromptConfig struct {
	System     string
	ContextDir string
	SkillsDir  string
}

// DefaultPath is the built-in location of config.toml.
const DefaultPath = "/etc/arissa/config.toml"

// raw mirrors the TOML shape; fields use snake_case on disk.
type raw struct {
	Slack struct {
		BotToken          string   `toml:"bot_token"`
		AppToken          string   `toml:"app_token"`
		AllowedChannelIDs []string `toml:"allowed_channel_ids"`
		AllowedUserIDs    []string `toml:"allowed_user_ids"`
	} `toml:"slack"`
	Anthropic struct {
		APIKey string `toml:"api_key"`
		Model  string `toml:"model"`
	} `toml:"anthropic"`
	Agent struct {
		Name              string `toml:"name"`
		MaxToolIterations *int   `toml:"max_tool_iterations"`
	} `toml:"agent"`
	Memory struct {
		Dir string `toml:"dir"`
	} `toml:"memory"`
	Prompt struct {
		System     string `toml:"system"`
		ContextDir string `toml:"context_dir"`
		SkillsDir  string `toml:"skills_dir"`
	} `toml:"prompt"`
}

// Load reads the config file. If the file is missing or required
// keys are absent it returns (nil, nil) with a log line on stderr.
// A parse failure is returned as an error.
func Load() (*Config, error) {
	path := strings.TrimSpace(os.Getenv("ARISSA_CONFIG"))
	if path == "" {
		path = DefaultPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "[arissa] config not found at %s\n", path)
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var r raw
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	botToken := strings.TrimSpace(r.Slack.BotToken)
	appToken := strings.TrimSpace(r.Slack.AppToken)
	apiKey := strings.TrimSpace(r.Anthropic.APIKey)

	if botToken == "" || appToken == "" || apiKey == "" {
		return nil, nil
	}

	model := strings.TrimSpace(r.Anthropic.Model)
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	agentName := strings.TrimSpace(r.Agent.Name)
	if agentName == "" {
		agentName = "arissa"
	}
	maxIter := 10
	if r.Agent.MaxToolIterations != nil {
		maxIter = *r.Agent.MaxToolIterations
	}
	memDir := strings.TrimSpace(r.Memory.Dir)
	if memDir == "" {
		memDir = "/var/lib/arissa/memories"
	}
	sysPrompt := strings.TrimSpace(r.Prompt.System)
	if sysPrompt == "" {
		sysPrompt = "/etc/arissa/system.prompt.md"
	}
	ctxDir := strings.TrimSpace(r.Prompt.ContextDir)
	if ctxDir == "" {
		ctxDir = "/etc/arissa/context"
	}
	skillsDir := strings.TrimSpace(r.Prompt.SkillsDir)
	if skillsDir == "" {
		skillsDir = "/etc/arissa/skills"
	}

	return &Config{
		Slack: SlackConfig{
			BotToken:          botToken,
			AppToken:          appToken,
			AllowedChannelIDs: toSet(r.Slack.AllowedChannelIDs),
			AllowedUserIDs:    toSet(r.Slack.AllowedUserIDs),
		},
		Anthropic: AnthropicConfig{
			APIKey: apiKey,
			Model:  model,
		},
		Agent: AgentConfig{
			Name:              agentName,
			MaxToolIterations: maxIter,
		},
		Memory: MemoryConfig{
			Dir: memDir,
		},
		Prompt: PromptConfig{
			System:     sysPrompt,
			ContextDir: ctxDir,
			SkillsDir:  skillsDir,
		},
	}, nil
}

func toSet(ss []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		t := strings.TrimSpace(s)
		if t != "" {
			out[t] = struct{}{}
		}
	}
	return out
}
