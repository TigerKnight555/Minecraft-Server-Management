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

func TestModrinthConfigDBBeforeEnv(t *testing.T) {
	t.Setenv("MSM_MODRINTH_PROJECT", "env-pack")
	t.Setenv("MSM_MODRINTH_TOKEN", "env-token")
	db := map[string]string{}
	s := New(func(k string) string { return db[k] }, func(k, v string) { db[k] = v })
	if c := s.ModrinthConfig(); c.Project != "env-pack" || c.Token != "env-token" || c.StartVersion != "" {
		t.Errorf("env-fallback = %+v", c)
	}
	s.Set(KeyModrinthProject, "mein-pack")
	s.Set(KeyModrinthStart, "2.0.0")
	c := s.ModrinthConfig()
	if c.Project != "mein-pack" || c.Token != "env-token" || c.StartVersion != "2.0.0" {
		t.Errorf("config = %+v", c)
	}
	v := s.View()
	if !v.ModrinthReady || v.ModrinthProject.Source != "dashboard" || v.ModrinthToken.Source != "env" {
		t.Errorf("view = %+v", v)
	}
	vals := s.Values()
	if vals["modrinthProject"] != "mein-pack" || vals["modrinthToken"] != "env-token" || vals["modrinthStartVersion"] != "2.0.0" {
		t.Errorf("values = %v", vals)
	}
}

// Ohne Eintrag (bestehende Installationen) bleibt der Autoversand an.
func TestAutoPublishDefaultsOn(t *testing.T) {
	s, kv := newMem()
	if !s.AutoPublish() || !s.View().AutoPublish {
		t.Fatal("Standard muss an sein")
	}
	s.SetAutoPublish(false)
	if kv[KeyAutoPublish] != "off" || s.AutoPublish() || s.View().AutoPublish {
		t.Fatalf("aus: kv=%q", kv[KeyAutoPublish])
	}
	s.SetAutoPublish(true)
	if kv[KeyAutoPublish] != "" || !s.AutoPublish() {
		t.Fatalf("wieder an: kv=%q", kv[KeyAutoPublish])
	}
}
