package main

import (
	"context"
	"crypto/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shatrunoff/yap_gofermart/internal/accrual"
	"github.com/shatrunoff/yap_gofermart/internal/auth"
	"github.com/shatrunoff/yap_gofermart/internal/config"
	"github.com/shatrunoff/yap_gofermart/internal/server"
	"github.com/shatrunoff/yap_gofermart/internal/storage"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()

	cfg := config.Load()
	logger.Info("gophermart starting",
		zap.String("run_address", cfg.RunAddress),
		zap.Bool("db_uri_set", cfg.DatabaseURI != ""),
		zap.String("accrual_address", cfg.AccrualAddress),
	)

	if cfg.DatabaseURI == "" {
		logger.Fatal("DATABASE_URI is required")
	}

	// Контекст завершения по сигналам
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	db, err := storage.New(ctx, cfg.DatabaseURI)
	if err != nil {
		logger.Fatal("db init failed", zap.Error(err))
	}

	// секрет для токенов
	secret := []byte(cfg.SecretKey)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			logger.Fatal("secret generation failed", zap.Error(err))
		}
	}
	signer := auth.NewSigner(secret, 24*time.Hour)

	// HTTP сервер
	srv := server.New(logger, db, signer)

	// Поллер начислений
	if cfg.AccrualAddress != "" {
		client, cerr := accrual.NewClient(cfg.AccrualAddress, nil)
		if cerr != nil {
			logger.Fatal("accrual client init failed", zap.Error(cerr))
		}
		repo := accrual.NewPGRepo(db)
		poller := accrual.NewPoller(client, repo, logger, accrual.Options{})
		go func() { _ = poller.Start(ctx) }()
	}

	if err := srv.Serve(ctx, cfg.RunAddress); err != nil {
		logger.Fatal("server stopped", zap.Error(err))
	}
}
