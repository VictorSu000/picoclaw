package api

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestComputeConfigSignature_ChangesForAdditionalToolToggles(t *testing.T) {
	cfg := config.DefaultConfig()

	before := computeConfigSignature(cfg)

	cfg.Tools.Subagent.Enabled = !cfg.Tools.Subagent.Enabled
	afterSubagent := computeConfigSignature(cfg)
	if before == afterSubagent {
		t.Fatal("config signature should change when subagent changes")
	}

	before = afterSubagent
	cfg.Tools.LoadImage.Enabled = !cfg.Tools.LoadImage.Enabled
	afterLoadImage := computeConfigSignature(cfg)
	if before == afterLoadImage {
		t.Fatal("config signature should change when load_image changes")
	}

	before = afterLoadImage
	cfg.Tools.SendTTS.Enabled = !cfg.Tools.SendTTS.Enabled
	afterSendTTS := computeConfigSignature(cfg)
	if before == afterSendTTS {
		t.Fatal("config signature should change when send_tts changes")
	}

	before = afterSendTTS
	cfg.Tools.Serial.Enabled = !cfg.Tools.Serial.Enabled
	afterSerial := computeConfigSignature(cfg)
	if before == afterSerial {
		t.Fatal("config signature should change when serial changes")
	}
}
