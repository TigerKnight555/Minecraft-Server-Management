// Package hostmetrics reads host-level metrics from /proc and /sys, which
// the compose file mounts read-only into the container (as /host/proc).
package hostmetrics

import (
	"context"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

type Source struct {
	procPath string // usually /host/proc (ro bind mount), /proc as fallback
	diskPath string // mount point to report disk usage for
	nasPath  string // NAS mount point to check, empty disables the check

	mu       sync.Mutex
	lastIdle uint64
	lastBusy uint64
}

func New(procPath, diskPath, nasPath string) *Source {
	if procPath == "" {
		procPath = "/proc"
	}
	if diskPath == "" {
		diskPath = "/"
	}
	return &Source{procPath: procPath, diskPath: diskPath, nasPath: nasPath}
}

func (s *Source) Sample(ctx context.Context) (collector.HostSample, error) {
	out := collector.HostSample{Time: time.Now()}

	if err := s.readMeminfo(&out); err != nil {
		return out, fmt.Errorf("meminfo: %w", err)
	}
	if err := s.readCPU(&out); err != nil {
		return out, fmt.Errorf("stat: %w", err)
	}
	if err := s.readLoadavg(&out); err != nil {
		return out, fmt.Errorf("loadavg: %w", err)
	}
	if err := s.readUptime(&out); err != nil {
		return out, fmt.Errorf("uptime: %w", err)
	}
	total, used, err := diskUsage(s.diskPath)
	if err == nil {
		out.DiskTotal, out.DiskUsed = total, used
	}
	out.NASOnline = s.nasPath != "" && s.nasMounted()
	return out, nil
}

func (s *Source) readMeminfo(out *collector.HostSample) error {
	data, err := os.ReadFile(s.procPath + "/meminfo")
	if err != nil {
		return err
	}
	var total, available uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			available = kb * 1024
		}
	}
	out.MemTotal = total
	if total >= available {
		out.MemUsed = total - available
	}
	return nil
}

// readCPU computes utilisation from the delta of the aggregate cpu line in
// /proc/stat between two samples; the first call reports 0.
func (s *Source) readCPU(out *collector.HostSample) error {
	data, err := os.ReadFile(s.procPath + "/stat")
	if err != nil {
		return err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 8 || fields[0] != "cpu" {
		return fmt.Errorf("unexpected /proc/stat format")
	}
	vals := make([]uint64, 0, len(fields)-1)
	for _, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		vals = append(vals, v)
	}
	// user nice system idle iowait irq softirq steal ...
	idle := vals[3] + vals[4]
	var busy uint64
	for i, v := range vals {
		if i != 3 && i != 4 {
			busy += v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dIdle := idle - s.lastIdle
	dBusy := busy - s.lastBusy
	if s.lastIdle != 0 && dIdle+dBusy > 0 {
		out.CPUPercent = float64(dBusy) / float64(dIdle+dBusy) * 100.0
	}
	s.lastIdle, s.lastBusy = idle, busy
	return nil
}

func (s *Source) readLoadavg(out *collector.HostSample) error {
	data, err := os.ReadFile(s.procPath + "/loadavg")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return fmt.Errorf("unexpected /proc/loadavg format")
	}
	out.Load1, _ = strconv.ParseFloat(fields[0], 64)
	out.Load5, _ = strconv.ParseFloat(fields[1], 64)
	out.Load15, _ = strconv.ParseFloat(fields[2], 64)
	return nil
}

func (s *Source) readUptime(out *collector.HostSample) error {
	data, err := os.ReadFile(s.procPath + "/uptime")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return fmt.Errorf("unexpected /proc/uptime format")
	}
	up, _ := strconv.ParseFloat(fields[0], 64)
	out.UptimeSec = uint64(up)
	return nil
}

func dirAccessible(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// nasMounted checks the NAS against the HOST's mount table
// (<procPath>/1/mountinfo). MSM_NAS_PATH ist ein Host-Pfad, der im
// MSM-Container gar nicht eingebunden ist — die alte Ordner-Prüfung meldete
// im Docker-Betrieb deshalb immer „offline" (WSL-Test 2026-11-10); und ein
// leerer Mountpunkt-Ordner ohne NAS wäre umgekehrt „online" gewesen.
// Ohne lesbares mountinfo (z. B. lokaler Start ohne /host/proc) bleibt es
// bei der Ordner-Prüfung.
func (s *Source) nasMounted() bool {
	data, err := os.ReadFile(filepath.Join(s.procPath, "1", "mountinfo"))
	if err != nil {
		return dirAccessible(s.nasPath)
	}
	return mountedIn(string(data), s.nasPath)
}

// netFS are filesystem types for which a path BELOW the mount point counts
// too (z. B. MSM_NAS_PATH=/mnt/nas/minecraft bei CIFS-Mount auf /mnt/nas).
var netFS = map[string]bool{"cifs": true, "smb3": true, "nfs": true, "nfs4": true, "fuse.sshfs": true, "fuse.rclone": true}

// mountedIn reports whether path is a mount point in the mountinfo text —
// oder unterhalb eines Netzwerk-Mounts liegt.
func mountedIn(mountinfo, path string) bool {
	path = pathpkg.Clean(path) // Host-Pfad: immer /-getrennt
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mp := strings.ReplaceAll(fields[4], `\040`, " ")
		if mp == path {
			return true
		}
		fstype := ""
		for i, f := range fields {
			if f == "-" && i+1 < len(fields) {
				fstype = fields[i+1]
				break
			}
		}
		if netFS[fstype] && strings.HasPrefix(path, mp+"/") {
			return true
		}
	}
	return false
}
