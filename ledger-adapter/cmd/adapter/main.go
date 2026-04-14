package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/config"
	"ledger-adapter/internal/consumer"
	"ledger-adapter/internal/dlq"
	"ledger-adapter/internal/handler"
	"ledger-adapter/internal/store"
)

// topics — WMS-topic'и которые слушает адаптер. Маппинг в solidity-методы
// BatchMappingWMS — в chain.topicToMethod.
var topics = []string{
	"wms.receiving.v1",
	"wms.putaway.v1",
	"wms.picking.v1",
	"wms.shipping.v1",
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("ledger-adapter exited", "err", err)
		os.Exit(1)
	}
}

// run собирает и запускает pipeline: config → store → chain client →
// dlq producer → Flusher → N consumer goroutines + health HTTP server.
// Блокирует до SIGTERM/SIGINT, потом graceful shutdown.
func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("config loaded",
		"port", cfg.Port,
		"kafka_brokers", cfg.KafkaBrokers,
		"contract_addr", cfg.ContractAddr,
		"batch_size", cfg.BatchSize,
		"batch_timeout", cfg.BatchTimeout)

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	pool, err := store.NewPool(rootCtx, cfg.DbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := store.NewRepository(pool)

	cli, err := chain.NewClient(cfg.RpcURL, cfg.PrivateKey, cfg.ContractAddr)
	if err != nil {
		return err
	}
	defer cli.Close()
	log.Info("chain client ready", "signer", cli.FromAddress().Hex())

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	prod := dlq.NewProducer(brokers, cfg.DLQTopic)
	defer func() { _ = prod.Close() }()

	flusher := consumer.NewFlusher(cli, repo, prod, cfg.ReceiptPollTimeout, log)

	var wg sync.WaitGroup
	for _, topic := range topics {
		t := topic
		wg.Add(1)
		go func() {
			defer wg.Done()
			tlog := log.With("topic", t)
			c := consumer.NewConsumer(brokers, t, cfg.KafkaGroupID, flusher, prod, cfg.BatchSize, cfg.BatchTimeout, tlog)
			if err := c.Run(rootCtx); err != nil && rootCtx.Err() == nil {
				tlog.Error("consumer stopped unexpectedly", "err", err)
			}
		}()
	}

	srv := startHealthServer(log, cfg.Port)

	<-rootCtx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}

	wg.Wait()
	log.Info("all consumers drained, bye")
	return nil
}

func startHealthServer(log *slog.Logger, port string) *http.Server {
	mux := http.NewServeMux()
	handler.New().RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("http listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
		}
	}()
	return srv
}
