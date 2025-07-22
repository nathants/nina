// logout removes stored authentication credentials for AI providers
// supports removing credentials for specific provider or prompts to choose
// uses lib.Logout which handles actual credential removal from storage
package auth

import (
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	"nina/lib"
)

type authLogoutArgs struct {
	Provider string `arg:"positional" help:"Provider to logout from (anthropic, gemini, openai)"`
}

func (authLogoutArgs) Description() string {
	return `logout - Remove stored authentication credentials

Removes stored OAuth tokens for AI providers.
If no provider specified, lists available providers.`
}

func authLogout() {
	var args authLogoutArgs
	arg.MustParse(&args)

	if err := lib.Logout(args.Provider); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
