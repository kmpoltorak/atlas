// Package exchangerate fetches real PLN exchange rates from NBP (Narodowy Bank Polski), Poland's
// central bank — the standard authoritative source for PLN reference rates, free and keyless.
package exchangerate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"atlas/internal/domain/currency"
)

// NBPProvider fetches NBP's Table A — the average/mid reference rate NBP itself publishes once
// per business day (~11:45 CET), one rate per currency. Table A (not Table C, NBP's buy/sell
// cash-exchange spread) is the standard choice for valuation rather than physical currency
// exchange — see PROJECT_CONTEXT.md's "Market data providers" for what this endpoint actually
// does and does not guarantee.
type NBPProvider struct {
	BaseURL string
	Client  *http.Client
}

func NewNBPProvider() *NBPProvider {
	return &NBPProvider{
		BaseURL: "https://api.nbp.pl/api/exchangerates/tables/A",
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// nbpUserAgent works around the same class of issue as yahoo.go's yahooUserAgent: NBP's API
// returns HTTP 403 for Go's default "Go-http-client/*" User-Agent specifically — confirmed live
// (curl with the same UA also gets 403; curl's own default UA, or no override at all, gets 200)
// — regardless of host or query parameters. Not NBP requiring "a browser" exactly, just
// rejecting this one default string — a bare, generic UA with no app-identifying details is
// enough and is deliberately all this sends.
const nbpUserAgent = "Mozilla/5.0"

type nbpTable struct {
	Rates []struct {
		Code string  `json:"code"`
		Mid  float64 `json:"mid"`
	} `json:"rates"`
}

// FetchRates returns today's PLN-per-unit mid rate for every currency.All() entry except PLN
// itself (which is always 1:1 by definition — see currency.Base). Errors if either EUR or USD
// is missing from NBP's response, since a partial rate set would silently break conversions for
// whichever currency got dropped.
func (p *NBPProvider) FetchRates() (map[currency.Code]float64, error) {
	req, err := http.NewRequest(http.MethodGet, p.BaseURL+"/?format=json", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", nbpUserAgent)

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch NBP table A: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected NBP status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read NBP response: %w", err)
	}

	var tables []nbpTable
	if err := json.Unmarshal(body, &tables); err != nil {
		return nil, fmt.Errorf("parse NBP response: %w", err)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("NBP returned no tables")
	}

	rates := make(map[currency.Code]float64)
	for _, rate := range tables[0].Rates {
		switch currency.Code(rate.Code) {
		case currency.EUR:
			rates[currency.EUR] = rate.Mid
		case currency.USD:
			rates[currency.USD] = rate.Mid
		}
	}
	for _, code := range []currency.Code{currency.EUR, currency.USD} {
		if _, ok := rates[code]; !ok {
			return nil, fmt.Errorf("NBP table A did not include a rate for %s", code)
		}
	}
	return rates, nil
}

// nbpSingleRate is the shape of NBP's per-currency endpoint, which returns one currency's rate
// for one date rather than the whole table.
type nbpSingleRate struct {
	Rates []struct {
		No            string  `json:"no"`
		EffectiveDate string  `json:"effectiveDate"`
		Mid           float64 `json:"mid"`
	} `json:"rates"`
}

// FetchRateOn returns the PLN-per-unit mid rate NBP published for code on a specific date, for
// converting a transaction entered with a past timestamp.
//
// Using today's rate for a trade from three months ago is simply a wrong number: it records a
// cost basis the trade never had, and every ROI and risk figure derived from it inherits the
// error. NBP publishes Table A per date precisely so this can be looked up rather than guessed.
func (p *NBPProvider) FetchRateOn(code currency.Code, date time.Time) (float64, error) {
	if code == currency.Base {
		return 1, nil
	}
	url := fmt.Sprintf("%s/rates/A/%s/%s/?format=json",
		strings.TrimSuffix(p.BaseURL, "/tables/A"), strings.ToLower(string(code)),
		date.UTC().Format("2006-01-02"))
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", nbpUserAgent)

	resp, err := p.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch NBP rate for %s on %s: %w", code, date.Format("2006-01-02"), err)
	}
	defer resp.Body.Close()

	// 404 is NBP's answer for "no publication that day", which is the normal weekend/holiday
	// case rather than a failure — see ErrNoRateForDate.
	if resp.StatusCode == http.StatusNotFound {
		return 0, currency.ErrNoRateForDate
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected NBP status for %s on %s: %d", code, date.Format("2006-01-02"), resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read NBP response: %w", err)
	}
	var payload nbpSingleRate
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("decode NBP response: %w", err)
	}
	if len(payload.Rates) == 0 || payload.Rates[0].Mid <= 0 {
		return 0, currency.ErrNoRateForDate
	}
	return payload.Rates[0].Mid, nil
}
