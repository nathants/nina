package oauth

// manages auth json store and api/oauth helpers with ms expiry timestamps

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type OAuthInfo struct {
	Type    string `json:"type"`
	Refresh string `json:"refresh"`
	Access  string `json:"access"`
	Expires int64  `json:"expires"`
}

type Info any

var authFilePath = filepath.Join(os.Getenv("HOME"), ".nina", "auth.json")

func ensureAuthFile() error {
	dir := filepath.Dir(authFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	if _, err := os.Stat(authFilePath); os.IsNotExist(err) {
		if err := os.WriteFile(authFilePath, []byte("{}"), 0600); err != nil {
			return err
		}
	}

	return nil
}

func All() (map[string]any, error) {
	if err := ensureAuthFile(); err != nil {
		return nil, err
	}

	file, err := os.Open(authFilePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func Get(provider string) (any, error) {
	all, err := All()
	if err != nil {
		return nil, err
	}

	info, ok := all[provider]
	if !ok {
		return nil, nil
	}

	return info, nil
}

func Set(provider string, info any) error {
	if err := ensureAuthFile(); err != nil {
		return err
	}

	all, err := All()
	if err != nil {
		return err
	}

	all[provider] = info

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(authFilePath, data, 0600)
}

func Remove(provider string) error {
	all, err := All()
	if err != nil {
		return err
	}

	delete(all, provider)

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(authFilePath, data, 0600)
}

// GetCredentials returns API key or OAuth access token for a provider
// Prefers OAuth if available and valid, falls back to API key
func GetCredentials(provider string) (string, error) {
	info, err := Get(provider)
	if err != nil {
		return "", err
	}

	if info == nil {
		// Check environment variable fallback
		envKey := ""
		switch provider {
		case "anthropic":
			envKey = os.Getenv("ANTHROPIC_API_KEY")
		default:
			// No environment variable for other providers
		}
		if envKey != "" {
			return envKey, nil
		}
		return "", fmt.Errorf("no credentials found for %s", provider)
	}

	// Check if it's OAuth info
	if m, ok := info.(map[string]any); ok {
		if m["type"] == "oauth" {
			// Check if access token is still valid

			if expires, ok := m["expires"].(float64); ok {
				if time.Now().UnixMilli() < int64(expires) {

					if access, ok := m["access"].(string); ok && access != "" {
						return access, nil
					}
				}
			}
			return "", fmt.Errorf("OAuth token expired for %s, please login again", provider)
		}
	}

	return "", fmt.Errorf("invalid credential format for %s", provider)
}
