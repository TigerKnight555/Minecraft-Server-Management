package dropbox

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/events"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/packpub"
)

// Pack zips the client profile dirs, uploads them and returns the shared
// link — ohne Discord-Meldung (die baut packpub gemeinsam für alle Kanäle).
func Pack(ctx context.Context, c *Client, dirs map[string]string) (string, int, error) {
	name := "/MSM/mc-clientpack-" + time.Now().Format("2006-01-02") + ".zip"
	pr, pw := io.Pipe()
	files := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := ZipDirs(pw, dirs)
		files = n
		pw.CloseWithError(err)
	}()
	if err := c.Upload(ctx, name, pr); err != nil {
		// Leseseite schließen, sonst blockiert die ZIP-Goroutine für immer
		// im Schreiben (Goroutine + offene Dateien leaken je Fehlversuch)
		pr.CloseWithError(err)
		<-done
		return "", 0, err
	}
	<-done
	link, err := c.ShareLink(ctx, name)
	if err != nil {
		return "", 0, err
	}
	return link, files, nil
}

// Channel is the Dropbox distribution channel for packpub.
type Channel struct {
	C    *Client
	Dirs map[string]string
}

func (ch Channel) Name() string  { return "dropbox" }
func (ch Channel) Label() string { return "Dropbox" }
func (ch Channel) Ready() bool   { return ch.C != nil && ch.Dirs != nil && ch.C.Ready() }

func (ch Channel) Publish(ctx context.Context) (packpub.Outcome, error) {
	link, files, err := Pack(ctx, ch.C, ch.Dirs)
	if err != nil {
		return packpub.Outcome{}, err
	}
	return packpub.Outcome{
		Lines: []string{"Download (ZIP): " + link + "\nIn den bestehenden .minecraft-Ordner entpacken — Karten, Wegpunkte und Configs bleiben erhalten."},
		Fields: []events.Field{
			{Name: "Dateien", Value: fmt.Sprint(files)},
			{Name: "Stand", Value: time.Now().Format("02.01.2006")},
		},
	}, nil
}
