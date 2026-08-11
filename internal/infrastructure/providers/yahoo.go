package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"atlas/internal/domain/market"
)

type YahooFinanceProvider struct {
	BaseURL string
	Client  *http.Client
	// Now supplies the current time for dropUnclosedBars' "has this candle finished forming"
	// test. A field rather than a direct time.Now call so tests can fix the clock relative to
	// their fixture timestamps; nil means time.Now.
	Now func() time.Time
}

func NewYahooFinanceProvider() *YahooFinanceProvider {
	return &YahooFinanceProvider{
		BaseURL: "https://query2.finance.yahoo.com/v8/finance/chart",
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *YahooFinanceProvider) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// yahooUserAgent is required: Yahoo's unofficial chart endpoint returns HTTP 429 for
// requests without a browser-like User-Agent, regardless of host or query parameters. A bare,
// generic UA with no app-identifying details is enough and is deliberately all this sends.
const yahooUserAgent = "Mozilla/5.0"

// interval is hourly, not daily: daily bars are too coarse for the trading-oriented use this
// engine targets. Yahoo's unofficial chart endpoint keeps roughly 730 days of 1h history; window
// (COLLECTOR_WINDOW_DAYS, default 30) stays well inside that, so this doesn't need clamping —
// requesting further back than Yahoo retains just returns whatever it still has, not an error.
const yahooInterval = "1h"

func (p *YahooFinanceProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	if window <= 0 {
		window = 30 // mirrors config.Load's COLLECTOR_WINDOW_DAYS default; guards direct calls (e.g. tests) that skip it
	}
	period2 := time.Now().UTC()
	period1 := period2.AddDate(0, 0, -window)
	url := fmt.Sprintf("%s/%s?interval=%s&period1=%d&period2=%d&events=history", p.BaseURL, symbol, yahooInterval, period1.Unix(), period2.Unix())

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", yahooUserAgent)

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected provider status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Chart struct {
			Result []struct {
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Open   []float64 `json:"open"`
						High   []float64 `json:"high"`
						Low    []float64 `json:"low"`
						Close  []float64 `json:"close"`
						Volume []float64 `json:"volume"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	if len(payload.Chart.Result) == 0 {
		return nil, fmt.Errorf("no result returned for symbol %s", symbol)
	}

	result := payload.Chart.Result[0]
	quote := result.Indicators.Quote[0]
	bars := make([]market.Bar, 0, len(result.Timestamp))

	for i := range result.Timestamp {
		bar := market.Bar{
			Symbol:    symbol,
			Open:      safeValue(quote.Open, i),
			High:      safeValue(quote.High, i),
			Low:       safeValue(quote.Low, i),
			Close:     safeValue(quote.Close, i),
			Volume:    safeValue(quote.Volume, i),
			Timestamp: time.Unix(result.Timestamp[i], 0),
		}
		if !hasUsablePrices(bar) {
			continue
		}
		bars = append(bars, bar)
	}

	return dropUnclosedBars(bars, p.now()), nil
}

// FetchInstrumentInfo reports what symbol actually is — its display name and instrument type —
// read off the same chart endpoint FetchBars already uses, but with a minimal one-day/daily
// request rather than a full bars window: the "meta" object it's after (symbol, instrumentType,
// shortName/longName, fullExchangeName) is identical regardless of range/interval, and this is
// called far less often than FetchBars (once per symbol, at watchlist-add time or from the
// Watchlist page's "Refresh info" button — see internal/app/web_handlers.go — not on every
// poll), so there's no reason to pay for hourly history just to read four metadata fields.
func (p *YahooFinanceProvider) FetchInstrumentInfo(symbol string) (market.InstrumentInfo, error) {
	url := fmt.Sprintf("%s/%s?interval=1d&range=1d", p.BaseURL, symbol)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return market.InstrumentInfo{}, err
	}
	req.Header.Set("User-Agent", yahooUserAgent)

	resp, err := p.Client.Do(req)
	if err != nil {
		return market.InstrumentInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return market.InstrumentInfo{}, fmt.Errorf("unexpected provider status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return market.InstrumentInfo{}, err
	}

	var payload struct {
		Chart struct {
			Result []struct {
				Meta struct {
					InstrumentType   string `json:"instrumentType"`
					ShortName        string `json:"shortName"`
					LongName         string `json:"longName"`
					FullExchangeName string `json:"fullExchangeName"`
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return market.InstrumentInfo{}, err
	}
	if len(payload.Chart.Result) == 0 {
		return market.InstrumentInfo{}, fmt.Errorf("no result returned for symbol %s", symbol)
	}

	// longName is the fuller form ("Apple Inc." vs. "Apple"-ish shortName) but Yahoo leaves it
	// empty for some instrument types (e.g. futures contracts) that do set shortName.
	meta := payload.Chart.Result[0].Meta
	name := meta.LongName
	if name == "" {
		name = meta.ShortName
	}
	return market.InstrumentInfo{
		Symbol:   symbol,
		Name:     name,
		Type:     meta.InstrumentType,
		Exchange: meta.FullExchangeName,
	}, nil
}

// yahooIntervalDuration is yahooInterval expressed as a Duration. Kept next to it so the two
// cannot drift: dropUnclosedBars needs to know when a candle closes, which the query string's
// "1h" only states as text.
const yahooIntervalDuration = time.Hour

// dropUnclosedBars removes trailing bars that have not finished forming yet.
//
// Yahoo's chart endpoint appends the live quote to an intraday series as an extra data point:
// its timestamp is the moment of the request rather than a candle boundary, and its OHLC are all
// the current price with volume 0. Measured against production on 2026-08-10, all 47 watchlist
// symbols carried exactly such a row as their newest bar, and 5.4% of the stored price history
// (2391 of 44198 rows) had accumulated from earlier polls.
//
// That row is silently wrong for every consumer downstream. The decision engine's momentum term
// reads (close-open)/open on the newest bar, which is identically 0 there, so its threshold
// could never fire on live traffic; the ML feature vector reads that bar's volume, so inference
// always saw 0 while the model had been trained on real volumes. Nothing errors in either case —
// the numbers are just wrong. Acting only on closed candles is also the ordinary discipline for
// bar-based signals: anything computed on a forming candle keeps changing until it closes, so a
// recommendation derived from one repaints rather than holds.
//
// Yahoo's timestamps are candle *start* times, so a candle is closed once start+interval has
// passed. The test is deliberately time-based rather than "drop rows that look degenerate": an
// illiquid instrument can genuinely print a flat, zero-volume hour, and that candle is real
// history worth keeping. The loop handles more than one trailing unclosed bar, which a single
// check would miss if the provider ever returned several.
func dropUnclosedBars(bars []market.Bar, now time.Time) []market.Bar {
	for len(bars) > 0 && bars[len(bars)-1].Timestamp.Add(yahooIntervalDuration).After(now) {
		bars = bars[:len(bars)-1]
	}
	return bars
}

// hasUsablePrices rejects bars whose OHLC did not survive parsing as real prices.
//
// Yahoo emits a timestamp for every slot in the interval grid, including ones the exchange was
// closed for — market holidays, halts, half-days — and fills the OHLC arrays with JSON null
// there. null unmarshals into float64 as 0, so those slots arrive here as bars priced at zero,
// indistinguishable from data by anything downstream.
//
// Nothing downstream survived that. The backtester's final equity is cash + quantity*close, so a
// zero-priced last bar reported exactly -100% return and 100% drawdown: on 2026-08-10 four WSE
// large caps (ALE.WA, KGH.WA, LPP.WA, PZU.WA) showed a total wipeout on a long-only strategy,
// which was Easter Monday's empty bar, not a trading result. Indicators fared no better — RSI,
// EMA, ATR and realized volatility all saw a crash to zero and an instant recovery — and the
// same rows went into ML training as genuine observations. 35 of them had accumulated, 30 on
// WSE symbols where the Polish holiday calendar diverges from the US one.
//
// A traded instrument is never worth zero, so a non-positive price is always missing data rather
// than a real quote. The bar is dropped instead of interpolated: a closed exchange has no
// hourly candle, and inventing one would be inventing data.
func hasUsablePrices(bar market.Bar) bool {
	return bar.Open > 0 && bar.High > 0 && bar.Low > 0 && bar.Close > 0
}

func safeValue(values []float64, idx int) float64 {
	if idx < 0 || idx >= len(values) {
		return 0
	}
	return values[idx]
}

func mustFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
