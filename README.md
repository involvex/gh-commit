# gh-commit

AI-powered git commit automation for GitHub CLI. One command to stage, generate commit message, commit, and optionally push.

## Features

- **Stage all changes**: Automatically runs `git add .` to stage all modifications
- **AI-generated messages**: Uses Kilo AI Gateway (`kilo-auto/free` model) to generate conventional commit messages
- **User confirmation**: Review and optionally edit the generated message before committing
- **Smart push workflow**: Prompts to push commits and tags after successful commit
- **Manual fallback**: If AI generation fails, you can type a commit message manually

## Prerequisites

- **Git**: Installed and available in PATH
- **GitHub CLI** (`gh`): Installed and authenticated
- **Kilo API Key**: Set as environment variable `KILO_API_KEY`
- **Go 1.26+** (for building from source)

## Installation

### Option 1: Install from GitHub (recommended)

```bash
gh extension install involvex/gh-commit
```

### Option 2: Build and install locally

```bash
git clone https://github.com/involvex/gh-commit.git
cd gh-commit
go build -o gh-commit .
gh extension install . --force
```

### Option 3: Pre-compiled binary

Download the latest release from the [Releases page](https://github.com/involvex/gh-commit/releases) and run:

```bash
gh extension install ./gh-commit.exe
```

## Configuration

### Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `KILO_API_KEY` | Your Kilo AI API key | **Yes** |

Get your API key from [Kilo AI Dashboard](https://kilo.ai/dashboard/api-keys).

### Optional: Shell Alias

Create an alias for faster access:

```bash
# Bash/Zsh
alias ghc='gh commit'

# PowerShell
Set-Alias ghc gh commit
```

## Usage

```bash
gh commit
```

Aliased (if you set `ghc`):
```bash
ghc
```

### Workflow

1. **Staging**: Extension runs `git add .` automatically
2. **Diff analysis**: Captures staged changes and sends to Kilo AI
3. **Message generation**: AI generates a conventional commit message
4. **Confirmation**: You see the message and choose:
   - `y` (press y) - Accept and commit
   - `n` (press n) - Cancel
   - `e` (press e) - Edit the message before committing
5. **Commit**: Creates the commit with the final message
6. **Push prompt**: "Push to remote? (y/n)"
   - `y` - Runs `git push`
   - `n` - Commits locally only
7. **Tags prompt**: (if pushing) "Push tags too? (y/n)"
   - `y` - Runs `git push --tags`
   - `n` - Pushes commits only

## Examples

### Standard workflow

```bash
$ gh commit
Staging all changes...
Generating commit message...

Generated commit message:
  feat(api): add user authentication endpoint

Accept this message? (y/n/edit): y
Commit created.
Push to remote? (y/n): y
Push tags too? (y/n): n
Pushed successfully.
```

### Manual edit mode

```bash
Accept this message? (y/n/edit): e
Current: feat(api): add user authentication endpoint
Enter new message: feat(auth): implement JWT login
```

### API failure fallback

```bash
Generating commit message...
Error generating commit message: API error 401: Unauthorized
Falling back to manual entry...
Enter commit message manually:
```

## How It Works

### Commit Message Generation

The extension uses the `kilo-auto/free` model via the Kilo AI Gateway. The system prompt enforces [Conventional Commits](https://www.conventionalcommits.org/) format with:

- **Type**: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`
- **Scope**: Optional component/module name in parentheses
- **Description**: Imperative mood, under 72 characters

### Git Operations

All git commands execute with output forwarded to your terminal. If any command fails, the extension exits with an error code and a descriptive message.

### Error Handling

- **Missing `KILO_API_KEY`**: Extension exits with error before any git operations
- **No staged changes**: Graceful exit (no commit created)
- **API failure**: Falls back to manual message entry
- **Git command errors**: Exit with non-zero status and stderr output

## Development

### Project Structure

```
gh-commit/
├── main.go          # Core logic
├── go.mod           # Go dependencies
├── go.sum           # Dependency checksums
├── .github/
│   └── workflows/
│       └── release.yml  # Auto-release on tag push
└── README.md        # This file
```

### Building

```bash
go build -o gh-commit .
```

### Testing locally

```bash
# In a test repository with KILO_API_KEY set:
.\gh-commit.exe
```

### Releasing

Tag a release and the workflow will build and publish the extension automatically:

```bash
git tag v1.0.0
git push origin v1.0.0
```

See [GitHub CLI extension docs](https://cli.github.com/manual/gh_extension_create) for more.

## Troubleshooting

### "KILO_API_KEY is not set"

Make sure the environment variable is defined in your shell:

```bash
# PowerShell
$env:KILO_API_KEY = "your-key-here"

# Bash
export KILO_API_KEY="your-key-here"
```

### "No staged changes to commit"

All changes are staged automatically via `git add .`. If you see this, your working directory is already clean.

### Slow performance

Network latency to Kilo API may vary. If generation takes >5 seconds, check your internet connection.

### Commit message not following convention

The AI generally produces good messages, but you can always edit via the `edit` option. Consider opening an issue with your diff if consistently poor results occur.

## License

MIT License - see LICENSE file for details.

## Contributing

Contributions welcome! Please open an issue or PR at https://github.com/involvex/gh-commit.
