package jvmstats

import (
	"errors"
	"testing"
)

func TestParseStatCommWithParens(t *testing.T) {
	st, err := parseStat("700 (weird ) name) S 1 700 700 0 -1 4194560 0 0 0 0 7 3 0 0 20 0 1 0 8000 0 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if st.Comm != "weird ) name" || st.PPID != 1 || st.Ticks != 10 || st.StartTime != 8000 {
		t.Errorf("parseStat = %+v", st)
	}
}

func TestParseStatTooShort(t *testing.T) {
	if _, err := parseStat("1 (x) S 0 1"); err == nil {
		t.Error("kurze Zeile ohne Fehler")
	}
}

func TestFindJavaFollowsChain(t *testing.T) {
	pid, start, err := FindJava("testdata/proc", 500)
	if err != nil {
		t.Fatal(err)
	}
	// 600 ist auch java, liegt aber nicht unter dem Container-Init
	if pid != 510 || start != 12345 {
		t.Errorf("FindJava = %d/%d, want 510/12345", pid, start)
	}
}

func TestFindJavaNotFound(t *testing.T) {
	if _, _, err := FindJava("testdata/proc", 999); !errors.Is(err, ErrNoJava) {
		t.Errorf("err = %v, want ErrNoJava", err)
	}
}

func TestFindJavaProcUnavailable(t *testing.T) {
	if _, _, err := FindJava("testdata/gibt-es-nicht", 500); !errors.Is(err, ErrProcUnavailable) {
		t.Errorf("err = %v, want ErrProcUnavailable", err)
	}
}

func TestHostCores(t *testing.T) {
	n, err := HostCores("testdata/proc")
	if err != nil || n != 4 {
		t.Errorf("HostCores = %d, %v; want 4", n, err)
	}
}
