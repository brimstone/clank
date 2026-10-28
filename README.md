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
- **db**: Manage a single-file vector database of text documents (init, load, query, stats).
- **help**: Display help information for any command.
- **list**: List all models available for prompting.
- **mcp**: Interact with MCP servers directly (list functions or call them).
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
| `--db` | Vector db file (or directory to index in memory) to search for context prepended to the user prompt. |
| `-i, --image` | Images to include in the user prompt (accepts multiple values). |
| `-m, --model` | Model to use for prompting. |
| `--prefix` | Required prefix for the model's response. |
| `-s, --system` | System prompt to guide the model's behavior. |
| `-t, --template` | Template for system and user prompts (custom formatting). |
| `--tool` | URL or command for MCP tool (e.g., `http://...` for streaming, `sse+http://...` for Server-Sent Events). |
| `--unload` | Unload the model immediately after generation. |
| `-u, --user` | User prompt to send to the model. |

---

## MCP Command

The `mcp` command lets you interact with an MCP server directly, without prompting a model. Both the base `mcp` command and `mcp function` accept the `--tool` flag, which takes either an `http://...` URL (streamable transport), an `sse+http://...` URL (SSE transport), or a command line that launches a stdio MCP server.

### List MCP Server Functions

```bash
clank mcp --tool "$SERVER"
```

### Call an MCP Server Function

```bash
clank mcp function FUNCTION [key=value ...] --tool "$SERVER"
```

The first positional argument is the name of the function to call. Each following positional argument is passed to the function as a `key=value` pair.

The response is pretty-printed by default. Add `--raw` to print it as a single line instead, for use when piping the output to other commands.

```bash
clank mcp function FUNCTION [key=value ...] --tool "$SERVER"
clank mcp function FUNCTION [key=value ...] --tool "$SERVER" --raw
```

---

## DB Command

The `db` command manages a single-file vector database of text documents. The database stores embeddings of the text files under a directory, so you can search by meaning rather than exact words. It runs entirely offline: no network or model server is needed.

The fastest path skips the database file entirely: `clank db query ~/notes "..."` searches a directory of text documents on the fly, in memory. For results that persist between runs, `load` indexes a directory into a single-file database (created automatically if it does not exist), and `query` searches it.

### Init a Database

```bash
clank db init docs.vdb
clank db init docs.vdb --model hash --dims 512
```

The `--model` flag names the embedding function and is stored in the file:

- `glove` (default) matches text by meaning, using bundled GloVe word vectors (300 dimensions).
- `hash` matches by shared words and letter patterns; `--dims` sets its vector dimension.

Because the model is stored in the file, `query` and `stats` read it back; `load` takes `--model` and `--dims` only to create a missing database.

### Load a Directory

```bash
clank db load docs.vdb ~/notes
```

If `docs.vdb` does not exist, `load` creates it first with the `glove` model, so `init` is optional. `--model` and `--dims` name the embedding model of a database being created; an existing database keeps the model stored in it.

Every regular file under the directory is read, split into chunks of about `--chunk-size` characters (neighbouring chunks share `--overlap` characters), embedded and stored with its path and byte offsets. Files that contain binary data, are empty, or are larger than `--max-size` bytes are skipped.

| Flag | Default | Description |
|------|---------|-------------|
| `--model` | glove | Embedding model used when the database is created: glove (meaning) or hash (lexical). |
| `--dims` | 512 | Vector dimension for the hash model when the database is created. |
| `--chunk-size` | 512 | Chunk size in characters. |
| `--overlap` | 64 | Characters shared between neighbouring chunks. |
| `--ext` | all text files | File extension to index, e.g. `.md` (repeatable). |
| `--hidden` | off | Include hidden files and directories. |
| `--max-size` | 10 MiB | Skip files larger than this many bytes. |
| `--save-every` | 100 | Save the database every N files. |

Loading a document again replaces its previous chunks, so re-running `load` after editing a file keeps the database current.

### Query the Database

```bash
clank db query docs.vdb "how does retrieval work"
clank db query ~/notes "how does retrieval work"
```

TARGET is either a database file or a directory. A directory is searched without a database file: the text documents under it are indexed on the fly into a memory-only database, so init and load happen automatically and nothing is written to disk.

Prints the `--k` best matches (default 5) with their similarity, source file, chunk number and byte offsets.

### Show Statistics

```bash
clank db stats docs.vdb
```

Shows the file size, the stored model, the document and chunk counts, and per-collection storage statistics.

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

### Prompt with Document Context

`--db` searches a vector db for the passages closest in meaning to the prompt and prepends them in front of the prompt as background context. Point it at a database file, or at a directory to index in memory on the fly:

```bash
clank prompt --db notes.vdb "What did I decide about the refund policy?"
clank prompt --db ~/notes "What did I decide about the refund policy?"
```

### List an MCP Server's Functions

```bash
clank mcp --tool http://127.0.0.1:8080/mcp
```

### Call an MCP Server Function

```bash
clank mcp function add a=2 b=3 --tool http://127.0.0.1:8080/mcp
```

### Search a Directory of Documents

For a one-off search, query the directory directly; it is indexed in memory on the fly and nothing is written to disk:

```bash
clank db query ~/notes "what did I decide about the refund policy?"
```

For a database that persists between runs:

```bash
clank db load notes.vdb ~/notes
clank db query notes.vdb "what did I decide about the refund policy?"
```

---

## Contributing

Contributions are welcome! Please open an issue or submit a pull request on [GitHub](https://github.com/brimstone/clank) for any improvements, bug fixes, or feature requests.

---

## License

This project is licensed under the [MIT License](LICENSE). See the LICENSE file for details.
