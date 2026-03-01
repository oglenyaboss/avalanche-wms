package chain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"ledger-adapter/internal/config"
)

// Client wraps an Ethereum client with contract interaction helpers.
type Client struct {
	eth          *ethclient.Client
	privateKey   string
	contractAddr common.Address
	parsedABI    abi.ABI
}

// NewClient creates a new chain Client from the application config.
func NewClient(cfg *config.Config) (*Client, error) {
	eth, err := ethclient.Dial(cfg.RpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %v", err)
	}

	parsed, err := abi.JSON(strings.NewReader(ContractABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}

	return &Client{
		eth:          eth,
		privateKey:   cfg.PrivateKey,
		contractAddr: common.HexToAddress(cfg.ContractAddr),
		parsedABI:    parsed,
	}, nil
}

// Close closes the underlying ethclient connection.
func (c *Client) Close() {
	c.eth.Close()
}

// GetMessage calls the getMessage view function on the contract.
func (c *Client) GetMessage() (string, error) {
	data, err := c.parsedABI.Pack("getMessage")
	if err != nil {
		return "", fmt.Errorf("failed to pack data: %v", err)
	}

	addr := c.contractAddr
	res, err := c.eth.CallContract(context.Background(), ethereum.CallMsg{
		To:   &addr,
		Data: data,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("contract call failed: %v", err)
	}

	var message string
	err = c.parsedABI.UnpackIntoInterface(&message, "getMessage", res)
	if err != nil {
		return "", fmt.Errorf("failed to unpack with ABI: %v", err)
	}

	return message, nil
}

// SetMessage sends a setMessage transaction to the contract.
func (c *Client) SetMessage(newMessage string) (*types.Receipt, error) {
	data, err := c.parsedABI.Pack("setMessage", newMessage)
	if err != nil {
		return nil, err
	}
	return c.sendTx(data)
}

// AddMessage sends an addMessage transaction to the contract.
func (c *Client) AddMessage(addition string) (*types.Receipt, error) {
	data, err := c.parsedABI.Pack("addMessage", addition)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}
	return c.sendTx(data)
}

// ViewLogs queries contract event logs for a specific block number.
func (c *Client) ViewLogs(blockNum int) ([]types.Log, error) {
	event, ok := c.parsedABI.Events["setlogs"]
	if !ok {
		return nil, fmt.Errorf("failed to find event")
	}
	filter := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(blockNum)),
		ToBlock:   big.NewInt(int64(blockNum)),
		Addresses: []common.Address{c.contractAddr},
		Topics: [][]common.Hash{
			{event.ID},
		},
	}
	logs, err := c.eth.FilterLogs(context.Background(), filter)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

// ParseReceipt formats a transaction receipt as a human-readable string.
func (c *Client) ParseReceipt(rec *types.Receipt) string {
	return fmt.Sprintf(
		"TxHash:%s\nContractAddress:%s\nGasUsed:%d\nBlockHash:%s\nBlockNumber:%s\nTransactionIndex:%d\n",
		rec.TxHash,
		rec.ContractAddress.String(),
		rec.GasUsed,
		rec.BlockHash.String(),
		rec.BlockNumber.String(),
		rec.TransactionIndex)
}

// PrettifiedData extracts the meaningful portion from ABI-encoded log data.
func PrettifiedData(x []byte) []byte {
	l := x[63]
	return x[64 : 64+l]
}

// sendTx is a shared helper for signing and sending transactions.
func (c *Client) sendTx(data []byte) (*types.Receipt, error) {
	privKey, err := crypto.HexToECDSA(c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %v", err)
	}
	publicKey := privKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	nonce, err := c.eth.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %v", err)
	}

	gasPrice, err := c.eth.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %v", err)
	}

	addr := c.contractAddr
	gasLimit, err := c.eth.EstimateGas(context.Background(), ethereum.CallMsg{
		From: fromAddress,
		To:   &addr,
		Data: data,
	})
	if err != nil {
		gasLimit = 100000
	}

	tx := types.NewTransaction(
		nonce,
		c.contractAddr,
		big.NewInt(0),
		gasLimit,
		gasPrice,
		data,
	)

	chainID, err := c.eth.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %v", err)
	}

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}

	err = c.eth.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %v", err)
	}

	time.Sleep(1 * time.Second)

	info, err := c.eth.TransactionReceipt(context.Background(), signedTx.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %#+v", err)
	}
	return info, nil
}
