// credential retrieval and automatic refresh helpers

package oauth

import (
	"fmt"
	"sync"
	"time"
)

var (
	credentialsMu sync.Mutex
)

// GetCredentialsWithRefresh gets credentials and refreshes OAuth tokens if needed.
func GetCredentialsWithRefresh(provider string) (string, error) {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()

	info, err := Get(provider)
	if err != nil {
		return "", err
	}

	if info == nil {
		return "", fmt.Errorf("no credentials found for %s", provider)
	}

	// Check if it's OAuth info
	if m, ok := info.(map[string]any); ok {
		if m["type"] == "oauth" {
			// Check if access token is still valid

			if expires, ok := m["expires"].(float64); ok {
				if time.Now().Add(5*time.Minute).UnixMilli() < int64(expires) {
					if access, ok := m["access"].(string); ok && access != "" {
						return access, nil
					}
				}
			}

			// Token expired, try to refresh for supported providers
			switch provider {
			case "anthropic":
				newAccess, err := AnthropicRefreshToken()
				if err != nil {
					return "", fmt.Errorf("failed to refresh token: %v", err)
				}
				return newAccess, nil
			default:
				return "", fmt.Errorf("OAuth token expired for %s, please login again", provider)
			}
		}
	}

	return "", fmt.Errorf("invalid credential format for %s", provider)
}
