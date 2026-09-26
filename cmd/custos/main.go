// Command custos serves the custos API and web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/config"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.RootDir, cfg.WorkDir, log)
	if err != nil {
		return err
	}
	keys, err := carabiner.LoadKeys(cfg.KeysDir)
	if err != nil {
		return err
	}
	log.Info("loaded trusted keys", "dir", cfg.KeysDir, "count", len(keys))
	verifier := carabiner.New(carabiner.WithKeys(keys...), carabiner.WithOnline(cfg.SigstoreOnline))
	go carabiner.Warmup()

	srv := &api.Server{
		Store:    st,
		Verifier: verifier,
		Keys: func() []attest.Key {
			var out []attest.Key
			for _, k := range verifier.Keys() {
				out = append(out, k.Key)
			}
			return out
		},
		ReloadKeys: func() error {
			keys, err := carabiner.LoadKeys(cfg.KeysDir)
			if err != nil {
				return err
			}
			verifier.SetKeys(keys)
			log.Info("reloaded trusted keys", "count", len(keys))
			return nil
		},
		UI:  web.UI(),
		Log: log,
	}
	hs := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
	}

	if cfg.Container && !cfg.NoBanner {
		writeBanner(os.Stdout, cfg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "rootDir", cfg.RootDir, "workDir", cfg.WorkDir)
		errc <- hs.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
