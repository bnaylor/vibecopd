package evaluator

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// InitializationPrompt is passed to the agent to generate a Guardian prompt.
const InitializationPrompt = `You are generating a system prompt for VibeCop, a lightweight AI that reviews
tool-use requests from a coding agent in real time and decides whether to approve,
deny, or escalate them to a human.

Your task: analyze this project and produce a VibeCop system prompt that will
help it make accurate, conservative decisions about what tool use is normal and
expected here.

To do this:
1. Read the project root: look for README.md, CLAUDE.md, AGENTS.md, package.json,
   Package.swift, Cargo.toml, pyproject.toml, go.mod, or any other top-level
   config that reveals the tech stack and project purpose.
2. Note the language(s), build tools, test frameworks, and any external services
   or APIs the project uses.
3. Note any existing agent configuration (hooks, permissions) that hints at what
   kinds of tool use are already expected.

CRITICAL: Always allow the project's own build toolchain (e.g., go, swift,
cargo, npm) even in early/empty project states — the first thing after init is
building the project itself.

Then write a system prompt for VibeCop. The prompt must:
- Explain VibeCop's role: second-opinion AI for tool-use approvals, no shared
  context with the primary agent, conservative by design.
- Describe this specific project: what it is, its tech stack, its build/test
  workflow, and what kinds of commands are routine.
- Give examples of what should be approved automatically for this project.
- Give examples of what should trigger escalation or denial.
- Specify the response format VibeCop must always use:
    { "verdict": "approve" | "deny" | "escalate", "reason": "..." }
- Instruct VibeCop: when in doubt, escalate rather than deny; never approve
  operations that touch files or network resources clearly outside the project.

CRITICAL: Your output will be saved verbatim as the VibeCop system prompt file.
Start with "You are VibeCop" as the very first line. Do NOT include any
introductory text, commentary, meta-commentary (e.g. "Now I have enough context"),
markdown fences, or closing remarks. Output ONLY the system prompt text,
beginning immediately with its first line and ending after its last.
No preamble of any kind.`

// GeneratePrompt runs the specified agent to generate a Guardian prompt.
// extraContext is appended to the initialization prompt (used by refine).
func GeneratePrompt(harness, extraContext string) (string, error) {
	prompt := InitializationPrompt
	if extraContext != "" {
		prompt += "\n\n" + extraContext
	}

	switch harness {
	case HarnessClaude:
		return runClaude(prompt)
	case HarnessGemini:
		return runGemini(prompt)
	default:
		return "", fmt.Errorf("unsupported harness: %s", harness)
	}
}

func runClaude(prompt string) (string, error) {
	cmd := exec.Command("claude", "-p", prompt, "--output-format", "text")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("claude produced no output")
	}
	return out, nil
}

func runGemini(prompt string) (string, error) {
	cmd := exec.Command("gemini", "-p", prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gemini: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("gemini produced no output")
	}
	return out, nil
}

// RefineContext builds extra context for the refine flow from the current
// system prompt and recent activity entries.
func RefineContext(currentPrompt, activityData string) string {
	var b strings.Builder
	b.WriteString("Current system prompt:\n\n")
	b.WriteString(currentPrompt)
	b.WriteString("\n\n")
	b.WriteString("Recent activity (tool-use verdicts from this project):\n\n")
	if activityData == "" {
		b.WriteString("(no recent activity)\n")
	} else {
		b.WriteString(activityData)
	}
	b.WriteString("\n\n")
	b.WriteString("Please review and improve the system prompt above based on this ")
	b.WriteString("activity. Keep what works, fix what doesn't, and output the ")
	b.WriteString("entire revised prompt following the same format rules as before.")
	return b.String()
}
