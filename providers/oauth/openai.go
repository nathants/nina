// OAuth 2.0 Authorization Code + Token-Exchange helpers for OpenAI (Codex fork).
// Mirrors the style of anthropic.go and gemini.go so that callers can treat all
// providers uniformly.

package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Values copied from codex/codex-rs/login/src/login_with_chatgpt.py.
const (
	openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIIssuer   = "https://auth.openai.com"
	openAuthEP     = openAIIssuer + "/oauth/authorize"
	openTokenEP    = openAIIssuer + "/oauth/token"

	// Default redirect URI if none specified (should not be used for login).
	openRedirectURI = "urn:ietf:wg:oauth:2.0:oob"
)

// --- PKCE helpers -----------------------------------------------------------

type pkce struct {
	Verifier  string
	Challenge string
}

func generatePKCEOpenAI() (*pkce, error) {
	v := make([]byte, 64)
	if _, err := rand.Read(v); err != nil {
		return nil, err
	}
	verifier := fmt.Sprintf("%x", v) // token_hex in py impl

	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &pkce{Verifier: verifier, Challenge: challenge}, nil
}

// --- Public helpers ---------------------------------------------------------

type AuthorizeResult struct {
	URL      string
	Verifier string
}

// OpenAIAuthorize returns the URL that the user should open in the browser and
// the PKCE verifier that must be supplied to OpenAIExchange.
func OpenAIAuthorize(redirectURI string) (*AuthorizeResult, error) {
	if redirectURI == "" {
		redirectURI = openRedirectURI
	}

	pkce, err := generatePKCEOpenAI()
	if err != nil {
		return nil, err
	}

	// State must be cryptographically random and independent of verifier.
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, err
	}
	state := fmt.Sprintf("%x", stateBytes)

	u, err := url.Parse(openAuthEP)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", openAIClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid profile email offline_access")
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("id_token_add_organizations", "true")
	q.Set("state", state)
	u.RawQuery = q.Encode()

	return &AuthorizeResult{URL: u.String(), Verifier: pkce.Verifier + "#" + state}, nil
}

// TokenResponse1 matches the first token call (authorization_code).
type tokenResponse1 struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// tokenResponse2 matches the token-exchange response (openai-api-key).
type tokenResponse2 struct {
	AccessToken string `json:"access_token"`
}

// LoginOpenAI launches browser for OAuth, runs local callback server, exchanges
// code for API key and stores credentials.
func LoginOpenAI() error {
	// Required fixed port 1455 per Codex implementation.
	const port = 1455
	addr := fmt.Sprintf("localhost:%d", port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)

	// Build authorization URL
	authRes, err := OpenAIAuthorize(redirectURI)
	if err != nil {
		_ = listener.Close()
		return err
	}

	vsplit := strings.SplitN(authRes.Verifier, "#", 2)
	_ = vsplit[0]
	state := ""
	if len(vsplit) == 2 {
		state = vsplit[1]
	}

	// Channel to receive code
	codeCh := make(chan string, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errStr := q.Get("error"); errStr != "" {
			http.Error(w, errStr, http.StatusBadRequest)
			codeCh <- ""
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			codeCh <- ""
			return
		}
		code := q.Get("code")
		_, _ = fmt.Fprintln(w, "Login successful, you can close this window.")
		codeCh <- code
	})

	go func() {
		// defer func() {}()
		_ = srv.Serve(listener)
	}()

	// Open browser
	fmt.Println("Opening browser for OpenAI login...")
	if err := openBrowser(authRes.URL); err != nil {
		fmt.Println("Failed to open browser. Please open the following URL manually:")
	}
	fmt.Println("\n" + authRes.URL)

	// Wait for code or timeout
	var code string
	select {
	case code = <-codeCh:
	case <-time.After(180 * time.Second):
		_ = srv.Close()
		return fmt.Errorf("timed out waiting for authorization code")
	}

	_ = srv.Close()

	if code == "" {
		return fmt.Errorf("authorization failed")
	}

	if err := OpenAIExchange(code, authRes.Verifier, redirectURI); err != nil {
		return err
	}

	fmt.Println("OpenAI login successful!")
	return nil
}

// OpenAIExchange exchanges the authorization code for an API key and stores it
// via oauth.Set so that callers can reuse it transparently.
func OpenAIExchange(code, verifierState string, redirect ...string) error {
	// Split verifier and state if combined
	parts := strings.SplitN(verifierState, "#", 2)
	verifier := parts[0]

	redirectURI := openRedirectURI
	if len(redirect) > 0 && redirect[0] != "" {
		redirectURI = redirect[0]
	}

	// Step 1: code -> id_token + refresh_token.
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", openAIClientID)
	data.Set("code_verifier", verifier)

	resp, err := http.Post(openTokenEP, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openai token exchange failed: status %d", resp.StatusCode)
	}

	var t1 tokenResponse1
	if err := json.NewDecoder(resp.Body).Decode(&t1); err != nil {
		return err
	}

	// Step 2: token-exchange -> API key.
	data2 := url.Values{}
	data2.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	data2.Set("client_id", openAIClientID)
	data2.Set("requested_token", "openai-api-key")
	data2.Set("subject_token", t1.IDToken)
	data2.Set("subject_token_type", "urn:ietf:params:oauth:token-type:id_token")
	data2.Set("name", "Codex CLI [auto-generated] (go)")

	resp2, err := http.Post(openTokenEP, "application/x-www-form-urlencoded", strings.NewReader(data2.Encode()))
	if err != nil {
		return err
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("openai api-key exchange failed: status %d", resp2.StatusCode)
	}

	var t2 tokenResponse2
	if err := json.NewDecoder(resp2.Body).Decode(&t2); err != nil {
		return err
	}

	info := map[string]any{
		"type":     "oauth",
		"refresh":  t1.RefreshToken,
		"id_token": t1.IDToken,
		"api_key":  t2.AccessToken,
		"access":   t1.AccessToken, // original access token (unused)
		"expires":  time.Now().UnixMilli() + int64(t1.ExpiresIn)*1000,
	}

	return Set("openai", info)
}

// OpenAIAccess returns the stored API key, refreshing if necessary.
// Currently we assume api_key does not expire; simply return it.
func OpenAIAccess() (string, error) {
	cred, err := Get("openai")
	if err != nil {
		return "", err
	}

	m, ok := cred.(map[string]any)
	if !ok || m["type"] != "oauth" {
		return "", fmt.Errorf("openai: oauth credentials not found")
	}

	if key, ok := m["api_key"].(string); ok && key != "" {
		return key, nil
	}
	return "", fmt.Errorf("openai: api key missing in credentials")
}
