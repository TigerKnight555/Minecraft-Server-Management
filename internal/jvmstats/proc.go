// Package jvmstats misst die Java-Threads des Minecraft-Servers über das
// Host-procfs, liest das GC-Log der JVM (echter Heap, GC-Pausen) und
// erkennt Lags samt wahrscheinlicher Ursache.
package jvmstats

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ClkTck ist USER_HZ des Kernels (x86/arm64-Linux: 100); utime/stime in
// /proc/<pid>/stat zählen in diesen Ticks.
const ClkTck = 100

var (
	// ErrNoJava: kein Java-Prozess unter dem Container-Init (Server aus,
	// Neustart läuft) — der nächste Takt sucht neu.
	ErrNoJava = errors.New("Java-Prozess nicht gefunden")
	// ErrProcUnavailable: das Host-procfs ist nicht lesbar (Mount fehlt).
	ErrProcUnavailable = errors.New("/host/proc nicht lesbar")
)

type procStat struct {
	Comm      string
	PPID      int
	Ticks     uint64 // utime + stime
	StartTime uint64 // Start seit Boot in Ticks — erkennt PID-Wiederverwendung
}

// parseStat zerlegt eine /proc/<pid>/stat-Zeile. comm steht in Klammern und
// darf selbst Leerzeichen und Klammern enthalten — deshalb ab der LETZTEN ')'.
func parseStat(data string) (procStat, error) {
	open := strings.IndexByte(data, '(')
	end := strings.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return procStat{}, fmt.Errorf("stat: unerwartetes Format")
	}
	// f[0]=state(3) f[1]=ppid(4) f[11]=utime(14) f[12]=stime(15) f[19]=starttime(22)
	f := strings.Fields(data[end+1:])
	if len(f) < 20 {
		return procStat{}, fmt.Errorf("stat: zu wenige Felder")
	}
	ppid, _ := strconv.Atoi(f[1])
	ut, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	start, _ := strconv.ParseUint(f[19], 10, 64)
	return procStat{Comm: data[open+1 : end], PPID: ppid, Ticks: ut + st, StartTime: start}, nil
}

func readStat(path string) (procStat, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return procStat{}, err
	}
	return parseStat(string(data))
}

// FindJava sucht ab initPID (Host-PID des Container-Init) den ersten Prozess
// mit comm "java" — initPID selbst oder ein Nachfahre (Breitensuche über
// das ppid-Feld aller /proc/*/stat). Liefert PID und starttime.
func FindJava(root string, initPID int) (int, uint64, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrProcUnavailable, err)
	}
	stats := map[int]procStat{}
	children := map[int][]int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		st, err := readStat(filepath.Join(root, e.Name(), "stat"))
		if err != nil {
			continue // Prozess inzwischen beendet
		}
		stats[pid] = st
		children[st.PPID] = append(children[st.PPID], pid)
	}
	queue := []int{initPID}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if st, ok := stats[pid]; ok && st.Comm == "java" {
			return pid, st.StartTime, nil
		}
		kids := children[pid]
		sort.Ints(kids)
		queue = append(queue, kids...)
	}
	return 0, 0, ErrNoJava
}

// HostCores zählt die cpuN-Zeilen in <root>/stat.
func HostCores(root string) (int, error) {
	data, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) > 3 && strings.HasPrefix(line, "cpu") && line[3] >= '0' && line[3] <= '9' {
			n++
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("stat: keine cpuN-Zeilen")
	}
	return n, nil
}

type thread struct {
	comm  string
	ticks uint64
}

// readThreads liest Name und CPU-Ticks aller Threads von pid. Der Name kommt
// aus Feld 2 von task/<tid>/stat (identisch mit task/<tid>/comm, max. 15
// Zeichen) — eine Datei pro Thread statt zwei.
func readThreads(root string, pid int) (map[int]thread, error) {
	dir := filepath.Join(root, strconv.Itoa(pid), "task")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[int]thread, len(entries))
	for _, e := range entries {
		tid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		st, err := readStat(filepath.Join(dir, e.Name(), "stat"))
		if err != nil {
			continue // Thread inzwischen beendet
		}
		out[tid] = thread{comm: st.Comm, ticks: st.Ticks}
	}
	return out, nil
}
