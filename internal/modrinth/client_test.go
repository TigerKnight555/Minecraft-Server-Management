package modrinth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProjectSupportsQueryEncoding fängt Learning 14: die JSON-Array-Filter
// müssen URL-encodiert ankommen. Der Fake prüft wie Modrinth die dekodierten
// Query-Parameter — rohe Klammern/Anführungszeichen ergäben hier 400.
func TestProjectSupportsQueryEncoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/project/AABBCCDD/version" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		loaders := r.URL.Query().Get("loaders")
		games := r.URL.Query().Get("game_versions")
		if loaders != `["fabric"]` || games != `["26.2"]` {
			http.Error(w, "unparsable filters: "+loaders+" / "+games, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode([]Version{{ID: "v1", VersionNumber: "1.0+26.2"}})
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL)
	ok, err := c.ProjectSupports(context.Background(), "AABBCCDD", "fabric", "26.2")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("supports = false, want true")
	}
}

func TestProjectSupportsFalseOnEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	ok, err := NewWithBase(srv.URL).ProjectSupports(context.Background(), "X", "fabric", "26.2")
	if err != nil || ok {
		t.Errorf("ok=%v err=%v, want false ohne Fehler", ok, err)
	}
}

func TestCreateVersionMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/version" {
			http.Error(w, "wrong route", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "mrp_test" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var meta NewVersion
		if err := json.Unmarshal([]byte(r.FormValue("data")), &meta); err != nil {
			http.Error(w, "data: "+err.Error(), http.StatusBadRequest)
			return
		}
		if meta.ProjectID != "P1" || meta.VersionNumber != "1.0.0" || len(meta.FileParts) != 1 ||
			meta.FileParts[0] != "file" || meta.PrimaryFile != "file" || meta.Dependencies == nil {
			http.Error(w, "bad meta", http.StatusBadRequest)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file: "+err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(f)
		if hdr.Filename != "pack-1.0.0.mrpack" || hdr.Header.Get("Content-Type") != "application/x-modrinth-modpack+zip" || string(body) != "ZIPDATA" {
			http.Error(w, "bad file part", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(Version{ID: "V1", VersionNumber: "1.0.0"})
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL)
	v, err := c.CreateVersion(context.Background(), "mrp_test", NewVersion{
		Name: "1.0.0 (MC 26.3 · Fabric 0.20.1)", VersionNumber: "1.0.0", GameVersions: []string{"26.3"},
		VersionType: "release", Loaders: []string{"fabric"}, Featured: true, ProjectID: "P1",
	}, "pack-1.0.0.mrpack", []byte("ZIPDATA"))
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "V1" {
		t.Errorf("version = %+v", v)
	}
}

func TestAPIErrorCarriesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := NewWithBase(srv.URL).CurrentUser(context.Background(), "bad")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *APIError 401", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestAuthProjectSendsTokenAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/project/mein-pack" || r.Header.Get("Authorization") != "mrp_x" {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(Project{ID: "P1", Slug: "mein-pack", Title: "Mein Pack", ProjectType: "modpack", Status: "draft"})
	}))
	defer srv.Close()
	p, err := NewWithBase(srv.URL).AuthProject(context.Background(), "mrp_x", "mein-pack")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "P1" || p.Status != "draft" || p.ProjectType != "modpack" {
		t.Errorf("project = %+v", p)
	}
}

func TestVersionFileSHA1Decoded(t *testing.T) {
	var v Version
	json.Unmarshal([]byte(`{"files":[{"url":"https://cdn.modrinth.com/x.jar","hashes":{"sha1":"aa","sha512":"bb"},"size":3}]}`), &v)
	if v.Files[0].Hashes.SHA1 != "aa" || v.Files[0].Hashes.SHA512 != "bb" {
		t.Errorf("hashes = %+v", v.Files[0].Hashes)
	}
}
