package utils

import (
	"context"
	"net"
	"net/http"

	"github.com/sipeed/picoclaw/pkg/safehttp"
)

type SafeHTTPClientOptions = safehttp.SafeHTTPClientOptions
type PrivateHostWhitelist = safehttp.PrivateHostWhitelist

func NewPrivateHostWhitelist(entries []string) (*PrivateHostWhitelist, error) {
	return safehttp.NewPrivateHostWhitelist(entries)
}

func CreateSafeHTTPClient(opts SafeHTTPClientOptions) (*http.Client, error) {
	return safehttp.CreateSafeHTTPClient(opts)
}

func ValidateSafeHTTPURL(urlStr string, whitelist *PrivateHostWhitelist, allowPrivateHosts func() bool) error {
	return safehttp.ValidateSafeHTTPURL(urlStr, whitelist, allowPrivateHosts)
}

func NewSafeDialContext(
	dialer *net.Dialer,
	whitelist *PrivateHostWhitelist,
	allowPrivateHosts func() bool,
) func(context.Context, string, string) (net.Conn, error) {
	return safehttp.NewSafeDialContext(dialer, whitelist, allowPrivateHosts)
}

func AllowConfiguredProxyFirstHop(req *http.Request, rt http.RoundTripper) {
	safehttp.AllowConfiguredProxyFirstHop(req, rt)
}

func IsObviousPrivateHost(
	host string,
	whitelist *PrivateHostWhitelist,
	allowPrivateHosts func() bool,
) bool {
	return safehttp.IsObviousPrivateHost(host, whitelist, allowPrivateHosts)
}

func IsPrivateOrRestrictedIP(ip net.IP) bool {
	return safehttp.IsPrivateOrRestrictedIP(ip)
}
