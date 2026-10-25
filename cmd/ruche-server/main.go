// Command ruche-server runs the API, the dashboard and the alerting loop.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"ruche/internal/server"
	"ruche/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	password := os.Getenv("RUCHE_ADMIN_PASSWORD")
	if len(password) < 8 {
		log.Fatal("RUCHE_ADMIN_PASSWORD doit faire au moins 8 caractères")
	}
	store, err := server.OpenStore(env("RUCHE_DB", "ruche.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var notifier server.Notifier = server.LogNotifier{}
	if tok, chat := os.Getenv("RUCHE_TELEGRAM_TOKEN"), os.Getenv("RUCHE_TELEGRAM_CHAT_ID"); tok != "" && chat != "" {
		notifier = server.TelegramNotifier{Token: tok, ChatID: chat}
	}
	maxTemp, _ := strconv.ParseFloat(env("RUCHE_MAX_TEMP", "80"), 64)
	alerter := &server.Alerter{Store: store, Notifier: notifier, MaxTempC: maxTemp}
	go alerter.Run(ctx, 30*time.Second)

	go func() {
		for {
			if err := store.PruneStats(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
				log.Printf("prune: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Hour):
			}
		}
	}()

	market := server.NewMarketSource()
	market.NetworkURL = env("RUCHE_XMR_NETWORK_URL", market.NetworkURL)
	market.PriceURL = env("RUCHE_XMR_PRICE_URL", market.PriceURL)

	srv := &http.Server{
		Addr:              env("RUCHE_LISTEN", ":8080"),
		Handler:           (&server.Server{Store: store, AdminPassword: password, Static: web.FS, Market: market}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("ruche-server écoute sur %s", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
