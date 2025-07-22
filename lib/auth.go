
// Package lib provides high-level authentication interface wrapping oauth providers.
// Supports interactive login/logout and credential listing across multiple AI providers.

package lib

import (
	"fmt"
	"strings"

	"nina/providers/oauth"
)

func List() error {
	creds, err := oauth.All()
	if err != nil {
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	if len(creds) == 0 {
		fmt.Println("No stored credentials found.")
		return nil
	}

	fmt.Println("Stored credentials:")
	for provider := range creds {
		fmt.Printf("  - %s\n", provider)
	}

	return nil
}

func Login(provider string) error {
	provider = strings.ToLower(provider)

	switch provider {
	case "anthropic", "claude":
		result, err := oauth.AnthropicAuthorize()
		if err != nil {
			return fmt.Errorf("anthropic authorization failed: %w", err)
		}

		fmt.Printf("Please visit this URL to authorize:\n%s\n\n", result.URL)
		fmt.Print("Enter the authorization code: ")
		
		var code string
		_, err = fmt.Scanln(&code)
		if err != nil {
			return fmt.Errorf("failed to read authorization code: %w", err)
		}

		return oauth.AnthropicExchange(code, result.Verifier)

	case "gemini", "google":
		return oauth.LoginGemini()

	case "openai":
		return oauth.LoginOpenAI()

	case "":
		return fmt.Errorf("provider required. Use: anthropic, gemini, or openai")

	default:
		return fmt.Errorf("unsupported provider: %s. Use: anthropic, gemini, or openai", provider)
	}
}

func Logout(provider string) error {
	if provider == "" {
		creds, err := oauth.All()
		if err != nil {
			return fmt.Errorf("failed to get credentials: %w", err)
		}

		if len(creds) == 0 {
			fmt.Println("No stored credentials to remove.")
			return nil
		}

		fmt.Println("Available providers:")
		for p := range creds {
			fmt.Printf("  - %s\n", p)
		}
		return fmt.Errorf("specify provider to logout from")
	}

	provider = strings.ToLower(provider)
	
	err := oauth.Remove(provider)
	if err != nil {
		return fmt.Errorf("failed to remove credentials for %s: %w", provider, err)
	}

	fmt.Printf("Logged out from %s\n", provider)
	return nil
}
