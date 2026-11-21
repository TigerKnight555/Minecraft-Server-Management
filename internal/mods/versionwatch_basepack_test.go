package mods_test

import (
	"context"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mods"
)

func TestCheckReportsBasepack(t *testing.T) {
	mgr, _, _ := setup(t)
	ctx := context.Background()
	w := mods.NewWatcher(mock.NewModrinth(), mgr, "fabric")
	srv := newJSONServer(t, map[string]string{
		"/manifest": `{"latest":{"release":"1.22"}}`,
		"/fabric":   `[{"version":"1.22","stable":true}]`,
	})
	w.SetEndpoints(srv+"/manifest", srv+"/fabric")

	status, err := w.Check(ctx, "1.21.11")
	if err != nil {
		t.Fatal(err)
	}
	if status.Basepack != nil {
		t.Errorf("ohne Setter: Basepack = %+v, want nil", status.Basepack)
	}

	w.SetBasepack(func(context.Context, string) (string, bool, bool) { return "Fabulously Optimized", false, true })
	status, err = w.Check(ctx, "1.21.11")
	if err != nil {
		t.Fatal(err)
	}
	if status.Basepack == nil || status.Basepack.Name != "Fabulously Optimized" || status.Basepack.Ready {
		t.Errorf("Basepack = %+v, want FO/not ready", status.Basepack)
	}
}
