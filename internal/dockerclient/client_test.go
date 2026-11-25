package dockerclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInspectContainerPid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.44/containers/mc-fabric/json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"State":{"Running":true,"Pid":4242,"ExitCode":0,
			"StartedAt":"2026-11-25T10:00:00Z","FinishedAt":"0001-01-01T00:00:00Z"},
			"RestartCount":1,"Config":{"Env":["A=B"]}}`))
	}))
	defer srv.Close()
	det, err := New(srv.URL).InspectContainer(context.Background(), "mc-fabric")
	if err != nil {
		t.Fatal(err)
	}
	if det.Pid != 4242 || !det.Running || det.RestartCount != 1 {
		t.Errorf("det = %+v, want Pid 4242", det)
	}
}
