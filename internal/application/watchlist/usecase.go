package watchlist

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas/internal/domain/watchlist"
)

// UseCase keeps a watchlist of symbols the user wants to track. Items is an in-memory
// working copy; when Repository is set, it's hydrated from persisted state on construction
// and written through on every mutation, so tracked symbols survive restarts.
type UseCase struct {
	mu         sync.RWMutex
	Items      []watchlist.Item
	Repository Repository
	// shared points back at the process-wide use case a request-scoped view was made from, and is
	// nil on that process-wide one. Only Reload needs it — see ForRequest.
	shared *UseCase
}

// ForRequest returns a view that persists through repository — the request-scoped storage handle
// — so a watchlist write started by a client that then disconnects is cancelled rather than
// running on against nobody.
//
// Scoping only Application.Storage was not enough, the same way it was not enough for portfolio
// views (see portfolio.UseCase.ForPortfolio): this use case holds its own repository reference,
// so however carefully a handler scoped its storage handle, every watchlist query still went
// through the original one.
//
// The view starts with no in-memory items, which costs nothing: with a repository configured,
// List re-reads, Import runs inside ReplaceWatchlist's transaction, and Add/Remove write through.
// The in-memory copy is only the no-database fallback, and a no-database use case is not scoped
// at all.
func (u *UseCase) ForRequest(repository Repository) *UseCase {
	if u == nil || u.Repository == nil || repository == nil {
		return u
	}
	return &UseCase{Repository: repository, shared: u}
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
	u.mu.Lock()
	err := u.reload()
	u.mu.Unlock()
	// A request-scoped view is discarded when the request ends, so reloading only that would leave
	// the long-lived copy — the one the alert loop and every other request start from — holding
	// whatever the bulk change just invalidated.
	if u.shared != nil {
		if sharedErr := u.shared.Reload(); err == nil {
			err = sharedErr
		}
	}
	return err
}

func (u *UseCase) reload() error {
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
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.add(symbol, addedAt)
}

func (u *UseCase) add(symbol string, addedAt string) error {
	symbol = NormalizeSymbol(symbol)
	if symbol == "" {
		return errors.New("symbol is required")
	}
	item := watchlist.Item{Symbol: symbol, AddedAt: addedAt}
	if u.Repository != nil {
		// Another process may have removed a locally cached item. Let the
		// repository decide whether this insert is a duplicate.
		if err := u.Repository.SaveItem(item); err != nil {
			return err
		}
	}
	for _, existing := range u.Items {
		if existing.Symbol == symbol {
			return nil
		}
	}
	u.Items = append(u.Items, item)
	return nil
}

// List returns every tracked symbol, re-reading persisted state when a Repository is configured.
//
// Read-through rather than serving the in-memory copy: with more than one replica, a symbol added
// on one process is invisible to the other until something reloads, and "the watchlist depends on
// which pod answered" is not a state this can be in. The list is a handful of rows behind one
// indexed query, so the cost of being right here is negligible. The in-memory copy remains as the
// no-database fallback (tests, a degraded start).
func (u *UseCase) List() ([]watchlist.Item, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Repository != nil {
		if err := u.reload(); err != nil {
			return nil, err
		}
	}
	return append([]watchlist.Item(nil), u.Items...), nil
}

// Remove untracks a symbol. Removing a symbol that isn't tracked is a no-op, matching Add's
// idempotent stance.
func (u *UseCase) Remove(symbol string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.remove(symbol)
}

func (u *UseCase) remove(symbol string) error {
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
	if u.Repository != nil {
		if err := u.Repository.DeleteItem(symbol); err != nil {
			return err
		}
	}
	u.Items = kept
	return nil
}

// RemoveMany untracks several symbols at once (the dashboard's checkbox-select bulk delete) —
// each symbol still goes through Remove individually rather than a single batched repository
// call, matching portfolio.UseCase.RemovePositions/RemoveTransactions' reasoning. Errors from
// individual symbols are joined rather than aborting the whole batch.
func (u *UseCase) RemoveMany(symbols []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.removeMany(symbols)
}

func (u *UseCase) removeMany(symbols []string) error {
	var errs []error
	for _, symbol := range symbols {
		if err := u.remove(symbol); err != nil {
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
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.importItems(items, mode, now)
}

func (u *UseCase) importItems(items []watchlist.Item, mode ImportMode, now string) (ImportResult, error) {
	if mode != ImportMerge && mode != ImportReplace {
		return ImportResult{}, fmt.Errorf("unknown import mode %q", mode)
	}
	if len(items) > MaxImportItems {
		return ImportResult{}, fmt.Errorf("import carries %d items, more than the %d maximum", len(items), MaxImportItems)
	}

	// An empty `now` means the caller has no opinion about when these were tracked from, not that
	// the timestamp should be stored empty — which is what it used to do, leaving a row the
	// database then had to guess about.
	if strings.TrimSpace(now) == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}

	// Validate and canonicalise first, mutating nothing and touching no database.
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
		if err := validateAddedAt(addedAt); err != nil {
			return ImportResult{}, fmt.Errorf("item %d (%s): %w", i, symbol, err)
		}
		incoming[symbol] = addedAt
		order = append(order, symbol)
	}

	// plan computes the desired end state from whatever the current state turns out to be. It is
	// pure, because with a repository it runs inside the import transaction.
	var result ImportResult
	plan := func(current []watchlist.Item) ([]watchlist.Item, error) {
		result = ImportResult{}
		existing := make(map[string]watchlist.Item, len(current))
		for _, item := range current {
			existing[item.Symbol] = item
		}
		desired := make([]watchlist.Item, 0, len(current)+len(order))
		for _, symbol := range order {
			if kept, ok := existing[symbol]; ok {
				// Already tracked: keep its original AddedAt. A re-import never rewrites history
				// that is already there.
				desired = append(desired, kept)
				result.Skipped++
				continue
			}
			desired = append(desired, watchlist.Item{Symbol: symbol, AddedAt: incoming[symbol]})
			result.Added++
		}
		if mode == ImportMerge {
			// Merge keeps everything the file doesn't mention.
			for _, item := range current {
				if _, mentioned := incoming[item.Symbol]; !mentioned {
					desired = append(desired, item)
				}
			}
			return desired, nil
		}
		for _, item := range current {
			if _, kept := incoming[item.Symbol]; !kept {
				result.Removed++
			}
		}
		return desired, nil
	}

	// Without a repository this is the in-memory working copy and there is nothing to serialize.
	if u.Repository == nil {
		desired, err := plan(u.Items)
		if err != nil {
			return ImportResult{}, err
		}
		u.Items = desired
		return result, nil
	}

	// One transaction under the watchlist row lock: the read, the plan and every write commit
	// together or not at all. Reading fresh and then writing item-by-item — which is what this
	// replaced — left a partial replacement on a later failure, and let a symbol added
	// concurrently escape the replacement entirely.
	replacer, ok := u.Repository.(TransactionalRepository)
	if !ok {
		return ImportResult{}, errors.New("repository does not support transactional import")
	}
	desired, err := replacer.ReplaceWatchlist(plan)
	if err != nil {
		return ImportResult{}, fmt.Errorf("import watchlist: %w", err)
	}
	u.Items = desired
	return result, nil
}

// validateAddedAt rejects a timestamp the repository would refuse later, so a bad row fails before
// anything is written rather than partway through.
func validateAddedAt(value string) error {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if _, err := time.Parse(layout, value); err == nil {
			return nil
		}
	}
	return fmt.Errorf("added_at %q must be RFC3339 or YYYY-MM-DD", value)
}
