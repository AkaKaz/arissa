// Package prompt composes the Claude system prompt at startup.
//
// Reads the base prompt file (Config.Prompt.System), every *.md
// file in ContextDir, and every *.md in SkillsDir, and glues them
// together. Context/skill fragments are wrapped in XML-ish tags so
// Claude can tell them apart.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"arissa/internal/config"
)

// Build returns the composed system prompt string.
func Build(cfg *config.Config) string {
	parts := []string{}

	if base := readFile(cfg.Prompt.System); base != "" {
		parts = append(parts, base)
	} else {
		parts = append(parts, "You are a helpful infrastructure assistant that communicates via Slack.")
	}

	if ctx := loadMarkdownDir(cfg.Prompt.ContextDir, "context"); ctx != "" {
		parts = append(parts, ctx)
	}
	if skills := loadMarkdownDir(cfg.Prompt.SkillsDir, "skill"); skills != "" {
		parts = append(parts, skills)
	}

	return strings.Join(parts, "\n\n")
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func loadMarkdownDir(dir, tag string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if len(names) == 0 {
		return ""
	}

	out := make([]string, 0, len(names))
	for _, name := range names {
		body := readFile(filepath.Join(dir, name))
		if body == "" {
			continue
		}
		base := strings.TrimSuffix(name, ".md")
		out = append(out, fmt.Sprintf("<%s name=%q>\n%s\n</%s>", tag, base, body, tag))
	}
	return strings.Join(out, "\n\n")
}
