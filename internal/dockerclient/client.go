// Package dockerclient is a minimal Docker Engine API client. It talks to the
// socket proxy over TCP and implements only what MSM needs (list, stats,
// logs) — no dependency on the full Docker SDK keeps the binary small.
package dockerclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

const apiVersion = "v1.43"

type Client struct {
	base string // e.g. http://socket-proxy:2375
	http *http.Client
}

func New(host string) *Client {
	return &Client{
		base: strings.TrimRight(host, "/") + "/" + apiVersion,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker api %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// post sends a body-less POST (start/stop/restart take query params only).
// Own client: stop/restart block until docker finishes (up to t=60s), which
// exceeds the default 15s read timeout.
func (c *Client) post(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 204 no content on success, 304 if already in desired state
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker api %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/start")
}

// StopContainer allows 60s for a clean shutdown — a Minecraft world save
// must never be cut short (data integrity invariant).
func (c *Client) StopContainer(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/stop?t=60")
}

func (c *Client) RestartContainer(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/restart?t=60")
}

// InspectContainer returns the state subset needed to supervise short-lived
// job containers (backup runner): running flag, exit code, timestamps.
func (c *Client) InspectContainer(ctx context.Context, id string) (collector.ContainerDetail, error) {
	var raw struct {
		State struct {
			Running    bool      `json:"Running"`
			ExitCode   int       `json:"ExitCode"`
			StartedAt  time.Time `json:"StartedAt"`
			FinishedAt time.Time `json:"FinishedAt"`
		} `json:"State"`
	}
	if err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/json", &raw); err != nil {
		return collector.ContainerDetail{}, err
	}
	return collector.ContainerDetail{
		Running:    raw.State.Running,
		ExitCode:   raw.State.ExitCode,
		StartedAt:  raw.State.StartedAt,
		FinishedAt: raw.State.FinishedAt,
	}, nil
}

// TailLogs returns the last lines of a container log as one string
// (no follow) — used for backup result summaries.
func (c *Client) TailLogs(ctx context.Context, id string, tail int) (string, error) {
	q := url.Values{}
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	q.Set("tail", strconv.Itoa(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/containers/"+url.PathEscape(id)+"/logs?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("docker logs: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rd io.Reader = resp.Body
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/vnd.docker.multiplexed-stream") {
		rd = &demuxReader{src: resp.Body}
	}
	data, err := io.ReadAll(io.LimitReader(rd, 64<<10))
	return string(data), err
}

type apiContainer struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

func (c *Client) ListContainers(ctx context.Context) ([]collector.Container, error) {
	var raw []apiContainer
	if err := c.get(ctx, "/containers/json?all=true", &raw); err != nil {
		return nil, err
	}
	out := make([]collector.Container, 0, len(raw))
	for _, r := range raw {
		name := r.ID[:12]
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		out = append(out, collector.Container{
			ID: r.ID, Name: name, Image: r.Image, State: r.State, Status: r.Status,
		})
	}
	return out, nil
}

// apiStats mirrors the fields MSM needs from the stats endpoint.
type apiStats struct {
	Read     time.Time `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			InactiveFile uint64 `json:"inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IOServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}

func (c *Client) ContainerStats(ctx context.Context, id string) (collector.ContainerStats, error) {
	var raw apiStats
	if err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/stats?stream=false&one-shot=false", &raw); err != nil {
		return collector.ContainerStats{}, err
	}
	st := collector.ContainerStats{
		ID:       id,
		Time:     raw.Read,
		MemLimit: raw.MemoryStats.Limit,
	}
	// cgroup v2: usage includes page cache; subtract inactive_file like `docker stats` does
	st.MemUsage = raw.MemoryStats.Usage - min(raw.MemoryStats.Stats.InactiveFile, raw.MemoryStats.Usage)

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage) - float64(raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage) - float64(raw.PreCPUStats.SystemUsage)
	cpus := raw.CPUStats.OnlineCPUs
	if cpus == 0 {
		cpus = 1
	}
	if sysDelta > 0 && cpuDelta >= 0 {
		st.CPUPercent = cpuDelta / sysDelta * float64(cpus) * 100.0
	}
	for _, n := range raw.Networks {
		st.NetRx += n.RxBytes
		st.NetTx += n.TxBytes
	}
	for _, b := range raw.BlkioStats.IOServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			st.BlockRead += b.Value
		case "write":
			st.BlockWrite += b.Value
		}
	}
	if st.Time.IsZero() {
		st.Time = time.Now()
	}
	return st, nil
}

// StreamLogs follows the container log. Docker multiplexes stdout/stderr in
// 8-byte-header frames when the container has no TTY; demux transparently.
func (c *Client) StreamLogs(ctx context.Context, id string, tail int) (io.ReadCloser, error) {
	q := url.Values{}
	q.Set("follow", "true")
	q.Set("stdout", "true")
	q.Set("stderr", "true")
	q.Set("tail", strconv.Itoa(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base+"/containers/"+url.PathEscape(id)+"/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// no timeout for a follow stream
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("docker logs: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/vnd.docker.multiplexed-stream") {
		return &demuxReader{src: resp.Body}, nil
	}
	return resp.Body, nil
}

// demuxReader strips the 8-byte stream headers from a multiplexed log stream.
type demuxReader struct {
	src     io.ReadCloser
	pending uint32 // bytes left in current frame
}

func (d *demuxReader) Read(p []byte) (int, error) {
	for d.pending == 0 {
		var hdr [8]byte
		if _, err := io.ReadFull(d.src, hdr[:]); err != nil {
			return 0, err
		}
		d.pending = binary.BigEndian.Uint32(hdr[4:])
	}
	if uint32(len(p)) > d.pending {
		p = p[:d.pending]
	}
	n, err := d.src.Read(p)
	d.pending -= uint32(n)
	return n, err
}

func (d *demuxReader) Close() error { return d.src.Close() }
