package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const (
	kiloAPIURL   = "https://api.kilo.ai/api/gateway/chat/completions"
	model        = "kilo-auto/free"
	systemPrompt = `You are a git commit message generator. Analyze the provided git diff and generate a conventional commit message following these rules:

1. Use the format: <type>(<scope>): <description>
   Types: feat, fix, docs, style, refactor, test, chore
   Scope: optional component/module name
   Description: concise imperative mood description

2. Keep the subject line under 72 characters.
3. Only output the commit message. No explanations, no backticks, no formatting.
4. If the changes are minor or unclear, use 'chore' or 'docs' as appropriate.

Examples:
- feat(auth): add JWT token validation
- fix(api): correct nil pointer in user handler
- docs(readme): update installation instructions
- chore: update dependencies`
	maxTokens    = 100
	temperature  = 0.5
)

type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []Message     `json:"messages"`
	MaxTokens int          `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func main() {
	// 1. Verify KILO_API_KEY
	apiKey := os.Getenv("KILO_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: KILO_API_KEY environment variable is not set")
		os.Exit(1)
	}

	// 2. Stage all changes
	fmt.Println("Staging all changes...")
	if err := runGitCmd("git", "add", "."); err != nil {
		fmt.Fprintf(os.Stderr, "Error staging changes: %v\n", err)
		os.Exit(1)
	}

	// 3. Get staged diff
	diff, err := getStagedDiff()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting staged diff: %v\n", err)
		os.Exit(1)
	}

	if strings.TrimSpace(diff) == "" {
		fmt.Println("No staged changes to commit.")
		os.Exit(0)
	}

	// 4. Generate commit message via Kilo API
	fmt.Println("Generating commit message...")
	commitMsg, err := generateCommitMessage(apiKey, diff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating commit message: %v\n", err)
		fmt.Println("Falling back to manual entry...")
		commitMsg = promptForCommitMessage()
	}

	// 5. Prompt user to accept/edit
	commitMsg = promptCommitConfirmation(commitMsg)
	if commitMsg == "" {
		fmt.Println("Commit cancelled.")
		os.Exit(0)
	}

	// 6. Execute commit
	fmt.Printf("Committing with message: %s\n", commitMsg)
	if err := runGitCmd("git", "commit", "-m", commitMsg); err != nil {
		fmt.Fprintf(os.Stderr, "Error committing: %v\n", err)
		os.Exit(1)
	}

	// 7. Ask about push
	if shouldPush() {
		if err := runGitCmd("git", "push"); err != nil {
			fmt.Fprintf(os.Stderr, "Error pushing: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Pushed successfully.")

		// 8. Ask about tags
		if shouldPushTags() {
			if err := runGitCmd("git", "push", "--tags"); err != nil {
				fmt.Fprintf(os.Stderr, "Error pushing tags: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Tags pushed successfully.")
		}
	} else {
		fmt.Println("Changes committed locally. Use 'git push' to push to remote.")
	}
}

func runGitCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func getStagedDiff() (string, error) {
	cmd := exec.Command("git", "diff", "--cached")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

func generateCommitMessage(apiKey, diff string) (string, error) {
	reqBody := ChatCompletionRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Temperature: temperature,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: diff},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", kiloAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from API")
	}

	msg := strings.TrimSpace(result.Choices[0].Message.Content)
	// Remove backticks if present
	msg = strings.Trim(msg, "`")
	// Take first line only (commit messages should be single-line)
	if idx := strings.Index(msg, "\n"); idx != -1 {
		msg = strings.TrimSpace(msg[:idx])
	}
	return msg, nil
}

func promptCommitConfirmation(message string) string {
	fmt.Printf("\nGenerated commit message:\n  %s\n\n", message)
	for {
		fmt.Print("Accept this message? (y/n/edit): ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			return ""
		}
		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "y":
			return message
		case "n":
			return ""
		case "edit":
			return editCommitMessage(message)
		default:
			fmt.Println("Please enter y, n, or edit.")
		}
	}
}

func editCommitMessage(current string) string {
	fmt.Printf("Current: %s\nEnter new message: ", current)
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		return ""
	}
	return strings.TrimSpace(input)
}

func promptForCommitMessage() string {
	fmt.Println("Enter commit message manually:")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
	return strings.TrimSpace(input)
}

func shouldPush() bool {
	fmt.Print("Push to remote? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}

func shouldPushTags() bool {
	fmt.Print("Push tags too? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}
