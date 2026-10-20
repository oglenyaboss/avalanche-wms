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

	"github.com/ethereum/go-ethereum/common"
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
// dlq producer → Flusher → единственный consumer goroutine + HTTP server.
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
		"registry_addr", cfg.RegistryAddr,
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

	// Signer ровно один (multi-signer запрещён: подписанные действия держат
	// Kafka-порядок одной nonce-цепочкой). Один клиент обслуживает и отправку,
	// и receipt-чтения (Confirm/reconcile/health) — receipt'ы node-global.
	// Источник signer'а — режим: static (локальный hex-ключ) или kms
	// (Web3Signer: адаптеру нужен только public key ключа).
	signer, err := buildSigner(cfg)
	if err != nil {
		return err
	}
	client, err := chain.NewClientWithSigner(cfg.RpcURL, signer)
	if err != nil {
		return fmt.Errorf("chain client: %w", err)
	}
	defer client.Close()
	log.Info("chain client ready", "signer_mode", cfg.SignerMode, "signer", client.FromAddress().Hex())

	// Fail-fast: signer должен быть профинансирован, иначе его tx'ы под
	// нагрузкой реверят (out-of-funds) и портят замер/целостность.
	if err := assertSignersFunded(rootCtx, cfg.RpcURL, []*chain.Client{client}, log); err != nil {
		return err
	}

	// Один ethclient для read-only резолверов подписанных действий: складские
	// (SignedMappingWMS) всегда, реестр (ConsortiumRegistry) — при заданном
	// REGISTRY_ADDR. Отдельно от signer-клиента: резолверам нужен только
	// contractCaller (eth_call + чтение логов).
	ec, err := ethclient.Dial(cfg.RpcURL)
	if err != nil {
		return fmt.Errorf("resolver dial: %w", err)
	}
	defer ec.Close()

	// Складские действия уходят через applyActions на CONTRACT_ADDR — резолвер
	// строится всегда, исходы разбираются из его receipt'ов.
	warehouse := chain.NewWarehouseActions(ec, common.HexToAddress(cfg.ContractAddr))

	// Интерфейсная переменная: при незаданном REGISTRY_ADDR реестр остаётся nil-интерфейсом
	// (typed-nil указатель внутри интерфейса не равен nil и обошёл бы проверку в handler).
	// Строится ДО flusher/reconciler: ключевые действия уходят через
	// applyKeyActions на этот адрес, исходы разбираются из его receipt'ов.
	var registry handler.RegistryReader
	var registryResolver consumer.ActionResolver
	if cfg.RegistryAddr != "" {
		reg, err := chain.NewRegistry(ec, common.HexToAddress(cfg.RegistryAddr))
		if err != nil {
			return fmt.Errorf("registry: %w", err)
		}
		registry = reg
		registryResolver = reg
	}

	brokers := strings.Split(cfg.KafkaBrokers, ",")
	prod := dlq.NewProducer(brokers, cfg.DLQTopic)
	defer func() { _ = prod.Close() }()

	flusher := consumer.NewFlusher(client, repo, prod, cfg.ReceiptPollTimeout, log).
		WithWarehouseActions(warehouse).
		WithRegistry(registryResolver)
	reconciler := consumer.NewReconciler(client, repo, cfg.ReconcileInterval, cfg.ReconcileMinAge, log).
		WithWarehouseActions(warehouse).
		WithRegistry(registryResolver)

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

	srv := startHTTPServer(log, cfg.Port, registry)

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

// buildSigner собирает TxSigner по режиму: static — локальный ecdsa-ключ
// из PRIVATE_KEY; kms — удалённая подпись в Web3Signer по
// KMS_PUBLIC_KEYS (адрес выводится из public key, приватник адаптеру
// не нужен вообще). Signer ровно один — это валидирует config.Load.
func buildSigner(cfg *config.Config) (chain.TxSigner, error) {
	if cfg.SignerMode != "kms" {
		return chain.NewStaticSigner(cfg.PrivateKey)
	}
	hc := &http.Client{Timeout: 10 * time.Second}
	return kms.NewSigner(cfg.KMSURL, cfg.KMSPublicKeys[0], hc)
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

func startHTTPServer(log *slog.Logger, port string, registry handler.RegistryReader) *http.Server {
	mux := http.NewServeMux()
	handler.New(registry).RegisterRoutes(mux)

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
