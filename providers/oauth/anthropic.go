// anthropic oauth pkce implementation moved from ask/auth.
// The contents are unchanged other than the package name so existing
// functionality continues to work for all dependants.

package oauth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"nina/providers/claude"
	"strings"
	"time"
)

const (
	AnthropicClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	AnthropicRedirectURI = "https://console.anthropic.com/oauth/code/callback"
)

type PKCEChallenge struct {
	Verifier  string
	Challenge string
}

func generatePKCE() (*PKCEChallenge, error) {
	// Generate random verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate challenge
	h := sha256.New()
	h.Write([]byte(verifier))
	challengeBytes := h.Sum(nil)
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes)

	return &PKCEChallenge{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

func AnthropicAuthorize() (*AuthorizeResult, error) {
	pkce, err := generatePKCE()
	if err != nil {
		return nil, err
	}

	u, err := url.Parse("https://claude.ai/oauth/authorize")
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("code", "true")
	q.Set("client_id", AnthropicClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", AnthropicRedirectURI)
	q.Set("scope", "org:create_api_key user:profile user:inference")
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", pkce.Verifier)
	u.RawQuery = q.Encode()

	return &AuthorizeResult{
		URL:      u.String(),
		Verifier: pkce.Verifier,
	}, nil
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func postToken(data map[string]string) (TokenResponse, error) {
	jsonBody, err := json.Marshal(data)
	if err != nil {
		return TokenResponse{}, err
	}

	resp, err := http.Post(
		"https://console.anthropic.com/v1/oauth/token",
		"application/json",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return TokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return TokenResponse{}, &ProviderAuthError{
			Provider: "anthropic",
			Message:  fmt.Sprintf("status %d", resp.StatusCode),
		}
	}

	var t TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return TokenResponse{}, err
	}
	return t, nil
}

// exchange auth code for tokens, splitting code and state by #, matches ts
// stores oauth tokens and expiry via Set in oauth info object
// strict 30 line limit, no extra responsibilities
func AnthropicExchange(code, verifier string) error {
	splits := strings.Split(code, "#")
	data := map[string]string{
		"code":          splits[0],
		"grant_type":    "authorization_code",
		"client_id":     AnthropicClientID,
		"redirect_uri":  AnthropicRedirectURI,
		"code_verifier": verifier,
	}
	if len(splits) > 1 && splits[1] != "" {
		data["state"] = splits[1]
	}

	t, err := postToken(data)
	if err != nil {
		return err
	}

	info := map[string]any{
		"type":    "oauth",
		"refresh": t.RefreshToken,
		"access":  t.AccessToken,
		"expires": time.Now().UnixMilli() + int64(t.ExpiresIn)*1000,
	}

	return Set("anthropic", info)
}

func AnthropicRefreshToken() (string, error) {
	cred, err := Get("anthropic")
	if err != nil {
		return "", err
	}

	m, ok := cred.(map[string]any)
	if !ok || m["type"] != "oauth" {
		return "", &ProviderAuthError{
			Provider: "anthropic",
			Message:  "missing oauth creds",
		}
	}

	refresh, ok := m["refresh"].(string)
	if !ok || refresh == "" {
		return "", &ProviderAuthError{
			Provider: "anthropic",
			Message:  "missing refresh token",
		}
	}

	t, err := postToken(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refresh,
		"client_id":     AnthropicClientID,
	})
	if err != nil {
		return "", err
	}

	newInfo := map[string]any{
		"type":    "oauth",
		"refresh": t.RefreshToken,
		"access":  t.AccessToken,
		"expires": time.Now().UnixMilli() + int64(t.ExpiresIn)*1000,
	}

	if err := Set("anthropic", newInfo); err != nil {
		return "", err
	}

	return t.AccessToken, nil
}

func AnthropicAccess() (string, error) {
	token, err := GetCredentialsWithRefresh("anthropic")
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("no anthropic credentials")
	}
	return token, nil
}

// AddAnthropicHeaders sets Authorization and required beta headers on req.
func AddAnthropicHeaders(req *http.Request) error {
	access, err := AnthropicAccess()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14")
	req.Header.Set("User-Agent", fmt.Sprintf("claude-cli/%s (external, cli)", claude.GetClaudeVersion()))
	req.Header.Del("x-api-key")
	req.Header.Del("X-Api-Key") // ensure canonical header removal
	return nil
}
