---
name: git-mcp-memory-cli
description: Use git-mcp-memory from the command line to save, search, sync, inspect, and recover Git-backed AI memories. Trigger this skill when a user asks to use the git-mcp-memory CLI, persist or retrieve agent memory without MCP tools, verify memory repository state, handle push failures, run retry-push, or script JSON-first memory operations.
---

# git-mcp-memory CLI

## Core Rules

- Prefer `/Users/towada/.local/bin/git-mcp-memory` when it exists; otherwise use `git-mcp-memory` from `PATH`.
- Use `--output json` unless the user explicitly asks for text or NDJSON.
- Do not use interactive prompts. If required input is missing, fail and report the structured error.
- Do not save secrets, API keys, private keys, tokens, personal information, customer names, or company names unless the local config explicitly permits the configurable category.
- Never repeat rejected secret values back to the user.
- Surface all `warnings` from command responses. They are part of the operational state.

## Command Selection

Use these commands for common tasks:

```bash
git-mcp-memory status --output json
git-mcp-memory schema --output json
git-mcp-memory save --workspace "$PWD" --title "Title" --content "Markdown body" --output json
git-mcp-memory save --workspace "$PWD" --title "Title" --content "Markdown body" --dry-run --output json
git-mcp-memory search "query" --workspace "$PWD" --limit 5 --output json
git-mcp-memory search "query" --all --limit 10 --output json
git-mcp-memory sync --output json
git-mcp-memory retry-push --output json
git-mcp-memory retry-push --dry-run --output json
```

Use `--input json` when constructing requests programmatically:

```bash
printf '%s\n' '{"current_workspace_path":"/path/to/project","title":"Title","content":"Markdown body"}' \
  | git-mcp-memory save --input json --output json
```

## Save Workflow

1. Run `save --dry-run --output json` when content is newly generated, long, or riskier than a small operational note.
2. Confirm the response has `"ok": true` and an `embedding_dim` greater than zero.
3. Run `save --output json` with the same title/content.
4. Check:
   - `data.pushed == true`
   - `data.indexed == true`
   - `warnings` is empty
5. If `pushed == false`, do not retry by saving again. Use `retry-push`.

## Search Workflow

Project search:

```bash
git-mcp-memory search "query" --workspace "/path/to/project" --limit 5 --output json
```

Cross-project search:

```bash
git-mcp-memory search "query" --all --limit 10 --output json
```

Reduce output only when needed:

```bash
git-mcp-memory search "query" --all --fields title,path --snippet-chars 300 --output json
```

If search returns `sync_failed_local_results`, report that results are based on local repository state and run:

```bash
git-mcp-memory retry-push --output json
```

## Push Failure Recovery

If `save` returns a warning like `push_failed`:

1. Treat the memory as locally committed but not remotely synchronized.
2. Run:

```bash
git-mcp-memory retry-push --dry-run --output json
git-mcp-memory retry-push --output json
```

3. If `retry-push` still fails, report:
   - `error_category`
   - `unpushed_commit_count`
   - whether search can continue locally
4. Do not delete local commits automatically.

## Status Checks

Use `status --output json` before or after risky operations. Important fields:

- `git_dir`
- `remote_url`
- `current_branch`
- `unpushed_commit_count`
- `dirty_working_tree`
- `indexed_document_count`
- `failed_embedding_count`

The healthy baseline is:

```json
{
  "unpushed_commit_count": 0,
  "dirty_working_tree": false,
  "failed_embedding_count": 0
}
```

## Expected Failure Behavior

- Empty query: `validation_failed` on `query`.
- Empty save content: command-level `content is required`.
- Secret or private content: `content_rejected_by_security_policy`.
- Push network/auth failure: local commit remains, `pushed:false`, `push_failed` warning.
- Search during push failure: returns local results when possible, with `sync_failed_local_results`.

## Reporting

When reporting results to the user, include only the operationally useful fields:

- save: `project_id`, `path`, `commit_hash`, `pushed`, `indexed`, warning codes
- search: top titles/paths/scores and any warning codes
- retry-push: `pushed`, `unpushed_commit_count`, `error_category`
- status: dirty state, unpushed count, failed embedding count
