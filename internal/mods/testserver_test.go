package mods_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newJSONServer serves fixed JSON bodies per path.
func newJSONServer(t *testing.T, routes map[string]string) string {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}
