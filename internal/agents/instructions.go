package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/archyl-com/cli/internal/repo"
)

const (
	blockStart = "<!-- archyl:start -->"
	blockEnd   = "<!-- archyl:end -->"
)

// Instructions is the block written to CLAUDE.md and AGENTS.md: it tells a
// coding agent how to use the archyl CLI from its shell. Kept short — every
// line of it is paid in context on every turn.
const Instructions = blockStart + `
## Architecture (Archyl)

This repository's architecture is documented in Archyl. Use the ` + "`archyl`" + ` CLI from the shell:

- Before a non-trivial change: ` + "`archyl plan \"<task>\"`" + ` for a plan grounded in the architecture.
- Before editing files: ` + "`archyl brief <files>`" + ` — the elements they belong to, what depends on them, the decisions and known pitfalls.
- What a change affects and who owns it: ` + "`archyl impact`" + `.
- Before finishing: ` + "`archyl check`" + ` (architecture rules) and ` + "`archyl radar check`" + ` (no dependency on hold).
- Record what the code cannot show: ` + "`archyl remember \"<fact>\" --element \"<name>\" --kind pitfall|convention|note`" + `.
- Look up past knowledge: ` + "`archyl recall \"<topic>\"`" + `.

Commands print compact markdown when run from a shell; add ` + "`--json`" + ` for structured output.
` + blockEnd + "\n"

// InstallInstructions writes or refreshes the archyl block in a markdown
// file, leaving the rest of the file as it is.
func InstallInstructions(path string) (Change, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, err
	}
	content := string(raw)
	action := "updated"

	start, end := strings.Index(content, blockStart), strings.Index(content, blockEnd)
	switch {
	case raw == nil:
		content = Instructions
		action = "created"
	case start >= 0 && end > start:
		end += len(blockEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		if content[start:end] == Instructions {
			return Change{Path: path, Action: "unchanged"}, nil
		}
		content = content[:start] + Instructions + content[end:]
	default:
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + Instructions
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Change{}, err
	}
	if err := repo.RefuseLink(path); err != nil {
		return Change{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Change{}, err
	}
	return Change{Path: path, Action: action}, nil
}

// StatuslineCommand is the status line archyl installs in Claude Code.
const StatuslineCommand = "archyl statusline"

// InstallStatusline sets archyl as the repository's Claude Code status line,
// unless one is already configured.
func InstallStatusline(env Env) (Change, error) {
	path := filepath.Join(env.Root, ".claude", "settings.json")
	doc, err := readJSON(path)
	if err != nil {
		return Change{}, err
	}
	created := doc == nil
	if doc == nil {
		doc = map[string]any{}
	}
	if existing, ok := doc["statusLine"].(map[string]any); ok {
		if existing["command"] == StatuslineCommand {
			return Change{Path: path, Action: "unchanged"}, nil
		}
		return Change{Path: path, Action: "kept (a status line is already set)"}, nil
	}
	doc["statusLine"] = map[string]any{"type": "command", "command": StatuslineCommand}
	if err := writeJSON(path, doc); err != nil {
		return Change{}, err
	}
	if created {
		return Change{Path: path, Action: "created"}, nil
	}
	return Change{Path: path, Action: "updated"}, nil
}
