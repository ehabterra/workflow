package storage_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/storage"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPostgresHistory verifies the PostgreSQL history dialect end-to-end:
// schema creation (BIGSERIAL/TIMESTAMPTZ), $N placeholders on save and query,
// time round-tripping, and atomic state+history via SaveStateInTx.
func TestPostgresHistory(t *testing.T) {
	dsn := postgresDSN(t) // skips when no database is available

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	histTable := fmt.Sprintf("wf_history_%d", pgTableSeq.Add(1))
	stateTable := fmt.Sprintf("wf_states_%d", pgTableSeq.Add(1))
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", histTable))
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", stateTable))
	})

	hist := history.NewPostgresHistory(db, history.WithTable(histTable),
		history.WithCustomFields(map[string]string{"channel": "channel TEXT"}))
	if err := hist.Initialize(ctx); err != nil {
		t.Fatalf("init history schema: %v", err)
	}

	at := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	err = hist.SaveTransition(ctx, &history.TransitionRecord{
		WorkflowID: "wf", FromState: "a", ToState: "b", Transition: "go",
		Actor: "alice", CreatedAt: at,
		CustomFields: map[string]any{"channel": "webhook"},
	})
	if err != nil {
		t.Fatalf("SaveTransition: %v", err)
	}

	records, err := hist.ListHistory(ctx, "wf", history.QueryOptions{Actor: "alice"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	r := records[0]
	if r.Transition != "go" || r.Actor != "alice" {
		t.Fatalf("record = %+v", r)
	}
	if !r.CreatedAt.Equal(at) {
		t.Fatalf("CreatedAt = %v, want %v", r.CreatedAt, at)
	}
	if r.CustomFields["channel"] != "webhook" {
		t.Fatalf("channel = %v, want webhook", r.CustomFields["channel"])
	}

	// Date filtering through the $N placeholder path.
	before := at.Add(-time.Hour)
	if recs, err := hist.ListHistory(ctx, "wf", history.QueryOptions{FromDate: &before}); err != nil || len(recs) != 1 {
		t.Fatalf("FromDate filter: %v, %d records, want 1", err, len(recs))
	}
	after := at.Add(time.Hour)
	if recs, err := hist.ListHistory(ctx, "wf", history.QueryOptions{FromDate: &after}); err != nil || len(recs) != 0 {
		t.Fatalf("future FromDate filter: %v, %d records, want 0", err, len(recs))
	}

	// Atomic state+history on Postgres via TransactionalStorage.
	store, err := storage.NewPostgresStorage(db, storage.WithTable(stateTable))
	if err != nil {
		t.Fatalf("NewPostgresStorage: %v", err)
	}
	if _, err := db.ExecContext(ctx, store.GenerateSchema()); err != nil {
		t.Fatalf("create state schema: %v", err)
	}
	if _, err := store.SaveState(ctx, "wf2", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatalf("create wf2: %v", err)
	}
	_, err = store.SaveStateInTx(ctx, "wf2", workflow.NewMarking([]workflow.Place{"b"}), nil, 1,
		func(ctx context.Context, tx any) error {
			return hist.SaveTransitionTx(ctx, tx.(*sql.Tx), &history.TransitionRecord{
				WorkflowID: "wf2", FromState: "a", ToState: "b", Transition: "go", CreatedAt: at,
			})
		})
	if err != nil {
		t.Fatalf("SaveStateInTx: %v", err)
	}
	if recs, err := hist.ListHistory(ctx, "wf2", history.QueryOptions{}); err != nil || len(recs) != 1 {
		t.Fatalf("wf2 history: %v, %d records, want 1", err, len(recs))
	}
}
