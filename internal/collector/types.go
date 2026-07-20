package collector

import (
	"context"
	"io"
	"time"
)

// Container is a snapshot of a single Docker container as reported by the
// Engine API through the socket proxy.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`  // running, exited, ...
	Status string `json:"status"` // human readable, e.g. "Up 3 hours"
}

// ContainerStats is a single resource usage sample for one container.
type ContainerStats struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Time       time.Time `json:"time"`
	CPUPercent float64   `json:"cpuPercent"`
	MemUsage   uint64    `json:"memUsage"` // bytes
	MemLimit   uint64    `json:"memLimit"` // bytes
	NetRx      uint64    `json:"netRx"`    // cumulative bytes
	NetTx      uint64    `json:"netTx"`    // cumulative bytes
	BlockRead  uint64    `json:"blockRead"`
	BlockWrite uint64    `json:"blockWrite"`
}

// HostSample is a snapshot of host-level metrics read from /proc and /sys.
type HostSample struct {
	Time       time.Time `json:"time"`
	CPUPercent float64   `json:"cpuPercent"`
	MemTotal   uint64    `json:"memTotal"`
	MemUsed    uint64    `json:"memUsed"`
	Load1      float64   `json:"load1"`
	Load5      float64   `json:"load5"`
	Load15     float64   `json:"load15"`
	DiskTotal  uint64    `json:"diskTotal"`
	DiskUsed   uint64    `json:"diskUsed"`
	UptimeSec  uint64    `json:"uptimeSec"`
	NASOnline  bool      `json:"nasOnline"`
}

// MCStatus is the combined Minecraft server state from Query and RCON.
type MCStatus struct {
	Time          time.Time `json:"time"`
	Online        bool      `json:"online"`
	Version       string    `json:"version"`
	MOTD          string    `json:"motd"`
	PlayersOnline int       `json:"playersOnline"`
	PlayersMax    int       `json:"playersMax"`
	Players       []string  `json:"players"`
	TPS           float64   `json:"tps"`  // from spark, 0 if unavailable
	MSPT          float64   `json:"mspt"` // from spark, 0 if unavailable
}

// WANSample is one round of connectivity probes.
type WANSample struct {
	Time    time.Time    `json:"time"`
	Targets []PingResult `json:"targets"`
}

// PingResult is the outcome of pinging one target.
type PingResult struct {
	Target   string  `json:"target"`
	Reached  bool    `json:"reached"`
	RTTMs    float64 `json:"rttMs"`
	JitterMs float64 `json:"jitterMs"`
	LossPct  float64 `json:"lossPct"`
}

// ContainerDetail is the inspect subset MSM needs (exit codes for
// short-lived job containers like the backup runner).
type ContainerDetail struct {
	Running    bool
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
}

// DockerClient reads container information through the socket proxy.
type DockerClient interface {
	ListContainers(ctx context.Context) ([]Container, error)
	ContainerStats(ctx context.Context, id string) (ContainerStats, error)
	// StreamLogs returns a demultiplexed plain-text log stream.
	StreamLogs(ctx context.Context, id string, tail int) (io.ReadCloser, error)
}

// ContainerController performs lifecycle actions (Phase 2). Kept separate
// from DockerClient so read-only consumers cannot accidentally mutate.
type ContainerController interface {
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
}

// MCStatusSource provides Minecraft server state (Query protocol).
type MCStatusSource interface {
	Status(ctx context.Context) (MCStatus, error)
}

// RCONClient executes commands on the Minecraft server console.
type RCONClient interface {
	Exec(ctx context.Context, command string) (string, error)
}

// HostMetricsSource samples host-level metrics.
type HostMetricsSource interface {
	Sample(ctx context.Context) (HostSample, error)
}

// WANChecker probes internet connectivity.
type WANChecker interface {
	Check(ctx context.Context) (WANSample, error)
}

// Store persists metric samples for history charts.
type Store interface {
	WriteSamples(ctx context.Context, samples []Sample) error
	QuerySeries(ctx context.Context, series string, from, to time.Time) ([]Sample, error)
	Close() error
}

// Sample is one point of one named time series (e.g. "host.cpu",
// "container.mc-fabric.mem", "mc.players", "wan.1.1.1.1.rtt").
type Sample struct {
	Series string    `json:"series"`
	Time   time.Time `json:"time"`
	Value  float64   `json:"value"`
}
