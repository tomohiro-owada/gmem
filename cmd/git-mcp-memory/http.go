package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tomohiro-owada/gmem/internal/gmem"
)

// maxRequestBytes caps a single JSON-RPC request body. Tool arguments carry
// memory content, which the service already limits to 1MB; the extra slack
// covers the JSON-RPC envelope and escaping.
const maxRequestBytes = 4 << 20

// runHTTP serves the same JSON-RPC handler as runMCP over Streamable HTTP so
// the server can live on a remote host instead of being spawned per session.
//
// Two authentication paths are offered because clients differ: a static bearer
// token for clients that can set a header (Claude Code's --header), and OAuth
// 2.1 for those that cannot (claude.ai connectors). Either one alone is enough
// to enable the server; both may run together.
//
// Unlike the stdio loop, HTTP requests arrive concurrently. tools/call spawns a
// child process that loads the ~470MB embedding model and commits into a single
// git repository, so calls are serialized with a mutex to keep the stdio
// transport's semantics: one tool running at a time.
func runHTTP(args []string) error {
	opts, _, err := parseArgs(args)
	if err != nil {
		return err
	}
	addr := firstNonEmpty(opts["addr"], os.Getenv("GMEM_HTTP_ADDR"), "127.0.0.1:8765")
	publicURL := strings.TrimRight(firstNonEmpty(opts["public-url"], os.Getenv("GMEM_PUBLIC_URL")), "/")
	staticToken := os.Getenv("GMEM_HTTP_TOKEN")
	oauthPassword := os.Getenv("GMEM_OAUTH_PASSWORD")

	if staticToken == "" && oauthPassword == "" {
		return fmt.Errorf("set GMEM_HTTP_TOKEN and/or GMEM_OAUTH_PASSWORD: the HTTP transport exposes personal memory and a git push key, so it has no unauthenticated mode")
	}
	if oauthPassword != "" && publicURL == "" {
		// Behind a TLS proxy the request Host and scheme do not describe the
		// origin clients must call back to, so the public URL cannot be guessed.
		return fmt.Errorf("--public-url (or GMEM_PUBLIC_URL) is required when GMEM_OAUTH_PASSWORD is set, e.g. https://gmem.example.com")
	}

	cfg, err := gmem.LoadConfig("")
	if err != nil {
		return err
	}
	svc, cleanup, err := newService(false)
	if err != nil {
		return err
	}
	defer cleanup()

	mux := http.NewServeMux()
	var oauth *oauthServer
	if oauthPassword != "" {
		oauth, err = newOAuthServer(publicURL, oauthPassword, filepath.Dir(cfg.IndexPath))
		if err != nil {
			return err
		}
		oauth.register(mux)
	}

	var callMu sync.Mutex
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, staticToken, oauth) {
			if publicURL != "" {
				// Points clients at RFC 9728 metadata; without this header they
				// never discover that an OAuth flow is available.
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="gmem", resource_metadata="%s/.well-known/oauth-protected-resource"`, publicURL))
			} else {
				w.Header().Set("WWW-Authenticate", `Bearer realm="gmem"`)
			}
			writeRPCError(w, http.StatusUnauthorized, nil, -32001, "unauthorized")
			return
		}
		if r.Method != http.MethodPost {
			// No server-initiated messages exist (capabilities are tools-only),
			// so there is no SSE stream to open and no session to delete.
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		if err != nil {
			writeRPCError(w, http.StatusBadRequest, nil, -32700, "request body too large or unreadable")
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeRPCError(w, http.StatusBadRequest, nil, -32700, "parse error")
			return
		}
		if req.ID == nil {
			// Notification (e.g. notifications/initialized): accepted, no reply.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "tools/call" {
			callMu.Lock()
			defer callMu.Unlock()
		}
		resp := handleRPC(svc, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	modes := []string{}
	if staticToken != "" {
		modes = append(modes, "bearer")
	}
	if oauth != nil {
		modes = append(modes, "oauth")
	}
	fmt.Fprintf(os.Stderr, "gmem MCP listening on http://%s/mcp (auth: %s)\n", addr, strings.Join(modes, "+"))
	return (&http.Server{Addr: addr, Handler: mux}).ListenAndServe()
}

func authorized(r *http.Request, staticToken string, oauth *oauthServer) bool {
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if got == "" {
		return false
	}
	if staticToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(staticToken)) == 1 {
		return true
	}
	return oauth != nil && oauth.validate(got)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func writeRPCError(w http.ResponseWriter, status int, id any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   map[string]any{"code": code, "message": msg},
	})
}
