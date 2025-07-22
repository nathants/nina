// DefaultTransport wrapper that injects Anthropics OAuth headers when the
// target host is api.anthropic.com. Other hosts are left untouched.

package oauth

import (
	"net/http"
	"strings"
)

type authTransport struct {
	base http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only inject on API host, skip console token endpoints to avoid refresh loops
	if strings.Contains(req.URL.Host, "api.anthropic.com") {
		_ = AddAnthropicHeaders(req)
	}
	return t.base.RoundTrip(req)
}

func init() {
	base := http.DefaultTransport
	if _, ok := base.(*authTransport); ok {
		return
	}
	http.DefaultTransport = &authTransport{base: base}
}
