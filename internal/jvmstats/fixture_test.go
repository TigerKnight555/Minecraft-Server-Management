package jvmstats

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// procFixture baut ein Mini-procfs im TempDir: Container-Init 500 → java 510.
type procFixture struct {
	t    *testing.T
	root string
}

func newProcFixture(t *testing.T) *procFixture {
	t.Helper()
	f := &procFixture{t: t, root: t.TempDir()}
	f.write("stat", "cpu  1 0 1 1 1 0 0 0 0 0\ncpu0 1 0 1 1 1 0 0 0 0 0\ncpu1 1 0 1 1 1 0 0 0 0 0\n")
	f.proc(500, "tini", 1, 5000)
	f.proc(510, "java", 500, 12345)
	return f
}

// statLine erzeugt eine /proc/<pid>/stat-Zeile mit utime (Feld 14),
// stime (15) und starttime (22).
func statLine(pid int, comm string, ppid int, utime, stime, start uint64) string {
	return fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194560 0 0 0 0 %d %d 0 0 20 0 1 0 %d 0 0\n",
		pid, comm, ppid, pid, pid, utime, stime, start)
}

func (f *procFixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *procFixture) proc(pid int, comm string, ppid int, start uint64) {
	f.write(filepath.Join(strconv.Itoa(pid), "stat"), statLine(pid, comm, ppid, 0, 0, start))
}

func (f *procFixture) thread(pid, tid int, comm string, utime, stime uint64) {
	f.write(filepath.Join(strconv.Itoa(pid), "task", strconv.Itoa(tid), "stat"),
		statLine(tid, comm, pid, utime, stime, 0))
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
