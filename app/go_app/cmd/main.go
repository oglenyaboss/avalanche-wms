package main

import (
	"log"
	"net/http"
	"os"
)

func CheckLedgerAdapter() {
	ledg_url := os.Getenv("Ledger_Adapter_Url")
	resp, err := http.Get(ledg_url + "/health")
	if err != nil {
		log.Printf("Failed to connect to ledger-adapter: %v", err)
	} else if resp.StatusCode == http.StatusOK {
		log.Println("Successfully connected to ledger-adapter")
	}
}

func main() {
	CheckLedgerAdapter()
}
