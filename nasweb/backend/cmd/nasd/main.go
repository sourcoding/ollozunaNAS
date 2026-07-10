// Command nasd è il daemon backend della NAS Web Management Interface.
// Espone una REST API + WebSocket, serve la SPA statica e orchestra i
// comandi di sistema (utenti, share, RAID, file manager, DLNA).
//
// È pensato per girare come servizio di sistema (systemd/OpenRC) con privilegi
// elevati limitati ai soli comandi necessari. Footprint a riposo target < 512MB
// sull'intera macchina, ma il binario in sé occupa pochi MB di RAM.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nasweb/nasd/internal/api"
	"github.com/nasweb/nasd/internal/auth"
	"github.com/nasweb/nasd/internal/config"
	"github.com/nasweb/nasd/internal/ws"
)

func main() {
	cfgPath := flag.String("config", "/etc/nasd/config.yaml", "percorso file di configurazione")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("caricamento configurazione fallito", "err", err)
		os.Exit(1)
	}

	// Apertura DB SQLite + migrazioni.
	db, err := config.OpenDB(cfg.Database.Path)
	if err != nil {
		slog.Error("apertura database fallita", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := config.Migrate(db, cfg.Database.MigrationsDir); err != nil {
		slog.Error("migrazioni fallite", "err", err)
		os.Exit(1)
	}

	// Hub WebSocket per eventi in tempo reale (progress, stato RAID...).
	hub := ws.NewHub()
	go hub.Run()

	sessions := auth.NewSessionStore(cfg.Security.SessionTTL)

	router := api.NewRouter(api.Deps{
		Config:   cfg,
		DB:       db,
		Hub:      hub,
		Sessions: sessions,
	})

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0, // disabilitato per supportare streaming/download lunghi
		IdleTimeout:       120 * time.Second,
	}

	// Avvio server (HTTPS se sono configurati i certificati, altrimenti HTTP).
	go func() {
		var serveErr error
		if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
			slog.Info("avvio server HTTPS", "addr", cfg.Server.Listen)
			serveErr = srv.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey)
		} else {
			slog.Warn("TLS non configurato: avvio in HTTP (sconsigliato in produzione)", "addr", cfg.Server.Listen)
			serveErr = srv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("server terminato con errore", "err", serveErr)
			os.Exit(1)
		}
	}()

	// Shutdown pulito su SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("arresto in corso...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown forzato", "err", err)
	}
	slog.Info("arrestato")
}
