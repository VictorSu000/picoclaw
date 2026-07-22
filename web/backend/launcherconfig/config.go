package launcherconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	// FileName is the launcher-specific settings file name.
	FileName = "launcher-config.json"
	// DefaultPort is the default port for the web launcher.
	DefaultPort = 18800
	// EnvLauncherHost overrides launcher listen host.
	EnvLauncherHost = "PICOCLAW_LAUNCHER_HOST"
)

// ExternalApp defines an external application to be served via the launcher.
type ExternalApp struct {
	ID             string `json:"id"`                        // Unique identifier for the app
	Name           string `json:"name"`                      // Display name for sidebar menu
	BasePath       string `json:"base_path,omitempty"`       // OS path to a split app's frontend files
	BackendURL     string `json:"backend_url,omitempty"`     // URL of a split app's backend
	ServiceURL     string `json:"service_url,omitempty"`     // URL of an integrated frontend/backend service
	PreservePrefix bool   `json:"preserve_prefix,omitempty"` // Keep the public mount prefix in proxied paths
}

// Config stores launch parameters for the web backend service.
type Config struct {
	Port                       int             `json:"port"`
	Public                     bool            `json:"public"`
	AllowedCIDRs               []string        `json:"allowed_cidrs,omitempty"`
	AllowLocalhostBypass       bool            `json:"allow_localhost_bypass"`
	AllowLocalhostBypassSource BoolFieldSource `json:"-"`
	TrustedProxyCIDRs          []string        `json:"trusted_proxy_cidrs,omitempty"`
	DashboardPasswordHash      string          `json:"dashboard_password_hash,omitempty"`
	// LegacyLauncherToken is read only for one-time migration from the removed
	// token login flow. Save always clears it so new configs do not persist it.
	LegacyLauncherToken string        `json:"launcher_token,omitempty"`
	ExternalApps        []ExternalApp `json:"external_apps,omitempty"`
}

// BoolFieldSource tracks whether a JSON boolean field was omitted, explicitly
// provided, or explicitly set to null. This is only used for diagnostics.
type BoolFieldSource uint8

const (
	BoolFieldAbsent BoolFieldSource = iota
	BoolFieldPresent
	BoolFieldNull
)

// Default returns default launcher settings.
func Default() Config {
	return Config{Port: DefaultPort, Public: false, AllowLocalhostBypass: true}
}

// Validate checks if launcher settings are valid.
func Validate(cfg Config) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", cfg.Port)
	}
	for _, cidr := range cfg.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid CIDR %q", cidr)
		}
	}
	for _, cidr := range cfg.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy CIDR %q", cidr)
		}
	}
	seenAppIDs := make(map[string]struct{}, len(cfg.ExternalApps))
	for _, app := range cfg.ExternalApps {
		if strings.TrimSpace(app.ID) == "" {
			return fmt.Errorf("external app: id cannot be empty")
		}
		if !validExternalAppID(app.ID) {
			return fmt.Errorf("external app %q: id must contain only letters, digits, '.', '_' or '-'", app.ID)
		}
		if _, exists := seenAppIDs[app.ID]; exists {
			return fmt.Errorf("external app %q: duplicate id", app.ID)
		}
		seenAppIDs[app.ID] = struct{}{}
		if strings.TrimSpace(app.Name) == "" {
			return fmt.Errorf("external app %q: name cannot be empty", app.ID)
		}

		basePath := strings.TrimSpace(app.BasePath)
		backendURL := strings.TrimSpace(app.BackendURL)
		serviceURL := strings.TrimSpace(app.ServiceURL)
		if serviceURL != "" {
			if basePath != "" || backendURL != "" {
				return fmt.Errorf(
					"external app %q: service_url cannot be combined with base_path or backend_url",
					app.ID,
				)
			}
			if err := validateExternalAppURL("service_url", serviceURL); err != nil {
				return fmt.Errorf("external app %q: %w", app.ID, err)
			}
			continue
		}

		if basePath == "" {
			return fmt.Errorf("external app %q: base_path cannot be empty", app.ID)
		}
		if backendURL == "" {
			return fmt.Errorf("external app %q: backend_url cannot be empty", app.ID)
		}
		if err := validateExternalAppURL("backend_url", backendURL); err != nil {
			return fmt.Errorf("external app %q: %w", app.ID, err)
		}
	}
	return nil
}

func validExternalAppID(id string) bool {
	if id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return id != ""
}

func validateExternalAppURL(field, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", field, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s scheme must be http or https", field)
	}
	if u.Host == "" {
		return fmt.Errorf("%s must have a host", field)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must not contain a query or fragment", field)
	}
	return nil
}

// NormalizeCIDRs trims entries, removes empty values, and deduplicates CIDRs.
func NormalizeCIDRs(cidrs []string) []string {
	if len(cidrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(cidrs))
	seen := make(map[string]struct{}, len(cidrs))
	for _, raw := range cidrs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PathForAppConfig returns launcher-config path near the app config file.
func PathForAppConfig(appConfigPath string) string {
	dir := filepath.Dir(appConfigPath)
	if dir == "" || dir == "." {
		dir = "."
	}
	return filepath.Join(dir, FileName)
}

// Load reads launcher settings; fallback is returned when file does not exist.
func Load(path string, fallback Config) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, nil
		}
		return Config{}, err
	}

	cfg := fallback
	cfg.AllowLocalhostBypassSource = detectBoolFieldSource(data, "allow_localhost_bypass")
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.AllowedCIDRs = NormalizeCIDRs(cfg.AllowedCIDRs)
	cfg.TrustedProxyCIDRs = NormalizeCIDRs(cfg.TrustedProxyCIDRs)
	cfg.DashboardPasswordHash = strings.TrimSpace(cfg.DashboardPasswordHash)
	cfg.LegacyLauncherToken = strings.TrimSpace(cfg.LegacyLauncherToken)
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func detectBoolFieldSource(data []byte, field string) BoolFieldSource {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return BoolFieldAbsent
	}

	value, ok := raw[field]
	if !ok {
		return BoolFieldAbsent
	}

	if string(value) == "null" {
		return BoolFieldNull
	}

	return BoolFieldPresent
}

// Save writes launcher settings to disk.
func Save(path string, cfg Config) error {
	cfg.AllowedCIDRs = NormalizeCIDRs(cfg.AllowedCIDRs)
	cfg.TrustedProxyCIDRs = NormalizeCIDRs(cfg.TrustedProxyCIDRs)
	cfg.DashboardPasswordHash = strings.TrimSpace(cfg.DashboardPasswordHash)
	cfg.LegacyLauncherToken = ""
	if err := Validate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
