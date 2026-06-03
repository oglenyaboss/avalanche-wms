package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/config"
	"ledger-adapter/internal/consumer"
	"ledger-adapter/internal/dlq"
	"ledger-adapter/internal/handler"
	"ledger-adapter/internal/store"
)

const eventsTopic = "wms.events.v1"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("ledger-adapter exited", "err", err)
		os.Exit(1)
	}
}

// run собирает и запускает pipeline: config → store → chain client →
// dlq producer → Flusher → единственный consumer goroutine + health HTTP server.
func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("config loaded",
		"port", cfg.Port,
		"kafka_brokers", cfg.KafkaBrokers,
		"topic", eventsTopic,
		"contract_addr", cfg.ContractAddr,
		"batch_size", cfg.BatchSize,
		"batch_timeout", cfg.BatchTimeout,
		"reconcile_interval", cfg.ReconcileInterval,
		"reconcile_min_age", cfg.ReconcileMinAge)

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
	reconciler := consumer.NewReconciler(cli, repo, cfg.ReconcileInterval, cfg.ReconcileMinAge, log)

	g, gCtx := errgroup.WithContext(rootCtx)
	g.Go(func() error {
		tlog := log.With("topic", eventsTopic)
		c := consumer.NewConsumer(brokers, eventsTopic, cfg.KafkaGroupID, flusher, prod, cfg.BatchSize, cfg.BatchTimeout, tlog)
		if err := c.Run(gCtx); err != nil && gCtx.Err() == nil {
			tlog.Error("consumer stopped unexpectedly", "err", err)
			return err
		}
		return nil
	})
	// Reconcile loop (N1): фоновая сверка stuck SENT/FAILED строк с on-chain receipt'ом.
	// Read-mostly — НИКОГДА не переотправляет tx; только подтягивает DB к chain-истине.
	g.Go(func() error {
		return reconciler.Run(gCtx)
	})

	srv := startHealthServer(log, cfg.Port, cli)

	runErr := g.Wait()
	log.Info("consumer stopped, shutting down http")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}

	if runErr != nil {
		return runErr
	}
	log.Info("consumer drained, bye")
	return nil
}

func startHealthServer(log *slog.Logger, port string, chainReader handler.ChainReader) *http.Server {
	mux := http.NewServeMux()
	handler.New(chainReader).RegisterRoutes(mux)

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
