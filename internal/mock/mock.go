// Package mock provides fake implementations of every collector dependency.
// They serve two purposes: unit tests, and the --mock flag which runs the
// full app locally without Docker or a Minecraft server.
package mock

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
	"github.com/TigerKnight555/Minecraft-Server-Management/internal/storage"
)

type Docker struct {
	start   time.Time
	mu      sync.Mutex
	Actions []string
}

func NewDocker() *Docker { return &Docker{start: time.Now()} }

func (d *Docker) ListContainers(context.Context) ([]collector.Container, error) {
	return []collector.Container{
		{ID: "aaaa000000000000", Name: "mc-fabric", Image: "itzg/minecraft-server", State: "running", Status: "Up 3 days"},
		{ID: "bbbb000000000000", Name: "webseite", Image: "nginx:alpine", State: "running", Status: "Up 3 days (healthy)"},
		{ID: "cccc000000000000", Name: "msm", Image: "msm:dev", State: "running", Status: "Up 10 minutes"},
		{ID: "dddd000000000000", Name: "mc-backup", Image: "restic/restic", State: "exited", Status: "Exited (0) 8 hours ago"},
	}, nil
}

// InspectContainer pretends a started job container finished successfully a
// moment later — enough for the backup runner's supervision loop.
func (d *Docker) InspectContainer(_ context.Context, id string) (collector.ContainerDetail, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.Actions) - 1; i >= 0; i-- {
		if d.Actions[i] == "start:"+id {
			return collector.ContainerDetail{
				Running: false, ExitCode: 0,
				StartedAt: d.start, FinishedAt: time.Now(),
			}, nil
		}
	}
	return collector.ContainerDetail{Running: false, ExitCode: 0}, nil
}

func (d *Docker) TailLogs(context.Context, string, int) (string, error) {
	return "processed 1234 files, 5.6 GiB in 0:42\nAdded to the repository: 120 MiB\nsnapshot ab12cd34 saved\n", nil
}

func (d *Docker) ContainerStats(_ context.Context, id string) (collector.ContainerStats, error) {
	t := time.Since(d.start).Seconds()
	wobble := math.Sin(t/30) * 0.5
	st := collector.ContainerStats{ID: id, Time: time.Now()}
	switch id[:4] {
	case "aaaa":
		st.CPUPercent = 35 + wobble*20 + rand.Float64()*5
		st.MemUsage = uint64((9.5 + wobble) * 1024 * 1024 * 1024)
		st.MemLimit = 12 * 1024 * 1024 * 1024
	case "bbbb":
		st.CPUPercent = 0.5 + rand.Float64()
		st.MemUsage = 48 * 1024 * 1024
		st.MemLimit = 256 * 1024 * 1024
	default:
		st.CPUPercent = 0.3 + rand.Float64()*0.2
		st.MemUsage = 42 * 1024 * 1024
		st.MemLimit = 200 * 1024 * 1024
	}
	return st, nil
}

// Actions records lifecycle calls so tests can assert them; mock mode just
// pretends everything worked.
func (d *Docker) StartContainer(_ context.Context, id string) error {
	d.recordAction("start", id)
	return nil
}

func (d *Docker) StopContainer(_ context.Context, id string) error {
	d.recordAction("stop", id)
	return nil
}

func (d *Docker) RestartContainer(_ context.Context, id string) error {
	d.recordAction("restart", id)
	return nil
}

func (d *Docker) recordAction(action, id string) {
	d.mu.Lock()
	d.Actions = append(d.Actions, action+":"+id)
	d.mu.Unlock()
}

// ActionLog returns recorded lifecycle calls (for tests).
func (d *Docker) ActionLog() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.Actions...)
}

func (d *Docker) StreamLogs(ctx context.Context, id string, tail int) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		lines := []string{
			"[Server thread/INFO]: Preparing spawn area: 97%%",
			"[Server thread/INFO]: Done (12.345s)! For help, type \"help\"",
			"[Server thread/INFO]: Steve joined the game",
			"[Server thread/INFO]: <Steve> hallo zusammen",
			"[Server thread/INFO]: Saving the game (this may take a moment!)",
			"[Server thread/INFO]: Saved the game",
		}
		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1500 * time.Millisecond):
				line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), lines[i%len(lines)])
				if _, err := pw.Write([]byte(line)); err != nil {
					return
				}
				i++
			}
		}
	}()
	return pr, nil
}

type MC struct{ start time.Time }

func NewMC() *MC { return &MC{start: time.Now()} }

func (m *MC) Status(context.Context) (collector.MCStatus, error) {
	players := []string{"Steve", "Alex", "TigerKnight555"}
	n := 1 + int(time.Since(m.start).Minutes())%3
	return collector.MCStatus{
		Time:          time.Now(),
		Online:        true,
		Version:       "1.21.1",
		MOTD:          "MSM Mock Server",
		PlayersOnline: n,
		PlayersMax:    20,
		Players:       players[:n],
		TPS:           19.5 + rand.Float64()*0.5,
		MSPT:          3.0 + rand.Float64()*4,
	}, nil
}

type RCON struct {
	mu      sync.Mutex
	history []string
}

func NewRCON() *RCON { return &RCON{} }

func (r *RCON) Exec(_ context.Context, command string) (string, error) {
	r.mu.Lock()
	r.history = append(r.history, command)
	r.mu.Unlock()
	switch {
	case command == "list":
		return "There are 2 of a max of 20 players online: Steve, Alex", nil
	case strings.HasPrefix(command, "spark tps"):
		return "TPS from last 5s, 10s, 1m, 5m, 15m: 19.8, 19.9, 20.0, 20.0, 20.0\n" +
			"Tick durations (min/med/95%ile/max ms) from last 10s, 1m: 1.2/3.4/8.1/40.2; 1.0/3.2/7.9/45.0", nil
	default:
		return "Executed: " + command, nil
	}
}

// Commands returns all commands executed so far (for tests).
func (r *RCON) Commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.history...)
}

type Host struct{ start time.Time }

func NewHost() *Host { return &Host{start: time.Now()} }

func (h *Host) Sample(context.Context) (collector.HostSample, error) {
	t := time.Since(h.start).Seconds()
	return collector.HostSample{
		Time:       time.Now(),
		CPUPercent: 40 + math.Sin(t/45)*15 + rand.Float64()*5,
		MemTotal:   16 * 1024 * 1024 * 1024,
		MemUsed:    uint64((11.5 + math.Sin(t/60)) * 1024 * 1024 * 1024),
		Load1:      1.2 + rand.Float64()*0.3,
		Load5:      1.1,
		Load15:     0.9,
		DiskTotal:  500 * 1024 * 1024 * 1024,
		DiskUsed:   320 * 1024 * 1024 * 1024,
		UptimeSec:  uint64(86400*3 + int(t)),
		NASOnline:  true,
	}, nil
}

type WAN struct{}

func NewWAN() *WAN { return &WAN{} }

func (w *WAN) Check(context.Context) (collector.WANSample, error) {
	mk := func(target string, base float64) collector.PingResult {
		return collector.PingResult{
			Target: target, Reached: true,
			RTTMs:    base + rand.Float64()*5,
			JitterMs: rand.Float64() * 2,
			LossPct:  0,
		}
	}
	return collector.WANSample{
		Time: time.Now(),
		Targets: []collector.PingResult{
			mk("1.1.1.1", 12), mk("9.9.9.9", 18), mk("192.168.1.1", 0.6),
		},
	}, nil
}

// Store is an in-memory Store + AdminStore for tests and mock mode.
type Store struct {
	mu       sync.Mutex
	Samples  []collector.Sample
	AuditLog []storage.AuditEntry
	Routines []storage.Routine
	Runs     []storage.RoutineRun
	Desired  map[string]string
	Windows  []storage.MaintenanceWindow
	nextID   int64
}

func NewStore() *Store { return &Store{nextID: 1} }

func (s *Store) Audit(_ context.Context, action, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AuditLog = append(s.AuditLog, storage.AuditEntry{
		ID: int64(len(s.AuditLog) + 1), Time: time.Now(), Action: action, Detail: detail,
	})
	return nil
}

func (s *Store) RecentAudit(_ context.Context, limit int) ([]storage.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]storage.AuditEntry(nil), s.AuditLog...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ListRoutines(_ context.Context) ([]storage.Routine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.Routine(nil), s.Routines...), nil
}

func (s *Store) GetRoutine(_ context.Context, id int64) (storage.Routine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.Routines {
		if r.ID == id {
			return r, nil
		}
	}
	return storage.Routine{}, sql.ErrNoRows
}

func (s *Store) CreateRoutine(_ context.Context, r storage.Routine) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = s.nextID
	s.nextID++
	s.Routines = append(s.Routines, r)
	return r.ID, nil
}

func (s *Store) UpdateRoutine(_ context.Context, r storage.Routine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Routines {
		if s.Routines[i].ID == r.ID {
			s.Routines[i] = r
			return nil
		}
	}
	return sql.ErrNoRows
}

func (s *Store) DeleteRoutine(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Routines {
		if s.Routines[i].ID == id {
			s.Routines = append(s.Routines[:i], s.Routines[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *Store) RecordRun(_ context.Context, run storage.RoutineRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run.ID = int64(len(s.Runs) + 1)
	s.Runs = append(s.Runs, run)
	return nil
}

func (s *Store) RecentRuns(_ context.Context, limit int) ([]storage.RoutineRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]storage.RoutineRun(nil), s.Runs...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) WriteSamples(_ context.Context, samples []collector.Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Samples = append(s.Samples, samples...)
	return nil
}

func (s *Store) QuerySeries(_ context.Context, series string, from, to time.Time) ([]collector.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []collector.Sample
	for _, smp := range s.Samples {
		if smp.Series == series && !smp.Time.Before(from) && !smp.Time.After(to) {
			out = append(out, smp)
		}
	}
	return out, nil
}

func (s *Store) Close() error { return nil }

// CreateFakeWorld builds a minimal MC data dir (playerdata + usercache) for
// the restore dropdown in mock mode. Returns the data dir path.
func CreateFakeWorld(base string) (string, error) {
	dataDir := filepath.Join(base, "data")
	pd := filepath.Join(dataDir, "world", "playerdata")
	if err := os.MkdirAll(pd, 0o755); err != nil {
		return "", err
	}
	players := map[string]string{
		"069a79f4-44e9-4726-a5be-fca90e38aaf5": "Steve",
		"853c80ef-3c37-49fd-aa49-938b674adae6": "Alex",
		"deadbeef-0000-4000-8000-000000000001": "", // nicht mehr im usercache
	}
	var cache []map[string]string
	for uuid, name := range players {
		if err := os.WriteFile(filepath.Join(pd, uuid+".dat"), []byte("fake nbt"), 0o644); err != nil {
			return "", err
		}
		if name != "" {
			cache = append(cache, map[string]string{"name": name, "uuid": uuid, "expiresOn": "2026-08-01 00:00:00 +0000"})
		}
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dataDir, "usercache.json"), data, 0o644); err != nil {
		return "", err
	}
	return dataDir, nil
}

// --- Soll-Zustand (Phase 4.5) ---

func (s *Store) SetDesiredState(_ context.Context, container, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Desired == nil {
		s.Desired = map[string]string{}
	}
	s.Desired[container] = state
	return nil
}

func (s *Store) ListDesiredStates(_ context.Context) ([]storage.DesiredState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.DesiredState
	for c, st := range s.Desired {
		out = append(out, storage.DesiredState{Container: c, State: st, Updated: time.Now()})
	}
	return out, nil
}

// --- Wartungsfenster (Phase 4.6) ---

func (s *Store) CreateWindow(_ context.Context, w storage.MaintenanceWindow) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.ID = s.nextID
	s.nextID++
	s.Windows = append(s.Windows, w)
	return w.ID, nil
}

func (s *Store) ListWindows(_ context.Context) ([]storage.MaintenanceWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.MaintenanceWindow(nil), s.Windows...), nil
}

func (s *Store) MarkWindow(_ context.Context, id int64, started, ended, stoppedServer bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Windows {
		if s.Windows[i].ID == id {
			s.Windows[i].Started, s.Windows[i].Ended, s.Windows[i].StoppedServer = started, ended, stoppedServer
			return nil
		}
	}
	return sql.ErrNoRows
}

func (s *Store) EndWindowNow(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Windows {
		if s.Windows[i].ID == id && !s.Windows[i].Ended {
			s.Windows[i].End = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}

func (s *Store) DeleteWindow(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Windows {
		if s.Windows[i].ID == id {
			s.Windows = append(s.Windows[:i], s.Windows[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *Store) MarkWindowNotified(_ context.Context, id int64, stage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Windows {
		if s.Windows[i].ID == id {
			switch stage {
			case "1h":
				s.Windows[i].Notified1h = true
			case "5m":
				s.Windows[i].Notified5m = true
			}
			return nil
		}
	}
	return sql.ErrNoRows
}
