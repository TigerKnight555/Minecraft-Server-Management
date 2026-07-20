package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeSignal struct {
	mu   sync.Mutex
	tags []string
}

func (f *fakeSignal) RequestSelfUpdate(tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags = append(f.tags, tag)
	return nil
}

func tagServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	type tag struct {
		Name string `json:"name"`
	}
	var tags []tag
	for _, n := range names {
		tags = append(tags, tag{Name: n})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tags") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(tags)
	}))
}

func TestCheckFindsHighestSemver(t *testing.T) {
	// absichtlich unsortiert + ein nicht-semver-Tag
	srv := tagServer(t, "v1.2.0", "experiment", "v1.10.1", "v1.9.9")
	defer srv.Close()
	c := New("o/r", "", "v1.2.0", &fakeSignal{})
	c.SetAPIBase(srv.URL)

	st, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Latest != "v1.10.1" || !st.Newer {
		t.Fatalf("status = %+v, want v1.10.1 als neuer", st)
	}
}

func TestCheckCurrentIsLatest(t *testing.T) {
	srv := tagServer(t, "v1.2.0", "v1.1.0")
	defer srv.Close()
	c := New("o/r", "", "v1.2.0", &fakeSignal{})
	c.SetAPIBase(srv.URL)
	st, _ := c.Check(context.Background())
	if st.Newer {
		t.Fatalf("status = %+v, want kein Update (läuft schon)", st)
	}
}

func TestDevBuildAlwaysUpdatable(t *testing.T) {
	srv := tagServer(t, "v0.1.0")
	defer srv.Close()
	// dev-Build (git-Hash) gilt als älter als jeder echte Tag
	c := New("o/r", "", "3aa2222", &fakeSignal{})
	c.SetAPIBase(srv.URL)
	st, _ := c.Check(context.Background())
	if !st.Newer {
		t.Fatalf("status = %+v, want Update erlaubt für dev-Build", st)
	}
}

func TestApplyGuards(t *testing.T) {
	srv := tagServer(t, "v2.0.0")
	defer srv.Close()
	sig := &fakeSignal{}
	c := New("o/r", "", "v1.0.0", sig)
	c.SetAPIBase(srv.URL)

	if err := c.Apply("v2.0.0"); err == nil || !strings.Contains(err.Error(), "kein Versions-Check") {
		t.Errorf("apply ohne Check: %v", err)
	}
	c.Check(context.Background())
	if err := c.Apply("v9.9.9"); err == nil || !strings.Contains(err.Error(), "nicht der geprüfte") {
		t.Errorf("falscher Tag akzeptiert: %v", err)
	}
	if err := c.Apply(`v2.0.0"; rm -rf`); err == nil {
		t.Error("injection-Tag akzeptiert")
	}
	if err := c.Apply("v2.0.0"); err != nil {
		t.Fatalf("gültiger Apply: %v", err)
	}
	if len(sig.tags) != 1 || sig.tags[0] != "v2.0.0" {
		t.Errorf("signale = %v", sig.tags)
	}
}

func TestSemverParsing(t *testing.T) {
	cases := []struct {
		a, b string
		aGEb bool
	}{
		{"v1.2.3", "v1.2.2", true},
		{"1.2.3", "v1.2.3", true},
		{"v2.0.0-rc1", "v2.0.0", true}, // Suffix wird ignoriert
		{"v1.9.0", "v1.10.0", false},
		{"abc123", "v0.0.1", false}, // dev-Build < jeder Tag
	}
	for _, c := range cases {
		if got := newerOrEqual(c.a, c.b); got != c.aGEb {
			t.Errorf("newerOrEqual(%q,%q) = %v, want %v", c.a, c.b, got, c.aGEb)
		}
	}
}
