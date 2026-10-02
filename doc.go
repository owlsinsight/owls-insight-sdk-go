// Package owls is the Go client for the Owls Insight sports odds API.
//
// It is meant for server-side programs: an API key does not belong in code that
// runs on someone else's machine.
//
// # REST
//
// Create a [Client] with your API key and call its methods. Every call takes a
// context first; the method names are the TypeScript SDK's names with the first
// letter upper-cased (getOdds is GetOdds).
//
//	c, err := owls.NewClient(os.Getenv("OWLS_INSIGHT_API_KEY"))
//	if err != nil { ... }
//	odds, err := c.GetOdds(ctx, "nba", &owls.OddsParams{Books: []string{"pinnacle"}})
//
// A failed call returns an [*APIError]. Match it with errors.Is against
// [ErrUnauthorized], [ErrForbidden], [ErrNotFound], [ErrRateLimited] or
// [ErrServiceBusy], or read its fields with errors.As. Nothing is retried unless
// you ask for it with [WithRetry]; the history pagers retry by default.
//
// Responses decode leniently. Every field is optional (a pointer, or a nil slice
// or map), unknown fields are ignored, enums are plain strings, and a field whose
// JSON type does not match the model is left unset rather than failing the call.
// Raw pass-through data (the v2 books) is kept as [encoding/json.RawMessage].
//
// # Webhooks
//
// MVP and Hall of Fame plans can register HTTPS endpoints that receive signed
// event notifications ([Client.CreateWebhook] and the other Webhook methods; beta).
// Check every delivery against its raw body with [VerifyWebhookSignature], then
// read it with [ParseWebhookEvent] and dedupe on [WebhookEvent.EventID].
//
// # WebSocket
//
// A [Stream] keeps one WebSocket connection open and delivers server events to
// the handlers you register with [Stream.On]. It stores your subscription and
// sends it again on every reconnect, and reconnects with a growing delay when
// connections do not last. A reconnect after a network drop or a server restart
// that is refused for the account (plan, payment, trial) is retried slowly, so it
// goes through once the account is fixed. Any other non-retryable refusal, such
// as a bad key, ends [Stream.Run], and so does a disconnect from the server (its
// periodic account check): call Run again once the account is fixed.
//
//	s := c.Stream(owls.WithSubscription(owls.Subscription{Sports: []string{"nba"}}))
//	s.OnOddsUpdate(func(u *owls.OddsUpdatePayload, err error) { ... })
//	err := s.Run(ctx) // returns when ctx ends, the key is refused or the server disconnects
//
// The API allows a small number of WebSocket connection attempts per IP address
// per minute and blocks the whole address, REST included, when they are exceeded.
// Run one Stream per API key and per process.
package owls
