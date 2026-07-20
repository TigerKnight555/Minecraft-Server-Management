package mcrcon

import (
	"context"
	"net"
	"testing"
	"time"
)

// fakeRCONServer speaks just enough protocol for the client tests.
func fakeRCONServer(t *testing.T, password, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		// auth
		id, _, payload, err := readPacket(conn)
		if err != nil {
			return
		}
		if payload != password {
			writePacket(conn, -1, typeAuthResponse, "")
			return
		}
		writePacket(conn, id, typeAuthResponse, "")

		// command
		id, _, _, err = readPacket(conn)
		if err != nil {
			return
		}
		writePacket(conn, id, typeResponse, response)

		// sentinel
		sid, _, _, err := readPacket(conn)
		if err != nil {
			return
		}
		writePacket(conn, sid, typeResponse, "")
	}()
	return ln.Addr().String()
}

func TestExec(t *testing.T) {
	addr := fakeRCONServer(t, "secret", "There are 0 of a max of 20 players online:")
	c := New(addr, "secret")
	out, err := c.Exec(context.Background(), "list")
	if err != nil {
		t.Fatal(err)
	}
	want := "There are 0 of a max of 20 players online:"
	if out != want {
		t.Errorf("response = %q, want %q", out, want)
	}
}

func TestExecWrongPassword(t *testing.T) {
	addr := fakeRCONServer(t, "secret", "")
	c := New(addr, "wrong")
	if _, err := c.Exec(context.Background(), "list"); err == nil {
		t.Fatal("expected auth error, got nil")
	}
}
