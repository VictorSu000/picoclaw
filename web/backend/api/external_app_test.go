package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/sipeed/picoclaw/web/backend/launcherconfig"
)

func TestExternalAppHTTPProxyPreservesRequestAndRewritesResponse(t *testing.T) {
	var upstream *httptest.Server
	var gotBody string
	var gotCookie string
	var gotPrefix string
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotCookie = r.Header.Get("Cookie")
		gotPrefix = r.Header.Get("X-Forwarded-Prefix")
		if r.URL.Path != "/base/items" || r.URL.RawQuery != "page=2" {
			t.Fatalf("upstream URL = %s, want /base/items?page=2", r.URL.RequestURI())
		}
		if r.Header.Get("X-Forwarded-Proto") != "http" {
			t.Fatalf("X-Forwarded-Proto = %q, want http", r.Header.Get("X-Forwarded-Proto"))
		}
		w.Header().Set("Location", upstream.URL+"/base/login?next=1")
		w.Header().Add("Set-Cookie", "external=session; Path=/base; Domain=localhost; HttpOnly")
		w.Header().Add("Set-Cookie", "picoclaw_launcher_auth=overwritten; Path=/; HttpOnly")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("redirecting"))
	}))
	defer upstream.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := launcherconfig.Default()
	cfg.ExternalApps = []launcherconfig.ExternalApp{{
		ID:         "split",
		Name:       "Split",
		BasePath:   t.TempDir(),
		BackendURL: upstream.URL + "/base",
	}}
	if err := launcherconfig.Save(launcherconfig.PathForAppConfig(configPath), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerExternalAppRoutes(mux)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/external/split/items?page=2",
		strings.NewReader("request-body"),
	)
	req.Header.Set("Cookie", "picoclaw_launcher_auth=secret; external=old")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusFound, rec.Body.String())
	}
	if gotBody != "request-body" {
		t.Fatalf("upstream body = %q, want request-body", gotBody)
	}
	if strings.Contains(gotCookie, "picoclaw_launcher_auth") {
		t.Fatalf("upstream received launcher auth cookie: %q", gotCookie)
	}
	if !strings.Contains(gotCookie, "external=old") {
		t.Fatalf("upstream cookie = %q, want external cookie", gotCookie)
	}
	if gotPrefix != "/api/external/split" {
		t.Fatalf("X-Forwarded-Prefix = %q, want /api/external/split", gotPrefix)
	}
	if got := rec.Header().Get("Location"); got != "/api/external/split/login?next=1" {
		t.Fatalf("Location = %q, want /api/external/split/login?next=1", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "external" || cookies[0].Path != "/" || cookies[0].Domain != "" {
		t.Fatalf("rewritten cookies = %#v, want root-scoped external cookie", cookies)
	}
}

func TestIntegratedExternalAppProxiesHTTPFromFrontendMount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/service/api/settings" || r.URL.RawQuery != "dry_run=true" {
			t.Fatalf("upstream request = %s %s, want PUT /service/api/settings?dry_run=true", r.Method, r.URL.RequestURI())
		}
		if got := r.Header.Get("X-Forwarded-Prefix"); got != "/_external-app/console" {
			t.Fatalf("X-Forwarded-Prefix = %q, want /_external-app/console", got)
		}
		w.Header().Add("Set-Cookie", "integrated=session; Path=/service; HttpOnly")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := launcherconfig.Default()
	cfg.ExternalApps = []launcherconfig.ExternalApp{{
		ID:         "console",
		Name:       "Console",
		ServiceURL: upstream.URL + "/service",
	}}
	if err := launcherconfig.Save(launcherconfig.PathForAppConfig(configPath), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerExternalAppRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(
		rec,
		httptest.NewRequest(http.MethodPut, "/_external-app/console/api/settings?dry_run=true", nil),
	)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Path != "/_external-app/console/" {
		t.Fatalf("rewritten cookies = %#v, want cookie scoped to integrated app", cookies)
	}
}

func TestExternalAppWebSocketProxySupportsSplitAndIntegratedApps(t *testing.T) {
	tests := []struct {
		name       string
		publicPath string
		configure  func(string) launcherconfig.ExternalApp
	}{
		{
			name:       "split",
			publicPath: "/api/external/split/ws?channel=events",
			configure: func(url string) launcherconfig.ExternalApp {
				return launcherconfig.ExternalApp{ID: "split", Name: "Split", BasePath: t.TempDir(), BackendURL: url}
			},
		},
		{
			name:       "integrated",
			publicPath: "/_external-app/integrated/ws?channel=events",
			configure: func(url string) launcherconfig.ExternalApp {
				return launcherconfig.ExternalApp{ID: "integrated", Name: "Integrated", ServiceURL: url}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ws" || r.URL.Query().Get("channel") != "events" {
					http.Error(w, "unexpected upstream URL", http.StatusBadRequest)
					return
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				messageType, message, err := conn.ReadMessage()
				if err == nil {
					_ = conn.WriteMessage(messageType, message)
				}
			}))
			defer upstream.Close()

			configPath := filepath.Join(t.TempDir(), "config.json")
			cfg := launcherconfig.Default()
			cfg.ExternalApps = []launcherconfig.ExternalApp{tt.configure(upstream.URL)}
			if err := launcherconfig.Save(launcherconfig.PathForAppConfig(configPath), cfg); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			h := NewHandler(configPath)
			mux := http.NewServeMux()
			h.registerExternalAppRoutes(mux)
			server := httptest.NewServer(mux)
			defer server.Close()

			wsURL := strings.Replace(server.URL, "http://", "ws://", 1) + tt.publicPath
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Dial() error = %v", err)
			}
			defer conn.Close()
			if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
				t.Fatalf("WriteMessage() error = %v", err)
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage() error = %v", err)
			}
			if string(message) != "hello" {
				t.Fatalf("echo = %q, want hello", message)
			}
		})
	}
}

func TestValidateExternalAppPathRejectsTraversal(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "app")
	if _, err := ValidateExternalAppPath(basePath, "../outside"); err == nil {
		t.Fatal("ValidateExternalAppPath() accepted traversal")
	}
	got, err := ValidateExternalAppPath(basePath, "/assets/app.js")
	if err != nil {
		t.Fatalf("ValidateExternalAppPath() error = %v", err)
	}
	want := filepath.Join(basePath, "assets", "app.js")
	if got != want {
		t.Fatalf("ValidateExternalAppPath() = %q, want %q", got, want)
	}
}

func TestExternalAppFrontendServesStaticFilesForSplitApp(t *testing.T) {
	basePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(basePath, "index.html"), []byte("<html>ok</html>"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := launcherconfig.Default()
	cfg.ExternalApps = []launcherconfig.ExternalApp{{
		ID:         "static",
		Name:       "Static",
		BasePath:   basePath,
		BackendURL: "http://127.0.0.1:9999",
	}}
	if err := launcherconfig.Save(launcherconfig.PathForAppConfig(configPath), cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerExternalAppRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_external-app/static/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html>ok</html>") {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}
