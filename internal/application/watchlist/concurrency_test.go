package watchlist

import (
	"sync"
	"testing"
)

func TestConcurrentReadsAndMutations(t *testing.T) {
	u := NewUseCase()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				if err := u.Add("AAPL", ""); err != nil {
					t.Error(err)
				}
				items, _ := u.List()
				if len(items) > 0 {
					items[0].Symbol = "LOCAL COPY"
				}
				if err := u.Remove("AAPL"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	items, _ := u.List()
	for _, item := range items {
		if item.Symbol == "LOCAL COPY" {
			t.Fatal("List leaked mutable storage")
		}
	}
}
