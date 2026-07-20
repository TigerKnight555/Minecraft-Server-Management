package dropbox

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDropbox emulates token, upload session and sharing endpoints.
type fakeDropbox struct {
	mu       sync.Mutex
	tokens   int
	uploaded map[string][]byte // path -> content
	sessions map[string][]byte
	linkDup  bool // simulate existing shared link
}

func newFake() *fakeDropbox {
	return &fakeDropbox{uploaded: map[string][]byte{}, sessions: map[string][]byte{}}
}

func (f *fakeDropbox) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/token"):
			f.tokens++
			json.NewEncoder(w).Encode(map[string]any{"access_token": "AT", "expires_in": 14400})
		case strings.HasSuffix(r.URL.Path, "/upload_session/start"):
			f.sessions["s1"] = nil
			json.NewEncoder(w).Encode(map[string]string{"session_id": "s1"})
		case strings.HasSuffix(r.URL.Path, "/upload_session/append_v2"):
			body, _ := io.ReadAll(r.Body)
			f.sessions["s1"] = append(f.sessions["s1"], body...)
			w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/upload_session/finish"):
			var arg struct {
				Commit struct {
					Path string `json:"path"`
				} `json:"commit"`
			}
			json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &arg)
			f.uploaded[arg.Commit.Path] = f.sessions["s1"]
			w.Write([]byte("{}"))
		case strings.HasSuffix(r.URL.Path, "/create_shared_link_with_settings"):
			if f.linkDup {
				w.WriteHeader(409)
				w.Write([]byte(`{"error_summary":"shared_link_already_exists/.."}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"url": "https://dropbox/neu"})
		case strings.HasSuffix(r.URL.Path, "/list_shared_links"):
			json.NewEncoder(w).Encode(map[string]any{"links": []map[string]string{{"url": "https://dropbox/alt"}}})
		default:
			http.Error(w, "unexpected "+r.URL.Path, 500)
		}
	}
}

func newTestClient(t *testing.T, f *fakeDropbox) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c := New(Config{AppKey: "k", AppSecret: "s", RefreshToken: "r"})
	c.SetEndpoints(srv.URL+"/oauth2/token", srv.URL, srv.URL)
	return c
}

func TestUploadStreamsInChunks(t *testing.T) {
	f := newFake()
	c := newTestClient(t, f)

	data := bytes.Repeat([]byte("x"), 3*chunkSize/2) // erzwingt 2 Appends
	if err := c.Upload(context.Background(), "/MSM/test.zip", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if got := f.uploaded["/MSM/test.zip"]; !bytes.Equal(got, data) {
		t.Errorf("upload = %d bytes, want %d (inhaltsgleich)", len(got), len(data))
	}
	if f.tokens != 1 {
		t.Errorf("tokens = %d, want 1 (Access-Token wird gecacht)", f.tokens)
	}
}

func TestShareLinkReusesExisting(t *testing.T) {
	f := newFake()
	f.linkDup = true
	c := newTestClient(t, f)
	link, err := c.ShareLink(context.Background(), "/MSM/test.zip")
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://dropbox/alt" {
		t.Errorf("link = %q, want bestehenden Link", link)
	}
}

func TestZipDirs(t *testing.T) {
	base := t.TempDir()
	mods := filepath.Join(base, "mods")
	shaders := filepath.Join(base, "shaderpacks")
	os.MkdirAll(mods, 0o755)
	os.MkdirAll(shaders, 0o755)
	os.WriteFile(filepath.Join(mods, "a.jar"), []byte("AAA"), 0o644)
	os.WriteFile(filepath.Join(mods, ".hidden"), []byte("x"), 0o644) // ignorieren
	os.WriteFile(filepath.Join(shaders, "s.zip"), []byte("SSS"), 0o644)

	var buf bytes.Buffer
	n, err := ZipDirs(&buf, map[string]string{"mods": mods, "shaderpacks": shaders, "resourcepacks": filepath.Join(base, "fehlt")})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("files = %d, want 2", n)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	want := []string{"mods/a.jar", "shaderpacks/s.zip"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("zip[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestConfigComplete(t *testing.T) {
	if (Config{AppKey: "k"}).Complete() {
		t.Error("unvollständige Config als komplett gemeldet")
	}
	if !(Config{AppKey: "k", AppSecret: "s", RefreshToken: "r"}).Complete() {
		t.Error("vollständige Config als unvollständig gemeldet")
	}
}
