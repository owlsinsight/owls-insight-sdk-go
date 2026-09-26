package owls

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The WebSocket tests run against conformance/server.mjs: the same socket.io as
// the API, configured the same way, on 127.0.0.1. They never touch the
// production API.
//
// The server needs node and socket.io, which it loads from $OWLS_API_REPO (a
// checkout of the API server), else conformance/node_modules (`npm ci` in
// conformance/, or `make conformance`), else the sibling ../nba-odds-app. Without
// them the tests are SKIPPED with a message, never passed; with CI or
// OWLS_REQUIRE_WS_SERVER_TESTS=1 set they fail instead.

type wsServer struct {
	t   *testing.T
	URL string
}

type serverAttempt struct {
	At                int64           `json:"at"`
	Auth              json.RawMessage `json:"auth"`
	UA                *string         `json:"ua"`
	APIKey            *string         `json:"apiKey"`
	DeflateOffered    bool            `json:"deflateOffered"`
	DeflateNegotiated bool            `json:"deflateNegotiated"`
	Refused           bool            `json:"refused"`
}

type serverEvent struct {
	At   int64             `json:"at"`
	Name string            `json:"name"`
	Args []json.RawMessage `json:"args"`
}

type serverConn struct {
	ID        string        `json:"id"`
	At        int64         `json:"at"`
	Events    []serverEvent `json:"events"`
	Connected bool          `json:"connected"`
	// Reason is socket.io's disconnect reason, e.g. "client namespace disconnect".
	Reason *string `json:"reason"`
}

type serverState struct {
	Attempts    []serverAttempt `json:"attempts"`
	Connections []serverConn    `json:"connections"`
	Blocked     []int64         `json:"blocked"`
	Rejected    []int64         `json:"rejected"`
}

// skipWSServer skips the test, or fails it when this run may not skip the
// conformance server (CI, or OWLS_REQUIRE_WS_SERVER_TESTS=1).
func skipWSServer(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("CI") != "" || os.Getenv("OWLS_REQUIRE_WS_SERVER_TESTS") == "1" {
		t.Fatalf("conformance server unavailable and this run may not skip it: %s", msg)
	}
	t.Skipf("SKIPPED: %s", msg)
}

// startWSServer starts a conformance server with the given ping timings.
func startWSServer(t *testing.T, pingInterval, pingTimeout time.Duration) *wsServer {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		skipWSServer(t, "node is not on PATH")
	}
	cmd := exec.Command(node, filepath.Join("conformance", "server.mjs"))
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PING_INTERVAL_MS=%d", pingInterval.Milliseconds()),
		fmt.Sprintf("PING_TIMEOUT_MS=%d", pingTimeout.Milliseconds()),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() {
			line <- sc.Text()
		}
		close(line)
		_, _ = io.Copy(io.Discard, stdout)
	}()
	var ready struct {
		Port     int    `json:"port"`
		Skip     string `json:"skip"`
		Modules  string `json:"modules"`
		SocketIO string `json:"socketIo"`
		EngineIO string `json:"engineIo"`
	}
	select {
	case l, ok := <-line:
		if !ok || json.Unmarshal([]byte(l), &ready) != nil {
			t.Fatalf("conformance server did not start: %q", l)
		}
		if ready.Skip != "" {
			skipWSServer(t, ready.Skip)
		}
		if ready.Port == 0 {
			t.Fatalf("conformance server did not start: %q", l)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("conformance server did not start in 15s")
	}
	serverVersionOnce.Do(func() {
		t.Logf("conformance server: socket.io %s, engine.io %s, from %s", ready.SocketIO, ready.EngineIO, ready.Modules)
	})
	return &wsServer{t: t, URL: fmt.Sprintf("http://127.0.0.1:%d", ready.Port)}
}

var serverVersionOnce sync.Once

func (s *wsServer) control(cmd map[string]any) {
	s.t.Helper()
	b, _ := json.Marshal(cmd)
	resp, err := http.Post(s.URL+"/control", "application/json", bytes.NewReader(b))
	if err != nil {
		s.t.Fatalf("control %v: %v", cmd, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("control %v: HTTP %d %s", cmd, resp.StatusCode, body)
	}
}

func (s *wsServer) state() serverState {
	s.t.Helper()
	b, _ := json.Marshal(map[string]any{"op": "state"})
	resp, err := http.Post(s.URL+"/control", "application/json", bytes.NewReader(b))
	if err != nil {
		s.t.Fatalf("state: %v", err)
	}
	defer resp.Body.Close()
	var st serverState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		s.t.Fatalf("state: %v", err)
	}
	return st
}

// until polls cond every 5 ms until it holds, failing the test after timeout.
func until(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// holds reports whether cond becomes true within timeout.
func holds(timeout time.Duration, cond func() bool) bool {
	end := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(end) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// running is a Stream.Run in the background.
type running struct {
	t      *testing.T
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// runStream starts s.Run in the background and cancels it when the test ends.
func runStream(t *testing.T, s *Stream) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{t: t, cancel: cancel, done: make(chan struct{})}
	go func() {
		r.err = s.Run(ctx)
		close(r.done)
	}()
	t.Cleanup(func() { r.stop() })
	return r
}

// wait returns Run's error once it has returned, or false after timeout.
func (r *running) wait(timeout time.Duration) (error, bool) {
	select {
	case <-r.done:
		return r.err, true
	case <-time.After(timeout):
		return nil, false
	}
}

// stop cancels Run and returns its error.
func (r *running) stop() error {
	r.cancel()
	err, ok := r.wait(10 * time.Second)
	if !ok {
		r.t.Error("Run did not return within 10s of cancel")
	}
	return err
}

// fastTiming shrinks every delay of the connection policy for tests.
func fastTiming() timing {
	return timing{
		ackTimeout:        300 * time.Millisecond,
		connLimitDelays:   []time.Duration{60 * time.Millisecond},
		refusalDelays:     []time.Duration{60 * time.Millisecond},
		accountDelays:     []time.Duration{60 * time.Millisecond},
		retryWindow:       60 * time.Second,
		retryMaxPerWindow: 4,
		reconnectDelay:    20 * time.Millisecond,
		reconnectDelayMax: 50 * time.Millisecond,
		stableAfter:       60 * time.Second,
		ipBlockWait:       120 * time.Second,
		handshakeTimeout:  5 * time.Second,
		writeTimeout:      5 * time.Second,
		closeTimeout:      time.Second,
	}
}

func newTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := NewClient("test-key", WithBaseURL(url))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
