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
}

func NewYahooFinanceProvider() *YahooFinanceProvider {
	return &YahooFinanceProvider{
		BaseURL: "https://query2.finance.yahoo.com/v8/finance/chart",
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// yahooUserAgent is required: Yahoo's unofficial chart endpoint returns HTTP 429 for
// requests without a browser-like User-Agent, regardless of host or query parameters.
const yahooUserAgent = "Mozilla/5.0 (compatible; AtlasPortfolioAdvisor/1.0)"

func (p *YahooFinanceProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	if window <= 0 {
		window = 30
	}
	period2 := time.Now().UTC()
	period1 := period2.AddDate(0, 0, -window)
	url := fmt.Sprintf("%s/%s?interval=1d&period1=%d&period2=%d&events=history", p.BaseURL, symbol, period1.Unix(), period2.Unix())

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
		bars = append(bars, bar)
	}

	return bars, nil
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
