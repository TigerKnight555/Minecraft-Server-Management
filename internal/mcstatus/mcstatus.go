// Package mcstatus combines the Query protocol (players, version, MOTD) with
// TPS/MSPT via RCON into one MCStatus. Primary source is spark; because
// spark answers asynchronously over RCON (response often arrives after the
// RCON reply and is lost), the vanilla `tick query` command (MC 1.20.3+,
// synchronous) serves as fallback.
package mcstatus

import (
	"context"
	"math"
	"regexp"
	"strconv"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

type Source struct {
	query collector.MCStatusSource
	rcon  collector.RCONClient // may be nil → TPS stays 0
}

func New(query collector.MCStatusSource, rcon collector.RCONClient) *Source {
	return &Source{query: query, rcon: rcon}
}

func (s *Source) Status(ctx context.Context) (collector.MCStatus, error) {
	st, err := s.query.Status(ctx)
	if err != nil || !st.Online || s.rcon == nil {
		return st, err
	}
	out, err := s.rcon.Exec(ctx, "spark tps")
	if err == nil {
		st.TPS, st.MSPT = ParseSparkTPS(out)
	}
	if st.TPS == 0 {
		// spark missing or answered asynchronously (empty RCON response) —
		// vanilla `tick query` responds synchronously since MC 1.20.3
		if out, err := s.rcon.Exec(ctx, "tick query"); err == nil {
			st.TPS, st.MSPT = ParseTickQuery(out)
		}
	}
	// TPS failing must not degrade the rest of the status
	return st, nil
}

var (
	// strips legacy §x color codes spark embeds in console output
	colorCodes = regexp.MustCompile(`§.`)
	// "TPS from last 5s, 10s, 1m, 5m, 15m: 20.0, 20.0, 20.0, 19.98, 20.0"
	tpsLine = regexp.MustCompile(`TPS[^:]*:\s*\*?([\d.]+)`)
	// "Tick durations (min/med/95%ile/max ms) from last 10s, 1m: 2.1/3.4/5.2/40.0; ..."
	msptLine = regexp.MustCompile(`durations[^:]*:\s*[\d.]+/([\d.]+)`)
)

// ParseSparkTPS extracts the most recent TPS window and the median MSPT from
// `spark tps` console output. Returns zeros when the format is unrecognised.
func ParseSparkTPS(raw string) (tps, mspt float64) {
	clean := colorCodes.ReplaceAllString(raw, "")
	if m := tpsLine.FindStringSubmatch(clean); m != nil {
		tps, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := msptLine.FindStringSubmatch(clean); m != nil {
		mspt, _ = strconv.ParseFloat(m[1], 64)
	}
	return tps, mspt
}

var (
	// "The target tick rate is 20.0 per second." / "Target tick rate: 20.0"
	tickRate = regexp.MustCompile(`(?i)t(?:arget)?[^\d]*tick rate[^\d]*([\d.]+)`)
	// "Average time per tick: 2.5ms (Target: 50.0ms)"
	tickAvg = regexp.MustCompile(`(?i)average time per tick:\s*([\d.]+)\s*ms`)
)

// ParseTickQuery derives TPS/MSPT from vanilla `tick query` output.
// Actual TPS = min(target rate, 1000/mspt) — the server never ticks faster
// than the target, and slower only when a tick exceeds its budget.
func ParseTickQuery(raw string) (tps, mspt float64) {
	clean := colorCodes.ReplaceAllString(raw, "")
	target := 20.0
	if m := tickRate.FindStringSubmatch(clean); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > 0 {
			target = v
		}
	}
	m := tickAvg.FindStringSubmatch(clean)
	if m == nil {
		return 0, 0
	}
	mspt, _ = strconv.ParseFloat(m[1], 64)
	if mspt <= 0 {
		return 0, 0
	}
	tps = math.Min(target, 1000.0/mspt)
	return tps, mspt
}
