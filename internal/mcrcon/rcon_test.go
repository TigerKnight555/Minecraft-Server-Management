package mcrcon

import (
	"context"
	"net"
	"strings"
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
		var typ int32
		id, _, payload, err := readPacket(conn)
		if err != nil {
			return
		}
		if payload != password {
			writePacket(conn, -1, typeAuthResponse, "")
			return
		}
		writePacket(conn, id, typeAuthResponse, "")

		// command — wie echtes Minecraft: nur Typ 2 wird ausgeführt, jeder
		// andere Typ beendet die Verbindung
		id, typ, _, err = readPacket(conn)
		if err != nil || typ != typeExecCommand {
			return
		}
		writeResponse(conn, id, response)
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

// writeResponse antwortet wie Minecraft: lange Antworten in Stücken zu
// maxChunk Bytes, alle mit der ID des Befehls.
func writeResponse(conn net.Conn, id int32, response string) {
	for {
		n := len(response)
		if n > maxChunk {
			n = maxChunk
		}
		writePacket(conn, id, typeResponse, response[:n])
		response = response[n:]
		if n < maxChunk {
			return
		}
	}
}

// Lange Antworten (z. B. "help") kommen in mehreren Stücken und werden
// vollständig zusammengesetzt.
func TestExecLongResponse(t *testing.T) {
	for _, n := range []int{2*maxChunk + 123, 2 * maxChunk} {
		long := strings.Repeat("x", n)
		addr := fakeRCONServer(t, "secret", long)
		start := time.Now()
		out, err := New(addr, "secret").Exec(context.Background(), "help")
		if err != nil {
			t.Fatalf("%d Bytes: %v", n, err)
		}
		if out != long {
			t.Errorf("Antwort %d Bytes, want %d", len(out), n)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%d Bytes: Antwort dauerte %s", n, time.Since(start))
		}
	}
}
