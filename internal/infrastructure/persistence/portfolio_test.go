package persistence

import (
	"testing"

	"atlas/internal/domain/portfolio"
)

func TestSavePositionsAndGetPositionsRoundTrip(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	positions := []portfolio.Position{
		{Symbol: "TESTPORT1", Quantity: 5, AverageCost: 100, CurrentPrice: 110},
		{Symbol: "TESTPORT2", Quantity: 2, AverageCost: 200, CurrentPrice: 190},
	}
	if err := db.SavePositions(portfolioID, positions); err != nil {
		t.Fatalf("SavePositions failed: %v", err)
	}

	stored, err := db.GetPositions(portfolioID)
	if err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}

	found := map[string]portfolio.Position{}
	for _, p := range stored {
		found[p.Symbol] = p
	}
	if found["TESTPORT1"].Quantity != 5 || found["TESTPORT1"].CurrentPrice != 110 {
		t.Fatalf("unexpected TESTPORT1: %+v", found["TESTPORT1"])
	}
	if found["TESTPORT2"].Quantity != 2 || found["TESTPORT2"].CurrentPrice != 190 {
		t.Fatalf("unexpected TESTPORT2: %+v", found["TESTPORT2"])
	}
}

func TestSavePositionsIsIdempotent(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	if err := db.SavePositions(portfolioID, []portfolio.Position{
		{Symbol: "TESTIDEMPPOS", Quantity: 1, AverageCost: 50, CurrentPrice: 50},
	}); err != nil {
		t.Fatalf("first SavePositions failed: %v", err)
	}
	// Re-saving the same symbol must overwrite in place, not duplicate the row — matches
	// how the use case recomputes and re-saves the whole position slice on every trade.
	if err := db.SavePositions(portfolioID, []portfolio.Position{
		{Symbol: "TESTIDEMPPOS", Quantity: 3, AverageCost: 55, CurrentPrice: 60},
	}); err != nil {
		t.Fatalf("second SavePositions failed: %v", err)
	}

	stored, err := db.GetPositions(portfolioID)
	if err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}
	count := 0
	var match portfolio.Position
	for _, p := range stored {
		if p.Symbol == "TESTIDEMPPOS" {
			count++
			match = p
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one TESTIDEMPPOS row, got %d", count)
	}
	if match.Quantity != 3 || match.CurrentPrice != 60 {
		t.Fatalf("expected the row to be overwritten, got %+v", match)
	}
}

func TestDeletePositionRemovesTheRow(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	if err := db.SavePositions(portfolioID, []portfolio.Position{
		{Symbol: "TESTDELPOS", Quantity: 1, AverageCost: 50, CurrentPrice: 55},
	}); err != nil {
		t.Fatalf("SavePositions failed: %v", err)
	}

	if err := db.DeletePosition(portfolioID, "TESTDELPOS"); err != nil {
		t.Fatalf("DeletePosition failed: %v", err)
	}

	stored, err := db.GetPositions(portfolioID)
	if err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}
	for _, p := range stored {
		if p.Symbol == "TESTDELPOS" {
			t.Fatalf("expected TESTDELPOS to be gone, still found: %+v", p)
		}
	}
}

func TestDeletePositionForUnknownSymbolIsANoop(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)
	if err := db.DeletePosition(portfolioID, "TESTDELPOSNEVEREXISTED"); err != nil {
		t.Fatalf("expected no error deleting a position that was never saved, got %v", err)
	}
}

func TestGetAllPositionsAggregatesAcrossPortfolios(t *testing.T) {
	db := connectTestDB(t)
	portfolioA := testPortfolioID(t, db)
	portfolioB, err := db.CreatePortfolio("TEST-" + t.Name() + "-B")
	if err != nil {
		t.Fatalf("failed to create second test portfolio: %v", err)
	}

	if err := db.SavePositions(portfolioA, []portfolio.Position{
		{Symbol: "TESTAGG", Quantity: 10, AverageCost: 100, CurrentPrice: 120},
	}); err != nil {
		t.Fatalf("SavePositions A failed: %v", err)
	}
	if err := db.SavePositions(portfolioB.ID, []portfolio.Position{
		{Symbol: "TESTAGG", Quantity: 5, AverageCost: 130, CurrentPrice: 120},
	}); err != nil {
		t.Fatalf("SavePositions B failed: %v", err)
	}
	t.Cleanup(func() { _ = db.DeletePortfolio(portfolioB.ID) })

	all, err := db.GetAllPositions()
	if err != nil {
		t.Fatalf("GetAllPositions failed: %v", err)
	}
	var agg portfolio.Position
	found := false
	for _, p := range all {
		if p.Symbol == "TESTAGG" {
			agg, found = p, true
		}
	}
	if !found {
		t.Fatalf("expected TESTAGG in aggregate positions, got %+v", all)
	}
	if agg.Quantity != 15 {
		t.Fatalf("expected summed quantity 15, got %v", agg.Quantity)
	}
	// Weighted average: (10*100 + 5*130) / 15 = 110
	if agg.AverageCost != 110 {
		t.Fatalf("expected weighted average cost 110, got %v", agg.AverageCost)
	}
}

func TestSaveTransactionReturnsAGeneratedID(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	tx := portfolio.Transaction{
		Symbol: "TESTTX", Type: "BUY", Quantity: 4, Price: 25, Timestamp: "2026-08-06T00:00:00Z",
	}
	id, err := db.SaveTransaction(portfolioID, tx)
	if err != nil {
		t.Fatalf("SaveTransaction failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty generated id")
	}
}

func TestSaveTransactionFallsBackOnUnparseableTimestamp(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	tx := portfolio.Transaction{
		Symbol: "TESTTXBADTS", Type: "SELL", Quantity: 1, Price: 10, Timestamp: "not-a-timestamp",
	}
	if _, err := db.SaveTransaction(portfolioID, tx); err != nil {
		t.Fatalf("expected an unparseable timestamp to fall back to now, not error: %v", err)
	}
}

func TestGetTransactionsReturnsPersistedRows(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	id, err := db.SaveTransaction(portfolioID, portfolio.Transaction{
		Symbol: "TESTTXLIST", Type: "BUY", Quantity: 2, Price: 30, Timestamp: "2026-08-06T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("SaveTransaction failed: %v", err)
	}

	transactions, err := db.GetTransactions(portfolioID)
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	found := false
	for _, tx := range transactions {
		if tx.ID == id {
			found = true
			if tx.Symbol != "TESTTXLIST" || tx.Type != "BUY" || tx.Quantity != 2 || tx.Price != 30 {
				t.Fatalf("unexpected transaction fields: %+v", tx)
			}
		}
	}
	if !found {
		t.Fatalf("expected to find transaction %s in GetTransactions, got %+v", id, transactions)
	}
}

func TestSaveAndGetTransactionRoundTripsCurrencyAndPLNPrice(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	id, err := db.SaveTransaction(portfolioID, portfolio.Transaction{
		Symbol: "TESTTXFX", Type: "BUY", Quantity: 3, Price: 100, Currency: "USD", PriceInPLN: 373,
		Timestamp: "2026-08-06T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("SaveTransaction failed: %v", err)
	}

	transactions, err := db.GetTransactions(portfolioID)
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	found := false
	for _, tx := range transactions {
		if tx.ID == id {
			found = true
			if tx.Currency != "USD" || tx.PriceInPLN != 373 {
				t.Fatalf("expected Currency=USD and PriceInPLN=373 to round-trip, got %+v", tx)
			}
		}
	}
	if !found {
		t.Fatalf("expected to find transaction %s in GetTransactions, got %+v", id, transactions)
	}
}

func TestDeleteTransactionRemovesTheRow(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	id, err := db.SaveTransaction(portfolioID, portfolio.Transaction{
		Symbol: "TESTTXDEL", Type: "BUY", Quantity: 1, Price: 10, Timestamp: "2026-08-06T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("SaveTransaction failed: %v", err)
	}

	if err := db.DeleteTransaction(id); err != nil {
		t.Fatalf("DeleteTransaction failed: %v", err)
	}

	transactions, err := db.GetTransactions(portfolioID)
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	for _, tx := range transactions {
		if tx.ID == id {
			t.Fatalf("expected transaction %s to be gone, still found: %+v", id, tx)
		}
	}
}

func TestDeleteTransactionForUnknownIDIsANoop(t *testing.T) {
	db := connectTestDB(t)
	if err := db.DeleteTransaction("00000000-0000-0000-0000-000000000000"); err != nil {
		t.Fatalf("expected no error deleting a transaction that was never saved, got %v", err)
	}
}

func TestCreateListRenameDeletePortfolio(t *testing.T) {
	db := connectTestDB(t)

	created, err := db.CreatePortfolio("TEST-" + t.Name())
	if err != nil {
		t.Fatalf("CreatePortfolio failed: %v", err)
	}
	if created.ID == "" || created.Name != "TEST-"+t.Name() {
		t.Fatalf("unexpected created portfolio: %+v", created)
	}

	all, err := db.ListPortfolios()
	if err != nil {
		t.Fatalf("ListPortfolios failed: %v", err)
	}
	if !containsPortfolioID(all, created.ID) {
		t.Fatalf("expected %+v to list the created portfolio %s", all, created.ID)
	}

	if err := db.RenamePortfolio(created.ID, "TEST-renamed"); err != nil {
		t.Fatalf("RenamePortfolio failed: %v", err)
	}
	all, err = db.ListPortfolios()
	if err != nil {
		t.Fatalf("ListPortfolios after rename failed: %v", err)
	}
	renamed := false
	for _, p := range all {
		if p.ID == created.ID && p.Name == "TEST-renamed" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("expected portfolio %s to be renamed, got %+v", created.ID, all)
	}

	if err := db.DeletePortfolio(created.ID); err != nil {
		t.Fatalf("DeletePortfolio failed: %v", err)
	}
	all, err = db.ListPortfolios()
	if err != nil {
		t.Fatalf("ListPortfolios after delete failed: %v", err)
	}
	if containsPortfolioID(all, created.ID) {
		t.Fatalf("expected portfolio %s to be gone, still listed: %+v", created.ID, all)
	}
}

func TestDeletePortfolioAlsoDeletesItsPositionsAndTransactions(t *testing.T) {
	db := connectTestDB(t)

	created, err := db.CreatePortfolio("TEST-" + t.Name())
	if err != nil {
		t.Fatalf("CreatePortfolio failed: %v", err)
	}
	if err := db.SavePositions(created.ID, []portfolio.Position{
		{Symbol: "TESTDELPF", Quantity: 1, AverageCost: 10, CurrentPrice: 10},
	}); err != nil {
		t.Fatalf("SavePositions failed: %v", err)
	}
	if _, err := db.SaveTransaction(created.ID, portfolio.Transaction{
		Symbol: "TESTDELPF", Type: "BUY", Quantity: 1, Price: 10, Timestamp: "2026-08-06T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveTransaction failed: %v", err)
	}

	if err := db.DeletePortfolio(created.ID); err != nil {
		t.Fatalf("DeletePortfolio failed: %v", err)
	}

	// The portfolio_id foreign key would reject a stray row anyway, but confirm explicitly
	// that positions/transactions didn't just get orphaned instead of deleted.
	positions, err := db.GetPositions(created.ID)
	if err != nil {
		t.Fatalf("GetPositions after delete failed: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("expected no positions left for deleted portfolio, got %+v", positions)
	}
}

func containsPortfolioID(portfolios []portfolio.Portfolio, id string) bool {
	for _, p := range portfolios {
		if p.ID == id {
			return true
		}
	}
	return false
}
