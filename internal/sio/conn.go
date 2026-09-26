package sio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Config says how to dial.
type Config struct {
	// URL is the server's base URL (http, https, ws or wss), without /socket.io/.
	URL    string
	APIKey string
	// Header is sent with the upgrade request (User-Agent).
	Header     http.Header
	HTTPClient *http.Client
	// Compress offers permessage-deflate with no context takeover.
	Compress bool
	// ReadLimit is the largest message accepted, in bytes; -1 means no limit.
	ReadLimit int64
}

// Conn is an open Socket.IO connection to the "/" namespace.
type Conn struct {
	ws *websocket.Conn
	// Open is the server's Engine.IO handshake.
	Open Open
	// SID is the Socket.IO session id from the CONNECT answer.
	SID string
}

// SocketURL is the Engine.IO v4 websocket-transport URL for base and apiKey.
func SocketURL(base, apiKey string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + "/socket.io/")
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	q.Set("apiKey", apiKey)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Dial opens the WebSocket, reads the Engine.IO open packet, sends the Socket.IO
// CONNECT with auth, and waits for the server's answer. ctx bounds the whole
// handshake and is not used after Dial returns. A refused connect is a
// *ConnectError; an upgrade answered with an HTTP error is an *HTTPError; a
// network failure is a *NetError, whose message never contains the API key.
func Dial(ctx context.Context, cfg Config, auth any) (*Conn, error) {
	u, err := SocketURL(cfg.URL, cfg.APIKey)
	if err != nil {
		return nil, err
	}
	opts := &websocket.DialOptions{HTTPClient: cfg.HTTPClient, HTTPHeader: cfg.Header}
	if cfg.Compress {
		opts.CompressionMode = websocket.CompressionNoContextTakeover
	}
	ws, resp, err := websocket.Dial(ctx, u, opts)
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			he := &HTTPError{StatusCode: resp.StatusCode, Header: resp.Header}
			if resp.Body != nil {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
				he.Body = string(b)
			}
			return nil, he
		}
		return nil, redact(err, cfg.APIKey)
	}
	c := &Conn{ws: ws}
	// The default limit is 32 KiB, which would close the connection (1009) on
	// the first full board.
	ws.SetReadLimit(cfg.ReadLimit)
	if err := c.handshake(ctx, auth); err != nil {
		ws.CloseNow()
		return nil, err
	}
	return c, nil
}

func (c *Conn) handshake(ctx context.Context, auth any) error {
	frame, err := c.Read(ctx)
	if err != nil {
		return fmt.Errorf("sio: waiting for the open packet: %w", err)
	}
	p, err := Parse(frame)
	if err != nil {
		return err
	}
	if p.Kind != KindOpen {
		return fmt.Errorf("sio: expected the open packet, got %q", truncate(frame))
	}
	c.Open = p.Open
	cf, err := ConnectFrame(auth)
	if err != nil {
		return err
	}
	if err := c.Write(ctx, cf); err != nil {
		return err
	}
	for {
		frame, err := c.Read(ctx)
		if err != nil {
			return fmt.Errorf("sio: waiting for the connect answer: %w", err)
		}
		p, err := Parse(frame)
		if err != nil {
			return err
		}
		switch p.Kind {
		case KindPing:
			if err := c.Write(ctx, Pong); err != nil {
				return err
			}
		case KindConnect:
			var d struct {
				SID string `json:"sid"`
			}
			_ = json.Unmarshal(p.Data, &d)
			c.SID = d.SID
			return nil
		case KindConnectError:
			ce := &ConnectError{}
			if err := json.Unmarshal(p.Data, ce); err != nil {
				// Protocol v4 servers sent the message as a bare string.
				var msg string
				_ = json.Unmarshal(p.Data, &msg)
				ce.Message = msg
			}
			return ce
		case KindClose, KindDisconnect:
			return errors.New("sio: the server closed the connection during the handshake")
		}
	}
}

// Read returns the next text frame. Cancelling ctx closes the connection.
func (c *Conn) Read(ctx context.Context) ([]byte, error) {
	typ, b, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return []byte("6"), nil // binary frames are not used by the server: ignore
	}
	return b, nil
}

// Write sends one text frame. It is safe for concurrent use; a write still
// blocked when ctx ends closes the connection.
func (c *Conn) Write(ctx context.Context, frame []byte) error {
	return c.ws.Write(ctx, websocket.MessageText, frame)
}

// Emit sends a Socket.IO event with one JSON argument (none when arg is nil).
func (c *Conn) Emit(ctx context.Context, name string, arg json.RawMessage) error {
	f, err := EventFrame(name, arg)
	if err != nil {
		return err
	}
	return c.Write(ctx, f)
}

// Close sends a Socket.IO disconnect and closes the WebSocket normally (1000),
// waiting at most timeout for the close handshake.
func (c *Conn) Close(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = c.Write(ctx, Disconnect)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.ws.Close(websocket.StatusNormalClosure, "")
	}()
	select {
	case <-done:
	case <-ctx.Done():
		_ = c.ws.CloseNow()
		<-done
	}
}

// CloseNow closes the connection without a handshake.
func (c *Conn) CloseNow() { _ = c.ws.CloseNow() }

// NetError is a WebSocket dial that failed before any HTTP response: DNS, TCP,
// TLS, or a context that ended. The request URL carries the API key in its query
// and net/http prints it in its errors, so the message is the underlying one with
// the key replaced by REDACTED, and the underlying chain is not exposed: errors.Is
// matches context.Canceled and context.DeadlineExceeded, and errors.As finds a
// *net.OpError, whose message holds no URL.
type NetError struct {
	msg string
	err error
}

func (e *NetError) Error() string { return e.msg }

// Is reports whether the dial failed because a context ended.
func (e *NetError) Is(target error) bool {
	return (target == context.Canceled || target == context.DeadlineExceeded) && errors.Is(e.err, target)
}

// As finds the *net.OpError of a network failure.
func (e *NetError) As(target any) bool {
	p, ok := target.(**net.OpError)
	if !ok {
		return false
	}
	var op *net.OpError
	if !errors.As(e.err, &op) {
		return false
	}
	*p = op
	return true
}

// redact wraps a dial error so that neither form of apiKey (raw and
// query-escaped) appears in its message. coder/websocket formats the message when
// it wraps the error, so the URL cannot be cleaned after the fact.
func redact(err error, apiKey string) error {
	msg := err.Error()
	if apiKey != "" {
		msg = strings.ReplaceAll(msg, url.QueryEscape(apiKey), "REDACTED")
		msg = strings.ReplaceAll(msg, apiKey, "REDACTED")
	}
	return &NetError{msg: msg, err: err}
}

func truncate(b []byte) string {
	if len(b) > 64 {
		return string(b[:64]) + "..."
	}
	return string(b)
}
