// Package sio is the subset of Engine.IO v4 and Socket.IO v5 the Owls Insight
// server uses: the websocket transport only, JSON text frames, the "/" namespace,
// no acknowledgements and no binary packets.
package sio

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

// Kind is what a received frame means to the client.
type Kind int

const (
	// KindOther is anything the client ignores (noop, upgrade, ack, binary).
	KindOther Kind = iota
	// KindOpen is the Engine.IO handshake (Open is set).
	KindOpen
	// KindPing is an Engine.IO ping; answer it with Pong.
	KindPing
	// KindClose is an Engine.IO close: the transport is going away.
	KindClose
	// KindConnect is the Socket.IO CONNECT answer to our connect (Data is {sid}).
	KindConnect
	// KindConnectError is a Socket.IO CONNECT_ERROR (Data is {message, data}).
	KindConnectError
	// KindDisconnect is a Socket.IO DISCONNECT sent by the server.
	KindDisconnect
	// KindEvent is a Socket.IO EVENT (Name, and Data for its first argument).
	KindEvent
)

// Open is the Engine.IO open packet.
type Open struct {
	SID          string `json:"sid"`
	PingInterval int    `json:"pingInterval"`
	PingTimeout  int    `json:"pingTimeout"`
	MaxPayload   int    `json:"maxPayload"`
}

// Packet is one parsed text frame.
type Packet struct {
	Kind Kind
	Open Open
	// Name is the event name of a KindEvent.
	Name string
	// Data is the first event argument (JSON null when there is none), or the
	// payload of a connect, connect error or disconnect packet.
	Data json.RawMessage
}

// Frames the client sends.
var (
	Pong       = []byte("3")
	Disconnect = []byte("41")
)

var null = json.RawMessage("null")

// Parse decodes one text frame.
func Parse(frame []byte) (Packet, error) {
	if len(frame) == 0 {
		return Packet{}, errors.New("sio: empty frame")
	}
	switch frame[0] {
	case '0':
		var o Open
		if err := json.Unmarshal(frame[1:], &o); err != nil {
			return Packet{}, errors.New("sio: bad open packet: " + err.Error())
		}
		return Packet{Kind: KindOpen, Open: o}, nil
	case '1':
		return Packet{Kind: KindClose}, nil
	case '2':
		return Packet{Kind: KindPing}, nil
	case '4':
		return parseMessage(frame[1:])
	}
	return Packet{Kind: KindOther}, nil
}

// parseMessage decodes a Socket.IO packet: <type>[<nsp>,][<ack id>][<json>].
func parseMessage(b []byte) (Packet, error) {
	if len(b) == 0 {
		return Packet{}, errors.New("sio: empty message")
	}
	typ := b[0]
	rest := b[1:]
	if len(rest) > 0 && rest[0] == '/' {
		i := bytes.IndexByte(rest, ',')
		if i < 0 {
			rest = nil // "/nsp" with nothing after it
		} else {
			if string(rest[:i]) != "/" {
				return Packet{Kind: KindOther}, nil // another namespace
			}
			rest = rest[i+1:]
		}
	}
	for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
		rest = rest[1:] // ack id: the server sends none, tolerate one
	}
	data := json.RawMessage(bytes.TrimSpace(rest))
	if len(data) == 0 {
		data = null
	}
	switch typ {
	case '0':
		return Packet{Kind: KindConnect, Data: data}, nil
	case '1':
		return Packet{Kind: KindDisconnect, Data: data}, nil
	case '4':
		return Packet{Kind: KindConnectError, Data: data}, nil
	case '2':
		var args []json.RawMessage
		if err := json.Unmarshal(data, &args); err != nil || len(args) == 0 {
			return Packet{}, errors.New("sio: bad event packet")
		}
		var name string
		if err := json.Unmarshal(args[0], &name); err != nil {
			return Packet{}, errors.New("sio: event name is not a string")
		}
		p := Packet{Kind: KindEvent, Name: name, Data: null}
		if len(args) > 1 {
			p.Data = args[1]
		}
		return p, nil
	}
	return Packet{Kind: KindOther}, nil
}

// ConnectFrame is the Socket.IO CONNECT to "/" carrying the handshake auth.
func ConnectFrame(auth any) ([]byte, error) {
	b, err := json.Marshal(auth)
	if err != nil {
		return nil, err
	}
	return append([]byte("40"), b...), nil
}

// EventFrame is a Socket.IO EVENT. arg nil sends the event with no argument.
func EventFrame(name string, arg json.RawMessage) ([]byte, error) {
	n, err := json.Marshal(name)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(n)+len(arg)+6)
	out = append(out, `42[`...)
	out = append(out, n...)
	if arg != nil {
		if !json.Valid(arg) {
			return nil, errors.New("sio: event argument is not valid JSON")
		}
		out = append(out, ',')
		out = append(out, arg...)
	}
	return append(out, ']'), nil
}

// ConnectError is a Socket.IO CONNECT_ERROR payload.
type ConnectError struct {
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *ConnectError) Error() string { return "sio: connect error: " + e.Message }

// HTTPError is a WebSocket upgrade answered with a status other than 101.
type HTTPError struct {
	StatusCode int
	Header     map[string][]string
	Body       string
}

func (e *HTTPError) Error() string {
	return "sio: websocket upgrade answered HTTP " + strconv.Itoa(e.StatusCode)
}
