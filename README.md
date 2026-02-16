# AI Agent CLI

A terminal-based AI coding agent built in Go. It connects to an LLM via OpenRouter and can read files, write files, and execute shell commands.

## Prerequisites

- Go 1.25+
- An [OpenRouter](https://openrouter.ai/) API key

## Setup

Build the binary:

```sh
go build -o agent app/*.go
```

## Configuration

The agent needs an `OPENROUTER_API_KEY`. It resolves the key in this order:

1. **Environment variable** — `export OPENROUTER_API_KEY=your-key`
2. **Config file** — `~/.config/agent/config.json`
3. **Interactive prompt** — if neither is set, it asks on first run and saves to the config file

You can also override the base URL with `OPENROUTER_BASE_URL` (defaults to `https://openrouter.ai/api/v1`).

## Usage

### Interactive mode (REPL)

```sh
./agent
```

This starts a chat session where you can type prompts and the agent maintains conversation history across turns. Type `exit`, `quit`, or press Ctrl+D to leave.

### One-shot mode

```sh
./agent -p "explain what main.go does"
```

Sends a single prompt, prints the response, and exits.

## Tools

The agent has access to three tools it can call autonomously:

| Tool | Description |
|------|-------------|
| `read_file` | Read the contents of a file |
| `write_file` | Write content to a file |
| `bash` | Execute a shell command |
