//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestDispatches_ConcurrentCreate_NoCollision reproduces Shipping-6.
//
// CreateDispatchCode builds the code as COUNT(*)+1 over today's dispatches inside
// the create transaction (dispatches/repository.go GetActualDispatchCode +
// CreateDispatchCode). That read-then-write is NOT atomic: concurrent POST
// /dispatches/ calls each read the same COUNT under READ COMMITTED, format the same
// DSP-YYYY-MMDD-NNN code, and collide on the UNIQUE(dispatch_code) constraint — the
// loser's transaction aborts and the handler returns 503.
//
// Expected RED (on dev AND on the issue-50 "fix" branch, which only renamed
// strconv->Sprintf): some concurrent creates fail, so successCount < N.
// Expected GREEN (after a real fix that serializes numbering, e.g. an advisory
// xact lock): every concurrent create succeeds with a distinct code.
func TestDispatches_ConcurrentCreate_NoCollision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env := testEnv
	require.NotNil(t, env)
	token := operatorToken(t, env)

	// A valid, existing destination (seed data guarantees at least one).
	var destID string
	require.NoError(t, env.db.QueryRow(ctx,
		`SELECT destination_id::text FROM wms_inventory.destinations LIMIT 1`).Scan(&destID))
	require.NotEmpty(t, destID)

	const n = 15
	vehiclePrefix := "SH6-" + uuid.NewString()[:8] + "-"
	scheduledAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)

	type result struct {
		status int
		code   string
		body   string
	}
	results := make([]result, n)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]any{
				"destination_id": destID,
				"vehicle_number": fmt.Sprintf("%s%d", vehiclePrefix, idx),
				"driver_name":    "Race Driver",
				"scheduled_at":   scheduledAt,
			})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				env.wmsURL+"/dispatches/", bytes.NewReader(payload))
			if err != nil {
				results[idx] = result{status: -1, body: err.Error()}
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			<-start // release all goroutines simultaneously to maximise the race
			resp, err := env.httpClient.Do(req)
			if err != nil {
				results[idx] = result{status: -1, body: err.Error()}
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)

			var env struct {
				Data struct {
					Dispatch struct {
						DispatchCode string `json:"dispatch_code"`
					} `json:"dispatch"`
				} `json:"data"`
			}
			_ = json.Unmarshal(raw, &env)
			results[idx] = result{status: resp.StatusCode, code: env.Data.Dispatch.DispatchCode, body: string(raw)}
		}(i)
	}
	close(start)
	wg.Wait()

	// Tally on the test goroutine (require/* is not goroutine-safe).
	var successes int
	seen := map[string]int{}
	var failures []string
	for i, r := range results {
		switch {
		case r.status == http.StatusOK:
			successes++
			if r.code != "" {
				seen[r.code]++
			}
		default:
			failures = append(failures, fmt.Sprintf("req#%d -> %d: %s", i, r.status, r.body))
		}
	}
	dupes := map[string]int{}
	for code, c := range seen {
		if c > 1 {
			dupes[code] = c
		}
	}

	t.Logf("concurrent creates: %d/%d succeeded, %d distinct codes, %d failures",
		successes, n, len(seen), len(failures))
	for _, f := range failures {
		t.Logf("  FAIL %s", f)
	}

	require.Empty(t, dupes, "duplicate dispatch_code(s) issued under concurrency: %v", dupes)
	require.Equalf(t, n, successes,
		"all %d concurrent dispatch creations must succeed; non-atomic numbering caused %d failures:\n%v",
		n, len(failures), failures)
}
