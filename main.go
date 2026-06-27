package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"golang.org/x/term"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const (
	kiloAPIURL   = "https://api.kilo.ai/api/gateway/chat/completions"
	model        = "kilo-auto/free"
	systemPrompt = `You are a git commit message expert. Analyze the CHANGED FILES SUMMARY and FULL DIFF provided by the user.

RULES:
1. Analyze ALL changed files to understand the FULL scope of changes
2. Format: type(scope): description
3. Types: feat, fix, docs, refactor, test, chore, perf, build, ci, style
4. Scope: use the most relevant component/module name
5. Description: imperative mood, specific to what changed, no trailing punctuation
6. Output ONLY the commit message - no explanations, no backticks

FORBIDDEN:
- NEVER mention line counts (e.g. "changed 20 lines", "modified 5 lines")
- NEVER mention file counts (e.g. "changed 3 files")
- NEVER describe changes by listing file paths or filenames

Guidelines:
- If many files change the same feature, describe that feature
- If changes span multiple areas, use a broader scope or focus on the primary change
- NEVER output generic messages like "update files" or "changed X files"
- Focus on the logical change or intent, not the files modified
- If you cannot determine the intent, use "chore: minor changes" instead of listing files`
	maxTokens   = 1024
	temperature = 0.3
)

type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func readChar() (string, error) {
	// Try to put terminal in raw mode for immediate character reading
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			// Fall back to line-based input if raw mode fails
			return readLine()
		}
		defer term.Restore(fd, oldState)

		var b [1]byte
		if _, err := os.Stdin.Read(b[:]); err != nil {
			return "", err
		}
		return string(b[:]), nil
	}
	// Fallback for non-terminal stdin
	return readLine()
}

func readLine() (string, error) {
	// Check if stdin is a terminal to give better error messages
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("stdin is not a terminal; interactive input required")
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			// EOF indicates input was closed (e.g., Ctrl+Z on Windows) - treat as empty/cancel
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
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
	diff, diffSummary, err := getStagedDiff()
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
	commitMsg, err := generateCommitMessage(apiKey, diffSummary, diff)
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

// getStagedDiff returns the staged diff, a summary of changed files, and file status
func getStagedDiff() (diff string, summary string, err error) {
	// Get the full diff
	diffCmd := exec.Command("git", "diff", "--cached")
	var diffOut bytes.Buffer
	diffCmd.Stdout = &diffOut
	err = diffCmd.Run()
	diff = diffOut.String()
	if err != nil {
		return
	}

	// Get a summary of changed files
	summaryCmd := exec.Command("git", "diff", "--cached", "--stat")
	var summaryOut bytes.Buffer
	summaryCmd.Stdout = &summaryOut
	summaryCmd.Run()
	summary = summaryOut.String()

	// Get file status codes (A/M/D/R)
	statusCmd := exec.Command("git", "status", "--short", "--branch")
	var statusOut bytes.Buffer
	statusCmd.Stdout = &statusOut
	if err := statusCmd.Run(); err == nil {
		status := statusOut.String()
		if status != "" {
			summary = "STAGED FILES:\n" + status + "\n\n" + summary
		}
	}

	return
}

func generateCommitMessage(apiKey, diffSummary, diff string) (string, error) {
	// First, try to get a message from the Kilo API
	// Send summary first, then full diff for context
	userContent := fmt.Sprintf("CHANGED FILES SUMMARY:\n%s\n\nFULL DIFF:\n%s", diffSummary, diff)

	// Helper function to call the API
	callAPI := func(messages []Message) (string, string, error) {
		reqBody := ChatCompletionRequest{
			Model:       model,
			MaxTokens:   maxTokens,
			Temperature: temperature,
			Messages:    messages,
		}

		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return "", "", fmt.Errorf("marshal request: %w", err)
		}

		req, err := http.NewRequest("POST", kiloAPIURL, bytes.NewBuffer(jsonData))
		if err != nil {
			return "", "", fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("send request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusTooManyRequests || strings.Contains(string(body), "429") {
				fmt.Fprintln(os.Stderr, "Rate limit exceeded. Try again in a minute.")
			}
			return "", "", fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(body))
		}

		var result ChatCompletionResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", "", fmt.Errorf("decode response: %w", err)
		}

		if len(result.Choices) == 0 {
			return "", "", fmt.Errorf("no choices returned from API")
		}

		return result.Choices[0].Message.Content, result.Choices[0].Message.Reasoning, nil
	}

	// Try to get message from API (up to 2 attempts)
	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	var msg string
	var apiErr error

	for attempt := 0; attempt < 2; attempt++ {
		content, reasoning, err := callAPI(messages)
		if err != nil {
			apiErr = err
			break
		}

		// Extract message using existing logic
		msg = extractCommitMessage(content, reasoning)

		// Clean up the message
		msg = cleanCommitMessage(msg)

		// Reject generic messages
		if strings.Contains(strings.ToLower(msg), "miscellaneous") {
			apiErr = fmt.Errorf("AI returned generic message containing 'miscellaneous'")
			break
		}

		// Validate conventional commit format
		if !isValidConventionalCommit(msg) {
			if extracted := extractConventionalCommit(msg); extracted != "" {
				msg = extracted
			} else {
				apiErr = fmt.Errorf("AI response does not match conventional commit format")
				break
			}
		}

		// Check for line count patterns
		lineCountPattern := regexp.MustCompile(`(?i)(changed|modified|updated|added|removed)?\s*\d+\s*lines?\s*$`)
		if lineCountPattern.MatchString(strings.ToLower(msg)) {
			if attempt == 0 {
				messages = append(messages, Message{
					Role:    "user",
					Content: "Your message contains a line count (e.g., 'changed 20 lines'). This is not allowed. Describe the PURPOSE or INTENT of the changes, not how many lines were modified. Example: 'feat: add user authentication' instead of 'changed 50 lines'.",
				})
				continue
			}
			apiErr = fmt.Errorf("AI returned line-count message after retry")
			break
		}

		// Check for generic/file-path messages
		if isMessageTooGeneric(msg) {
			if attempt == 0 {
				// Retry with feedback
				messages = append(messages, Message{
					Role:    "user",
					Content: "Your previous message described files instead of intent. Generate a message that explains the *purpose* or *intent* of the changes, not which files were modified. Focus on what was accomplished.",
				})
				continue
			}
			apiErr = fmt.Errorf("AI returned file-focused message after retry")
			break
		}

		return msg, nil
	}

	return "", apiErr
}

// extractCommitMessage extracts a commit message from API response content
func extractCommitMessage(content, reasoning string) string {
	msg := strings.TrimSpace(content)

	// If content is empty, try to extract from reasoning
	if msg == "" && reasoning != "" {
		if extracted := extractConventionalCommit(reasoning); extracted != "" {
			return extracted
		}

		lines := strings.Split(reasoning, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.Contains(strings.ToLower(line), "commit") {
				if extracted := extractConventionalCommit(line); extracted != "" {
					return extracted
				}
			}
		}

		lines = strings.Split(reasoning, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "feat") || strings.HasPrefix(lower, "fix") ||
				strings.HasPrefix(lower, "docs") || strings.HasPrefix(lower, "style") ||
				strings.HasPrefix(lower, "refactor") || strings.HasPrefix(lower, "test") ||
				strings.HasPrefix(lower, "chore") || strings.HasPrefix(lower, "perf") ||
				strings.HasPrefix(lower, "build") || strings.HasPrefix(lower, "ci") {
				if strings.Contains(line, ":") {
					return cleanCommitMessage(line)
				}
			}
		}
	}

	return msg
}

// isMessageTooGeneric detects commit messages that are overly file-focused or lack semantic meaning
func isMessageTooGeneric(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))

	// Check for line count patterns like "changed 20 lines" or "modified 5 lines"
	lineCountPattern := regexp.MustCompile(`(?i)(changed|modified|updated|added|removed)?\s*\d+\s*lines?\s*$`)
	if lineCountPattern.MatchString(lower) {
		return true
	}

	// Check for file path patterns at end or throughout description
	// Detects patterns like "add .kilo/plans/file.md" or "update README.md"
	filePathPattern := regexp.MustCompile(`(^|\s)(add|update|remove|delete|modify)\s+(\.?[a-z0-9_\-./]+\.[a-z]{1,4})(\s|$|\n)`)
	if filePathPattern.MatchString(lower) {
		return true
	}

	// Check for directory-style path patterns (contains slashes and looks like a file path)
	// Matches things like ".kilo/plans/..." or "src/main.go"
	if matches := regexp.MustCompile(`(^|\s)[\.a-z0-9_\-]+/[\.a-z0-9_\-]+`).FindAllString(lower, -1); len(matches) > 0 {
		// Count how many matched patterns look like file paths
		pathLikeCount := 0
		for _, m := range matches {
			m = strings.TrimSpace(m)
			if strings.Contains(m, ".") || strings.Count(m, "/") >= 1 {
				pathLikeCount++
			}
		}
		if pathLikeCount >= 1 {
			// If description is mostly path-like, it's too generic
			words := strings.Fields(lower)
			if len(words) > 0 && pathLikeCount >= len(words)/2 {
				return true
			}
		}
	}

	// Check for simple file operations without meaningful description
	// e.g., "chore: add file", "feat: update main.go"
	if strings.Contains(lower, "add ") && regexp.MustCompile(`add\s+[a-z0-9_\-./]+\.[a-z]{2,4}\s*$`).MatchString(lower) {
		return true
	}
	if strings.Contains(lower, "update ") && regexp.MustCompile(`update\s+[a-z0-9_\-./]+\.[a-z]{2,4}\s*$`).MatchString(lower) {
		return true
	}
	if strings.Contains(lower, "remove ") && regexp.MustCompile(`remove\s+[a-z0-9_\-./]+\.[a-z]{2,4}\s*$`).MatchString(lower) {
		return true
	}

	return false
}

// cleanCommitMessage removes formatting artifacts and trailing noise
func cleanCommitMessage(msg string) string {
	// Remove backticks and markdown fences
	msg = strings.Trim(msg, "`")
	msg = strings.TrimPrefix(msg, "```")
	msg = strings.TrimSuffix(msg, "```")

	// Take first line only
	if idx := strings.Index(msg, "\n"); idx != -1 {
		msg = strings.TrimSpace(msg[:idx])
	}

	// Truncate at first occurrence of common trailing noise
	for _, sep := range []string{",", ".", "!", "?", " and ", " or ", " so ", " thus ", " therefore "} {
		if idx := strings.Index(msg, sep); idx != -1 {
			msg = strings.TrimSpace(msg[:idx])
		}
	}

	// Remove trailing file paths from description
	// Match file paths with extensions that appear at the end
	pathPattern := regexp.MustCompile(`(\s+[a-z]:[\\/])?[\\/]?((\.\/|\.\.\/|[a-z0-9_\-]+)[\\/])*([a-z0-9_\-]+\.[a-z]{2,4})\s*$`)
	if pathPattern.MatchString(msg) {
		msg = strings.TrimSpace(pathPattern.ReplaceAllString(msg, ""))
	}

	// Remove trailing line count patterns
	lineCountPattern := regexp.MustCompile(`(?i)\s*[-–—]\s*(changed|modified|updated|added|removed)?\s*\d+\s*lines?\s*$`)
	if lineCountPattern.MatchString(msg) {
		msg = strings.TrimSpace(lineCountPattern.ReplaceAllString(msg, ""))
	}

	// Keep under 72 characters
	if len(msg) > 72 {
		msg = msg[:72]
	}
	return strings.TrimSpace(msg)
}

// isValidConventionalCommit checks if the message matches conventional commit format exactly
func isValidConventionalCommit(msg string) bool {
	// Pattern: type(scope)?: description (non-empty description required)
	validTypes := `feat|fix|docs|style|refactor|test|chore|perf|build|ci`
	// Allow any non-empty characters in scope (if present) and any non-newline chars in description
	pattern := `^(` + validTypes + `)(\([^)]+\))?:\s*.+$`

	re := regexp.MustCompile(pattern)
	return re.MatchString(strings.ToLower(msg))
}

// extractConventionalCommit searches for a conventional commit pattern within text
func extractConventionalCommit(text string) string {
	// Split into lines and check each
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && isValidConventionalCommit(line) {
			// Clean it up further
			return cleanCommitMessage(line)
		}
	}

	// If no full line matches, try to extract using regex from the whole text
	validTypes := `feat|fix|docs|style|refactor|test|chore|perf|build|ci`
	pattern := `(` + validTypes + `)(\([^)]+\))?:\s*.+`
	re := regexp.MustCompile(pattern)
	if match := re.FindString(strings.ToLower(text)); match != "" {
		// Clean up and return
		return cleanCommitMessage(match)
	}

	return ""
}

func promptCommitConfirmation(message string) string {
	fmt.Printf("\nGenerated commit message:\n  %s\n\n", message)

	for {
		fmt.Print("Accept this message? (y/n/edit): ")
		input, err := readChar()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading input:", err)
			return ""
		}
		input = strings.ToLower(input)
		switch input {
		case "y":
			return message
		case "n":
			return ""
		case "e":
			return editCommitMessage(message)
		default:
			fmt.Println("Please enter 'y', 'n', or 'e' (for edit).")
		}
	}
}

func editCommitMessage(current string) string {
	fmt.Printf("Current: %s\nEnter new message: ", current)
	input, err := readLine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		return ""
	}
	return input
}

func promptForCommitMessage() string {
	fmt.Println("Enter commit message manually:")
	input, err := readLine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
	return input
}

func shouldPush() bool {
	fmt.Print("Push to remote? (y/n): ")
	input, err := readChar()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading input:", err)
		return false
	}
	return strings.ToLower(input) == "y"
}

func shouldPushTags() bool {
	fmt.Print("Push tags too? (y/n): ")
	input, err := readChar()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading input:", err)
		return false
	}
	return strings.ToLower(input) == "y"
}
