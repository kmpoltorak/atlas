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
		BaseURL: "https://query1.finance.yahoo.com/v8/finance/chart",
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *YahooFinanceProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	url := fmt.Sprintf("%s/%s?interval=1d&range=%dd", p.BaseURL, symbol, window)
	resp, err := p.Client.Get(url)
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
