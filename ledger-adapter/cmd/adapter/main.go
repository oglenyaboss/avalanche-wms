package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"golang.org/x/sync/errgroup"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/config"
	"ledger-adapter/internal/consumer"
	"ledger-adapter/internal/dlq"
	"ledger-adapter/internal/handler"
	"ledger-adapter/internal/kms"
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
		"pipeline_window", cfg.PipelineWindow,
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

	// Multi-signer: один Client на signer. Flusher шардит in-flight tx по
	// ProductID между ними (N× in-flight ёмкость). clients[0] обслуживает receipt-
	// чтения (Confirm/reconcile/health) — receipt'ы node-global.
	// Источник signer'ов — режим: static (локальные hex-ключи) или kms
	// (Web3Signer: адаптеру нужен только public key каждого ключа).
	signers, err := buildSigners(cfg)
	if err != nil {
		return err
	}
	callers := make([]consumer.ChainCaller, 0, len(signers))
	clients := make([]*chain.Client, 0, len(signers))
	// Закрываем все клиенты одним deferred-closure (ranges по слайсу на момент
	// выполнения → закроет и те, что успели создаться при раннем return).
	defer func() {
		for _, cl := range clients {
			cl.Close()
		}
	}()
	for i, s := range signers {
		cl, cerr := chain.NewClientWithSigner(cfg.RpcURL, cfg.ContractAddr, s)
		if cerr != nil {
			return fmt.Errorf("signer %d: %w", i, cerr)
		}
		clients = append(clients, cl)
		callers = append(callers, cl)
		log.Info("chain client ready", "signer_mode", cfg.SignerMode, "signer_index", i, "signer", cl.FromAddress().Hex())
	}
	// Fail-fast: каждый signer должен быть профинансирован, иначе его tx'ы под
	// нагрузкой реверят (out-of-funds) и портят замер/целостность.
	if err := assertSignersFunded(rootCtx, cfg.RpcURL, clients, log); err != nil {
		return err
	}

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	prod := dlq.NewProducer(brokers, cfg.DLQTopic)
	defer func() { _ = prod.Close() }()

	flusher := consumer.NewFlusher(callers, repo, prod, cfg.ReceiptPollTimeout, log)
	reconciler := consumer.NewReconciler(clients[0], repo, cfg.ReconcileInterval, cfg.ReconcileMinAge, log)

	g, gCtx := errgroup.WithContext(rootCtx)
	g.Go(func() error {
		tlog := log.With("topic", eventsTopic)
		c := consumer.NewConsumer(brokers, eventsTopic, cfg.KafkaGroupID, flusher, prod, cfg.BatchSize, cfg.BatchTimeout, cfg.PipelineWindow, tlog)
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

	srv := startHealthServer(log, cfg.Port, clients[0])

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

// buildSigners собирает TxSigner'ы по режиму: static — локальные ecdsa-ключи
// из PRIVATE_KEY/PRIVATE_KEYS; kms — удалённая подпись в Web3Signer по
// KMS_PUBLIC_KEYS (адрес каждого выводится из public key, приватник адаптеру
// не нужен вообще).
func buildSigners(cfg *config.Config) ([]chain.TxSigner, error) {
	if cfg.SignerMode != "kms" {
		signers := make([]chain.TxSigner, 0, len(cfg.PrivateKeys))
		for i, key := range cfg.PrivateKeys {
			s, err := chain.NewStaticSigner(key)
			if err != nil {
				return nil, fmt.Errorf("static signer %d: %w", i, err)
			}
			signers = append(signers, s)
		}
		return signers, nil
	}

	hc := &http.Client{Timeout: 10 * time.Second}
	signers := make([]chain.TxSigner, 0, len(cfg.KMSPublicKeys))
	for i, id := range cfg.KMSPublicKeys {
		s, err := kms.NewSigner(cfg.KMSURL, id, hc)
		if err != nil {
			return nil, fmt.Errorf("kms signer %d: %w", i, err)
		}
		signers = append(signers, s)
	}
	return signers, nil
}

// assertSignersFunded fail-fast'ит, если любой signer не профинансирован: под
// нагрузкой out-of-funds tx реверят и портят и замер, и WMS↔chain целостность.
// Throwaway-dial — отдельно от signer-клиентов, только для проверки на старте.
func assertSignersFunded(ctx context.Context, rpcURL string, clients []*chain.Client, log *slog.Logger) error {
	ec, err := ethclient.Dial(rpcURL)
	if err != nil {
		return fmt.Errorf("balance-check dial: %w", err)
	}
	defer ec.Close()
	for i, cl := range clients {
		bal, err := ec.BalanceAt(ctx, cl.FromAddress(), nil)
		if err != nil {
			return fmt.Errorf("signer %d (%s) balance: %w", i, cl.FromAddress().Hex(), err)
		}
		if bal.Sign() == 0 {
			return fmt.Errorf("signer %d (%s) is UNFUNDED — fund it before start", i, cl.FromAddress().Hex())
		}
		log.Info("signer funded", "signer_index", i, "addr", cl.FromAddress().Hex(), "balance_wei", bal.String())
	}
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
