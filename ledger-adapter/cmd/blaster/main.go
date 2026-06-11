// blaster — adapter-bypass saturation load generator for the geo chain.
//
// Purpose: remove the submission bottleneck that capped the earlier `cast send --async`
// blast (~100 tx/s, one subprocess per tx) so we can find the REAL chain ceiling and the
// binding resource (block gas%, validator 1-core CPU, or build_block cadence). It pre-signs
// deep per-signer nonce queues of cheap 21k-gas transfers and submits them via BATCHED
// eth_sendRawTransaction (many txs per HTTP round-trip → WAN RTT amortized to ~0), keeping a
// sliding in-flight window full per signer so the mempool never starves.
//
// Signers are the SAME deterministic accounts as deploy/geo/fund-signers.sh
// (key = keccak("geo-ms-signer-v1:<i>")) — fund them first with N=<signers> fund-signers.sh.
//
// Usage:
//
//	go run ./cmd/blaster -rpcs URL1,URL2 -signers 64 -dur 60s -gasprice 500 -window 256
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
)

var deadAddr = common.HexToAddress("0x000000000000000000000000000000000000dEaD")

type counters struct {
	accepted    atomic.Int64 // ok or already-known/nonce-too-low (already in chain/pool)
	queuedFull  atomic.Int64 // pool rejected (nonce too high / account queue full) — expected at saturation
	underpriced atomic.Int64
	otherErr    atomic.Int64
}

func main() {
	rpcsCSV := flag.String("rpcs", "", "comma-separated RPC endpoints (round-robined)")
	nSigners := flag.Int("signers", 64, "number of deterministic signers (keccak geo-ms-signer-v1:i)")
	seedFmt := flag.String("seed", "geo-ms-signer-v1:%d", "signer seed format string")
	dur := flag.Duration("dur", 60*time.Second, "blast duration")
	gpGwei := flag.Int64("gasprice", 500, "gas price (gwei)")
	batch := flag.Int("batch", 100, "txs per JSON-RPC batch call")
	window := flag.Int("window", 256, "in-flight (unmined) txs kept queued per signer")
	depth := flag.Int("depth", 3000, "pre-signed txs per signer")
	tick := flag.Duration("tick", 600*time.Millisecond, "per-signer resubmit cadence")
	pollEvery := flag.Duration("poll", 1500*time.Millisecond, "per-signer on-chain nonce refresh interval (NOT per tick — WAN polls starve the blaster)")
	flag.Parse()

	if *rpcsCSV == "" {
		log.Fatal("-rpcs required")
	}
	urls := strings.Split(*rpcsCSV, ",")
	clients := make([]*rpc.Client, len(urls))
	for i, u := range urls {
		c, err := rpc.Dial(strings.TrimSpace(u))
		if err != nil {
			log.Fatalf("dial %s: %v", u, err)
		}
		clients[i] = c
	}
	ctx := context.Background()

	// chainID from the first endpoint.
	var cidHex string
	if err := clients[0].CallContext(ctx, &cidHex, "eth_chainId"); err != nil {
		log.Fatalf("eth_chainId: %v", err)
	}
	chainID := hexToBig(cidHex)
	signer := types.NewEIP155Signer(chainID)
	gasPrice := new(big.Int).Mul(big.NewInt(*gpGwei), big.NewInt(1_000_000_000))
	log.Printf("chainId=%s endpoints=%d signers=%d gasPrice=%d gwei window=%d depth=%d dur=%s",
		chainID, len(clients), *nSigners, *gpGwei, *window, *depth, *dur)

	// Build + pre-sign every signer's nonce queue in parallel.
	type sgn struct {
		addr  common.Address
		start uint64   // first nonce (pending at init)
		raws  []string // pre-signed raw txs, raws[k] has nonce start+k
	}
	signers := make([]*sgn, *nSigners)
	var wg sync.WaitGroup
	for i := 0; i < *nSigners; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key, err := crypto.ToECDSA(crypto.Keccak256([]byte(fmt.Sprintf(*seedFmt, i))))
			if err != nil {
				log.Fatalf("signer %d key: %v", i, err)
			}
			addr := crypto.PubkeyToAddress(key.PublicKey)
			var nHex string
			if err := clients[i%len(clients)].CallContext(ctx, &nHex, "eth_getTransactionCount", addr, "pending"); err != nil {
				log.Fatalf("signer %d nonce: %v", i, err)
			}
			start := hexToBig(nHex).Uint64()
			raws := make([]string, *depth)
			for k := 0; k < *depth; k++ {
				tx := types.NewTx(&types.LegacyTx{
					Nonce: start + uint64(k), To: &deadAddr, Value: big.NewInt(1),
					Gas: 21000, GasPrice: gasPrice,
				})
				st, err := types.SignTx(tx, signer, key)
				if err != nil {
					log.Fatalf("signer %d sign: %v", i, err)
				}
				b, _ := st.MarshalBinary()
				raws[k] = hexutil.Encode(b)
			}
			signers[i] = &sgn{addr: addr, start: start, raws: raws}
		}(i)
	}
	wg.Wait()
	log.Printf("pre-signed %d txs (%d signers x %d depth) — starting blast",
		*nSigners**depth, *nSigners, *depth)

	var c counters
	deadline := time.Now().Add(*dur)
	sem := make(chan struct{}, 48) // cap concurrent in-flight HTTP batch calls

	send := func(cl *rpc.Client, chunk []string) {
		defer func() { <-sem }()
		elems := make([]rpc.BatchElem, len(chunk))
		for i, raw := range chunk {
			elems[i] = rpc.BatchElem{Method: "eth_sendRawTransaction", Args: []any{raw}, Result: new(string)}
		}
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := cl.BatchCallContext(sctx, elems); err != nil {
			c.otherErr.Add(int64(len(chunk)))
			return
		}
		for i := range elems {
			classify(&c, elems[i].Error)
		}
	}

	var swg sync.WaitGroup
	for i := 0; i < *nSigners; i++ {
		swg.Add(1)
		go func(i int) {
			defer swg.Done()
			s := signers[i]
			// poll nonce from one endpoint (spread across endpoints by signer)…
			cl := clients[i%len(clients)]
			// …but BROADCAST every batch to ALL endpoints so every proposer's mempool is
			// filled directly (not via cross-validator gossip, which lagged → starved the
			// non-fed proposer's blocks). Duplicates bounce "already known" (cheap).
			// RE-SEND model: every tick re-submit the contiguous window [base, base+window)
			// from the lowest unmined nonce. Txs already in the pool bounce "already known",
			// mined ones "nonce too low" (both cheap, counted landed); the leading edge enters
			// as base advances. Keeps the pool CONTINUOUSLY full and self-heals dropped txs
			// (no gaps), provided in-flight (signers*window) <= pool capacity.
			//
			// base is refreshed only every pollEvery (NOT per tick): a per-signer per-tick WAN
			// eth_getTransactionCount is what starved earlier runs. Between polls we re-send the
			// cached window, which keeps the pool full regardless.
			base := 0
			lastPoll := time.Now().Add(-time.Hour) // force first poll
			for time.Now().Before(deadline) {
				if time.Since(lastPoll) >= *pollEvery {
					var nHex string
					pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
					if err := cl.CallContext(pctx, &nHex, "eth_getTransactionCount", s.addr, "latest"); err == nil {
						if mined := hexToBig(nHex).Uint64(); mined >= s.start {
							base = int(mined - s.start)
						}
					}
					cancel()
					lastPoll = time.Now()
				}
				hi := base + *window
				if hi > len(s.raws) {
					hi = len(s.raws)
				}
				for off := base; off < hi; off += *batch {
					end := off + *batch
					if end > hi {
						end = hi
					}
					chunk := s.raws[off:end]
					for _, c := range clients {
						sem <- struct{}{}
						go send(c, chunk)
					}
				}
				time.Sleep(*tick)
			}
		}(i)
	}

	// live progress
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		var last int64
		startT := time.Now()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				acc := c.accepted.Load()
				el := time.Since(startT).Seconds()
				log.Printf("[%.0fs] accepted=%d (+%.0f/s last5) queuedFull=%d underpriced=%d err=%d",
					el, acc, float64(acc-last)/5.0, c.queuedFull.Load(), c.underpriced.Load(), c.otherErr.Load())
				last = acc
			}
		}
	}()

	swg.Wait()
	close(done)
	log.Printf("DONE: accepted=%d queuedFull=%d underpriced=%d otherErr=%d",
		c.accepted.Load(), c.queuedFull.Load(), c.underpriced.Load(), c.otherErr.Load())
}

func classify(c *counters, err error) {
	if err == nil {
		c.accepted.Add(1)
		return
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "already known"), strings.Contains(m, "nonce too low"):
		c.accepted.Add(1) // already in pool/chain — counts as landed
	case strings.Contains(m, "nonce too high"), strings.Contains(m, "queue"), strings.Contains(m, "full"), strings.Contains(m, "limit"):
		c.queuedFull.Add(1)
	case strings.Contains(m, "underpriced"), strings.Contains(m, "fee"):
		c.underpriced.Add(1)
	default:
		c.otherErr.Add(1)
	}
}

func hexToBig(h string) *big.Int {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(h, "0x"), 16)
	if !ok {
		return big.NewInt(0)
	}
	return n
}
