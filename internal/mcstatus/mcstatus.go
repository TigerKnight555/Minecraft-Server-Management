// Package mcstatus combines the Query protocol (players, version, MOTD) with
// spark via RCON (TPS/MSPT) into one MCStatus, per the concept: spark values
// are "deutlich verlässlicher als eigene Schätzungen".
package mcstatus

import (
	"context"
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
	// spark failing must not degrade the rest of the status
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
