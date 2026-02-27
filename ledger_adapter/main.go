package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

var (
	envfile, _ = godotenv.Read(".env")
)

var (
	rpcUrl     = envfile["rpcUrl"]
	privateKey = envfile["privateKey"]
)

type LogJSON struct {
	Address     string `json:"address"`
	Data        string `json:"data"`
	BlockNumber uint64 `json:"blockNumber"`
	TxHash      string `json:"transactionHash"`
	TxIndex     uint   `json:"transactionIndex"`
	BlockHash   string `json:"blockHash"`
	Removed     bool   `json:"removed"`
}

var addr common.Address = common.HexToAddress(envfile["addr"])

const contractABI = `[
  {
    "anonymous": false,
    "inputs": [
      {
        "indexed": false,
        "internalType": "string",
        "name": "mes",
        "type": "string"
      }
    ],
    "name": "setlogs",
    "type": "event"
  },
  {
    "inputs": [
      {
        "internalType": "string",
        "name": "_addition",
        "type": "string"
      }
    ],
    "name": "addMessage",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "getMessage",
    "outputs": [
      {
        "internalType": "string",
        "name": "",
        "type": "string"
      }
    ],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "message",
    "outputs": [
      {
        "internalType": "string",
        "name": "",
        "type": "string"
      }
    ],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [
      {
        "internalType": "string",
        "name": "_newMessage",
        "type": "string"
      }
    ],
    "name": "setMessage",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  }
]`

func logsViewer(client *ethclient.Client, id int) ([]types.Log, error) {
	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}
	event, ok := parsedABI.Events["setlogs"]
	if !ok {
		return nil, fmt.Errorf("failed to find event")
	}
	filter := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(id)),
		ToBlock:   big.NewInt(int64(id)),
		Addresses: []common.Address{addr},
		Topics: [][]common.Hash{
			{event.ID},
		},
	}
	logs, err := client.FilterLogs(context.Background(), filter)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func addMessage(client *ethclient.Client, addition string) (*types.Receipt, error) {
	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}
	data, err := parsedABI.Pack("addMessage", addition)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}
	privateKey, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %v", err)
	}
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %v", err)
	}
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: fromAddress,
		To:   &addr,
		Data: data,
	})
	if err != nil {
		gasLimit = 100000
	}
	tx := types.NewTransaction(
		nonce,
		addr,
		big.NewInt(0),
		gasLimit,
		gasPrice,
		data,
	)
	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %v", err)
	}
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %v", err)
	}
	time.Sleep(1 * time.Second)
	info, err := client.TransactionReceipt(context.Background(), signedTx.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %#+v", err)
	}
	return info, nil
}

func setMessage(client *ethclient.Client, x string) (*types.Receipt, error) {
	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		return nil, err
	}
	data, err := parsedABI.Pack("setMessage", x)
	if err != nil {
		return nil, err
	}
	privateKey, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %v", err)
	}
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %v", err)
	}
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: fromAddress,
		To:   &addr,
		Data: data,
	})
	if err != nil {
		gasLimit = 100000
	}
	tx := types.NewTransaction(
		nonce,
		addr,
		big.NewInt(0),
		gasLimit,
		gasPrice,
		data,
	)

	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %v", err)
	}
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %v", err)
	}
	time.Sleep(1 * time.Second)
	info, err := client.TransactionReceipt(context.Background(), signedTx.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %#+v", err)
	}
	return info, nil
}

func getHelloWorld(client *ethclient.Client) (string, error) {
	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		return "", fmt.Errorf("failed to parse ABI: %v", err)
	}

	data, err := parsedABI.Pack("getMessage")
	if err != nil {
		return "", fmt.Errorf("failed to pack data: %v", err)
	}

	res, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &addr,
		Data: data,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("contract call failed: %v", err)
	}

	var message string
	err = parsedABI.UnpackIntoInterface(&message, "getMessage", res)
	if err != nil {
		return "", fmt.Errorf("failed to unpack with ABI: %v", err)
	}

	return message, nil
}

func chech_service_health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "healty",
		"time":   time.Now(),
	})
}

func main() {
	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()
	if err != nil {
		fmt.Print(err.Error())
	}

	log.Print("server staring...")
	http.HandleFunc("/health", chech_service_health)

	x := func(w http.ResponseWriter, r *http.Request) {
		res, err := getHelloWorld(client)
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

	y := func(w http.ResponseWriter, r *http.Request) {
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
		info, err := setMessage(client, x["NewWord"])
		if err != nil {
			http.Error(w, fmt.Sprintf(`{error:"unexpected behaviour of subnet:%v"}`, err), http.StatusBadGateway)
			return
		}
		res := parseReceipt(info)
		w.WriteHeader(200)
		_, err = w.Write([]byte(res))
		if err != nil {
			fmt.Print(err.Error())
		}
	}
	z := func(w http.ResponseWriter, r *http.Request) {
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
		info, err := addMessage(client, x["addition"])
		if err != nil {
			http.Error(w, fmt.Sprintf(`{error:"unexpected behaviour of subnet:%v"}`, err), http.StatusBadGateway)
			return
		}
		res := parseReceipt(info)
		w.WriteHeader(200)
		_, err = w.Write([]byte(res))
		if err != nil {
			fmt.Print(err.Error())
		}
	}
	w := func(w http.ResponseWriter, r *http.Request) {
		x := r.URL.Query()
		id, err := strconv.Atoi(x.Get("id"))
		if err != nil {
			http.Error(w, "error: invalid id", http.StatusBadRequest)
			return
		}
		logs, err := logsViewer(client, id)
		prettified := []LogJSON{}
		for _, v := range logs {
			pd := prettifiedData(v.Data)
			x := LogJSON{
				Address:     v.Address.String(),
				Data:        string(pd),
				BlockNumber: v.BlockNumber,
				TxHash:      v.TxHash.String(),
				TxIndex:     v.TxIndex,
				BlockHash:   v.BlockHash.String(),
				Removed:     v.Removed,
			}
			prettified = append(prettified, x)
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
	http.HandleFunc("/", x)
	http.HandleFunc("/viewlogs", w)
	http.HandleFunc("/setstring", y)
	http.HandleFunc("/addfunc", z)
	err = http.ListenAndServe(":8085", nil)
	if err != nil {
		fmt.Print(err.Error())
		log.Fatalf("server failed: %w", err)
	}
}

func parseReceipt(rec *types.Receipt) string {
	return fmt.Sprintf(
		"TxHash:%s\nContractAddress:%s\nGasUsed:%d\nBlockHash:%s\nBlockNumber:%s\nTransactionIndex:%d\n",
		rec.TxHash,
		rec.ContractAddress.String(),
		rec.GasUsed,
		rec.BlockHash.String(),
		rec.BlockNumber.String(),
		rec.TransactionIndex)
}

func prettifiedData(x []byte) []byte {
	l := x[63]
	return x[64 : 64+l]
}

// made by me
