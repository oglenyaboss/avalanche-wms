package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"

	"wms/internal/analytics"
	"wms/internal/assembly"
	"wms/internal/auth"
	"wms/internal/config"
	"wms/internal/destinations"
	"wms/internal/dispatches"
	"wms/internal/ledger"
	"wms/internal/onchain"
	"wms/internal/platform/kafka"
	"wms/internal/platform/postgres"
	"wms/internal/products"
	"wms/internal/putaway"
	"wms/internal/receiving"
	"wms/internal/shipping"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("WMS config invalid: %v", err)
	}

	// PostgreSQL
	dbPool, err := postgres.NewPool(ctx, cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)
	if err != nil {
		log.Fatalf("PostgreSQL connection failed: %v", err)
	}
	defer dbPool.Close()
	log.Println("Connected to PostgreSQL")

	// Kafka
	kafkaConn, err := kafka.NewConn(cfg.KafkaBroker)
	if err != nil {
		log.Printf("Kafka connection failed: %v", err)
		return
	}
	defer kafkaConn.Close()
	log.Println("Connected to Kafka")

	// Ledger Adapter
	var ledgerClient *ledger.Client
	if cfg.LedgerAdapterURL != "" {
		ledgerClient = ledger.NewClient(cfg.LedgerAdapterURL)
		if err := ledgerClient.HealthCheck(); err != nil {
			log.Printf("Ledger adapter unreachable: %v", err)
		} else {
			log.Println("Ledger adapter is healthy")
		}
	}

	// Module wiring: repositories → services → handlers
	receivingRepo := receiving.NewRepository(dbPool)
	receivingSvc := receiving.NewService(receivingRepo)
	receivingHandler := receiving.NewHandler(receivingSvc)

	assemblyRepo := assembly.NewRepository(dbPool)
	assemblySvc := assembly.NewService(assemblyRepo)
	assemblyHandler := assembly.NewHandler(assemblySvc)

	putawayRepo := putaway.NewRepository(dbPool)
	putawaySvc := putaway.NewService(putawayRepo)
	putawayHandler := putaway.NewHandler(putawaySvc)

	shippingRepo := shipping.NewRepository(dbPool)
	shippingSvc := shipping.NewService(shippingRepo)
	shippingHandler := shipping.NewHandler(shippingSvc)

	authRepo := auth.NewRepository(dbPool)
	authSvc := auth.NewService(authRepo, cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	authHandler := auth.NewHandler(authSvc)

	dispatchesRepo := dispatches.NewRepository(dbPool)
	dispatchesSvc := dispatches.NewService(dispatchesRepo)
	dispatchesHandler := dispatches.NewHandler(dispatchesSvc)

	destinationsRepo := destinations.NewRepository(dbPool)
	destinationsSvc := destinations.NewService(destinationsRepo)
	destinationsHandler := destinations.NewHandler(destinationsSvc)

	analyticsRepo := analytics.NewRepository(dbPool)
	analyticsSvc := analytics.NewService(analyticsRepo)
	analyticsHandler := analytics.NewHandler(analyticsSvc)

	productsRepo := products.NewRepository(dbPool)
	productsSvc := products.NewService(productsRepo)
	productsHandler := products.NewHandler(productsSvc)

	// ledgerClient is a concrete *ledger.Client and is nil when LEDGER_ADAPTER_URL is
	// unset. Assign THROUGH the interface var so a typed-nil pointer doesn't defeat
	// the handler's `h.ledger == nil` check (a nil *ledger.Client stored directly in
	// the interface is NOT == nil and would panic on call).
	var receiptGetter onchain.ReceiptGetter
	if ledgerClient != nil {
		receiptGetter = ledgerClient
	}
	onchainHandler := onchain.NewHandler(receiptGetter)

	// Router
	r := mux.NewRouter()
	r.HandleFunc("/health", healthHandler(dbPool, kafkaConn, ledgerClient)).Methods("GET")
	authHandler.RegisterRoutes(r)

	receivingRouter := r.PathPrefix("/receiving").Subrouter()
	receivingRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	receivingHandler.RegisterRoutes(receivingRouter)

	putawayRouter := r.PathPrefix("/putaway").Subrouter()
	putawayRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	putawayHandler.RegisterRoutes(putawayRouter)

	assemblyRouter := r.PathPrefix("/assembly").Subrouter()
	assemblyRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	assemblyHandler.RegisterRoutes(assemblyRouter)

	dispatchesRouter := r.PathPrefix("/dispatches").Subrouter()
	dispatchesRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	dispatchesHandler.RegisterRoutes(dispatchesRouter)

	destinationsRouter := r.PathPrefix("/destinations").Subrouter()
	destinationsRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	destinationsHandler.RegisterRoutes(destinationsRouter)

	analyticsRouter := r.PathPrefix("/analytics").Subrouter()
	analyticsRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	analyticsHandler.RegisterRoutes(analyticsRouter)

	productsRouter := r.PathPrefix("/products").Subrouter()
	productsRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	productsHandler.RegisterRoutes(productsRouter)

	onchainRouter := r.PathPrefix("/onchain").Subrouter()
	onchainRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	onchainHandler.RegisterRoutes(onchainRouter)

	shippingRouter := r.PathPrefix("/shipping").Subrouter()
	shippingRouter.Use(auth.Middleware([]byte(cfg.JWTSecret)))
	shippingHandler.RegisterRoutes(shippingRouter)

	log.Printf("WMS service starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, r); err != nil {
		log.Printf("Server failed: %v", err)
	}
}

func healthHandler(dbPool *pgxpool.Pool, kafkaConn *kafkago.Conn, ledgerClient *ledger.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		checks := map[string]string{}

		if err := dbPool.Ping(r.Context()); err != nil {
			status = "degraded"
			checks["postgres"] = err.Error()
		} else {
			checks["postgres"] = "ok"
		}

		if kafkaConn != nil {
			if _, err := kafkaConn.Brokers(); err != nil {
				status = "degraded"
				checks["kafka"] = err.Error()
			} else {
				checks["kafka"] = "ok"
			}
		}

		if ledgerClient != nil {
			if err := ledgerClient.HealthCheck(); err != nil {
				status = "degraded"
				checks["ledger_adapter"] = err.Error()
			} else {
				checks["ledger_adapter"] = "ok"
			}
		}

		result := map[string]interface{}{
			"status": status,
			"checks": checks,
			"time":   time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		if status != "ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Printf("Failed to encode health response: %v", err)
		}
	}
}
