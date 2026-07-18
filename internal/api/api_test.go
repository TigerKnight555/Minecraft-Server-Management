package api

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
)

func testServer(t *testing.T) (*Server, *collector.Collector, context.CancelFunc) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	docker := mock.NewDocker()
	rcon := mock.NewRCON()
	store := mock.NewStore()
	coll := collector.New(collector.Config{
		ContainerInterval: 50 * time.Millisecond,
		HostInterval:      50 * time.Millisecond,
		MCInterval:        50 * time.Millisecond,
		WANInterval:       50 * time.Millisecond,
	}, docker, mock.NewMC(), mock.NewHost(), mock.NewWAN(), store, log)
	ctx, cancel := context.WithCancel(context.Background())
	go coll.Run(ctx)
	srv := New(Deps{
		Collector:  coll,
		Docker:     docker,
		Controller: docker,
		RCON:       rcon,
		Store:      store,
		Admin:      store,
		Managed:    []string{"mc-fabric"},
		Log:        log,
	})
	return srv, coll, cancel
}

func TestSnapshotEndpoint(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()
	time.Sleep(150 * time.Millisecond) // let pollers run once

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/snapshot", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var snap collector.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Containers) != 4 {
		t.Errorf("containers = %d, want 4 (inkl. mc-backup)", len(snap.Containers))
	}
	if !snap.MC.Online {
		t.Error("MC.Online = false, want true (mock)")
	}
}

func TestRCONEndpoint(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()

	body := strings.NewReader(`{"command":"list"}`)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/rcon", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp["response"], "players online") {
		t.Errorf("unexpected rcon response: %q", resp["response"])
	}
}

func TestRCONEndpointRejectsEmpty(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/rcon", strings.NewReader(`{"command":"  "}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestStatsStreamSendsSnapshotFirst(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancelReq := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelReq()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/stream/stats", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	scanner := bufio.NewScanner(resp.Body)
	var firstEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			firstEvent = strings.TrimPrefix(line, "event: ")
			break
		}
	}
	if firstEvent != "snapshot" {
		t.Errorf("first event = %q, want snapshot", firstEvent)
	}
}

func TestContainerActionAllowlist(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()
	time.Sleep(150 * time.Millisecond) // collector must know the containers

	// allowed container
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/containers/mc-fabric/restart", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("allowed action: status = %d, body %s", rec.Code, rec.Body.String())
	}

	// container outside the allowlist
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/containers/webseite/stop", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-allowlisted action: status = %d, want 403", rec.Code)
	}

	// unknown action verb
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/containers/mc-fabric/explode", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: status = %d, want 400", rec.Code)
	}
}

func TestRoutineCRUDAndAudit(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()

	// create
	body := `{"name":"Nachtneustart","cron":"30 4 * * *","kind":"announce-restart","payload":"mc-fabric","warnMinutes":5,"enabled":true}`
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/routines", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body.String())
	}

	// invalid cron rejected
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/routines",
		strings.NewReader(`{"name":"x","cron":"quatsch","kind":"rcon","payload":"list"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid cron: status = %d, want 400", rec.Code)
	}

	// list contains the routine
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/routines", nil))
	var routines []map[string]any
	json.NewDecoder(rec.Body).Decode(&routines)
	if len(routines) != 1 {
		t.Fatalf("routines = %d, want 1", len(routines))
	}

	// audit log recorded the create
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/audit", nil))
	var audit []map[string]any
	json.NewDecoder(rec.Body).Decode(&audit)
	found := false
	for _, e := range audit {
		if e["action"] == "routine.create" {
			found = true
		}
	}
	if !found {
		t.Errorf("audit log missing routine.create: %v", audit)
	}
}

func TestHistoryRequiresSeries(t *testing.T) {
	srv, _, cancel := testServer(t)
	defer cancel()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/history", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
