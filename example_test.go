package owls_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	owls "github.com/owlsinsight/owls-insight-sdk-go"
)

func ExampleNewClient() {
	c, err := owls.NewClient(os.Getenv("OWLS_INSIGHT_API_KEY"),
		owls.WithRetry(owls.RetryPolicy{MaxRetries: 2}))
	if err != nil {
		log.Fatal(err)
	}
	odds, err := c.GetOdds(context.Background(), "nba", &owls.OddsParams{Books: []string{"pinnacle"}})
	var apiErr *owls.APIError
	switch {
	case errors.Is(err, owls.ErrRateLimited) && errors.As(err, &apiErr):
		log.Printf("rate limited, retry in %v", apiErr.RetryAfter)
		return
	case err != nil:
		log.Fatal(err)
	}
	for book, events := range odds.Data {
		fmt.Println(book, len(events))
	}
}

func ExampleClient_Stream() {
	c, err := owls.NewClient(os.Getenv("OWLS_INSIGHT_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	s := c.Stream(owls.WithSubscription(owls.Subscription{
		Sports: []string{"nba"},
		Books:  []string{"pinnacle", "fanduel"},
	}))
	s.OnOddsUpdate(func(u *owls.OddsUpdatePayload, err error) {
		if err != nil {
			log.Printf("odds-update: %v", err)
			return
		}
		for sport, events := range u.Sports {
			fmt.Println(sport, len(events), "events changed")
		}
	})
	for {
		// Run keeps its own backoff across calls, so calling it again after a
		// retryable refusal waits as long as the server asked.
		err := s.Run(context.Background())
		var refused *owls.RefusalError
		if errors.As(err, &refused) && refused.Retryable {
			log.Printf("refused, retrying: %v", err)
			continue
		}
		log.Fatal(err)
	}
}

func ExampleClient_IterHistoryOdds() {
	c, err := owls.NewClient(os.Getenv("OWLS_INSIGHT_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	p := owls.HistoryOddsParams{EventID: "nba:Boston Celtics@New York Knicks-20260310", Book: "pinnacle"}
	for snap, err := range c.IterHistoryOdds(context.Background(), p, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(*snap.RecordedAt, *snap.Market, *snap.Price)
	}
}

func ExampleVerifyWebhookSignature() {
	secret := os.Getenv("OWLS_WEBHOOK_SECRET") // CreateWebhook's Data.Secret
	http.HandleFunc("/owls", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil || !owls.VerifyWebhookSignature(body, r.Header.Get(owls.WebhookSignatureHeader), secret) {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		ev, err := owls.ParseWebhookEvent(body)
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		log.Println("event", ev.EventID()) // the same on every retry: dedupe on it
		switch ev := ev.(type) {
		case *owls.WebhookLineMovedEvent:
			log.Println(*ev.Data.Market, *ev.Data.Reason)
		case *owls.WebhookEvFoundEvent:
			log.Println(*ev.Data.Signal.Venue, *ev.Data.Signal.EvPercent)
		}
		w.WriteHeader(http.StatusOK) // any 2xx within 10 s acknowledges it; 410 Gone disables the endpoint
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func ExampleClient_CreateWebhook() {
	c, err := owls.NewClient(os.Getenv("OWLS_INSIGHT_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	created, err := c.CreateWebhook(context.Background(), owls.CreateWebhookParams{
		URL:        "https://example.com/owls",
		EventTypes: []string{owls.WebhookEventLineMoved},
		Filters:    &owls.WebhookFiltersInput{Sports: []string{"nba"}, LineMoved: &owls.WebhookLineMovedFiltersInput{PointStep: 1}},
	})
	var apiErr *owls.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		log.Fatalf("refused: %s (%s)", apiErr.Message, apiErr.Code)
	} else if err != nil {
		log.Fatal(err)
	}
	fmt.Println(*created.Data.ID, "store the secret now:", len(*created.Data.Secret) > 0)
}
