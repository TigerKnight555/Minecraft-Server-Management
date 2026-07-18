// Package mcquery implements the Minecraft Query protocol (full stat),
// enabled on the server via enable-query=true. UDP, two round trips:
// handshake (type 9) for a challenge token, then full stat (type 0).
package mcquery

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/TigerKnight555/Minecraft-Server-Management/internal/collector"
)

type Client struct {
	addr    string // host:port of the query port (default 25565)
	timeout time.Duration
}

func New(addr string) *Client {
	return &Client{addr: addr, timeout: 5 * time.Second}
}

const (
	typeHandshake = 9
	typeStat      = 0
)

var magic = []byte{0xFE, 0xFD}

func (c *Client) Status(ctx context.Context) (collector.MCStatus, error) {
	out := collector.MCStatus{Time: time.Now()}

	d := net.Dialer{Timeout: c.timeout}
	conn, err := d.DialContext(ctx, "udp", c.addr)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	deadline := time.Now().Add(c.timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)

	sessionID := int32(0x01010101) // only lower 4 bits per byte are used

	token, err := handshake(conn, sessionID)
	if err != nil {
		return out, fmt.Errorf("handshake: %w", err)
	}
	raw, err := fullStat(conn, sessionID, token)
	if err != nil {
		return out, fmt.Errorf("full stat: %w", err)
	}
	parseFullStat(raw, &out)
	out.Online = true
	return out, nil
}

func handshake(conn net.Conn, sessionID int32) (int32, error) {
	req := make([]byte, 0, 7)
	req = append(req, magic...)
	req = append(req, typeHandshake)
	req = binary.BigEndian.AppendUint32(req, uint32(sessionID))
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, err
	}
	if n < 6 || buf[0] != typeHandshake {
		return 0, fmt.Errorf("unexpected handshake response")
	}
	// token is a null-terminated ASCII number after type+session
	tokenStr := string(bytes.TrimRight(buf[5:n], "\x00"))
	token, err := strconv.ParseInt(strings.TrimSpace(tokenStr), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("bad challenge token %q: %w", tokenStr, err)
	}
	return int32(token), nil
}

func fullStat(conn net.Conn, sessionID, token int32) ([]byte, error) {
	req := make([]byte, 0, 15)
	req = append(req, magic...)
	req = append(req, typeStat)
	req = binary.BigEndian.AppendUint32(req, uint32(sessionID))
	req = binary.BigEndian.AppendUint32(req, uint32(token))
	req = append(req, 0, 0, 0, 0) // padding selects full stat
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 16 || buf[0] != typeStat {
		return nil, fmt.Errorf("unexpected stat response")
	}
	// skip type(1) + session(4) + constant padding "splitnum\x00\x80\x00" (11)
	return buf[16:n], nil
}

// parseFullStat reads the key\0value\0 section terminated by an empty key,
// followed by "\x01player_\x00\x00" and a null-separated player list.
func parseFullStat(raw []byte, out *collector.MCStatus) {
	parts := bytes.Split(raw, []byte{0})
	kv := map[string]string{}
	i := 0
	for ; i+1 < len(parts); i += 2 {
		key := string(parts[i])
		if key == "" {
			i++ // consumed terminator
			break
		}
		kv[key] = string(parts[i+1])
	}
	out.MOTD = kv["hostname"]
	out.Version = kv["version"]
	out.PlayersOnline, _ = strconv.Atoi(kv["numplayers"])
	out.PlayersMax, _ = strconv.Atoi(kv["maxplayers"])

	// find player section marker
	for ; i < len(parts); i++ {
		if bytes.HasSuffix(parts[i], []byte("player_")) || string(parts[i]) == "\x01player_" {
			i += 2 // marker + one empty
			break
		}
	}
	for ; i < len(parts); i++ {
		name := string(parts[i])
		if name == "" {
			break
		}
		out.Players = append(out.Players, name)
	}
}
