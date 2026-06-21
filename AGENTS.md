# git-mcp-memory Agent Guide

## CLI Usage

- Use JSON by default.
- Prefer `--input json --output json` for AI-driven calls.
- Do not rely on interactive prompts.
- `save` requires non-empty content. If content is missing, fail instead of opening an editor.
- Use `schema --output json` to inspect command and tool schemas.

## Safety

- Never retry saving rejected content by echoing the rejected value back to the user.
- Secrets such as API keys, private keys, bearer tokens, cookies, and session IDs are always rejected.
- Personal, organization, and customer identifiers are rejected by default unless the local config explicitly changes the category policy.
- If `search` returns `sync_failed_local_results`, surface that warning and run `retry-push` when appropriate.

## Output Control

- Search returns full content by default.
- Use `--fields` or `--snippet-chars` only when context size needs to be reduced.

## Git Behavior

- All memory writes are committed.
- Push is attempted immediately.
- If remote is ahead, use pull rebase and retry push.
- Do not delete local commits automatically.
