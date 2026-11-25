package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/auth"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/jvmstats"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
)

type fakePerf struct {
	status jvmstats.Status
	lags   []jvmstats.LagEvent
	since  time.Time
	limit  int
}

func (f *fakePerf) Current() jvmstats.Status { return f.status }

func (f *fakePerf) Lags(_ context.Context, since time.Time, limit int) ([]jvmstats.LagEvent, error) {
	f.since, f.limit = since, limit
	return f.lags, nil
}

func perfHandler(t *testing.T, fp *fakePerf, am *auth.Manager, store *mock.Store) http.Handler {
	t.Helper()
	var p PerfSource
	if fp != nil {
		p = fp // kein typisiertes nil ins Interface
	}
	if store == nil {
		store = mock.NewStore()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Deps{Perf: p, Store: store, Auth: am, Log: log}).Handler()
}

func get(h http.Handler, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	return rec
}

func TestPerfEndpoint(t *testing.T) {
	cpu := 97.5
	fp := &fakePerf{status: jvmstats.Status{
		Threads:         jvmstats.Availability{OK: true},
		GCLog:           jvmstats.Availability{Reason: "GC-Log nicht eingeschaltet"},
		ServerThreadCPU: &cpu,
		Groups:          []jvmstats.ThreadGroup{{Name: "Server thread", Count: 1, CPU: 97.5}},
	}}
	rec := get(perfHandler(t, fp, nil, nil), "/api/perf")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Threads         jvmstats.Availability  `json:"threads"`
		GCLog           jvmstats.Availability  `json:"gcLog"`
		ServerThreadCPU *float64               `json:"serverThreadCpu"`
		HeapAfterGC     *uint64                `json:"heapAfterGc"`
		Groups          []jvmstats.ThreadGroup `json:"groups"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Threads.OK || body.GCLog.Reason != "GC-Log nicht eingeschaltet" ||
		body.ServerThreadCPU == nil || *body.ServerThreadCPU != 97.5 || body.HeapAfterGC != nil || len(body.Groups) != 1 {
		t.Errorf("body = %+v", body)
	}
}

func TestPerfLags(t *testing.T) {
	fp := &fakePerf{lags: []jvmstats.LagEvent{{ID: 7, Trigger: "gc", Diagnosis: "x"}}}
	h := perfHandler(t, fp, nil, nil)

	rec := get(h, "/api/perf/lags?days=3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var lags []jvmstats.LagEvent
	json.NewDecoder(rec.Body).Decode(&lags)
	if len(lags) != 1 || lags[0].ID != 7 || fp.limit != 500 {
		t.Errorf("lags = %+v, limit = %d", lags, fp.limit)
	}
	if d := time.Since(fp.since) - 72*time.Hour; d < -time.Minute || d > time.Minute {
		t.Errorf("since = %v, want vor 3 Tagen", fp.since)
	}

	get(h, "/api/perf/lags")
	if d := time.Since(fp.since) - 7*24*time.Hour; d < -time.Minute || d > time.Minute {
		t.Errorf("Default since = %v, want vor 7 Tagen", fp.since)
	}
	for _, bad := range []string{"0", "31", "x"} {
		if rec := get(h, "/api/perf/lags?days="+bad); rec.Code != http.StatusBadRequest {
			t.Errorf("days=%s: status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestPerfLagsEmptyIsArray(t *testing.T) {
	rec := get(perfHandler(t, &fakePerf{}, nil, nil), "/api/perf/lags")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("body = %q, want []", rec.Body.String())
	}
}

func TestPerfRoutesOffWithoutSource(t *testing.T) {
	if rec := get(perfHandler(t, nil, nil, nil), "/api/perf"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPerfNeedsLogin(t *testing.T) {
	hash, err := auth.HashPassword("geheim")
	if err != nil {
		t.Fatal(err)
	}
	am := auth.NewManager(hash, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := perfHandler(t, &fakePerf{}, am, nil)
	for _, url := range []string{"/api/perf", "/api/perf/lags"} {
		if rec := get(h, url); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", url, rec.Code)
		}
	}
}

func TestHistoryFromTo(t *testing.T) {
	store := mock.NewStore()
	base := time.Now().Add(-3 * 24 * time.Hour).Truncate(time.Second)
	store.WriteSamples(context.Background(), []collector.Sample{
		{Series: "jvm.gc.pause_max", Time: base, Value: 1},
		{Series: "jvm.gc.pause_max", Time: base.Add(100 * time.Second), Value: 2},
		{Series: "jvm.gc.pause_max", Time: base.Add(200 * time.Second), Value: 3},
	})
	h := perfHandler(t, nil, nil, store)
	from, to := base.Add(50*time.Second).Unix(), base.Add(150*time.Second).Unix()
	rec := get(h, "/api/history?series=jvm.gc.pause_max&from="+itoa(from)+"&to="+itoa(to))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got []collector.Sample
	json.NewDecoder(rec.Body).Decode(&got)
	if len(got) != 1 || got[0].Value != 2 {
		t.Errorf("got %+v, want nur den mittleren Punkt", got)
	}
	for _, q := range []string{
		"&from=" + itoa(to) + "&to=" + itoa(from), // to vor from
		"&from=" + itoa(from),                     // to fehlt
		"&from=x&to=" + itoa(to),
		"&from=0&to=" + itoa(40*24*3600), // mehr als 30 Tage
	} {
		if rec := get(h, "/api/history?series=jvm.gc.pause_max"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestDownsample(t *testing.T) {
	var in []collector.Sample
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3000; i++ {
		v := 1.0
		if i == 1001 {
			v = 900 // Spitze
		}
		in = append(in, collector.Sample{Series: "s", Time: base.Add(time.Duration(i) * 5 * time.Second), Value: v})
	}
	mean := downsample(in, 1500, false)
	peak := downsample(in, 1500, true)
	if len(mean) != 1500 || len(peak) != 1500 {
		t.Fatalf("len = %d/%d, want 1500", len(mean), len(peak))
	}
	if mean[500].Value != 450.5 || peak[500].Value != 900 {
		t.Errorf("Eimer 500: mean %v (want 450.5), peak %v (want 900)", mean[500].Value, peak[500].Value)
	}
	if !peak[500].Time.Equal(in[1001].Time) {
		t.Errorf("Zeit = %v, want letzter Punkt im Eimer", peak[500].Time)
	}
	short := in[:10]
	if got := downsample(short, 1500, false); len(got) != 10 {
		t.Errorf("kurze Reihe verändert: %d", len(got))
	}
}
