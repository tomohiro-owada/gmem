package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tomohiro-owada/gmem/internal/gmem"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(gmem.Fail[any]("command_failed", err.Error(), "", nil))
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "save":
		return runSave(args[1:], stdin, stdout)
	case "search":
		return runSearch(args[1:], stdin, stdout)
	case "retry-push":
		return runRetryPush(args[1:], stdout)
	case "sync":
		return runSync(args[1:], stdout)
	case "status":
		return runStatus(args[1:], stdout)
	case "schema":
		return json.NewEncoder(stdout).Encode(schema())
	case "mcp":
		return runMCP(stdin, stdout)
	case "http":
		return runHTTP(args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func newService(ensureAssets bool) (*gmem.Service, func(), error) {
	cfg, err := gmem.LoadConfig("")
	if err != nil {
		return nil, nil, err
	}
	if ensureAssets {
		if err := gmem.EnsureAssets(context.Background(), cfg); err != nil {
			return nil, nil, err
		}
	}
	idx, err := gmem.OpenIndex(cfg.IndexPath)
	if err != nil {
		return nil, nil, err
	}
	emb := &gmem.E5Embedder{Config: cfg}
	cleanup := func() {
		_ = idx.Close()
		if err := emb.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "embedder close failed:", err)
		}
	}
	return gmem.NewService(cfg, idx, emb), cleanup, nil
}

func runSave(args []string, stdin io.Reader, stdout io.Writer) error {
	opts, _, err := parseArgs(args)
	if err != nil {
		return err
	}
	var req gmem.SaveRequest
	if opts["input"] == "json" {
		if err := json.NewDecoder(stdin).Decode(&req); err != nil {
			return err
		}
	} else {
		if opts["content"] != "" && opts["file"] != "" {
			return fmt.Errorf("--content and --file cannot be used together")
		}
		body := opts["content"]
		if opts["file"] != "" {
			b, err := os.ReadFile(opts["file"])
			if err != nil {
				return err
			}
			body = string(b)
		}
		if body == "" {
			return fmt.Errorf("content is required")
		}
		workspace := opts["workspace"]
		if workspace == "" {
			wd, _ := os.Getwd()
			workspace = wd
		}
		req = gmem.SaveRequest{CurrentWorkspacePath: workspace, Title: opts["title"], Content: body, DryRun: opts["dry-run"] == "true"}
	}
	svc, cleanup, err := newService(true)
	if err != nil {
		return err
	}
	defer cleanup()
	return writeResponse(stdout, opts["output"], svc.Save(context.Background(), req))
}

func runSearch(args []string, stdin io.Reader, stdout io.Writer) error {
	opts, rest, err := parseArgs(args)
	if err != nil {
		return err
	}
	var req gmem.SearchRequest
	if opts["input"] == "json" {
		if err := json.NewDecoder(stdin).Decode(&req); err != nil {
			return err
		}
	} else {
		if len(rest) < 1 {
			return fmt.Errorf("query is required")
		}
		workspace := opts["workspace"]
		all := opts["all"] == "true"
		if workspace == "" && !all {
			wd, _ := os.Getwd()
			workspace = wd
		}
		req = gmem.SearchRequest{Query: rest[0], CurrentWorkspacePath: workspace, All: all, Limit: atoiDefault(opts["limit"], 10), SnippetChars: atoiDefault(opts["snippet-chars"], 0)}
		if opts["fields"] != "" {
			req.Fields = strings.Split(opts["fields"], ",")
		}
	}
	svc, cleanup, err := newService(true)
	if err != nil {
		return err
	}
	defer cleanup()
	return writeSearchResponse(stdout, opts["output"], svc.Search(context.Background(), req))
}

func runRetryPush(args []string, stdout io.Writer) error {
	opts, _, err := parseArgs(args)
	if err != nil {
		return err
	}
	svc, cleanup, err := newService(false)
	if err != nil {
		return err
	}
	defer cleanup()
	return writeResponse(stdout, opts["output"], svc.RetryPush(context.Background(), gmem.RetryPushRequest{DryRun: opts["dry-run"] == "true"}))
}

func runSync(args []string, stdout io.Writer) error {
	opts, _, err := parseArgs(args)
	if err != nil {
		return err
	}
	svc, cleanup, err := newService(true)
	if err != nil {
		return err
	}
	defer cleanup()
	return writeResponse(stdout, opts["output"], svc.Sync(context.Background()))
}

func runStatus(args []string, stdout io.Writer) error {
	opts, _, err := parseArgs(args)
	if err != nil {
		return err
	}
	svc, cleanup, err := newService(false)
	if err != nil {
		return err
	}
	defer cleanup()
	return writeResponse(stdout, opts["output"], svc.Status(context.Background()))
}

func runMCP(stdin io.Reader, stdout io.Writer) error {
	// The MCP transport is a long-running loop. The embedding model (~470MB of
	// native ONNX memory) is expensive to load, and once loaded the OS does not
	// reclaim it in-process even after the session is destroyed (the C allocator
	// keeps the pages). To keep an idle MCP process small — instead of N
	// concurrent sessions each pinning ~600MB — the embedding-heavy tools
	// (save/search) are delegated to a short-lived child process that loads the
	// model, does the work, and exits, at which point the OS reclaims all of it.
	//
	// The parent service here never loads the model; it only serves retry_push
	// (git-only, no assets), so newService(false) keeps startup asset-free and
	// offline-safe.
	svc, cleanup, err := newService(false)
	if err != nil {
		return err
	}
	defer cleanup()
	scanner := bufio.NewScanner(stdin)
	writer := bufio.NewWriter(stdout)
	defer writer.Flush()
	for scanner.Scan() {
		line := scanner.Bytes()
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		resp := handleRPC(svc, req)
		b, _ := json.Marshal(resp)
		_, _ = writer.Write(b)
		_ = writer.WriteByte('\n')
		_ = writer.Flush()
	}
	return scanner.Err()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   map[string]any `json:"error,omitempty"`
}

func handleRPC(svc *gmem.Service, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"protocolVersion": negotiateProtocolVersion(req.Params), "serverInfo": map[string]any{"name": "git-mcp-memory", "version": "0.1.0"}, "capabilities": map[string]any{"tools": map[string]any{}}}}
	case "tools/list":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": mcpTools()}}
	case "tools/call":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: callTool(svc, req.Params)}
	default:
		if req.ID == nil {
			return rpcResponse{}
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}}
	}
}

// supportedProtocolVersions lists the MCP revisions this server speaks, newest
// first. Streamable HTTP was introduced in 2025-03-26, so the stdio-era
// 2024-11-05 alone is not enough once the HTTP transport is in play.
var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// negotiateProtocolVersion echoes the client's requested revision when this
// server supports it, and otherwise answers with the newest one it speaks —
// which is what the spec asks a server to do on a version it cannot match.
func negotiateProtocolVersion(raw json.RawMessage) string {
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &in); err == nil {
		for _, v := range supportedProtocolVersions {
			if in.ProtocolVersion == v {
				return v
			}
		}
	}
	return supportedProtocolVersions[0]
}

func callTool(svc *gmem.Service, raw json.RawMessage) any {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return toolText(gmem.Fail[any]("invalid_request", err.Error(), "", nil))
	}
	switch in.Name {
	case "save_memory":
		// Delegate to a short-lived child so the ~470MB model is reclaimed by
		// the OS the moment the child exits (see runMCP for why).
		return runToolViaSubprocess("save", in.Arguments)
	case "search_memory":
		return runToolViaSubprocess("search", in.Arguments)
	case "retry_push":
		var req gmem.RetryPushRequest
		_ = json.Unmarshal(in.Arguments, &req)
		return toolText(svc.RetryPush(context.Background(), req))
	default:
		return toolText(gmem.Fail[any]("unknown_tool", "unknown tool", "", nil))
	}
}

// runToolViaSubprocess executes an embedding-heavy tool (save/search) in a
// fresh child process using the CLI subcommand of the same binary, forwarding
// the MCP arguments as the child's JSON stdin and relaying its JSON response.
// Running in a child guarantees the native model memory is returned to the OS
// when the child exits, keeping the resident MCP server small while idle.
func runToolViaSubprocess(sub string, args json.RawMessage) any {
	exe, err := os.Executable()
	if err != nil {
		return toolText(gmem.Fail[any]("server_error", err.Error(), "", nil))
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	cmd := exec.Command(exe, sub, "--input", "json", "--output", "json")
	cmd.Stdin = bytes.NewReader(args)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		msg := "embedding subprocess produced no output"
		if runErr != nil {
			msg = runErr.Error()
		}
		return toolText(gmem.Fail[any]("server_error", msg, "", nil))
	}
	// The child already emits the canonical Response JSON; relay it verbatim as
	// the tool's text content.
	return map[string]any{"content": []map[string]any{{"type": "text", "text": out}}}
}

func toolText(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
}

// Tool descriptions carry the whole convention for this knowledge base — when to
// write, what shape the entry takes, and what the server refuses — because a
// client may have no project instructions telling it any of that.
const saveMemoryDescription = `Save a decision or a hard-won finding to a git-backed knowledge base, then commit and push it.

WHEN TO CALL THIS — call it on your own, without being asked, as soon as any of these happens:
- A decision was made: which approach, which tool or library, where a file lives, what will NOT be done.
  This includes a short approval of your proposal ("yes", "OK", "go ahead") — that is still a decision.
- A cause that was only found after being stuck, or a workaround that turned out to work.
- A constraint, requirement, or piece of context that cannot be derived from the code itself.
Small decisions count. When unsure, save it. Prefer saving at the moment it happens rather than
batching at the end of a session, where entries get forgotten.

WHEN NOT TO CALL THIS:
- Facts already recorded in the repository or its git history (code structure, past fixes).
- Anything that only matters inside the current conversation.

CONTENT FORMAT — Markdown using exactly these headings, in this order:

## 場面
The situation and context in which the decision was made.

## 決定内容
What was decided, concretely enough to act on later.

## 選択肢
The other options that were on the table. Omit this heading entirely if there were none.

## 決定基準・理由
Why this one was chosen over the others.

## 日付
YYYY-MM-DD. Resolve relative dates to absolute ones — never write "today" or "last week".

REJECTED CONTENT — the save fails, rather than being redacted, when the title or content holds:
a private key, an AWS/GitHub/OpenAI/Slack token, an "Authorization: Bearer ..." header, a
PASSWORD=/SECRET=/TOKEN=/API_KEY=-style line, an email address, or a phone-number-like run of
digits. Watch for two that are easy to hit by accident: an SSH remote such as
git@github.com:owner/repo.git matches the email rule (write "the GitHub SSH remote" instead), and
the literal strings 会社名 / 顧客名 (also 会社: / 顧客:) are refused. On rejection, rephrase the
offending part and call again — do not silently drop the memory.`

const searchMemoryDescription = `Search saved memories by meaning, not by keyword — a query in one language finds entries written in another, and paraphrases match.

Call this before answering a question about why something is the way it is, before repeating a
decision that may already have been made, and when picking up work that was left unfinished.

Results are ranked by similarity and each carries a score, title, project, and file path. Searching
is scoped to the current project by default; pass all=true to search across every project, which is
what you want when looking for a decision whose project you cannot name.

Entries were written at a point in time and describe what was true then. Before acting on one that
names a file, function, or flag, check that it still exists.`

func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "save_memory", "description": saveMemoryDescription, "inputSchema": schema()["tools"].(map[string]any)["save_memory"]},
		{"name": "search_memory", "description": searchMemoryDescription, "inputSchema": schema()["tools"].(map[string]any)["search_memory"]},
		{"name": "retry_push", "description": "Push memory commits that were committed locally but failed to reach the remote (network loss, or the remote having moved ahead). Call it when a save reported pushed=false, or when a session starts and earlier saves may not have landed.", "inputSchema": schema()["tools"].(map[string]any)["retry_push"]},
	}
}

func schema() map[string]any {
	return map[string]any{
		"tools": map[string]any{
			"save_memory": map[string]any{
				"type":     "object",
				"required": []string{"current_workspace_path", "title", "content"},
				"properties": map[string]any{
					"current_workspace_path": map[string]any{"type": "string", "description": "Absolute path of the project this memory belongs to; memories are grouped by it. Use the repository root when inside one, otherwise the working directory."},
					"title":                  map[string]any{"type": "string", "description": "One line stating what was decided or found — specific enough to recognise later in a list of results. \"Use X because Y\" beats \"about X\"."},
					"content":                map[string]any{"type": "string", "description": "The memory itself, in the Markdown heading format given in this tool's description (場面 / 決定内容 / 選択肢 / 決定基準・理由 / 日付)."},
					"dry_run":                map[string]any{"type": "boolean", "description": "Run the security and format checks and report what would be written, without committing or pushing."},
				},
			},
			"search_memory": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query":                  map[string]any{"type": "string", "description": "What you want to know, phrased as the words you would expect in the answer. Matching is semantic, so a natural-language question works better than bare keywords."},
					"current_workspace_path": map[string]any{"type": "string", "description": "Absolute path of the project to search within. Ignored when all=true."},
					"limit":                  map[string]any{"type": "integer", "description": "Maximum results to return (default 5)."},
					"all":                    map[string]any{"type": "boolean", "description": "Search every project instead of only the current one. Use this when the decision you are after may have been recorded under a different project."},
					"fields":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Restrict the returned fields, e.g. [\"title\",\"path\"] to list candidates cheaply before fetching full content."},
					"snippet_chars":          map[string]any{"type": "integer", "description": "Truncate each result's content to this many characters. Use a small value to scan many results, or omit for the full text."},
				},
			},
			"retry_push": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dry_run": map[string]any{"type": "boolean", "description": "Report which commits are unpushed without attempting the push."},
				},
			},
		},
		"commands": map[string]any{
			"save":       map[string]any{"output": []string{"json", "text"}},
			"search":     map[string]any{"output": []string{"json", "ndjson", "text"}},
			"sync":       map[string]any{"output": []string{"json", "text"}},
			"status":     map[string]any{"output": []string{"json", "text"}},
			"retry-push": map[string]any{"output": []string{"json", "text"}},
			"schema":     map[string]any{"output": []string{"json"}},
			"mcp":        map[string]any{"transport": "stdio"},
		},
	}
}

func writeResponse(stdout io.Writer, output string, v any) error {
	if output == "" || output == "json" {
		return json.NewEncoder(stdout).Encode(v)
	}
	if output != "text" {
		return fmt.Errorf("unsupported output: %s", output)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	_, err := fmt.Fprintln(stdout, string(b))
	return err
}

func writeSearchResponse(stdout io.Writer, output string, resp gmem.Response[gmem.SearchData]) error {
	if output == "ndjson" {
		if !resp.OK {
			return json.NewEncoder(stdout).Encode(resp)
		}
		for _, result := range resp.Data.Results {
			if err := json.NewEncoder(stdout).Encode(result); err != nil {
				return err
			}
		}
		return nil
	}
	return writeResponse(stdout, output, resp)
}

func parseArgs(args []string) (map[string]string, []string, error) {
	opts := map[string]string{}
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			rest = append(rest, arg)
			continue
		}
		key := strings.TrimPrefix(arg, "--")
		if key == "" {
			return nil, nil, fmt.Errorf("invalid option")
		}
		if strings.Contains(key, "=") {
			k, v, _ := strings.Cut(key, "=")
			opts[k] = v
			continue
		}
		if key == "all" || key == "dry-run" || key == "non-interactive" {
			opts[key] = "true"
			continue
		}
		if i+1 >= len(args) {
			return nil, nil, fmt.Errorf("--%s requires a value", key)
		}
		opts[key] = args[i+1]
		i++
	}
	return opts, rest, nil
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
