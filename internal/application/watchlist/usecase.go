package watchlist

import (
	"errors"
	"fmt"
	"strings"

	"atlas/internal/domain/watchlist"
)

// UseCase keeps a watchlist of symbols the user wants to track. Items is an in-memory
// working copy; when Repository is set, it's hydrated from persisted state on construction
// and written through on every mutation, so tracked symbols survive restarts.
type UseCase struct {
	Items      []watchlist.Item
	Repository Repository
}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// NewUseCaseWithRepository hydrates in-memory state from the repository up front. A load
// failure here is returned rather than silently starting empty.
func NewUseCaseWithRepository(repo Repository) (*UseCase, error) {
	uc := &UseCase{Repository: repo}
	if err := uc.Reload(); err != nil {
		return nil, err
	}
	return uc, nil
}

// Reload re-reads Items from Repository, discarding whatever the in-memory working copy
// currently holds — see portfolio.UseCase.Reload, the same fix for the same class of bug.
func (u *UseCase) Reload() error {
	if u.Repository == nil {
		u.Items = nil
		return nil
	}
	items, err := u.Repository.GetItems()
	if err != nil {
		return err
	}
	u.Items = items
	return nil
}

// NormalizeSymbol puts a ticker into the single canonical form the rest of Atlas expects:
// upper-case, no surrounding whitespace.
//
// This lives here, on the way into the use case, rather than in each caller. It used to be done
// only by the dashboard handler, so the JSON API accepted whatever it was given: POSTing "aapl"
// created a watchlist entry that Add's dedup would never match against "AAPL" and that the
// collector would then try to fetch verbatim. Import made that inconsistency impossible to
// ignore — a file exported from one instance has to land identically on another — so the fix is
// to normalise once, at the boundary every path already goes through.
func NormalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

// Add tracks a symbol. Adding an already-tracked symbol is a no-op.
func (u *UseCase) Add(symbol string, addedAt string) error {
	symbol = NormalizeSymbol(symbol)
	if symbol == "" {
		return errors.New("symbol is required")
	}
	for _, item := range u.Items {
		if item.Symbol == symbol {
			return nil
		}
	}
	item := watchlist.Item{Symbol: symbol, AddedAt: addedAt}
	u.Items = append(u.Items, item)
	if u.Repository != nil {
		if err := u.Repository.SaveItem(item); err != nil {
			return err
		}
	}
	return nil
}

// List returns every tracked symbol.
func (u *UseCase) List() ([]watchlist.Item, error) {
	return u.Items, nil
}

// Remove untracks a symbol. Removing a symbol that isn't tracked is a no-op, matching Add's
// idempotent stance.
func (u *UseCase) Remove(symbol string) error {
	symbol = NormalizeSymbol(symbol)
	if symbol == "" {
		return errors.New("symbol is required")
	}
	kept := u.Items[:0:0]
	for _, item := range u.Items {
		if item.Symbol != symbol {
			kept = append(kept, item)
		}
	}
	u.Items = kept
	if u.Repository != nil {
		return u.Repository.DeleteItem(symbol)
	}
	return nil
}

// RemoveMany untracks several symbols at once (the dashboard's checkbox-select bulk delete) —
// each symbol still goes through Remove individually rather than a single batched repository
// call, matching portfolio.UseCase.RemovePositions/RemoveTransactions' reasoning. Errors from
// individual symbols are joined rather than aborting the whole batch.
func (u *UseCase) RemoveMany(symbols []string) error {
	var errs []error
	for _, symbol := range symbols {
		if err := u.Remove(symbol); err != nil {
			errs = append(errs, fmt.Errorf("remove watchlist item %s: %w", symbol, err))
		}
	}
	return errors.Join(errs...)
}

// ImportMode selects what an import does about symbols already tracked but absent from the
// incoming set.
type ImportMode string

const (
	// ImportMerge adds what's missing and leaves everything else alone. Idempotent: importing
	// the same file twice changes nothing the second time. This is the default because it
	// cannot destroy anything.
	ImportMerge ImportMode = "merge"

	// ImportReplace additionally untracks symbols the incoming set doesn't contain, making the
	// watchlist an exact mirror of the file — what you want when copying one instance's state
	// onto another, and a loaded gun pointed at a populated watchlist otherwise.
	ImportReplace ImportMode = "replace"
)

// ParseImportMode resolves a mode from a query parameter or form value. An empty value means
// merge, so a caller who forgets the parameter gets the safe behaviour rather than the
// destructive one. Anything else is rejected rather than silently coerced.
func ParseImportMode(value string) (ImportMode, error) {
	switch ImportMode(strings.ToLower(strings.TrimSpace(value))) {
	case "":
		return ImportMerge, nil
	case ImportMerge:
		return ImportMerge, nil
	case ImportReplace:
		return ImportReplace, nil
	default:
		return "", fmt.Errorf("unknown import mode %q: must be %q or %q", value, ImportMerge, ImportReplace)
	}
}

// MaxImportItems bounds how many symbols one import may carry. Generous next to any real
// watchlist (the Author's runs to a few dozen) and small enough that a malformed or hostile file
// can't turn a single request into thousands of database writes.
const MaxImportItems = 5000

// ImportResult reports what an import actually did, so the caller can say "added 31, skipped 16
// already present" instead of "ok" — the difference between a user knowing the import worked and
// merely being told so.
type ImportResult struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
	Removed int `json:"removed"`
}

// Import applies a set of watchlist items, per mode.
//
// The whole batch is validated before anything is written. That matters because Repository has
// no transaction spanning multiple items (see SaveItem/DeleteItem): a batch that fails halfway
// would leave the watchlist in a state that is neither the old one nor the new one, which is
// worse than refusing the file outright. So malformed input is rejected while the watchlist is
// still untouched.
//
// An item's AddedAt is preserved when present, so an exported file restores as a faithful
// snapshot rather than restamping every symbol with the import time; blank values fall back to
// now. Symbols already tracked keep their existing AddedAt — a re-import never rewrites history
// that is already there.
func (u *UseCase) Import(items []watchlist.Item, mode ImportMode, now string) (ImportResult, error) {
	if mode != ImportMerge && mode != ImportReplace {
		return ImportResult{}, fmt.Errorf("unknown import mode %q", mode)
	}
	if len(items) > MaxImportItems {
		return ImportResult{}, fmt.Errorf("import carries %d items, more than the %d maximum", len(items), MaxImportItems)
	}

	// Validate and canonicalise first, mutating nothing.
	incoming := make(map[string]string, len(items)) // symbol -> AddedAt
	order := make([]string, 0, len(items))
	for i, item := range items {
		symbol := NormalizeSymbol(item.Symbol)
		if symbol == "" {
			return ImportResult{}, fmt.Errorf("item %d has an empty symbol", i)
		}
		if _, duplicate := incoming[symbol]; duplicate {
			return ImportResult{}, fmt.Errorf("symbol %s appears more than once in the import", symbol)
		}
		addedAt := strings.TrimSpace(item.AddedAt)
		if addedAt == "" {
			addedAt = now
		}
		incoming[symbol] = addedAt
		order = append(order, symbol)
	}

	existing := make(map[string]bool, len(u.Items))
	for _, item := range u.Items {
		existing[item.Symbol] = true
	}

	var result ImportResult
	for _, symbol := range order {
		if existing[symbol] {
			result.Skipped++
			continue
		}
		if err := u.Add(symbol, incoming[symbol]); err != nil {
			return result, fmt.Errorf("add %s: %w", symbol, err)
		}
		result.Added++
	}

	if mode == ImportReplace {
		// Only the symbols the file doesn't mention are removed. Clearing everything and
		// re-adding would churn the repository and reset AddedAt on symbols that were never in
		// question.
		var stale []string
		for _, item := range u.Items {
			if _, kept := incoming[item.Symbol]; !kept {
				stale = append(stale, item.Symbol)
			}
		}
		if len(stale) > 0 {
			if err := u.RemoveMany(stale); err != nil {
				return result, err
			}
			result.Removed = len(stale)
		}
	}

	return result, nil
}
