package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OAuth 2.1 for a single user. Claude clients that cannot send a static header
// (claude.ai connectors) discover this flow from the 401 on /mcp and walk
// RFC 9728 metadata → RFC 8414 metadata → RFC 7591 dynamic registration →
// authorization code + PKCE → token.
//
// Deliberately omitted, because one person owns this server: multiple users,
// scopes, refresh tokens (access tokens are long-lived instead), and consent
// beyond the password prompt.

const (
	authCodeTTL    = 10 * time.Minute
	accessTokenTTL = 90 * 24 * time.Hour
	oauthStateFile = "oauth.json"
	// maxRegisteredClients bounds the unauthenticated /register endpoint.
	maxRegisteredClients = 50
)

type oauthClient struct {
	ClientID     string    `json:"client_id"`
	ClientName   string    `json:"client_name,omitempty"`
	RedirectURIs []string  `json:"redirect_uris"`
	CreatedAt    time.Time `json:"created_at"`
}

type oauthToken struct {
	// Only the SHA-256 hash is stored: a leaked state file must not yield a
	// usable bearer token.
	Hash      string    `json:"hash"`
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type oauthState struct {
	Clients []oauthClient `json:"clients"`
	Tokens  []oauthToken  `json:"tokens"`
}

// authCode is short-lived and single-use, so it lives in memory only; a server
// restart mid-flow just means the client retries the authorization.
type authCode struct {
	clientID    string
	redirectURI string
	challenge   string
	expiresAt   time.Time
}

type oauthServer struct {
	publicURL string
	// resourceURL is the MCP endpoint as the user types it into Claude. RFC 9728
	// metadata must echo it exactly, path included, or the connector rejects it.
	resourceURL string
	password    string
	statePath   string

	mu    sync.Mutex
	state oauthState
	codes map[string]authCode
}

func newOAuthServer(publicURL, password, configDir string) (*oauthServer, error) {
	base := strings.TrimRight(publicURL, "/")
	o := &oauthServer{
		publicURL:   base,
		resourceURL: base + "/mcp",
		password:    password,
		statePath:   filepath.Join(configDir, oauthStateFile),
		codes:       map[string]authCode{},
	}
	if err := o.load(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *oauthServer) load() error {
	b, err := os.ReadFile(o.statePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &o.state)
}

// save must be called with o.mu held.
func (o *oauthServer) save() error {
	b, err := json.MarshalIndent(o.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.statePath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(o.statePath, b, 0o600)
}

func (o *oauthServer) register(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", o.handleResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", o.handleResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", o.handleAuthServerMetadata)
	mux.HandleFunc("/register", o.handleRegister)
	mux.HandleFunc("/authorize", o.handleAuthorize)
	mux.HandleFunc("/token", o.handleToken)
}

func (o *oauthServer) handleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 o.resourceURL,
		"authorization_servers":    []string{o.publicURL},
		"bearer_methods_supported": []string{"header"},
	})
}

func (o *oauthServer) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                o.publicURL,
		"authorization_endpoint":                o.publicURL + "/authorize",
		"token_endpoint":                        o.publicURL + "/token",
		"registration_endpoint":                 o.publicURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

func (o *oauthServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	if len(in.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_redirect_uri"})
		return
	}
	c := oauthClient{
		ClientID:     randomToken(16),
		ClientName:   in.ClientName,
		RedirectURIs: in.RedirectURIs,
		CreatedAt:    time.Now().UTC(),
	}
	o.mu.Lock()
	// /register is necessarily unauthenticated — a client registers before it
	// can hold a token. Claude registers a fresh client on every new connection,
	// so the file would grow unbounded even without an attacker. Registrations
	// carry no secret, so dropping the oldest is safe: that client just
	// registers again.
	o.state.Clients = append(o.state.Clients, c)
	if n := len(o.state.Clients); n > maxRegisteredClients {
		o.state.Clients = o.state.Clients[n-maxRegisteredClients:]
	}
	err := o.save()
	o.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ClientID,
		"client_name":                c.ClientName,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

var authorizeForm = template.Must(template.New("authorize").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>gmem を承認</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;max-width:26rem;margin:4rem auto;padding:0 1rem;line-height:1.6}
input{width:100%;padding:.6rem;font-size:1rem;box-sizing:border-box}
button{margin-top:1rem;padding:.6rem 1.2rem;font-size:1rem;cursor:pointer}
.err{color:#b00020}
.dest{background:#f4f4f5;border-radius:.4rem;padding:.6rem .8rem;font-family:ui-monospace,monospace;word-break:break-all}
.warn{background:#fff4e5;border-left:3px solid #d97706;padding:.6rem .8rem;margin-top:1rem}
</style>
<h1>gmem へのアクセスを承認</h1>
<p><strong>{{.ClientName}}</strong> があなたのメモリへのアクセスを求めています。</p>
<p>承認すると、次の宛先へリダイレクトされます:</p>
<p class="dest">{{.RedirectHost}}</p>
{{if .Loopback}}<p class="warn">この宛先はこの端末上のローカルアドレスです。心当たりのある操作（Claude Code などの接続）の最中でなければ承認しないでください。同じ端末の別のプログラムが受け取る可能性があります。</p>{{end}}
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="/authorize">
  {{range $k, $v := .Fields}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
  <label>パスワード<input type="password" name="password" autofocus autocomplete="current-password"></label>
  <button type="submit">承認する</button>
</form>
`))

func (o *oauthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	q := r.Form
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	challenge := q.Get("code_challenge")

	client, ok := o.findClient(clientID)
	if !ok {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if !matchRedirectURI(client.RedirectURIs, redirectURI) {
		http.Error(w, "redirect_uri does not match the registered values", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" {
		redirectError(w, r, redirectURI, q.Get("state"), "unsupported_response_type")
		return
	}
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		redirectError(w, r, redirectURI, q.Get("state"), "invalid_request")
		return
	}

	fields := map[string]string{
		"client_id":             clientID,
		"redirect_uri":          redirectURI,
		"state":                 q.Get("state"),
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"response_type":         "code",
	}
	data := map[string]any{
		"ClientName":   clientOrDefault(client),
		"Fields":       fields,
		"Error":        "",
		"RedirectHost": redirectURI,
		"Loopback":     isLoopbackRedirect(redirectURI),
	}

	if r.Method != http.MethodPost {
		_ = authorizeForm.Execute(w, data)
		return
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("password")), []byte(o.password)) != 1 {
		data["Error"] = "パスワードが違います。"
		w.WriteHeader(http.StatusUnauthorized)
		_ = authorizeForm.Execute(w, data)
		return
	}

	code := randomToken(32)
	o.mu.Lock()
	o.codes[code] = authCode{clientID: clientID, redirectURI: redirectURI, challenge: challenge, expiresAt: time.Now().Add(authCodeTTL)}
	o.mu.Unlock()

	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	target := redirectURI + sep + "code=" + code
	if s := q.Get("state"); s != "" {
		target += "&state=" + url.QueryEscape(s)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (o *oauthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	code, ok := o.codes[r.Form.Get("code")]
	// Single use: the code is consumed whether or not the rest of the exchange
	// validates, so a leaked code cannot be replayed after a failed attempt.
	delete(o.codes, r.Form.Get("code"))
	if !ok || time.Now().After(code.expiresAt) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if code.clientID != r.Form.Get("client_id") || code.redirectURI != r.Form.Get("redirect_uri") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if s256(r.Form.Get("code_verifier")) != code.challenge {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}

	token := randomToken(32)
	o.state.Tokens = append(o.pruneExpiredLocked(), oauthToken{
		Hash:      hashToken(token),
		ClientID:  code.clientID,
		ExpiresAt: time.Now().Add(accessTokenTTL).UTC(),
	})
	if err := o.save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(accessTokenTTL.Seconds()),
	})
}

// validate reports whether a presented bearer token is a live OAuth token.
func (o *oauthServer) validate(token string) bool {
	if token == "" {
		return false
	}
	h := hashToken(token)
	now := time.Now()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, t := range o.state.Tokens {
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(h)) == 1 && now.Before(t.ExpiresAt) {
			return true
		}
	}
	return false
}

// pruneExpiredLocked must be called with o.mu held.
func (o *oauthServer) pruneExpiredLocked() []oauthToken {
	now := time.Now()
	kept := o.state.Tokens[:0]
	for _, t := range o.state.Tokens {
		if now.Before(t.ExpiresAt) {
			kept = append(kept, t)
		}
	}
	return kept
}

func (o *oauthServer) findClient(id string) (oauthClient, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range o.state.Clients {
		if c.ClientID == id {
			return c, true
		}
	}
	return oauthClient{}, false
}

func clientOrDefault(c oauthClient) string {
	if c.ClientName == "" {
		return "登録済みクライアント"
	}
	return c.ClientName
}

// matchRedirectURI reports whether a requested redirect_uri is covered by the
// client's registered URIs.
//
// Non-loopback redirects must match exactly; anything looser turns /authorize
// into an open redirect that leaks authorization codes.
//
// Loopback redirects are matched with the port ignored. Native clients bind an
// ephemeral port per session and cannot register it in advance — Claude Code
// registers http://localhost/callback and http://127.0.0.1/callback but calls
// back on e.g. http://localhost:3118/callback. RFC 8252 §7.3 requires this
// port-agnostic match for 127.0.0.1, and Claude needs the same for localhost.
func matchRedirectURI(registered []string, want string) bool {
	for _, v := range registered {
		if v == want {
			return true
		}
		if isLoopbackRedirect(v) && isLoopbackRedirect(want) && loopbackKey(v) == loopbackKey(want) {
			return true
		}
	}
	return false
}

func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// loopbackKey strips the port so two loopback URIs differing only in port
// compare equal. Host is kept: localhost and 127.0.0.1 are not interchangeable
// for the client, and both are registered separately anyway.
func loopbackKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Hostname() + u.EscapedPath() + "?" + u.RawQuery
}

func redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	target := redirectURI + sep + "error=" + code
	if state != "" {
		target += "&state=" + url.QueryEscape(state)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
