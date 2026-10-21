package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// captureQuerier запоминает SQL и аргументы запроса и отдаёт заготовленные строки.
type captureQuerier struct {
	sql  string
	args []any

	ids []uuid.UUID
}

func (c *captureQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.sql, c.args = sql, args
	return &fakeRows{ids: c.ids}, nil
}

// captureRowQuerier запоминает SQL и аргументы запроса QueryRow.
type captureRowQuerier struct {
	sql  string
	args []any
}

func (c *captureRowQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	c.sql, c.args = sql, args
	return fakeRow{}
}

// fakeRow — минимальная реализация pgx.Row: Scan всегда пишет false (не отклонено).
type fakeRow struct{}

func (fakeRow) Scan(dest ...any) error {
	b, ok := dest[0].(*bool)
	if !ok {
		return errors.New("fakeRow: unsupported dest")
	}
	*b = false
	return nil
}

// fakeRows — минимальная реализация pgx.Rows поверх слайса uuid.
type fakeRows struct {
	ids []uuid.UUID
	i   int
}

func (r *fakeRows) Next() bool                                   { r.i++; return r.i <= len(r.ids) }
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *fakeRows) Scan(dest ...any) error {
	id, ok := dest[0].(*uuid.UUID)
	if !ok {
		return errors.New("fakeRows: unsupported dest")
	}
	*id = r.ids[r.i-1]
	return nil
}

// TestUnconfirmedProductsEmptyInput проверяет, что пустой набор не ходит в БД.
func TestUnconfirmedProductsEmptyInput(t *testing.T) {
	q := &captureQuerier{}

	out, err := UnconfirmedProducts(context.Background(), q, nil, AggregatePicking)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out != nil {
		t.Fatalf("out = %v, want nil", out)
	}
	if q.sql != "" {
		t.Fatalf("query executed for empty input: %s", q.sql)
	}
}

// TestUnconfirmedProductsArgBuilding проверяет построение запроса: unnest по товарам
// и action_type этапа как отдельные аргументы; результат отсортирован.
func TestUnconfirmedProductsArgBuilding(t *testing.T) {
	// Фиксированные UUID в порядке возрастания сырых байтов (= порядок канонической строки).
	a := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	b := uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	c := uuid.MustParse("00000000-0000-0000-0000-00000000000c")
	// Строки приходят из SQL несортированными — функция приводит их к детерминированному виду.
	q := &captureQuerier{ids: []uuid.UUID{c, a, b}}

	out, err := UnconfirmedProducts(context.Background(), q, []uuid.UUID{a, b, c}, AggregatePicking)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(out) != 3 || out[0] != a || out[1] != b || out[2] != c {
		t.Fatalf("out = %v, want sorted [%s %s %s]", out, a, b, c)
	}

	if len(q.args) != 2 {
		t.Fatalf("args = %v (%d), want 2", q.args, len(q.args))
	}
	ids, ok := q.args[0].([]uuid.UUID)
	if !ok || len(ids) != 3 || ids[0] != a || ids[1] != b || ids[2] != c {
		t.Errorf("product_ids arg = %v, want the three ids in order", q.args[0])
	}
	// picking = 3 — код SignedAction.Type.Pick (ADR-0001 §3).
	if got, ok := q.args[1].(int16); !ok || got != 3 {
		t.Errorf("action_type arg = %v (%T), want int16(3)", q.args[1], q.args[1])
	}
	for _, part := range []string{"unnest($1::uuid[])", "oe.status = 'COMMITTED'", "wa.submitted_at IS NOT NULL", "wa.action_type = $2", "wa.item_ids && $1::uuid[]", "AS MATERIALIZED"} {
		if !strings.Contains(q.sql, part) {
			t.Errorf("query lacks %q:\n%s", part, q.sql)
		}
	}
	if strings.Contains(q.sql, "outbox_events") {
		t.Errorf("query still references outbox_events:\n%s", q.sql)
	}
	if strings.Contains(q.sql, "@> ARRAY[p.product_id]") {
		t.Errorf("query still checks each product against the whole history (@>):\n%s", q.sql)
	}
}

// TestCheckChainStatusQueriesActionsOnly проверяет, что гейт ходит только по подписанным
// действиям (warehouse_actions ⋈ onchain_events) — без легаси-ветки по outbox_events.
func TestCheckChainStatusQueriesActionsOnly(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	q := &captureRowQuerier{}

	if err := CheckChainStatus(context.Background(), q, []uuid.UUID{a}, AggregatePutaway); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(q.args) != 2 {
		t.Fatalf("args = %v (%d), want 2", q.args, len(q.args))
	}
	ids, ok := q.args[0].([]uuid.UUID)
	if !ok || len(ids) != 1 || ids[0] != a {
		t.Errorf("product_ids arg = %v, want [%s]", q.args[0], a)
	}
	// putaway = 2 — код SignedAction.Type.PutAway (ADR-0001 §3).
	if got, ok := q.args[1].(int16); !ok || got != 2 {
		t.Errorf("action_type arg = %v (%T), want int16(2)", q.args[1], q.args[1])
	}
	for _, part := range []string{"wa.action_type = $2", "oe.status = 'FAILED'"} {
		if !strings.Contains(q.sql, part) {
			t.Errorf("query lacks %q:\n%s", part, q.sql)
		}
	}
	if strings.Contains(q.sql, "outbox_events") {
		t.Errorf("query still references outbox_events:\n%s", q.sql)
	}

	// Пустой набор товаров: ничего не проверяем, в БД не ходим.
	q2 := &captureRowQuerier{}
	if err := CheckChainStatus(context.Background(), q2, nil, AggregatePicking); err != nil {
		t.Fatalf("empty input: err = %v, want nil", err)
	}
	if q2.sql != "" {
		t.Fatalf("query executed for empty input: %s", q2.sql)
	}
}
