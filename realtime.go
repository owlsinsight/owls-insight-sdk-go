package owls

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RealtimeSportEvents returns the events a pinnacle-realtime or ps3838-realtime
// frame carries for sport, or nil when it carries none for it.
//
// A frame is keyed by sport ({"timestamp": ..., "<sport>": [...], ...}) and holds
// one sport; Stream.Sport names it when the frame has a stream position.
// [RealtimeOddsPayload] names the common sports as fields; this reads any sport,
// including one the API adds later. The events decode like [Decode]. Only a frame
// that is not a JSON object is an error.
//
//	s.On(owls.EventPinnacleRealtime, func(raw json.RawMessage) {
//		events, err := owls.RealtimeSportEvents(raw, "soccer")
//		...
//	})
func RealtimeSportEvents(frame json.RawMessage, sport string) ([]OddsEvent, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(frame, &fields); err != nil {
		return nil, fmt.Errorf("owls: decode realtime frame: %w", err)
	}
	raw := bytes.TrimSpace(fields[sport])
	if len(raw) == 0 || raw[0] != '[' {
		return nil, nil
	}
	var events []OddsEvent
	if err := decodeBody(raw, &events); err != nil {
		return nil, err
	}
	return events, nil
}
