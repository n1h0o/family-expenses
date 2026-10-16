package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStorePostgresCRUDAndFilters(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schemaName := fmt.Sprintf("expenses_test_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
	})

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	testPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create isolated test pool: %v", err)
	}
	t.Cleanup(testPool.Close)

	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read database schema: %v", err)
	}
	if _, err := testPool.Exec(ctx, string(schema)); err != nil {
		t.Fatalf("initialize test schema: %v", err)
	}

	store := NewStore(testPool)
	first, err := store.Create(ctx, ExpenseInput{
		UserID: 1001, Amount: 500, Category: "еда", SpentAt: "2026-10-16", Comment: "кофе",
	})
	if err != nil {
		t.Fatalf("create first expense: %v", err)
	}
	second, err := store.Create(ctx, ExpenseInput{
		UserID: 1001, Amount: 500, Category: "еда", SpentAt: "2026-10-15", Comment: "завтрак",
	})
	if err != nil {
		t.Fatalf("create second expense: %v", err)
	}
	if _, err := store.Create(ctx, ExpenseInput{
		UserID: 2002, Amount: 1200, Category: "транспорт", SpentAt: "2026-10-16", Comment: "такси",
	}); err != nil {
		t.Fatalf("create third expense: %v", err)
	}

	got, err := store.Get(ctx, first.ID)
	if err != nil || got.Comment != "кофе" {
		t.Fatalf("Get() = (%+v, %v), want first expense", got, err)
	}

	items, count, err := store.List(ctx, Filters{
		UserID: 1001, Category: "еда", From: "2026-10-16", To: "2026-10-16", Limit: 10,
	})
	if err != nil || count != 1 || len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("List() = (%+v, %d, %v), want only first expense", items, count, err)
	}

	amount, count, err := store.Total(ctx, Filters{UserID: 1001, Category: "еда"})
	if err != nil || amount != 1000 || count != 2 {
		t.Fatalf("Total() = (%v, %d, %v), want (1000, 2, nil)", amount, count, err)
	}

	last, err := store.LastByUser(ctx, 1001)
	if err != nil || last.ID != second.ID {
		t.Fatalf("LastByUser() = (%+v, %v), want second expense", last, err)
	}

	updated, err := store.Update(ctx, second.ID, ExpenseInput{
		UserID: 1001, Amount: 550, Category: "еда", SpentAt: "2026-10-15", Comment: "поздний завтрак",
	})
	if err != nil || updated.Amount != 550 || updated.Comment != "поздний завтрак" {
		t.Fatalf("Update() = (%+v, %v)", updated, err)
	}

	deleted, err := store.DeleteLastMatching(ctx, 1001, "еда", "", 500, "2026-10-16")
	if err != nil || deleted.ID != first.ID {
		t.Fatalf("DeleteLastMatching() = (%+v, %v), want first expense", deleted, err)
	}
	if _, err := store.Get(ctx, first.ID); err != pgx.ErrNoRows {
		t.Fatalf("Get(deleted) error = %v, want ErrNoRows", err)
	}

	members, err := store.Members(ctx)
	if err != nil || len(members) != 2 || members[0] != 1001 || members[1] != 2002 {
		t.Fatalf("Members() = (%v, %v), want [1001 2002]", members, err)
	}

	if err := store.Delete(ctx, second.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := store.Delete(ctx, second.ID); err != pgx.ErrNoRows {
		t.Fatalf("Delete(missing) error = %v, want ErrNoRows", err)
	}
}
