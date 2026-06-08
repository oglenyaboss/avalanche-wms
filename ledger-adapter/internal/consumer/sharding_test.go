package consumer

import (
	"testing"

	"github.com/google/uuid"
)

// TestSignerIndex_DeterministicPerProduct — FSM-критичный инвариант multi-signer'а:
// все стадии одного товара (один ProductID) обязаны попадать к одному signer'у,
// иначе их nonce'ы независимы и putaway мог бы замайниться раньше receiving →
// контракт реверт. signerIndex чистая функция ProductID → это гарантирует.
func TestSignerIndex_DeterministicPerProduct(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16} {
		for i := 0; i < 1000; i++ {
			pid := uuid.New()
			first := signerIndex(pid, n)
			// та же ProductID на каждой из 4 FSM-стадий → один и тот же signer.
			for stage := 0; stage < 4; stage++ {
				if got := signerIndex(pid, n); got != first {
					t.Fatalf("n=%d: signerIndex non-deterministic for %s: %d vs %d", n, pid, first, got)
				}
			}
			if first < 0 || first >= n {
				t.Fatalf("n=%d: signerIndex out of range: %d", n, first)
			}
		}
	}
}

// TestShardBySigner_PreservesAndGroups — shardBySigner ничего не теряет/дублирует
// и держит все сообщения одного ProductID в одном shard'е.
func TestShardBySigner_PreservesAndGroups(t *testing.T) {
	const n = 8
	pids := make([]uuid.UUID, 50)
	for i := range pids {
		pids[i] = uuid.New()
	}
	var msgs []*Message
	for _, pid := range pids {
		for _, agg := range fsmOrder { // 4 стадии на товар
			msgs = append(msgs, &Message{EventID: uuid.New(), ProductID: pid, AggregateType: agg})
		}
	}

	shards := shardBySigner(msgs, n)
	if len(shards) != n {
		t.Fatalf("want %d shards, got %d", n, len(shards))
	}

	// 1) сохранность: сумма размеров shard'ов == len(msgs), без дублей по EventID.
	seen := map[uuid.UUID]struct{}{}
	total := 0
	for _, sh := range shards {
		total += len(sh)
		for _, m := range sh {
			if _, dup := seen[m.EventID]; dup {
				t.Fatalf("duplicate EventID across shards: %s", m.EventID)
			}
			seen[m.EventID] = struct{}{}
		}
	}
	if total != len(msgs) {
		t.Fatalf("lost messages: want %d, got %d", len(msgs), total)
	}

	// 2) группировка: все сообщения одного ProductID — в одном shard'е.
	pidShard := map[uuid.UUID]int{}
	for si, sh := range shards {
		for _, m := range sh {
			if prev, ok := pidShard[m.ProductID]; ok && prev != si {
				t.Fatalf("ProductID %s split across shards %d and %d", m.ProductID, prev, si)
			}
			pidShard[m.ProductID] = si
		}
	}
}
