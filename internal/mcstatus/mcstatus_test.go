package mcstatus

import (
	"context"
	"testing"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/mock"
)

func TestParseSparkTPS(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantTPS  float64
		wantMSPT float64
	}{
		{
			name: "plain",
			raw: "TPS from last 5s, 10s, 1m, 5m, 15m: 19.8, 19.9, 20.0, 20.0, 20.0\n" +
				"Tick durations (min/med/95%ile/max ms) from last 10s, 1m: 1.2/3.4/8.1/40.2; 1.0/3.2/7.9/45.0",
			wantTPS:  19.8,
			wantMSPT: 3.4,
		},
		{
			name:     "with color codes and star",
			raw:      "§8[§e⚡§8] §7TPS from last 5s, 10s, 1m: §a*20.0§7, §a19.95§7, §a20.0",
			wantTPS:  20.0,
			wantMSPT: 0,
		},
		{
			name:     "garbage",
			raw:      "Unknown command",
			wantTPS:  0,
			wantMSPT: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tps, mspt := ParseSparkTPS(tc.raw)
			if tps != tc.wantTPS {
				t.Errorf("tps = %v, want %v", tps, tc.wantTPS)
			}
			if mspt != tc.wantMSPT {
				t.Errorf("mspt = %v, want %v", mspt, tc.wantMSPT)
			}
		})
	}
}

// fakeQuery lets us control the query result independent of mock package.
type fakeQuery struct{ st collector.MCStatus }

func (f fakeQuery) Status(context.Context) (collector.MCStatus, error) { return f.st, nil }

func TestStatusMergesSparkValues(t *testing.T) {
	rcon := mock.NewRCON()
	s := New(fakeQuery{st: collector.MCStatus{Online: true, PlayersOnline: 2}}, rcon)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.TPS != 19.8 {
		t.Errorf("TPS = %v, want 19.8 (from mock spark output)", st.TPS)
	}
	if st.PlayersOnline != 2 {
		t.Errorf("PlayersOnline = %v, want 2", st.PlayersOnline)
	}
}

func TestStatusOfflineSkipsRCON(t *testing.T) {
	rcon := mock.NewRCON()
	s := New(fakeQuery{st: collector.MCStatus{Online: false}}, rcon)
	if _, err := s.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rcon.Commands()) != 0 {
		t.Errorf("rcon was called for offline server: %v", rcon.Commands())
	}
}
