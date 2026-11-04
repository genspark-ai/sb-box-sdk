package sbbox

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Minimal RFC 6455 client: text/binary frames, client masking, ping/pong,
// close. Enough for the PTY channel; not a general-purpose implementation.

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

func wsDial(ctx context.Context, rawURL string, hdr http.Header, hc *http.Client) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	tlsOn := u.Scheme == "wss"
	if !strings.Contains(host, ":") {
		if tlsOn {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	if tlsOn {
		var cfg *tls.Config
		if t, ok := hc.Transport.(*http.Transport); ok && t.TLSClientConfig != nil {
			cfg = t.TLSClientConfig.Clone()
		} else {
			cfg = &tls.Config{}
		}
		if cfg.ServerName == "" {
			cfg.ServerName = u.Hostname()
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, &TransportError{Err: err}
		}
		conn = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, u.Host, key)
	for k, vs := range hdr {
		for _, v := range vs {
			fmt.Fprintf(&req, "%s: %s\r\n", k, v)
		}
	}
	req.WriteString("\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		conn.Close()
		return nil, &TransportError{Err: err}
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, &TransportError{Err: err}
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		conn.Close()
		return nil, parseError(resp, body)
	}
	expect := wsAccept(key)
	if resp.Header.Get("Sec-WebSocket-Accept") != expect {
		conn.Close()
		return nil, errors.New("sbbox: websocket handshake: bad Sec-WebSocket-Accept")
	}
	// Clear the handshake deadline; reads are then governed by the caller.
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br}, nil
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

const (
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

// writeFrame writes a masked client frame.
func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	var hdr []byte
	hdr = append(hdr, 0x80|op)
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 1<<16:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	hdr = append(hdr, mask...)
	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(append(hdr, masked...)); err != nil {
		return &TransportError{Err: err}
	}
	return nil
}

func (c *wsConn) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(opText, b)
}

// readFrame reads one frame (reassembling continuation frames).
func (c *wsConn) readFrame() (byte, []byte, error) {
	var out []byte
	var firstOp byte
	for {
		h := make([]byte, 2)
		if _, err := io.ReadFull(c.br, h); err != nil {
			return 0, nil, err
		}
		fin := h[0]&0x80 != 0
		op := h[0] & 0x0F
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			b := make([]byte, 2)
			if _, err := io.ReadFull(c.br, b); err != nil {
				return 0, nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b))
		case 127:
			b := make([]byte, 8)
			if _, err := io.ReadFull(c.br, b); err != nil {
				return 0, nil, err
			}
			n = binary.BigEndian.Uint64(b)
		}
		if n > 64<<20 {
			return 0, nil, errors.New("sbbox: websocket frame too large")
		}
		var mask []byte
		if masked {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(c.br, mask); err != nil {
				return 0, nil, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		if op >= 0x8 { // control frame, never fragmented
			return op, payload, nil
		}
		if op != 0 {
			firstOp = op
		}
		out = append(out, payload...)
		if fin {
			return firstOp, out, nil
		}
	}
}

// readText returns the next text/binary message, answering pings and
// returning io.EOF on close.
func (c *wsConn) readText() ([]byte, error) {
	for {
		op, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opText, opBinary:
			return payload, nil
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
		case opPong:
		case opClose:
			_ = c.writeFrame(opClose, payload)
			return nil, io.EOF
		}
	}
}

// Close sends a close frame and closes the socket.
func (c *wsConn) Close() error {
	_ = c.writeFrame(opClose, []byte{0x03, 0xE8})
	return c.conn.Close()
}
