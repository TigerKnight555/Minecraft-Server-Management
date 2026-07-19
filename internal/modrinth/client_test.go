package modrinth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
