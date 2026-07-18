// Package netcheck probes WAN quality via ICMP echo against independent
// targets (1.1.1.1, 9.9.9.9) plus the default gateway, to tell LAN issues
// apart from internet issues (concept chapter 4).
package netcheck

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

type Checker struct {
	targets  []string
	probes   int // echo requests per target per check
	timeout  time.Duration
	procPath string // for gateway discovery, default /proc
}

func New(targets []string, procPath string) *Checker {
	if len(targets) == 0 {
		targets = []string{"1.1.1.1", "9.9.9.9"}
	}
	if procPath == "" {
		procPath = "/proc"
	}
	return &Checker{targets: targets, probes: 3, timeout: 2 * time.Second, procPath: procPath}
}

func (c *Checker) Check(ctx context.Context) (collector.WANSample, error) {
	out := collector.WANSample{Time: time.Now()}
	targets := c.targets
	if gw, err := defaultGateway(c.procPath); err == nil {
		targets = append(append([]string{}, targets...), gw)
	}
	for _, t := range targets {
		out.Targets = append(out.Targets, c.pingTarget(ctx, t))
	}
	return out, nil
}

func (c *Checker) pingTarget(ctx context.Context, target string) collector.PingResult {
	res := collector.PingResult{Target: target, LossPct: 100}
	var rtts []float64
	for i := 0; i < c.probes; i++ {
		if ctx.Err() != nil {
			break
		}
		rtt, err := pingOnce(target, c.timeout, i)
		if err == nil {
			rtts = append(rtts, float64(rtt.Microseconds())/1000.0)
		}
	}
	if len(rtts) == 0 {
		return res
	}
	res.Reached = true
	res.LossPct = float64(c.probes-len(rtts)) / float64(c.probes) * 100.0
	sort.Float64s(rtts)
	res.RTTMs = rtts[len(rtts)/2] // median
	if len(rtts) > 1 {
		var sum float64
		for i := 1; i < len(rtts); i++ {
			sum += math.Abs(rtts[i] - rtts[i-1])
		}
		res.JitterMs = sum / float64(len(rtts)-1)
	}
	return res
}

// pingOnce sends one ICMP echo. Tries an unprivileged datagram socket first
// (needs net.ipv4.ping_group_range on the host), falls back to a raw socket
// (needs CAP_NET_RAW — granted in compose).
func pingOnce(target string, timeout time.Duration, seq int) (time.Duration, error) {
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	proto := "udp"
	if err != nil {
		conn, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		proto = "ip"
		if err != nil {
			return 0, fmt.Errorf("icmp socket: %w", err)
		}
	}
	defer conn.Close()

	var dst net.Addr
	if proto == "udp" {
		dst = &net.UDPAddr{IP: net.ParseIP(target)}
	} else {
		dst = &net.IPAddr{IP: net.ParseIP(target)}
	}

	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: seq, Data: payload},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	if _, err := conn.WriteTo(wire, dst); err != nil {
		return 0, err
	}
	conn.SetReadDeadline(start.Add(timeout))
	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, err
		}
		parsed, err := icmp.ParseMessage(1, buf[:n])
		if err != nil {
			continue
		}
		if parsed.Type == ipv4.ICMPTypeEchoReply {
			return time.Since(start), nil
		}
	}
}

// defaultGateway parses /proc/net/route for the 0.0.0.0 route.
func defaultGateway(procPath string) (string, error) {
	data, err := os.ReadFile(procPath + "/net/route")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		gw, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		ip := net.IPv4(byte(gw), byte(gw>>8), byte(gw>>16), byte(gw>>24))
		return ip.String(), nil
	}
	return "", fmt.Errorf("no default route found")
}
