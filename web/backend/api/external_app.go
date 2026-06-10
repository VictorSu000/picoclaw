package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/web/backend/launcherconfig"
)

// ExternalAppInfo represents public info about an external app for the frontend.
type ExternalAppInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
}

// registerExternalAppRoutes registers routes for external app management and proxying.
func (h *Handler) registerExternalAppRoutes(mux *http.ServeMux) {
	// Get list of external apps (public endpoint)
	mux.HandleFunc("/api/launcher/external-apps", h.handleGetExternalApps)

	// Serve static files from external app base paths (prefix)
	mux.HandleFunc("/_external-app/", h.handleExternalAppStaticFiles)

	// Proxy requests to external apps (prefix)
	mux.HandleFunc("/api/external/", h.handleExternalAppProxy)
}

// handleGetExternalApps returns the list of configured external applications.
// Only returns basic info (id, name, icon) without sensitive backend URLs.
func (h *Handler) handleGetExternalApps(w http.ResponseWriter, r *http.Request) {
	launcherCfgPath := launcherconfig.PathForAppConfig(h.configPath)
	cfg, err := launcherconfig.Load(launcherCfgPath, launcherconfig.Default())
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
			Icon: app.Icon,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apps)
}

// handleExternalAppStaticFiles serves static files from the external app's base path.
// Path format: /_external-app/{appId}/* -> {basePath}/{remaining path}
func (h *Handler) handleExternalAppStaticFiles(w http.ResponseWriter, r *http.Request) {
	// Expected path: /_external-app/{appId}/*
	// Extract appId and suffix from URL path since plain ServeMux does not
	// support templated patterns.
	trimmed := strings.TrimPrefix(r.URL.Path, "/_external-app/")
	// trimmed may be "" or "{appId}" or "{appId}/rest/of/path"
	var appID, pathSuffix string
	if trimmed == "" {
		http.Error(w, "missing app id", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(trimmed, "/", 2)
	appID = parts[0]
	if len(parts) == 2 {
		pathSuffix = parts[1]
	} else {
		pathSuffix = ""
	}

	// Load config to find app settings
	launcherCfgPath := launcherconfig.PathForAppConfig(h.configPath)
	cfg, err := launcherconfig.Load(launcherCfgPath, launcherconfig.Default())
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to load launcher config: %v", err))
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Find the app config
	var appCfg launcherconfig.ExternalApp
	found := false
	for _, app := range cfg.ExternalApps {
		if app.ID == appID {
			appCfg = app
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "External app not found", http.StatusNotFound)
		return
	}

	// Validate and resolve the requested path
	basePath := strings.TrimSpace(appCfg.BasePath)
	requestedPath := "/" + strings.TrimLeft(pathSuffix, "/")

	// Prevent directory traversal
	fullPath, err := ValidateExternalAppPath(basePath, requestedPath)
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Path validation failed for app %s: %v", appID, err))
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// Handle directory requests
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
		// If it's a directory, try to serve index.html
		indexPath := filepath.Join(fullPath, "index.html")
		indexInfo, err := os.Stat(indexPath)
		if err != nil || indexInfo.IsDir() {
			http.Error(w, "Directory listing not allowed", http.StatusForbidden)
			return
		}
		fullPath = indexPath
	}

	// Serve the file
	http.ServeFile(w, r, fullPath)
}

// handleExternalAppProxy proxies requests to the external app's backend.
// Path format: /api/external/{appId}/* -> {backendURL}/{remaining path}
func (h *Handler) handleExternalAppProxy(w http.ResponseWriter, r *http.Request) {
	// Expected path: /api/external/{appId}/*
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/external/")
	var appID, pathSuffix string
	if trimmed == "" {
		http.Error(w, "missing app id", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(trimmed, "/", 2)
	appID = parts[0]
	if len(parts) == 2 {
		pathSuffix = parts[1]
	} else {
		pathSuffix = ""
	}

	// Load config to find app settings
	launcherCfgPath := launcherconfig.PathForAppConfig(h.configPath)
	cfg, err := launcherconfig.Load(launcherCfgPath, launcherconfig.Default())
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to load launcher config: %v", err))
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Find the app config
	var appCfg launcherconfig.ExternalApp
	found := false
	for _, app := range cfg.ExternalApps {
		if app.ID == appID {
			appCfg = app
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "External app not found", http.StatusNotFound)
		return
	}

	// Build target URL
	backendURL := strings.TrimRight(appCfg.BackendURL, "/")
	targetPath := "/" + strings.TrimLeft(pathSuffix, "/")
	targetURL := backendURL + targetPath

	// Parse and preserve query parameters
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Create proxy request
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to create proxy request: %v", err))
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Copy relevant headers from original request
	copyProxyHeaders(r.Header, proxyReq.Header)

	// Execute proxy request
	client := &http.Client{Timeout: 0} // Use default timeout
	resp, err := client.Do(proxyReq)
	if err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to proxy request to %s: %v", targetURL, err))
		http.Error(w, fmt.Sprintf("Failed to connect to external app: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response status and headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Add CORS headers for iframe access
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.ErrorC("api", fmt.Sprintf("Failed to copy response body: %v", err))
	}
}

// copyProxyHeaders copies request headers for proxying, excluding sensitive headers.
func copyProxyHeaders(src http.Header, dst http.Header) {
	// List of headers NOT to proxy
	excludeHeaders := map[string]bool{
		"host":                     true,
		"connection":               true,
		"keep-alive":               true,
		"proxy-authenticate":       true,
		"proxy-authorization":      true,
		"te":                       true,
		"trailers":                 true,
		"transfer-encoding":        true,
		"upgrade":                  true,
		"content-length":           true, // Handled by http.Client
		"x-forwarded-for":          true,
		"x-forwarded-proto":        true,
		"x-forwarded-host":         true,
		"x-original-forwarded-for": true,
	}

	for key, values := range src {
		keyLower := strings.ToLower(key)
		if !excludeHeaders[keyLower] {
			for _, value := range values {
				dst.Add(key, value)
			}
		}
	}
}

// ValidateExternalAppPath checks if the given path is safe (no directory traversal).
func ValidateExternalAppPath(basePath, requestedPath string) (string, error) {
	// Clean the paths
	cleanBase := filepath.Clean(basePath)
	fullPath := filepath.Join(cleanBase, requestedPath)
	cleanPath := filepath.Clean(fullPath)

	// Ensure the resolved path is within the base path
	if !strings.HasPrefix(cleanPath, cleanBase) {
		return "", fmt.Errorf("path traversal detected")
	}

	return cleanPath, nil
}

// parseBackendURL validates and parses a backend URL.
func parseBackendURL(urlStr string) (*url.URL, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("backend URL scheme must be http or https")
	}

	if u.Host == "" {
		return nil, fmt.Errorf("backend URL must have a host")
	}

	return u, nil
}
