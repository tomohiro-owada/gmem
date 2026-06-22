package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	return gmem.NewService(cfg, idx, emb), func() { _ = idx.Close() }, nil
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
	scanner := bufio.NewScanner(stdin)
	writer := bufio.NewWriter(stdout)
	defer writer.Flush()
	for scanner.Scan() {
		line := scanner.Bytes()
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		resp := handleRPC(req)
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

func handleRPC(req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]any{"name": "git-mcp-memory", "version": "0.1.0"}, "capabilities": map[string]any{"tools": map[string]any{}}}}
	case "tools/list":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": mcpTools()}}
	case "tools/call":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: callTool(req.Params)}
	default:
		if req.ID == nil {
			return rpcResponse{}
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}}
	}
}

func callTool(raw json.RawMessage) any {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return toolText(gmem.Fail[any]("invalid_request", err.Error(), "", nil))
	}
	ensure := in.Name == "save_memory" || in.Name == "search_memory"
	svc, cleanup, err := newService(ensure)
	if err != nil {
		return toolText(gmem.Fail[any]("server_error", err.Error(), "", nil))
	}
	defer cleanup()
	switch in.Name {
	case "save_memory":
		var req gmem.SaveRequest
		_ = json.Unmarshal(in.Arguments, &req)
		return toolText(svc.Save(context.Background(), req))
	case "search_memory":
		var req gmem.SearchRequest
		_ = json.Unmarshal(in.Arguments, &req)
		return toolText(svc.Search(context.Background(), req))
	case "retry_push":
		var req gmem.RetryPushRequest
		_ = json.Unmarshal(in.Arguments, &req)
		return toolText(svc.RetryPush(context.Background(), req))
	default:
		return toolText(gmem.Fail[any]("unknown_tool", "unknown tool", "", nil))
	}
}

func toolText(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "save_memory", "description": "Save a memory", "inputSchema": schema()["tools"].(map[string]any)["save_memory"]},
		{"name": "search_memory", "description": "Search memories", "inputSchema": schema()["tools"].(map[string]any)["search_memory"]},
		{"name": "retry_push", "description": "Retry pushing local commits", "inputSchema": schema()["tools"].(map[string]any)["retry_push"]},
	}
}

func schema() map[string]any {
	return map[string]any{
		"tools": map[string]any{
			"save_memory":   map[string]any{"type": "object", "required": []string{"current_workspace_path", "title", "content"}, "properties": map[string]any{"current_workspace_path": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "dry_run": map[string]any{"type": "boolean"}}},
			"search_memory": map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string"}, "current_workspace_path": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}, "all": map[string]any{"type": "boolean"}, "fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "snippet_chars": map[string]any{"type": "integer"}}},
			"retry_push":    map[string]any{"type": "object", "properties": map[string]any{"dry_run": map[string]any{"type": "boolean"}}},
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
