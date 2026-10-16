package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type scriptedParser struct {
	intent Intent
	err    error
}

func (p scriptedParser) Parse(context.Context, string, time.Time) (Intent, error) {
	return p.intent, p.err
}

func TestBotAddsExpenseWithFallbackComment(t *testing.T) {
	store := newMemoryStore()
	bot, replies := newTestBot(store, scriptedParser{intent: Intent{
		Action: "add", Amount: 350, Category: "еда", SpentAt: "2026-10-16",
	}})
	if err := bot.handle(context.Background(), 101, 101, "кофе 350"); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	item, err := store.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("get added expense: %v", err)
	}
	if item.UserID != 101 || item.Comment != "кофе" {
		t.Fatalf("added expense = %+v, want user 101 and comment coffee", item)
	}
	if !strings.Contains(lastReply(t, replies), "Записал") {
		t.Fatal("bot did not confirm the new expense")
	}
}

func TestBotUpdatesOnlyCallersLastExpense(t *testing.T) {
	store := newMemoryStore()
	first, _ := store.Create(context.Background(), ExpenseInput{
		UserID: 101, Amount: 350, Category: "еда", SpentAt: "2026-10-15", Comment: "кофе",
	})
	_, _ = store.Create(context.Background(), ExpenseInput{
		UserID: 202, Amount: 900, Category: "транспорт", SpentAt: "2026-10-16", Comment: "такси",
	})
	bot, _ := newTestBot(store, scriptedParser{intent: Intent{Action: "update_last", Amount: 450}})
	if err := bot.handle(context.Background(), 101, 101, "исправь последнюю сумму на 450"); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	updated, _ := store.Get(context.Background(), first.ID)
	other, _ := store.Get(context.Background(), 2)
	if updated.Amount != 450 || other.Amount != 900 {
		t.Fatalf("caller expense amount = %v and other user amount = %v", updated.Amount, other.Amount)
	}
}

func TestBotDeletesUsingUserCategoryAmountAndDate(t *testing.T) {
	store := newMemoryStore()
	match, _ := store.Create(context.Background(), ExpenseInput{
		UserID: 101, Amount: 500, Category: "еда", SpentAt: "2026-10-16", Comment: "кофе",
	})
	keep, _ := store.Create(context.Background(), ExpenseInput{
		UserID: 101, Amount: 500, Category: "еда", SpentAt: "2026-10-15", Comment: "завтрак",
	})
	otherUser, _ := store.Create(context.Background(), ExpenseInput{
		UserID: 202, Amount: 500, Category: "еда", SpentAt: "2026-10-16", Comment: "кофе",
	})
	bot, _ := newTestBot(store, scriptedParser{intent: Intent{
		Action: "delete", Category: "еда", Amount: 500, SpentAt: "2026-10-16", Query: "еда",
	}})
	if err := bot.handle(context.Background(), 101, 101, "удали категорию еда на сумму 500 за сегодня"); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	if _, err := store.Get(context.Background(), match.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("matching expense error = %v, want not found", err)
	}
	if _, err := store.Get(context.Background(), keep.ID); err != nil {
		t.Fatalf("unmatched date expense was deleted: %v", err)
	}
	if _, err := store.Get(context.Background(), otherUser.ID); err != nil {
		t.Fatalf("another user's expense was deleted: %v", err)
	}
}

func TestBotListsOnlyCallersExpenses(t *testing.T) {
	store := newMemoryStore()
	_, _ = store.Create(context.Background(), ExpenseInput{
		UserID: 101, Amount: 350, Category: "еда", SpentAt: "2026-10-16", Comment: "кофе",
	})
	_, _ = store.Create(context.Background(), ExpenseInput{
		UserID: 202, Amount: 900, Category: "транспорт", SpentAt: "2026-10-16", Comment: "такси",
	})
	bot, replies := newTestBot(store, scriptedParser{intent: Intent{
		Action: "list", From: "2026-10-01", To: "2026-10-31",
	}})
	if err := bot.handle(context.Background(), 101, 101, "покажи траты за месяц"); err != nil {
		t.Fatalf("handle() error = %v", err)
	}
	answer := lastReply(t, replies)
	if !strings.Contains(answer, "350.00") || strings.Contains(answer, "900.00") {
		t.Fatalf("bot list reply = %q, want only caller's expense", answer)
	}
}

func newTestBot(store *memoryStore, parser scriptedParser) (*Bot, *[]string) {
	replies := make([]string, 0)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		replies = append(replies, body.Text)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	})}
	return &Bot{
		store:    store,
		llm:      parser,
		token:    "test-token",
		location: time.UTC,
		client:   client,
	}, &replies
}

func lastReply(t *testing.T, replies *[]string) string {
	t.Helper()
	if len(*replies) == 0 {
		t.Fatal("bot sent no reply")
	}
	return (*replies)[len(*replies)-1]
}
