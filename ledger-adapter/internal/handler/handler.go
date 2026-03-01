package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"ledger-adapter/internal/chain"
)

// LogJSON is the JSON-friendly representation of a contract event log.
type LogJSON struct {
	Address     string `json:"address"`
	Data        string `json:"data"`
	BlockNumber uint64 `json:"blockNumber"`
	TxHash      string `json:"transactionHash"`
	TxIndex     uint   `json:"transactionIndex"`
	BlockHash   string `json:"blockHash"`
	Removed     bool   `json:"removed"`
}

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	chain *chain.Client
}

// New creates a new Handler with the given chain client.
func New(c *chain.Client) *Handler {
	return &Handler{chain: c}
}

// RegisterRoutes registers all HTTP routes on the default ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.Health)
	mux.HandleFunc("/", h.GetMessage)
	mux.HandleFunc("/setstring", h.SetMessage)
	mux.HandleFunc("/addfunc", h.AddMessage)
	mux.HandleFunc("/viewlogs", h.ViewLogs)
}

// Health responds with service health status.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "healty",
		"time":   time.Now(),
	})
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
}

// GetMessage returns the current message from the contract.
func (h *Handler) GetMessage(w http.ResponseWriter, r *http.Request) {
	res, err := h.chain.GetMessage()
	if err != nil {
		w.WriteHeader(500)
		_, Err := w.Write([]byte(err.Error()))
		if Err != nil {
			fmt.Print("error")
		}
		return
	}
	w.WriteHeader(200)
	_, err = w.Write([]byte(res))
	if err != nil {
		fmt.Print("error")
	}
}

// SetMessage handles POST requests to set a new message on the contract.
func (h *Handler) SetMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{error:"page not found"}`, http.StatusNotFound)
		return
	}
	x := map[string]string{
		"NewWord": "",
	}
	m, _ := io.ReadAll(r.Body)
	err := json.Unmarshal(m, &x)
	if err != nil {
		http.Error(w, `{error:"expected json"}`, http.StatusBadRequest)
		return
	}
	info, err := h.chain.SetMessage(x["NewWord"])
	if err != nil {
		http.Error(w, fmt.Sprintf(`{error:"unexpected behaviour of subnet:%v"}`, err), http.StatusBadGateway)
		return
	}
	res := h.chain.ParseReceipt(info)
	w.WriteHeader(200)
	_, err = w.Write([]byte(res))
	if err != nil {
		fmt.Print(err.Error())
	}
}

// AddMessage handles POST requests to append to the contract message.
func (h *Handler) AddMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "not found", http.StatusNotFound)
	}
	x := map[string]string{
		"addition": "",
	}
	m, _ := io.ReadAll(r.Body)
	err := json.Unmarshal(m, &x)
	if err != nil {
		http.Error(w, `{error:"expected json"}`, http.StatusBadRequest)
		return
	}
	info, err := h.chain.AddMessage(x["addition"])
	if err != nil {
		http.Error(w, fmt.Sprintf(`{error:"unexpected behaviour of subnet:%v"}`, err), http.StatusBadGateway)
		return
	}
	res := h.chain.ParseReceipt(info)
	w.WriteHeader(200)
	_, err = w.Write([]byte(res))
	if err != nil {
		fmt.Print(err.Error())
	}
}

// ViewLogs returns contract event logs for the specified block.
func (h *Handler) ViewLogs(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	id, err := strconv.Atoi(params.Get("id"))
	if err != nil {
		http.Error(w, "error: invalid id", http.StatusBadRequest)
		return
	}
	logs, err := h.chain.ViewLogs(id)
	prettified := []LogJSON{}
	for _, v := range logs {
		pd := chain.PrettifiedData(v.Data)
		entry := LogJSON{
			Address:     v.Address.String(),
			Data:        string(pd),
			BlockNumber: v.BlockNumber,
			TxHash:      v.TxHash.String(),
			TxIndex:     v.TxIndex,
			BlockHash:   v.BlockHash.String(),
			Removed:     v.Removed,
		}
		prettified = append(prettified, entry)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("%v", err), 500)
	}
	res, err := json.Marshal(prettified)
	if err != nil {
		fmt.Print("shit happens")
	}
	w.WriteHeader(200)
	_, err = w.Write(res)
	if err != nil {
		fmt.Print(err.Error())
	}
}
