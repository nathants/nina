// login handles OAuth authentication for Anthropic Claude models
// supports browser-based OAuth flow with automatic token storage
// tokens are stored in environment for subsequent API calls
package auth

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/alexflint/go-arg"
	oauth "nina/providers/oauth"
)


type loginArgs struct {
	Provider string `arg:"-p,--provider" default:"anthropic" help:"Provider to login to (anthropic)"`
}

func (loginArgs) Description() string {
	return `login - Authenticate with AI providers using OAuth

Performs OAuth login for Anthropic Claude API.
Opens browser for authentication and stores token.`
}

func login() {
	var args loginArgs
	arg.MustParse(&args)

	switch args.Provider {
	case "anthropic":
		if err := loginAnthropic(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown provider: %s\n", args.Provider)
		os.Exit(1)
	}
}

func loginAnthropic() error {
	authResult, err := oauth.AnthropicAuthorize()
	if err != nil {
		return fmt.Errorf("authorization error: %w", err)
	}

	fmt.Println("Trying to open browser...")
	if err := openBrowser(authResult.URL); err != nil {
		fmt.Println("Failed to open browser. Please open the following URL manually:")
	}
	fmt.Println("\n" + authResult.URL)

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("\nPaste the authorization code here: ")
	code, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("error reading code: %w", err)
	}
	code = strings.TrimSpace(code)

	if err := oauth.AnthropicExchange(code, authResult.Verifier); err != nil {
		return fmt.Errorf("error exchanging code: %w", err)
	}

	// The token is now stored by oauth.AnthropicExchange
	fmt.Println("\nSuccessfully authenticated with Anthropic!")
	fmt.Println("Credentials stored for future use.")

	return nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "linux":
		cmd = "xdg-open"
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	default:
		return fmt.Errorf("unsupported platform")
	}

	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
