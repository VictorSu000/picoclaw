package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/web/backend/launcherconfig"
	"github.com/sipeed/picoclaw/web/backend/middleware"
)

const (
	externalAppFrontendPrefix = "/_external-app/"
	externalAppBackendPrefix  = "/api/external/"
)

// ExternalAppInfo represents public info about an external app for the frontend.
type ExternalAppInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// registerExternalAppRoutes registers routes for external app discovery and proxying.
func (h *Handler) registerExternalAppRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/launcher/external-apps", h.handleGetExternalApps)
	mux.HandleFunc(externalAppFrontendPrefix, h.handleExternalAppFrontend)
	mux.HandleFunc(externalAppBackendPrefix, h.handleExternalAppBackend)
}

// handleGetExternalApps returns the configured applications without exposing upstream URLs.
func (h *Handler) handleGetExternalApps(w http.ResponseWriter, _ *http.Request) {
	cfg, err := h.loadLauncherConfig()
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to load launcher config: %v", err))
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	apps := make([]ExternalAppInfo, len(cfg.ExternalApps))
	for i, app := range cfg.ExternalApps {
		apps[i] = ExternalAppInfo{
			ID:   app.ID,
			Name: app.Name,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apps)
}

// handleExternalAppFrontend serves a split app's static files or proxies an
// integrated app at /_external-app/{appID}/.
func (h *Handler) handleExternalAppFrontend(w http.ResponseWriter, r *http.Request) {
	appID, pathSuffix, ok := splitExternalAppPath(r.URL.Path, externalAppFrontendPrefix)
	if !ok {
		http.Error(w, "missing app id", http.StatusBadRequest)
		return
	}

	app, ok := h.findExternalApp(w, appID)
	if !ok {
		return
	}

	if strings.TrimSpace(app.ServiceURL) != "" {
		h.proxyExternalApp(
			w,
			r,
			externalAppProxyOptions{
				TargetURL:            app.ServiceURL,
				PathSuffix:           pathSuffix,
				PublicPrefix:         externalAppMountPath(externalAppFrontendPrefix, appID),
				PreservePrefix:       app.PreservePrefix,
				ScopeCookiesToPrefix: true,
			},
		)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.serveExternalAppStaticFile(w, r, app, pathSuffix)
}

// handleExternalAppBackend proxies a split app's backend at /api/external/{appID}/.
func (h *Handler) handleExternalAppBackend(w http.ResponseWriter, r *http.Request) {
	appID, pathSuffix, ok := splitExternalAppPath(r.URL.Path, externalAppBackendPrefix)
	if !ok {
		http.Error(w, "missing app id", http.StatusBadRequest)
		return
	}

	app, ok := h.findExternalApp(w, appID)
	if !ok {
		return
	}
	if strings.TrimSpace(app.ServiceURL) != "" {
		http.Error(w, "External app backend proxy not found", http.StatusNotFound)
		return
	}

	h.proxyExternalApp(
		w,
		r,
		externalAppProxyOptions{
			TargetURL:            app.BackendURL,
			PathSuffix:           pathSuffix,
			PublicPrefix:         externalAppMountPath(externalAppBackendPrefix, appID),
			PreservePrefix:       app.PreservePrefix,
			ScopeCookiesToPrefix: false,
		},
	)
}

func splitExternalAppPath(requestPath, prefix string) (appID, pathSuffix string, ok bool) {
	trimmed := strings.TrimPrefix(requestPath, prefix)
	if trimmed == requestPath || trimmed == "" {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if parts[0] == "" {
		return "", "", false
	}
	if len(parts) == 1 || parts[1] == "" {
		return parts[0], "/", true
	}
	return parts[0], "/" + strings.TrimLeft(parts[1], "/"), true
}

func externalAppMountPath(prefix, appID string) string {
	return strings.TrimRight(prefix, "/") + "/" + appID
}

func (h *Handler) findExternalApp(w http.ResponseWriter, appID string) (launcherconfig.ExternalApp, bool) {
	cfg, err := h.loadLauncherConfig()
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to load launcher config: %v", err))
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return launcherconfig.ExternalApp{}, false
	}
	for _, app := range cfg.ExternalApps {
		if app.ID == appID {
			return app, true
		}
	}
	http.Error(w, "External app not found", http.StatusNotFound)
	return launcherconfig.ExternalApp{}, false
}

func (h *Handler) serveExternalAppStaticFile(
	w http.ResponseWriter,
	r *http.Request,
	app launcherconfig.ExternalApp,
	pathSuffix string,
) {
	fullPath, err := ValidateExternalAppPath(strings.TrimSpace(app.BasePath), pathSuffix)
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Path validation failed for app %s: %v", app.ID, err))
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access file", http.StatusInternalServerError)
		}
		return
	}
	if info.IsDir() {
		indexPath := filepath.Join(fullPath, "index.html")
		indexInfo, statErr := os.Stat(indexPath)
		if statErr != nil || indexInfo.IsDir() {
			http.Error(w, "Directory listing not allowed", http.StatusForbidden)
			return
		}
		fullPath = indexPath
	}

	http.ServeFile(w, r, fullPath)
}

type externalAppProxyOptions struct {
	TargetURL            string
	PathSuffix           string
	PublicPrefix         string
	PreservePrefix       bool
	ScopeCookiesToPrefix bool
}

func (h *Handler) proxyExternalApp(w http.ResponseWriter, r *http.Request, opts externalAppProxyOptions) {
	target, err := parseExternalAppURL(opts.TargetURL)
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Invalid external app target: %v", err))
		http.Error(w, "Invalid external app configuration", http.StatusInternalServerError)
		return
	}
	upstreamPath := opts.PathSuffix
	if opts.PreservePrefix {
		upstreamPath = r.URL.Path
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyReq *httputil.ProxyRequest) {
			proxyReq.Out.URL.Path = upstreamPath
			proxyReq.Out.URL.RawPath = ""
			proxyReq.SetURL(target)
			proxyReq.SetXForwarded()
			proxyReq.Out.Header.Set("X-Forwarded-Prefix", opts.PublicPrefix)
			removeLauncherAuthCookie(proxyReq.Out)
		},
		ModifyResponse: func(resp *http.Response) error {
			rewriteExternalAppLocation(resp, target, opts.PublicPrefix)
			rewriteExternalAppCookies(resp, target, opts.PublicPrefix, opts.ScopeCookiesToPrefix)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
			logger.ErrorC("api", fmt.Sprintf("Failed to proxy external app: %v", proxyErr))
			http.Error(w, "External app unavailable", http.StatusBadGateway)
		},
		FlushInterval: -1 * time.Second,
	}
	proxy.ServeHTTP(w, r)
}

func removeLauncherAuthCookie(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != middleware.LauncherDashboardCookieName {
			r.AddCookie(cookie)
		}
	}
}

func rewriteExternalAppLocation(resp *http.Response, target *url.URL, publicPrefix string) {
	rawLocation := resp.Header.Get("Location")
	if rawLocation == "" {
		return
	}
	location, err := url.Parse(rawLocation)
	if err != nil {
		return
	}

	if location.IsAbs() || location.Host != "" {
		if !strings.EqualFold(location.Host, target.Host) {
			return
		}
		if location.Scheme != "" && !strings.EqualFold(location.Scheme, target.Scheme) {
			return
		}
		location.Scheme = ""
		location.Host = ""
	} else if !strings.HasPrefix(location.Path, "/") {
		return
	}

	location.Path = externalAppPublicPath(location.Path, target.Path, publicPrefix)
	location.RawPath = ""
	resp.Header.Set("Location", location.String())
}

func rewriteExternalAppCookies(
	resp *http.Response,
	target *url.URL,
	publicPrefix string,
	scopeToPrefix bool,
) {
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if cookie.Name == middleware.LauncherDashboardCookieName {
			continue
		}
		cookie.Domain = ""
		if scopeToPrefix {
			cookie.Path = externalAppPublicPath(cookie.Path, target.Path, publicPrefix)
		} else {
			// Split apps use separate frontend and API prefixes, so a narrower
			// cookie path would make non-HttpOnly CSRF cookies invisible to the UI.
			cookie.Path = "/"
		}
		resp.Header.Add("Set-Cookie", cookie.String())
	}
}

func stripExternalAppTargetPath(upstreamPath, targetBasePath string) string {
	if upstreamPath == "" {
		return "/"
	}
	basePath := strings.TrimRight(targetBasePath, "/")
	if basePath == "" {
		return upstreamPath
	}
	if upstreamPath == basePath {
		return "/"
	}
	if strings.HasPrefix(upstreamPath, basePath+"/") {
		return strings.TrimPrefix(upstreamPath, basePath)
	}
	return upstreamPath
}

func joinExternalAppPath(prefix, suffix string) string {
	prefix = strings.TrimRight(prefix, "/")
	if suffix == "" || suffix == "/" {
		return prefix + "/"
	}
	return prefix + "/" + strings.TrimLeft(suffix, "/")
}

func externalAppPublicPath(upstreamPath, targetBasePath, publicPrefix string) string {
	pathWithoutTargetBase := stripExternalAppTargetPath(upstreamPath, targetBasePath)
	cleanPrefix := strings.TrimRight(publicPrefix, "/")
	if pathWithoutTargetBase == cleanPrefix || strings.HasPrefix(pathWithoutTargetBase, cleanPrefix+"/") {
		return pathWithoutTargetBase
	}
	return joinExternalAppPath(cleanPrefix, pathWithoutTargetBase)
}

// ValidateExternalAppPath checks that a static file request stays inside basePath.
func ValidateExternalAppPath(basePath, requestedPath string) (string, error) {
	cleanBase := filepath.Clean(basePath)
	requestedPath = strings.TrimLeft(requestedPath, `/\`)
	cleanPath := filepath.Clean(filepath.Join(cleanBase, requestedPath))
	relativePath, err := filepath.Rel(cleanBase, cleanPath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected")
	}
	return cleanPath, nil
}

func parseExternalAppURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL must have a host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("URL must not contain a query or fragment")
	}
	return u, nil
}
