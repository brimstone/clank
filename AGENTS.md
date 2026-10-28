# clank: agent guidance

Go CLI (cobra) for working with local models through [Ollama](https://ollama.ai): prompting, listing models, calling MCP tools, and a single-file vector database for searching text documents. Module `github.com/brimstone/clank`.

## Layout

- `main.go` — root cobra command; with no subcommand it defaults to `prompt`.
- `prompt/` — prompting: model selection, images, templates under `prompt/templates/`. `db.go` prepends vector db context to the user prompt when `--db` is given (a database file, or a directory indexed in memory).
- `db/` — `clank db`: a single-file vector database of text documents. `db.go` is the API (Init, Load, Query, QueryDir, Stats), `cmd.go` the cobra wiring, `db_test.go` the tests. Storage and search come from `github.com/brimstone/go-vector`; chunking and embedding from `github.com/brimstone/go-embedding`.
- `list/`, `mcp/`, `update/`, `version/` — the other subcommands.
- `utils/` — shared HTTP/TLS/logging helpers for the Ollama and MCP clients.

## Build, test, lint

```sh
make            # go build -v
go test ./...   # CI (.github/workflows/test.yml) runs exactly this
make lint       # golangci-lint v2.0.2 (pinned in the Makefile), config in .golangci.yaml
```

`make lint` currently fails on main with pre-existing issues (a `FIXME` in
utils/tools.go, an unused `slogWriter`, an `nlreturn` in db/db_test.go);
`make pre-commit` runs with godox disabled. Compare lint output against main
before treating a finding as yours.

## Rules

- Every `.go` file starts with the copyright header from `header.tmpl`
  (enforced by the goheader linter).
- A new subcommand is a package exposing `Cmd() *cobra.Command` (see
  `db/cmd.go`) registered in `main.go`.
- Command errors are logged with `slog.Error` before being returned;
  user-facing output goes to `cmd.OutOrStdout()`, per-item progress to
  `cmd.ErrOrStderr()`.
- `db` runs entirely offline: no network, no Ollama model. Keep it that way.
- `db` file format: two collections ("docs" for chunks, "config" for
  settings) in one go-vector file. The embedding model name is stored in the
  config collection and read back by load, query and stats; a missing
  database is auto-created by load and by query-of-a-directory (memory-only
  there). Format changes must keep `db_test.go` passing.
- `dist/` is goreleaser output; `make release` builds it.
