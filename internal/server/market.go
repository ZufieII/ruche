package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Market is what the dashboard needs to estimate Monero earnings.
type Market struct {
	Difficulty float64 `json:"difficulty"` // network difficulty
	RewardXMR  float64 `json:"reward_xmr"` // current block reward
	PriceEUR   float64 `json:"price_eur"`  // 0 when unknown
	PriceUSD   float64 `json:"price_usd"`  // 0 when unknown
	UpdatedAt  int64   `json:"updated_at"` // unix seconds
	Error      string  `json:"error,omitempty"`
}

// MarketSource fetches Monero network stats and price, caching them so the
// public APIs are called at most once per TTL whatever the number of viewers.
type MarketSource struct {
	NetworkURL string // nodejs-pool style /api/network/stats
	PriceURL   string // CoinGecko simple price
	TTL        time.Duration
	Client     *http.Client

	mu     sync.Mutex
	cached Market
}

func NewMarketSource() *MarketSource {
	return &MarketSource{
		NetworkURL: "https://supportxmr.com/api/network/stats",
		PriceURL:   "https://api.coingecko.com/api/v3/simple/price?ids=monero&vs_currencies=eur,usd",
		TTL:        10 * time.Minute,
		Client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *MarketSource) Get(ctx context.Context) Market {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached.UpdatedAt != 0 && time.Since(time.Unix(m.cached.UpdatedAt, 0)) < m.TTL {
		return m.cached
	}
	fresh, err := m.fetch(ctx)
	if err != nil {
		// Keep the last good values; only report the error.
		stale := m.cached
		stale.Error = err.Error()
		return stale
	}
	m.cached = fresh
	return fresh
}

func (m *MarketSource) fetch(ctx context.Context) (Market, error) {
	var net struct {
		Difficulty float64 `json:"difficulty"`
		Value      float64 `json:"value"` // block reward in atomic units (1e-12 XMR)
	}
	if err := m.getJSON(ctx, m.NetworkURL, &net); err != nil {
		return Market{}, fmt.Errorf("réseau Monero: %w", err)
	}
	if net.Difficulty <= 0 || net.Value <= 0 {
		return Market{}, fmt.Errorf("réseau Monero: réponse incomplète")
	}
	out := Market{Difficulty: net.Difficulty, RewardXMR: net.Value / 1e12, UpdatedAt: time.Now().Unix()}

	var price struct {
		Monero struct {
			EUR float64 `json:"eur"`
			USD float64 `json:"usd"`
		} `json:"monero"`
	}
	if err := m.getJSON(ctx, m.PriceURL, &price); err != nil {
		// Earnings in XMR still work without a price.
		out.Error = "prix: " + err.Error()
	}
	out.PriceEUR, out.PriceUSD = price.Monero.EUR, price.Monero.USD
	return out, nil
}

func (m *MarketSource) getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("statut %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
