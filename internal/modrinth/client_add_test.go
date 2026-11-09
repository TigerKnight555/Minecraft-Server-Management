package modrinth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchSendsEncodedFacets(t *testing.T) {
	var gotQuery, gotFacets string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.Error(w, "path "+r.URL.Path, http.StatusNotFound)
			return
		}
		gotQuery = r.URL.Query().Get("query")
		gotFacets = r.URL.Query().Get("facets")
		w.Write([]byte(`{"hits":[{"project_id":"P1","title":"Jade","client_side":"required","server_side":"optional","downloads":5}]}`))
	}))
	defer srv.Close()

	hits, err := NewWithBase(srv.URL).Search(context.Background(), "jade", "fabric", "26.3", 20)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "jade" {
		t.Errorf("query = %q", gotQuery)
	}
	if want := `[["project_type:mod"],["categories:fabric"],["versions:26.3"]]`; gotFacets != want {
		t.Errorf("facets = %q, want %q", gotFacets, want)
	}
	if len(hits) != 1 || hits[0].ProjectID != "P1" || hits[0].ClientSide != "required" || hits[0].Downloads != 5 {
		t.Errorf("hits = %+v", hits)
	}
}

func TestProjectAndVersionsWithDependencies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/P1":
			w.Write([]byte(`{"id":"P1","title":"Waystones","client_side":"required","server_side":"required","project_type":"mod"}`))
		case "/project/P1/version":
			if r.URL.Query().Get("loaders") != `["fabric"]` || r.URL.Query().Get("game_versions") != `["26.3"]` {
				http.Error(w, "bad filter", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`[{"id":"V1","project_id":"P1","version_number":"21.1","files":[{"url":"u","filename":"waystones.jar","primary":true,"hashes":{"sha512":"abc"}}],"dependencies":[{"project_id":"BALM","dependency_type":"required"}]}]`))
		default:
			http.Error(w, "path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewWithBase(srv.URL)

	p, err := c.Project(context.Background(), "P1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Waystones" || p.ServerSide != "required" {
		t.Errorf("project = %+v", p)
	}
	vs, err := c.ProjectVersions(context.Background(), "P1", "fabric", "26.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || len(vs[0].Dependencies) != 1 || vs[0].Dependencies[0].ProjectID != "BALM" || vs[0].Dependencies[0].DependencyType != "required" {
		t.Errorf("versions = %+v", vs)
	}
	ok, err := c.ProjectSupports(context.Background(), "P1", "fabric", "26.3")
	if err != nil || !ok {
		t.Errorf("ProjectSupports = %v, %v", ok, err)
	}
}

func TestProjectsBulkEncodesIDs(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects" {
			http.Error(w, "path "+r.URL.Path, http.StatusNotFound)
			return
		}
		got = r.URL.Query().Get("ids")
		w.Write([]byte(`[{"id":"A","title":"Waystones"},{"id":"B","title":"Balm"}]`))
	}))
	defer srv.Close()
	ps, err := NewWithBase(srv.URL).Projects(context.Background(), []string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if got != `["A","B"]` {
		t.Errorf("ids = %q", got)
	}
	if len(ps) != 2 || ps[0].Title != "Waystones" {
		t.Errorf("projects = %+v", ps)
	}
}
