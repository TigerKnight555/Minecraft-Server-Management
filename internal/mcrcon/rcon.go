// Package mcrcon implements the Source RCON protocol used by Minecraft
// (enable-rcon=true). One TCP connection per command keeps state handling
// trivial; command volume is a handful per minute at most.
package mcrcon

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	typeAuth         = 3
	typeAuthResponse = 2
	typeExecCommand  = 2
	typeResponse     = 0
)

type Client struct {
	addr     string
	password string
	timeout  time.Duration
}

func New(addr, password string) *Client {
	return &Client{addr: addr, password: password, timeout: 5 * time.Second}
}

func (c *Client) Exec(ctx context.Context, command string) (string, error) {
	d := net.Dialer{Timeout: c.timeout}
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	deadline := time.Now().Add(c.timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)

	if err := writePacket(conn, 1, typeAuth, c.password); err != nil {
		return "", fmt.Errorf("auth write: %w", err)
	}
	id, _, _, err := readPacket(conn)
	if err != nil {
		return "", fmt.Errorf("auth read: %w", err)
	}
	if id == -1 {
		return "", fmt.Errorf("rcon authentication failed")
	}

	if err := writePacket(conn, 2, typeExecCommand, command); err != nil {
		return "", fmt.Errorf("exec write: %w", err)
	}
	// Responses can be fragmented; send a sentinel and read until its echo.
	if err := writePacket(conn, 3, typeResponse, ""); err != nil {
		return "", fmt.Errorf("sentinel write: %w", err)
	}
	var body string
	for {
		id, _, payload, err := readPacket(conn)
		if err != nil {
			return "", fmt.Errorf("exec read: %w", err)
		}
		if id == 3 {
			break
		}
		body += payload
	}
	return body, nil
}

func writePacket(w io.Writer, id int32, ptype int32, payload string) error {
	length := int32(4 + 4 + len(payload) + 2)
	buf := make([]byte, 0, 4+length)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(length))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(id))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(ptype))
	buf = append(buf, payload...)
	buf = append(buf, 0, 0)
	_, err := w.Write(buf)
	return err
}

func readPacket(r io.Reader) (id int32, ptype int32, payload string, err error) {
	var length int32
	if err = binary.Read(r, binary.LittleEndian, &length); err != nil {
		return
	}
	if length < 10 || length > 1<<20 {
		err = fmt.Errorf("invalid packet length %d", length)
		return
	}
	body := make([]byte, length)
	if _, err = io.ReadFull(r, body); err != nil {
		return
	}
	id = int32(binary.LittleEndian.Uint32(body[0:4]))
	ptype = int32(binary.LittleEndian.Uint32(body[4:8]))
	payload = string(body[8 : length-2])
	return
}
