// list displays current authentication status for AI providers
// shows all stored credentials without exposing sensitive tokens
// uses lib.List which reads credentials from storage
package auth

import (
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	"nina/lib"
)

type authListArgs struct {
}

func (authListArgs) Description() string {
	return `list - List current authentication status

Shows stored authentication credentials for AI providers.
Does not display actual tokens for security.`
}

func authList() {
	var args authListArgs
	arg.MustParse(&args)

	if err := lib.List(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
