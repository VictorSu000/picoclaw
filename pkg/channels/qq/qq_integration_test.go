//go:build integration

package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"

	"github.com/tencent-connect/botgo/constant"
)

// TestQQBotEcho runs an integration test against the real QQ Bot API.
//
// Usage:
//
//	go test -tags integration -run TestQQBotEcho ./pkg/channels/qq/ -v \
//	  -qq_app_id=YOUR_APP_ID -qq_app_secret=YOUR_APP_SECRET
func TestQQBotEcho(t *testing.T) {
	appID := envOrFlag("QQBOT_APP_ID", "qq_app_id")
	appSecret := envOrFlag("QQBOT_APP_SECRET", "qq_app_secret")

	if appID == "" || appSecret == "" {
		t.Fatal("app_id and app_secret are required.\n" +
			"  Pass via flags:   -qq_app_id=xxx -qq_app_secret=xxx\n" +
			"  Or env vars:      QQBOT_APP_ID=xxx QQBOT_APP_SECRET=xxx")
	}

	// --- Step 1: Manual token fetch ---
	fmt.Println("========== Step 1/3: Fetching access token ==========")
	tokenURL := "https://bots.qq.com/app/getAppAccessToken"
	tokenReqBody, _ := json.Marshal(map[string]string{
		"appId":        appID,
		"clientSecret": appSecret,
	})

	tokenHTTPResp, err := http.Post(tokenURL, "application/json", bytes.NewReader(tokenReqBody))
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	defer tokenHTTPResp.Body.Close()
	tokenRespBody, _ := io.ReadAll(tokenHTTPResp.Body)

	var tokenResp struct {
		Code        int    `json:"code"`
		Message     string `json:"message"`
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"`
	}
	if err := json.Unmarshal(tokenRespBody, &tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokenResp.Code != 0 {
		t.Fatalf("token error: code=%d message=%s", tokenResp.Code, tokenResp.Message)
	}
	accessToken := tokenResp.AccessToken
	if len(accessToken) > 16 {
		fmt.Printf("  token OK (len=%d, expires=%v)\n", len(accessToken), tokenResp.ExpiresIn)
	}

	// --- Step 2: Manual gateway request ---
	fmt.Println("========== Step 2/3: GET /gateway/bot ==========")
	gatewayURL := constant.APIDomain + "/gateway/bot"
	gatewayReq, _ := http.NewRequest("GET", gatewayURL, nil)
	gatewayReq.Header.Set("Authorization", "QQBot "+accessToken)
	gatewayReq.Header.Set("X-Union-Appid", appID)

	gatewayHTTPResp, err := http.DefaultClient.Do(gatewayReq)
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	defer gatewayHTTPResp.Body.Close()
	gatewayRespBody, _ := io.ReadAll(gatewayHTTPResp.Body)

	if gatewayHTTPResp.StatusCode != 200 {
		t.Fatalf("gateway returned status %d: %s", gatewayHTTPResp.StatusCode, string(gatewayRespBody))
	}
	fmt.Printf("  gateway OK (status %d)\n", gatewayHTTPResp.StatusCode)

	// --- Step 3: Start bot ---
	fmt.Println("========== Step 3/3: Starting bot ==========")
	settingsJSON, _ := json.Marshal(map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	})

	bc := &config.Channel{
		Enabled:   true,
		Type:      "qq",
		AllowFrom: []string{},
	}
	bc.SetName("qq")
	bc.Settings = settingsJSON

	qqSettings := &config.QQSettings{
		AppID:     appID,
		AppSecret: *config.NewSecureString(appSecret),
	}

	messageBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()

	ch, err := NewQQChannel(bc, qqSettings, messageBus)
	if err != nil {
		t.Fatalf("NewQQChannel() error = %v", err)
	}
	ch.SetMediaStore(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = ch.Stop(shutCtx)
	}()

	fmt.Println("========================================")
	fmt.Println("  QQ Bot Integration Test Running")
	fmt.Println("  Send a message to the bot on QQ.")
	fmt.Println("  Press Ctrl+C to stop.")
	fmt.Println("========================================")

	// Echo: outbound listener
	go func() {
		for msg := range messageBus.OutboundChan() {
			fmt.Printf("[OUTBOUND] chat=%s content=%q\n", msg.ChatID, msg.Content)
			msgIDs, err := ch.Send(ctx, msg)
			if err != nil {
				fmt.Printf("[OUTBOUND ERROR] %v\n", err)
				continue
			}
			fmt.Printf("[OUTBOUND SENT] ids=%v\n", msgIDs)
		}
	}()

	// Echo: inbound → reply "hello"
	go func() {
		for inbound := range messageBus.InboundChan() {
			fmt.Printf("[INBOUND] channel=%s chat=%s sender=%s content=%q\n",
				inbound.Channel, inbound.ChatID, inbound.SenderID, inbound.Content)
			reply := bus.OutboundMessage{
				Channel: "qq",
				ChatID:  inbound.ChatID,
				Context: inbound.Context,
				Content: "hello",
			}
			if err := messageBus.PublishOutbound(ctx, reply); err != nil {
				fmt.Printf("[REPLY ERROR] %v\n", err)
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	cancel()
	time.Sleep(500 * time.Millisecond)
}

func envOrFlag(envKey, flagName string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	prefix := "-" + flagName + "="
	for i, arg := range os.Args {
		if arg == "-"+flagName && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
		if len(arg) > len(prefix) && arg[:len(prefix)] == prefix {
			return arg[len(prefix):]
		}
	}
	return ""
}
