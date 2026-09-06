package portfolio

import (
	"atlas/internal/domain/currency"
	domain "atlas/internal/domain/portfolio"
	"errors"
	"math"
	"testing"
)

type blockingFX struct{ entered, resume chan struct{} }

func (f blockingFX) ToBase(v float64, _ currency.Code) (float64, error) {
	close(f.entered)
	<-f.resume
	return v * 4, nil
}

func TestRequestPortfolioSurvivesConcurrentSelectionDuringFX(t *testing.T) {
	repo := &fakeRepository{}
	root, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first := root.ActivePortfolio()
	second, err := root.CreatePortfolio("Second")
	if err != nil {
		t.Fatal(err)
	}
	fx := blockingFX{make(chan struct{}), make(chan struct{})}
	root.ExchangeRates = fx
	request, err := root.ForPortfolio(first, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- request.RecordTransaction(domain.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD"})
	}()
	<-fx.entered
	if err := root.SwitchPortfolio(second.ID); err != nil {
		t.Fatal(err)
	}
	close(fx.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(repo.transactions[first]) != 1 || len(repo.transactions[second.ID]) != 0 {
		t.Fatalf("wrong target: %+v", repo.transactions)
	}
}

type rejectedCommit struct{ *fakeRepository }

func (r rejectedCommit) MutatePortfolio(id string, apply func([]domain.Position, []domain.Transaction) ([]domain.Position, []domain.Transaction, error)) error {
	if _, _, err := apply(r.positions[id], r.transactions[id]); err != nil {
		return err
	}
	return errors.New("commit failed")
}
func TestFailedCommitDoesNotPublishWorkingState(t *testing.T) {
	repo := rejectedCommit{&fakeRepository{}}
	u, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	err = u.RecordTransaction(domain.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100})
	if err == nil {
		t.Fatal("expected failure")
	}
	if len(u.Transactions) != 0 || len(u.Positions) != 0 || len(repo.transactions[u.ActivePortfolio()]) != 0 {
		t.Fatal("failed commit published state")
	}
}

func TestRejectInvalidTransactionNumbers(t *testing.T) {
	for _, v := range []float64{-1, 0, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, field := range []string{"price", "quantity"} {
			tx := domain.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100}
			if field == "price" {
				tx.Price = v
			} else {
				tx.Quantity = v
			}
			if err := NewUseCase().RecordTransaction(tx); err == nil {
				t.Fatalf("accepted %s=%v", field, v)
			}
		}
	}
	if err := NewUseCase().RecordTransaction(domain.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 1, Price: 100}); err == nil {
		t.Fatal("accepted oversell")
	}
}

func TestTransactionIDMakesRetriesIdempotent(t *testing.T) {
	u := NewUseCase()
	tx := domain.Transaction{ID: "00000000-0000-0000-0000-000000000001", Symbol: " aapl ", Type: "BUY", Quantity: 1, Price: 100}
	for i := 0; i < 2; i++ {
		if err := u.RecordTransaction(tx); err != nil {
			t.Fatal(err)
		}
	}
	if len(u.Transactions) != 1 || u.Positions[0].Quantity != 1 || u.Positions[0].Symbol != "AAPL" {
		t.Fatal("retry duplicated trade")
	}
	tx.Quantity = 2
	if err := u.RecordTransaction(tx); err == nil {
		t.Fatal("accepted conflicting id")
	}
}

func TestStaleRequestReloadsStateInsideMutation(t *testing.T) {
	repo := &fakeRepository{}
	root, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := root.ForPortfolio(root.ActivePortfolio(), nil)
	second, _ := root.ForPortfolio(root.ActivePortfolio(), nil)
	tx := domain.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100}
	if err := first.RecordTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if err := second.RecordTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if repo.positions[root.ActivePortfolio()][0].Quantity != 2 {
		t.Fatal("stale snapshot overwrote previous trade")
	}
}
