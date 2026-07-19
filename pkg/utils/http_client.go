package utils

import (
	"net/http"
	"time"

	"github.com/sipeed/picoclaw/pkg/safehttp"
)

// CreateHTTPClient creates an HTTP client with optional proxy support.
// If proxyURL is empty, it uses the system environment proxy settings.
// Supported proxy schemes: http, https, socks5, socks5h.
func CreateHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	return safehttp.CreateHTTPClient(proxyURL, timeout)
}
