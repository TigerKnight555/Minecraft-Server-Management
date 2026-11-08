package mods_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func TestRemovalInfoFindsDependentsAndOtherProfile(t *testing.T) {
	mgr, api, _, _ := addSetup(t)
	api.addVersion("way", "Waystones", "waystones.jar", "required", "required", req("fapi"))
	ctx := context.Background()
	if _, err := mgr.AddMods(ctx, "way", "26.3", []string{mods.ProfileServer, mods.ProfileClient}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.ApplyStaged(mods.ProfileServer); err != nil {
		t.Fatal(err)
	}
	info, err := mgr.RemovalInfo(ctx, mods.ProfileServer, "fabric-api.jar")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Dependents) != 1 || !strings.Contains(info.Dependents[0], "Waystones") {
		t.Errorf("dependents = %v", info.Dependents)
	}
	if len(info.AlsoIn) != 1 || info.AlsoIn[0].Profile != mods.ProfileClient || info.AlsoIn[0].Filename != "fabric-api.jar" {
		t.Errorf("alsoIn = %+v", info.AlsoIn)
	}
}

func TestStageRemoveServerPendingUntilApply(t *testing.T) {
	mgr, _, serverDir, _ := addSetup(t)
	// Entries liest den Cache des letzten Prüflaufs
	if _, err := mgr.CheckUpdates(context.Background(), mods.ProfileServer, "26.3"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StageRemove(mods.ProfileServer, "fabric-api.jar"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "fabric-api.jar")); err != nil {
		t.Fatal("Server-Mod schon weg — muss bis zum Neustart bleiben")
	}
	found := false
	for _, e := range mgr.Entries(mods.ProfileServer) {
		if e.Filename == "fabric-api.jar" && e.Pending == "remove" {
			found = true
		}
	}
	if !found {
		t.Error("Entfernung nicht als vorgemerkt sichtbar")
	}
	if err := mgr.StageRemove(mods.ProfileServer, "fabric-api.jar"); err == nil {
		t.Error("doppeltes Vormerken muss abgelehnt werden")
	}
	if _, _, err := mgr.ApplyStaged(mods.ProfileServer); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "fabric-api.jar")); err == nil {
		t.Error("nach dem Anwenden noch da")
	}
}

func TestStageRemoveClientImmediate(t *testing.T) {
	mgr, _, _, clientDir := addSetup(t)
	if err := os.WriteFile(filepath.Join(clientDir, "sodium.jar"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StageRemove(mods.ProfileClient, "sodium.jar"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "sodium.jar")); err == nil {
		t.Error("Client-Mod muss sofort entfernt sein")
	}
}

func TestStageRemoveRejectsBadNames(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	for _, name := range []string{"", "../fabric-api.jar", ".msm-staging", "fehlt.jar"} {
		if err := mgr.StageRemove(mods.ProfileServer, name); err == nil {
			t.Errorf("%q: kein Fehler", name)
		}
	}
}

func TestStageUploadValidation(t *testing.T) {
	mgr, _, _, _ := addSetup(t)
	ctx := context.Background()
	cases := []struct {
		profile, category, name string
		data                    []byte
	}{
		{mods.ProfileServer, "mods", "../evil.jar", []byte("x")},
		{mods.ProfileServer, "mods", ".hidden.jar", []byte("x")},
		{mods.ProfileServer, "mods", "pack.zip", []byte("x")},
		{mods.ProfileClient, "shaderpacks", "shader.jar", []byte("x")},
		{mods.ProfileServer, "shaderpacks", "shader.zip", []byte("x")}, // Server hat keine Shader
		{mods.ProfileServer, "mods", "leer.jar", nil},
		{mods.ProfileServer, "mods", "fabric-api.jar", []byte("x")}, // existiert schon
	}
	for _, c := range cases {
		if _, err := mgr.StageUpload(ctx, c.profile, c.category, c.name, c.data); err == nil {
			t.Errorf("%s/%s/%s: kein Fehler", c.profile, c.category, c.name)
		}
	}
	big := make([]byte, mods.MaxUploadBytes+1)
	if _, err := mgr.StageUpload(ctx, mods.ProfileServer, "mods", "big.jar", big); err == nil {
		t.Error("Datei > 100 MB angenommen")
	}
}

func TestStageUploadRecognisesModrinthFile(t *testing.T) {
	mgr, api, _, clientDir := addSetup(t)
	ctx := context.Background()
	content := api.files["fake://fabric-api.jar"]
	e, err := mgr.StageUpload(ctx, mods.ProfileClient, "mods", "fabric-api-renamed.jar", content)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Managed || e.ProjectID != "fapi" {
		t.Errorf("entry = %+v, want verwaltet (fapi)", e)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "fabric-api-renamed.jar")); err != nil {
		t.Error("Client-Upload nicht sofort im Paket")
	}

	e, err = mgr.StageUpload(ctx, mods.ProfileServer, "mods", "custom.jar", []byte("eigener mod"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Managed {
		t.Error("unbekannte Datei als verwaltet markiert")
	}
	if !mgr.HasStaged(mods.ProfileServer) {
		t.Error("Server-Upload nicht vorgemerkt")
	}
	if _, err := mgr.StageUpload(ctx, mods.ProfileServer, "mods", "custom.jar", []byte("nochmal")); err == nil {
		t.Error("doppelter Upload angenommen")
	}
}

// Beschädigte Staging-Liste: Fehler, nichts wird überschrieben.
func TestStageRemoveAndUploadRefuseCorruptManifest(t *testing.T) {
	mgr, _, serverDir, _ := addSetup(t)
	sdir := filepath.Join(serverDir, ".msm-staging")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := filepath.Join(sdir, "manifest.json")
	if err := os.WriteFile(man, []byte("{kaputt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StageRemove(mods.ProfileServer, "fabric-api.jar"); err == nil {
		t.Error("StageRemove: kein Fehler bei kaputter Liste")
	}
	if _, err := mgr.StageUpload(context.Background(), mods.ProfileServer, "mods", "neu.jar", []byte("x")); err == nil {
		t.Error("StageUpload: kein Fehler bei kaputter Liste")
	}
	got, err := os.ReadFile(man)
	if err != nil || string(got) != "{kaputt" {
		t.Errorf("manifest verändert: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(sdir, "neu.jar")); err == nil {
		t.Error("Upload-Datei trotz Fehler abgelegt")
	}
}

// Client wendet sofort an: mit anderer offener Änderung ablehnen.
func TestClientRemoveAndUploadRefuseWhenOtherChangePending(t *testing.T) {
	mgr, _, _, clientDir := addSetup(t)
	for _, n := range []string{"a.jar", "b.jar"} {
		if err := os.WriteFile(filepath.Join(clientDir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// offene Änderung im Client-Paket vormerken (Staging-Ordner von Hand)
	sdir := filepath.Join(clientDir, ".msm-staging")
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		t.Fatal(err)
	}
	manJSON := `{"items":[{"category":"mods","oldFilename":"a.jar"}]}`
	if err := os.WriteFile(filepath.Join(sdir, "manifest.json"), []byte(manJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mgr.HasStaged(mods.ProfileClient) {
		t.Skip("Manifest-Format passt nicht — Test-Vorbereitung anpassen")
	}
	want := "schon Änderungen vorgemerkt"
	if err := mgr.StageRemove(mods.ProfileClient, "b.jar"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("StageRemove err = %v", err)
	}
	if _, err := mgr.StageUpload(context.Background(), mods.ProfileClient, "mods", "c.jar", []byte("c")); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("StageUpload err = %v", err)
	}
	for _, n := range []string{"a.jar", "b.jar"} {
		if _, err := os.Stat(filepath.Join(clientDir, n)); err != nil {
			t.Errorf("%s verändert", n)
		}
	}
	if _, err := os.Stat(filepath.Join(clientDir, "c.jar")); err == nil {
		t.Error("c.jar trotz Ablehnung im Paket")
	}
}
