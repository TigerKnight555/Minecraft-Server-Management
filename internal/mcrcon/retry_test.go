package mcrcon

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// flakyRCONServer kills the first connection immediately (simulating the
// sporadic EOF), then behaves normally.
func flakyRCONServer(t *testing.T, password, response string) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var conns atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n := conns.Add(1)
			if n == 1 {
				conn.Close() // first attempt: immediate EOF
				continue
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				id, _, _, err := readPacket(conn)
				if err != nil {
					return
				}
				writePacket(conn, id, typeAuthResponse, "")
				id, _, _, err = readPacket(conn)
				if err != nil {
					return
				}
				writePacket(conn, id, typeResponse, response)
				sid, _, _, err := readPacket(conn)
				if err != nil {
					return
				}
				writePacket(conn, sid, typeResponse, "")
			}()
		}
	}()
	return ln.Addr().String(), &conns
}

func TestExecRetriesOnEOF(t *testing.T) {
	addr, conns := flakyRCONServer(t, "pw", "pong")
	c := New(addr, "pw")
	out, err := c.Exec(context.Background(), "ping")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if out != "pong" {
		t.Errorf("response = %q, want pong", out)
	}
	if conns.Load() != 2 {
		t.Errorf("connections = %d, want 2 (one failed + one retry)", conns.Load())
	}
}
