package sio

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const secretKey = "SECRET-KEY+123/="

// closedAddr returns the address of a port nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// silentAddr returns the address of a listener that accepts connections and never
// answers, so a dial hangs until its context ends.
func silentAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		l.Close()
		for _, c := range conns {
			c.Close()
		}
	})
	return l.Addr().String()
}

func leaks(err error) bool {
	for _, s := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(s, secretKey) || strings.Contains(s, "SECRET-KEY%2B123%2F%3D") {
			return true
		}
	}
	return false
}

func TestDialErrorsNeverContainTheKey(t *testing.T) {
	for _, tc := range []struct {
		name    string
		addr    string
		timeout time.Duration
		is      error
	}{
		{"connection refused", closedAddr(t), 5 * time.Second, nil},
		{"dial timeout", silentAddr(t), 200 * time.Millisecond, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel()
			base := "http://" + tc.addr
			_, err := Dial(ctx, Config{URL: base, APIKey: secretKey, ReadLimit: 1 << 20}, map[string]any{"sdk": "t"})
			if err == nil {
				t.Fatal("Dial succeeded")
			}
			if leaks(err) {
				t.Fatalf("the key is in the error: %v", err)
			}
			if !strings.Contains(err.Error(), "REDACTED") {
				t.Errorf("error %q does not show where the key was", err)
			}
			var ne *NetError
			if !errors.As(err, &ne) {
				t.Fatalf("error %T is not a *NetError", err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("errors.Is(%v, %v) = false", err, tc.is)
			}
			if tc.is == nil {
				var op *net.OpError
				if !errors.As(err, &op) || strings.Contains(op.Error(), secretKey) {
					t.Errorf("errors.As(*net.OpError) = %v", op)
				}
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					t.Error("a refused connection matches a context error")
				}
			}
			// The chain behind the redacted message is not reachable.
			if errors.Unwrap(err) != nil {
				t.Error("Unwrap exposes the raw error")
			}

			// CONTROL: the same dial through coder/websocket directly puts the key in
			// the error, so the check above can fail.
			u, _ := SocketURL(base, secretKey)
			ctx2, cancel2 := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel2()
			_, _, raw := websocket.Dial(ctx2, u, nil)
			if raw == nil || !leaks(raw) {
				t.Fatalf("control: the raw error does not contain the key: %v", raw)
			}
		})
	}
}

func TestRedactWithoutKey(t *testing.T) {
	// An empty key must not turn every position of the message into REDACTED.
	if got := redact(errors.New("boom"), "").Error(); got != "boom" {
		t.Fatalf("redact with no key = %q", got)
	}
}
