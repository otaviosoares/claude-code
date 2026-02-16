package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type config struct {
	APIKey string `json:"api_key"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "agent", "config.json")
}

func loadConfig() config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return config{}
	}
	var cfg config
	json.Unmarshal(data, &cfg)
	return cfg
}

func saveConfig(cfg config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

type spinner struct {
	stop chan struct{}
	done chan struct{}
}

func startSpinner(label string) *spinner {
	s := &spinner{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	go func() {
		defer close(s.done)
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			default:
				fmt.Fprintf(os.Stderr, "\r%s %s", frames[i%len(frames)], label)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return s
}

func (s *spinner) Stop() {
	close(s.stop)
	<-s.done
}

func getTools() []openai.ChatCompletionToolUnionParam {
	return []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        "read_file",
			Description: openai.String("Read and return the contents of a file"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read",
					},
				},
				"required": []string{"file_path"},
			},
		}),
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        "write_file",
			Description: openai.String("Write content to a file"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "The path to the file to write to",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file",
					},
				},
				"required": []string{"file_path", "content"},
			},
		}),
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        "bash",
			Description: openai.String("Execute a shell command"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The command to execute",
					},
				},
				"required": []string{"command"},
			},
		}),
	}
}

func handleToolCall(toolCall openai.ChatCompletionMessageToolCallUnion, messages []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	messages = append(messages, openai.ChatCompletionMessageParamUnion{
		OfAssistant: &openai.ChatCompletionAssistantMessageParam{
			ToolCalls: []openai.ChatCompletionMessageToolCallUnionParam{
				toolCall.ToParam(),
			},
		},
	})

	var arguments map[string]any
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &arguments); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing arguments: %v\n", err)
		return messages
	}

	var result string

	switch toolCall.Function.Name {
	case "read_file":
		filePath := arguments["file_path"].(string)
		fmt.Fprintf(os.Stderr, "Reading file: %s\n", filePath)
		contents, err := os.ReadFile(filePath)
		if err != nil {
			result = fmt.Sprintf("error reading file: %v", err)
		} else {
			result = string(contents)
		}

	case "write_file":
		filePath := arguments["file_path"].(string)
		content := arguments["content"].(string)
		fmt.Fprintf(os.Stderr, "Writing file: %s\n", filePath)
		err := os.WriteFile(filePath, []byte(content), 0644)
		if err != nil {
			result = fmt.Sprintf("error writing file: %v", err)
		} else {
			result = "File written successfully"
		}

	case "bash":
		command := arguments["command"].(string)
		fmt.Fprintf(os.Stderr, "Executing: %s\n", command)
		cmd := exec.Command("sh", "-c", command)
		output, err := cmd.CombinedOutput()
		if err != nil {
			result = fmt.Sprintf("error: %v\noutput: %s", err, string(output))
		} else {
			result = string(output)
		}
	}

	messages = append(messages, openai.ChatCompletionMessageParamUnion{
		OfTool: &openai.ChatCompletionToolMessageParam{
			ToolCallID: toolCall.ID,
			Content: openai.ChatCompletionToolMessageParamContentUnion{
				OfString: openai.String(result),
			},
		},
	})

	return messages
}

func runAgentLoop(client openai.Client, messages []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	tools := getTools()

	for {
		sp := startSpinner("Thinking...")
		resp, err := client.Chat.Completions.New(context.Background(),
			openai.ChatCompletionNewParams{
				Model:    "anthropic/claude-haiku-4.5",
				Tools:    tools,
				Messages: messages,
			},
		)
		sp.Stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return messages
		}
		if len(resp.Choices) == 0 {
			fmt.Fprintln(os.Stderr, "No choices in response")
			return messages
		}

		if resp.Choices[0].Message.ToolCalls == nil {
			fmt.Println(resp.Choices[0].Message.Content)
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.String(resp.Choices[0].Message.Content),
					},
				},
			})
			return messages
		}

		for _, toolCall := range resp.Choices[0].Message.ToolCalls {
			messages = handleToolCall(toolCall, messages)
		}
	}
}

func main() {
	var prompt string
	flag.StringVar(&prompt, "p", "", "Prompt to send to LLM (omit for interactive mode)")
	flag.Parse()

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	baseUrl := os.Getenv("OPENROUTER_BASE_URL")
	if baseUrl == "" {
		baseUrl = "https://openrouter.ai/api/v1"
	}

	if apiKey == "" {
		cfg := loadConfig()
		apiKey = cfg.APIKey
	}

	if apiKey == "" {
		fmt.Fprint(os.Stderr, "OPENROUTER_API_KEY not set. Enter API key: ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr, "No API key provided")
			os.Exit(1)
		}
		apiKey = strings.TrimSpace(scanner.Text())
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "No API key provided")
			os.Exit(1)
		}
		if err := saveConfig(config{APIKey: apiKey}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save config: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "API key saved to %s\n", configPath())
		}
	}

	client := openai.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(baseUrl))

	if prompt != "" {
		messages := []openai.ChatCompletionMessageParamUnion{
			{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(prompt),
					},
				},
			},
		}
		runAgentLoop(client, messages)
		return
	}

	fmt.Println("AI Agent (type 'exit' to quit)")
	fmt.Println()

	messages := []openai.ChatCompletionMessageParamUnion{}
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}

		messages = append(messages, openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(input),
				},
			},
		})

		messages = runAgentLoop(client, messages)
		fmt.Println()
	}
}
