// OAuth 2.0 Authorization Code flow helpers for Google Gemini (PaLM / Generative
// AI) API. The implementation mirrors the anthropic helpers already present in
// this package so that higher-level code can treat providers uniformly.

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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// These values are copied from gemini-cli/AUTH.md and correspond to the desktop
// installed-application OAuth client published by Google.
const (
	geminiClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	geminiClientSecret = "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"

	// We use an out-of-band redirect so that the user can manually copy/paste the
	// code, avoiding the need to spin up a local HTTP server.
	geminiRedirectURI = "urn:ietf:wg:oauth:2.0:oob"

	geminiAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	geminiTokenEndpoint = "https://oauth2.googleapis.com/token"
)

var geminiScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/generative-language-api",
	"https://www.googleapis.com/auth/generative-language",
}

type GeminiCredentials struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
	Expiry       int64  `json:"expiry_date"`
}

func getCachedCredentialPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nina", "gemini_oauth_creds.json"), nil
}

func readCachedCredentials() (*GeminiCredentials, error) {
	path, err := getCachedCredentialPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var creds GeminiCredentials
	err = json.Unmarshal(data, &creds)
	if err != nil {
		return nil, err
	}
	return &creds, nil
}

func writeCachedCredentials(creds *GeminiCredentials) error {
	path, err := getCachedCredentialPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// GeminiAccess returns a valid access token, refreshing it if needed.
func GeminiAccess() (string, error) {
	creds, err := readCachedCredentials()
	if err != nil {
		// If we can't read the credentials, we can't do anything.
		// The user needs to login via gemini-cli first.
		return "", &ProviderAuthError{Provider: "gemini", Message: "not logged in. please run `gemini` command to login"}
	}

	// Check if the token is expired
	if time.Now().Unix() > creds.Expiry {
		// Token is expired, refresh it
		payload := url.Values{}
		payload.Set("grant_type", "refresh_token")
		payload.Set("refresh_token", creds.RefreshToken)
		payload.Set("client_id", geminiClientID)
		payload.Set("client_secret", geminiClientSecret)

		resp, err := http.Post(geminiTokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(payload.Encode()))
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return "", &ProviderAuthError{Provider: "gemini", Message: fmt.Sprintf("status %d", resp.StatusCode)}
		}

		var newCreds GeminiCredentials
		if err := json.NewDecoder(resp.Body).Decode(&newCreds); err != nil {
			return "", err
		}

		// Update the credentials with the new values
		creds.AccessToken = newCreds.AccessToken
		creds.ExpiresIn = newCreds.ExpiresIn
		creds.Expiry = time.Now().Unix() + int64(newCreds.ExpiresIn)
		if newCreds.RefreshToken != "" {
			creds.RefreshToken = newCreds.RefreshToken
		}

		if err := writeCachedCredentials(creds); err != nil {
			return "", err
		}
	}

	return creds.AccessToken, nil
}

func generatePKCEGemini() (*pkce, error) {
	v := make([]byte, 32)
	if _, err := rand.Read(v); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(v)

	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &pkce{Verifier: verifier, Challenge: challenge}, nil
}

// GeminiAuthorize constructs the authorization URL and returns it along with
// the code verifier so that the caller can later exchange the code.
func GeminiAuthorize() (*AuthorizeResult, error) {
	pkce, err := generatePKCEGemini()
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(geminiAuthEndpoint)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", geminiClientID)
	q.Set("redirect_uri", geminiRedirectURI)
	q.Set("access_type", "offline") // request refresh token
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", strings.Join(geminiScopes, " "))
	u.RawQuery = q.Encode()

	return &AuthorizeResult{
		URL:      u.String(),
		Verifier: pkce.Verifier,
	}, nil
}

// GeminiExchange exchanges the authorization code for access & refresh tokens
// and stores them via oauth.Set so that they can be reused by callers.
func GeminiExchange(code, verifier string, redirect ...string) error {
	redirectURI := geminiRedirectURI
	if len(redirect) > 0 && redirect[0] != "" {
		redirectURI = redirect[0]
	}

	payload := url.Values{}
	payload.Set("grant_type", "authorization_code")
	payload.Set("code", code)
	payload.Set("code_verifier", verifier)
	payload.Set("redirect_uri", redirectURI)
	payload.Set("client_id", geminiClientID)
	payload.Set("client_secret", geminiClientSecret)

	resp, err := http.Post(geminiTokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(payload.Encode()))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &ProviderAuthError{Provider: "gemini", Message: fmt.Sprintf("status %d", resp.StatusCode)}
	}

	var tr GeminiCredentials
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return err
	}

	tr.Expiry = time.Now().Unix() + int64(tr.ExpiresIn)

	return writeCachedCredentials(&tr)
}

// AddGeminiHeaders attaches the Authorization header expected by the Google
// Generative AI backend.
func AddGeminiHeaders(req *http.Request) error {
	token, err := GeminiAccess()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// LoginGemini performs the complete OAuth flow for Gemini, including opening
// the browser and handling the callback via a local HTTP server.
func LoginGemini() error {
	// Start local server for OAuth callback
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/oauth2callback", port)

	// Generate PKCE
	pkce, err := generatePKCEGemini()
	if err != nil {
		return err
	}

	// Build auth URL with local redirect
	authURL, err := buildGeminiAuthURLWithRedirect(pkce.Challenge, redirectURI)
	if err != nil {
		return err
	}

	// Setup callback handler
	codeCh := make(chan string, 1)
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		if errVals := r.URL.Query().Get("error"); errVals != "" {
			_, _ = fmt.Fprintf(w, "Login failed: %s", errVals)
			codeCh <- ""
			return
		}
		code := r.URL.Query().Get("code")
		_, _ = fmt.Fprintln(w, "Login successful, you can close this window.")
		codeCh <- code
	})

	// Start server
	go func() {
		defer func() {}()
		_ = srv.Serve(listener)
	}()

	// Open browser
	fmt.Println("Opening browser for Gemini login...")
	if err := openBrowser(authURL); err != nil {
		fmt.Println("Failed to open browser. Please open the following URL manually:")
	}
	fmt.Println("\n" + authURL)

	// Wait for callback
	code := <-codeCh
	_ = srv.Close()
	if code == "" {
		return fmt.Errorf("no authorization code received")
	}

	// Exchange code for tokens
	if err := GeminiExchange(code, pkce.Verifier, redirectURI); err != nil {
		return fmt.Errorf("error exchanging code: %w", err)
	}

	fmt.Println("Gemini login successful!")
	return nil
}

// buildGeminiAuthURLWithRedirect constructs the OAuth authorization URL with a
// custom redirect URI (for local server callback).
func buildGeminiAuthURLWithRedirect(challenge, redirectURI string) (string, error) {
	u, err := url.Parse(geminiAuthEndpoint)
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", geminiClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("access_type", "offline")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", strings.Join(geminiScopes, " "))
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// openBrowser attempts to open the specified URL in the default browser.
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported platform")
	}

	return exec.Command(cmd, args...).Start()
}
