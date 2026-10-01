# opencode-mvp — Go TUI agent like opencode (MVP)

> SUPERSEDED by [docs/design.md](../design.md). The direct OpenAI-client MVP
> (`internal/{llm,agent,tools}`) was deleted; pi is the backend now.
> Kept for history.

## Goal
Chat TUI + OpenAI-compatible LLM calls + 4 basic tools. Usable locally, not a full opencode clone.

## Out of scope (YAGNI)
- Session persistence, multi-session, diff preview, approval UI, multi-provider config, MCP, embeddings.
- Add when: explicitly requested.

## Architecture (4 files, stdlib first)
```
main.go                  # Bubble Tea TUI: viewport messages + textarea input + status
internal/llm/openai.go   # POST {baseURL}/chat/completions via net/http, no SDK
internal/tools/tools.go  # read_file, write_file, list_files, bash (os/exec, os)
internal/agent/agent.go  # loop: messages -> LLM -> tool_calls -> exec -> repeat (max 10)
```

## Tool schema (OpenAI function calling)
- `read_file {path}` — ~20KB cap, clear errors
- `write_file {path, content}` — creates parent dirs if missing
- `list_files {dir=".", limit=50}` — skips .git, build binaries
- `bash {cmd, timeout_s=30}` — runs via `sh -c`, merges stdout+stderr, blocks crude rm -rf /

## Config (env, never hardcode keys)
- `OPENAI_BASE_URL` (default `https://api.openai.com/v1`)
- `OPENAI_API_KEY` (required for real chat)
- `OPENAI_MODEL` (default `gpt-4o-mini`)

## TUI keys
- Enter send, Ctrl+C / q quit, ↑/↓ scroll when input is empty (if easy)

## Verify
- `go vet ./... && go build -o /tmp/pitago .`
- Manual run `OPENAI_API_KEY=... go run .`, try: "list files", "read main.go", "run ls"
