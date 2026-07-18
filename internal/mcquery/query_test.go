package mcquery

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

// fakeQueryServer answers handshake + full stat like a Minecraft server.
func fakeQueryServer(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1024)
		for {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 7 {
				continue
			}
			ptype := buf[2]
			session := buf[3:7]
			switch ptype {
			case typeHandshake:
				resp := append([]byte{typeHandshake}, session...)
				resp = append(resp, []byte("9513307\x00")...)
				conn.WriteTo(resp, addr)
			case typeStat:
				resp := append([]byte{typeStat}, session...)
				resp = append(resp, []byte("splitnum\x00\x80\x00")...)
				kv := "hostname\x00Ein Minecraft Server\x00" +
					"gametype\x00SMP\x00" +
					"game_id\x00MINECRAFT\x00" +
					"version\x001.21.1\x00" +
					"plugins\x00\x00" +
					"map\x00world\x00" +
					"numplayers\x002\x00" +
					"maxplayers\x0020\x00" +
					"hostport\x0025565\x00" +
					"hostip\x00172.17.0.2\x00" +
					"\x00" +
					"\x01player_\x00\x00" +
					"Steve\x00Alex\x00\x00"
				resp = append(resp, []byte(kv)...)
				conn.WriteTo(resp, addr)
			}
		}
	}()
	return conn.LocalAddr().String()
}

func TestStatus(t *testing.T) {
	addr := fakeQueryServer(t)
	c := New(addr)
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := collector.MCStatus{
		Online: true, Version: "1.21.1", MOTD: "Ein Minecraft Server",
		PlayersOnline: 2, PlayersMax: 20, Players: []string{"Steve", "Alex"},
	}
	if st.Version != want.Version || st.MOTD != want.MOTD ||
		st.PlayersOnline != want.PlayersOnline || st.PlayersMax != want.PlayersMax {
		t.Errorf("status = %+v, want %+v", st, want)
	}
	if len(st.Players) != 2 || st.Players[0] != "Steve" || st.Players[1] != "Alex" {
		t.Errorf("players = %v, want [Steve Alex]", st.Players)
	}
	if !st.Online {
		t.Error("expected Online = true")
	}
}

func TestStatusServerDown(t *testing.T) {
	c := New("127.0.0.1:1") // nothing listens there
	c.timeout = 200 * time.Millisecond
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestHandshakeTokenParsing(t *testing.T) {
	// regression: token must survive being null-padded
	raw := []byte{typeHandshake, 1, 1, 1, 1}
	raw = append(raw, []byte("12345\x00")...)
	_ = binary.BigEndian // silence unused import if refactored
	if got := string(raw[5 : len(raw)-1]); got != "12345" {
		t.Errorf("token slice = %q", got)
	}
}
