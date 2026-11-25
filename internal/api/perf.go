package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/jvmstats"
)

// PerfSource liefert den Leistungs-Stand (jvmstats.Monitor).
type PerfSource interface {
	Current() jvmstats.Status
	Lags(ctx context.Context, since time.Time, limit int) ([]jvmstats.LagEvent, error)
}

const maxLags = 500

// handlePerf: aktueller Stand — Kacheln, Top-15-Thread-Gruppen, Verfügbarkeit.
func (s *Server) handlePerf(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.perf.Current())
}

// handlePerfLags: /api/perf/lags?days=7 — Lag-Protokoll, neueste zuerst.
func (s *Server) handlePerfLags(w http.ResponseWriter, r *http.Request) {
	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 || n > 30 {
			httpError(w, http.StatusBadRequest, "invalid days parameter")
			return
		}
		days = n
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	lags, err := s.perf.Lags(r.Context(), since, maxLags)
	if err != nil {
		s.log.Error("lag query failed", "err", err)
		httpError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if lags == nil {
		lags = []jvmstats.LagEvent{}
	}
	writeJSON(w, http.StatusOK, lags)
}
