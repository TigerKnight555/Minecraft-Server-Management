package settings

import (
	"testing"
)

func newMem() (*Store, map[string]string) {
	kv := map[string]string{}
	return New(
		func(k string) string { return kv[k] },
		func(k, v string) { kv[k] = v },
	), kv
}

func TestDashboardValueWinsOverEnv(t *testing.T) {
	s, _ := newMem()
	t.Setenv("MSM_DISCORD_WEBHOOK_URL", "https://discord.com/api/webhooks/env/xxx")

	// ohne DB-Wert: env greift
	hooks := s.DiscordHooks()
	if len(hooks) != 1 || hooks[0].URL != "https://discord.com/api/webhooks/env/xxx" {
		t.Fatalf("hooks = %+v, want env-Fallback", hooks)
	}
	// DB-Wert gewinnt
	s.Set(KeyDiscordWebhook, "https://discord.com/api/webhooks/db/yyy")
	hooks = s.DiscordHooks()
	if len(hooks) != 1 || hooks[0].URL != "https://discord.com/api/webhooks/db/yyy" {
		t.Fatalf("hooks = %+v, want Dashboard-Wert", hooks)
	}
	// Löschen -> env-Fallback wieder aktiv
	s.Set(KeyDiscordWebhook, "")
	hooks = s.DiscordHooks()
	if len(hooks) != 1 || hooks[0].URL != "https://discord.com/api/webhooks/env/xxx" {
		t.Fatalf("hooks = %+v, want env-Fallback nach Löschen", hooks)
	}
}

func TestDropboxPerFieldFallback(t *testing.T) {
	s, _ := newMem()
	t.Setenv("MSM_DROPBOX_APP_KEY", "envkey")
	t.Setenv("MSM_DROPBOX_APP_SECRET", "")
	t.Setenv("MSM_DROPBOX_REFRESH_TOKEN", "envtoken")

	s.Set(KeyDropboxSecret, "dbsecret")
	cfg := s.DropboxConfig()
	if cfg.AppKey != "envkey" || cfg.AppSecret != "dbsecret" || cfg.RefreshToken != "envtoken" {
		t.Fatalf("cfg = %+v, want Feld-weise Mischung", cfg)
	}
	if !cfg.Complete() {
		t.Error("Complete() = false, want true")
	}
}

func TestViewNeverLeaksValues(t *testing.T) {
	s, _ := newMem()
	secret := "supergeheimertoken1234"
	s.Set(KeyDropboxToken, secret)
	v := s.View()
	if !v.DropboxToken.Set || v.DropboxToken.Source != "dashboard" {
		t.Fatalf("view = %+v", v.DropboxToken)
	}
	if v.DropboxToken.Hint != "…1234" {
		t.Errorf("hint = %q, want nur die letzten 4 Zeichen", v.DropboxToken.Hint)
	}
}
