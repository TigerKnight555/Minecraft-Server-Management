package dropbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
)

// Publish zips the client profile dirs, uploads them and posts the shared
// link to Discord — gemeinsamer Kern für den Mods-Tab-Button und die
// automatische Veröffentlichung nach dem Ein-Klick-MC-Update.
func Publish(ctx context.Context, c *Client, dirs map[string]string, bus *events.Bus, log *slog.Logger) error {
	name := "/MSM/mc-clientpack-" + time.Now().Format("2006-01-02") + ".zip"

	pr, pw := io.Pipe()
	files := 0
	go func() {
		n, err := ZipDirs(pw, dirs)
		files = n
		pw.CloseWithError(err)
	}()
	fail := func(err error) error {
		log.Error("client-pack publish failed", "err", err)
		bus.Publish(events.Event{
			Type: events.TypeClientPack, Severity: events.SevError,
			Title: "⚠️ Mod-Paket-Upload fehlgeschlagen", Message: "Info für den Admin — Details unten.",
			Fields: []events.Field{{Name: "Details", Value: err.Error()}},
		})
		return err
	}
	if err := c.Upload(ctx, name, pr); err != nil {
		return fail(err)
	}
	link, err := c.ShareLink(ctx, name)
	if err != nil {
		return fail(err)
	}
	bus.Publish(events.Event{
		Type: events.TypeClientPack, Severity: events.SevSuccess,
		Title:   "📦 Neues Mod-Paket zum Download!",
		Message: "Download: " + link + "\nZIP in den bestehenden .minecraft-Ordner entpacken — Karten, Wegpunkte und Configs bleiben erhalten.",
		Fields: []events.Field{
			{Name: "Dateien", Value: fmt.Sprint(files)},
			{Name: "Stand", Value: time.Now().Format("02.01.2006")},
		},
	})
	return nil
}
