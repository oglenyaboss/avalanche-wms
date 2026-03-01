package main

import (
	"fmt"
	"log"
	"net/http"

	"ledger-adapter/internal/chain"
	"ledger-adapter/internal/config"
	"ledger-adapter/internal/handler"
)

func main() {
	cfg := config.Load()

	client, err := chain.NewClient(cfg)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	h := handler.New(client)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	log.Print("server staring...")
	err = http.ListenAndServe(fmt.Sprintf(":%s", cfg.Port), mux)
	if err != nil {
		fmt.Print(err.Error())
		log.Fatalf("server failed: %v", err)
	}
}
