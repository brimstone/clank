# clank

A command-line interface (CLI) tool for interacting with models via [Ollama](https://ollama.ai).

---

## Overview

clank is a lightweight CLI utility designed to simplify the process of working with AI models. It allows users to list available models, generate responses using specific models, and customize prompts with images, system instructions, and more. This tool is ideal for developers, researchers, and enthusiasts looking to leverage AI models efficiently.

---

## Usage

```bash
clank [command]
```

Use `clank [command] --help` to explore detailed usage and flags for each command.

---

## Available Commands

- **completion**: Generate autocompletion scripts for supported shells (e.g., bash, zsh).
- **help**: Display help information for any command.
- **list**: List all models available for prompting.
- **prompt**: Prompt a model with user-defined instructions and parameters.
- **version**: Display the current version of the tool.

---

## Flags

### General Flags

| Flag | Description |
|------|-------------|
| -h, --help | Display help information for the current command. |
| --debug | Show messages meant for debugging |

---

## Prompt Command Flags

```bash
clank prompt [flags]
```

| Flag | Description |
|------|-------------|
| `-i, --image` | Images to include in the user prompt (accepts multiple values). |
| `-m, --model` | Model to use for prompting. |
| `--prefix` | Required prefix for the model's response. |
| `-s, --system` | System prompt to guide the model's behavior. |
| `-t, --template` | Template for system and user prompts (custom formatting). |
| `--tool` | URL or command for MCP tool (e.g., `http://...` for streaming, `sse+http://...` for Server-Sent Events). |
| `--unload` | Unload the model immediately after generation. |
| `-u, --user` | User prompt to send to the model. |

---

## Examples

### List Available Models

```bash
clank list
```

### Prompt a Model

```bash
clank prompt "What is the capital of France?"
```

### Prompt with System Instructions

```bash
clank prompt -m llama2 -s "You are a helpful assistant" -u "Explain quantum computing in simple terms."
```

### Prompt with Image

```bash
clank prompt -m llama2 -i image1.png "Describe the content of the image."
```

---

## Contributing

Contributions are welcome! Please open an issue or submit a pull request on [GitHub](https://github.com/brimstone/clank) for any improvements, bug fixes, or feature requests.

---

## License

This project is licensed under the [MIT License](LICENSE). See the LICENSE file for details.
