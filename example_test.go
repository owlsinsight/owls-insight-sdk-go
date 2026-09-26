package owls_test

import (
	"context"
	"errors"
	"fmt"
	"log"
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
