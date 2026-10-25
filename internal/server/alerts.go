package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

// Notifier sends a human-readable alert somewhere.
type Notifier interface {
	Notify(ctx context.Context, msg string) error
}

type LogNotifier struct{}

func (LogNotifier) Notify(_ context.Context, msg string) error {
	log.Printf("ALERTE: %s", msg)
	return nil
}

type TelegramNotifier struct {
	Token  string
	ChatID string
	Client *http.Client
}

func (t TelegramNotifier) Notify(ctx context.Context, msg string) error {
	endpoint := "https://api.telegram.org/bot" + t.Token + "/sendMessage"
	form := url.Values{"chat_id": {t.ChatID}, "text": {msg}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.URL.RawQuery = form.Encode()
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: status %d", resp.StatusCode)
	}
	return nil
}

// Alerter watches rigs and notifies once when a rig goes offline or overheats,
// and once more when it recovers.
type Alerter struct {
	Store    *Store
	Notifier Notifier
	MaxTempC float64

	offline map[string]bool
	hot     map[string]bool
}

func (a *Alerter) Check(ctx context.Context) error {
	if a.offline == nil {
		a.offline, a.hot = map[string]bool{}, map[string]bool{}
	}
	rigs, err := a.Store.Rigs()
	if err != nil {
		return err
	}
	for _, r := range rigs {
		if r.LastSeen == 0 {
			continue // never connected yet
		}
		if !r.Online && !a.offline[r.ID] {
			a.notify(ctx, fmt.Sprintf("🔴 %s est hors ligne (dernier signal %s)", r.Name, time.Unix(r.LastSeen, 0).Format("15:04:05")))
		} else if r.Online && a.offline[r.ID] {
			a.notify(ctx, fmt.Sprintf("🟢 %s est de retour en ligne", r.Name))
		}
		a.offline[r.ID] = !r.Online

		var hottest float64
		for _, g := range r.Report.GPUs {
			hottest = max(hottest, g.TempC)
		}
		isHot := r.Online && hottest >= a.MaxTempC
		if isHot && !a.hot[r.ID] {
			a.notify(ctx, fmt.Sprintf("🔥 %s : un GPU atteint %.0f °C (seuil %.0f °C)", r.Name, hottest, a.MaxTempC))
		}
		a.hot[r.ID] = isHot
	}
	return nil
}

func (a *Alerter) notify(ctx context.Context, msg string) {
	if err := a.Notifier.Notify(ctx, msg); err != nil {
		log.Printf("alert failed: %v", err)
	}
}

func (a *Alerter) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := a.Check(ctx); err != nil {
			log.Printf("alert check: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
