package collector

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Event is one update pushed to SSE subscribers.
type Event struct {
	Type string // "container", "host", "mc", "wan"
	Data any
}

// Config controls polling intervals; zero values fall back to the
// concept defaults (container/host 5s, minecraft 10s, wan 30s).
type Config struct {
	ContainerInterval time.Duration
	HostInterval      time.Duration
	MCInterval        time.Duration
	WANInterval       time.Duration
	MCContainerName   string // name of the minecraft container, e.g. "mc-fabric"
}

func (c *Config) applyDefaults() {
	if c.ContainerInterval <= 0 {
		c.ContainerInterval = 5 * time.Second
	}
	if c.HostInterval <= 0 {
		c.HostInterval = 5 * time.Second
	}
	if c.MCInterval <= 0 {
		c.MCInterval = 10 * time.Second
	}
	if c.WANInterval <= 0 {
		c.WANInterval = 30 * time.Second
	}
}

// Collector polls all sources, fans events out to subscribers and batches
// samples into the store.
type Collector struct {
	cfg    Config
	docker DockerClient
	mc     MCStatusSource
	host   HostMetricsSource
	wan    WANChecker
	store  Store
	log    *slog.Logger

	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
	containers  []Container
	lastStats   map[string]ContainerStats
	lastHost    HostSample
	lastMC      MCStatus
	lastWAN     WANSample
	// survives short offline windows (e.g. restart after mod apply) so
	// update checks never run with an empty version
	lastMCVersion string

	pending   []Sample
	pendingMu sync.Mutex
}

func New(cfg Config, docker DockerClient, mc MCStatusSource, host HostMetricsSource, wan WANChecker, store Store, log *slog.Logger) *Collector {
	cfg.applyDefaults()
	return &Collector{
		cfg:         cfg,
		docker:      docker,
		mc:          mc,
		host:        host,
		wan:         wan,
		store:       store,
		log:         log,
		subscribers: make(map[chan Event]struct{}),
		lastStats:   make(map[string]ContainerStats),
	}
}

// Subscribe registers an SSE consumer. The returned cancel func must be
// called when the consumer goes away. Slow consumers drop events instead of
// blocking the collector.
func (c *Collector) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	c.mu.Lock()
	c.subscribers[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.subscribers, ch)
		c.mu.Unlock()
	}
}

// HasSubscribers reports whether any browser is currently connected. Pollers
// stretch their interval when nobody is watching (concept: resource saving).
func (c *Collector) HasSubscribers() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.subscribers) > 0
}

func (c *Collector) publish(ev Event) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for ch := range c.subscribers {
		select {
		case ch <- ev:
		default: // drop for slow consumers
		}
	}
}

// Snapshot returns the latest known state of everything, for initial page load.
type Snapshot struct {
	Containers []Container               `json:"containers"`
	Stats      map[string]ContainerStats `json:"stats"`
	Host       HostSample                `json:"host"`
	MC         MCStatus                  `json:"mc"`
	WAN        WANSample                 `json:"wan"`
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stats := make(map[string]ContainerStats, len(c.lastStats))
	for k, v := range c.lastStats {
		stats[k] = v
	}
	return Snapshot{
		Containers: append([]Container(nil), c.containers...),
		Stats:      stats,
		Host:       c.lastHost,
		MC:         c.lastMC,
		WAN:        c.lastWAN,
	}
}

// MCVersion returns the last known Minecraft version — also while the
// server is briefly offline (restart windows).
func (c *Collector) MCVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastMCVersion
}

func (c *Collector) Containers() []Container {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Container(nil), c.containers...)
}

// Run starts all polling loops and the flush loop; blocks until ctx is done.
func (c *Collector) Run(ctx context.Context) {
	var wg sync.WaitGroup
	loops := []struct {
		name     string
		interval time.Duration
		fn       func(context.Context)
	}{
		{"containers", c.cfg.ContainerInterval, c.pollContainers},
		{"host", c.cfg.HostInterval, c.pollHost},
		{"mc", c.cfg.MCInterval, c.pollMC},
		{"wan", c.cfg.WANInterval, c.pollWAN},
	}
	for _, l := range loops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.loop(ctx, l.interval, l.fn)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.flushLoop(ctx)
	}()
	wg.Wait()
}

// loop runs fn on the given interval; when no subscriber is connected the
// interval is stretched 6x to reduce idle load (samples still get stored).
func (c *Collector) loop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	fn(ctx)
	t := time.NewTimer(interval)
	defer t.Stop()
	for {
		next := interval
		if !c.HasSubscribers() {
			next = interval * 6
		}
		t.Reset(next)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

func (c *Collector) pollContainers(ctx context.Context) {
	list, err := c.docker.ListContainers(ctx)
	if err != nil {
		c.log.Warn("list containers failed", "err", err)
		return
	}
	c.mu.Lock()
	c.containers = list
	c.mu.Unlock()

	for _, ct := range list {
		if ct.State != "running" {
			continue
		}
		st, err := c.docker.ContainerStats(ctx, ct.ID)
		if err != nil {
			c.log.Warn("container stats failed", "container", ct.Name, "err", err)
			continue
		}
		st.Name = ct.Name
		c.mu.Lock()
		c.lastStats[ct.Name] = st
		c.mu.Unlock()
		c.publish(Event{Type: "container", Data: st})
		c.enqueue(
			Sample{Series: "container." + ct.Name + ".cpu", Time: st.Time, Value: st.CPUPercent},
			Sample{Series: "container." + ct.Name + ".mem", Time: st.Time, Value: float64(st.MemUsage)},
		)
	}
}

func (c *Collector) pollHost(ctx context.Context) {
	s, err := c.host.Sample(ctx)
	if err != nil {
		c.log.Warn("host metrics failed", "err", err)
		return
	}
	c.mu.Lock()
	c.lastHost = s
	c.mu.Unlock()
	c.publish(Event{Type: "host", Data: s})
	c.enqueue(
		Sample{Series: "host.cpu", Time: s.Time, Value: s.CPUPercent},
		Sample{Series: "host.mem", Time: s.Time, Value: float64(s.MemUsed)},
		Sample{Series: "host.load1", Time: s.Time, Value: s.Load1},
	)
}

func (c *Collector) pollMC(ctx context.Context) {
	s, err := c.mc.Status(ctx)
	if err != nil {
		// offline is a valid, reportable state — not only an error
		s = MCStatus{Time: time.Now(), Online: false}
		if !isConnRefused(err) {
			c.log.Warn("mc status failed", "err", err)
		}
	}
	c.mu.Lock()
	c.lastMC = s
	if s.Online && s.Version != "" {
		c.lastMCVersion = s.Version
	}
	c.mu.Unlock()
	c.publish(Event{Type: "mc", Data: s})
	online := 0.0
	if s.Online {
		online = 1.0
	}
	c.enqueue(
		Sample{Series: "mc.online", Time: s.Time, Value: online},
		Sample{Series: "mc.players", Time: s.Time, Value: float64(s.PlayersOnline)},
		Sample{Series: "mc.tps", Time: s.Time, Value: s.TPS},
	)
}

func (c *Collector) pollWAN(ctx context.Context) {
	s, err := c.wan.Check(ctx)
	if err != nil {
		c.log.Warn("wan check failed", "err", err)
		return
	}
	c.mu.Lock()
	c.lastWAN = s
	c.mu.Unlock()
	c.publish(Event{Type: "wan", Data: s})
	for _, t := range s.Targets {
		c.enqueue(
			Sample{Series: "wan." + t.Target + ".rtt", Time: s.Time, Value: t.RTTMs},
			Sample{Series: "wan." + t.Target + ".loss", Time: s.Time, Value: t.LossPct},
		)
	}
}

func (c *Collector) enqueue(samples ...Sample) {
	c.pendingMu.Lock()
	c.pending = append(c.pending, samples...)
	c.pendingMu.Unlock()
}

// flushLoop writes batched samples to SQLite every 30s instead of per tick
// (concept: bundle writes, keep the ring in RAM).
func (c *Collector) flushLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.flush(context.Background())
			return
		case <-t.C:
			c.flush(ctx)
		}
	}
}

func (c *Collector) flush(ctx context.Context) {
	c.pendingMu.Lock()
	batch := c.pending
	c.pending = nil
	c.pendingMu.Unlock()
	if len(batch) == 0 || c.store == nil {
		return
	}
	if err := c.store.WriteSamples(ctx, batch); err != nil {
		c.log.Error("flush samples failed", "err", err)
	}
}

func isConnRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "refused")
}
