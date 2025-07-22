// ProviderAuthError mirrors the TypeScript Provider.AuthError structure.
// It is kept in a dedicated file so it can be imported without pulling in the
// entire Anthropics flow when only structured error values are needed.

package oauth

import "fmt"

type ProviderAuthError struct {
	Provider string
	Message  string
}

func (e *ProviderAuthError) Error() string {
	return fmt.Sprintf("%s auth error: %s", e.Provider, e.Message)
}
