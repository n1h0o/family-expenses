package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type memoryStore struct {
	items  map[int64]Expense
	nextID int64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: make(map[int64]Expense), nextID: 1}
}

func (s *memoryStore) Create(_ context.Context, in ExpenseInput) (Expense, error) {
	now := time.Now()
	item := Expense{
		ID:        s.nextID,
		UserID:    in.UserID,
		Amount:    in.Amount,
		Category:  in.Category,
		SpentAt:   in.SpentAt,
		Comment:   in.Comment,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.items[item.ID] = item
	s.nextID++
	return item, nil
}

func (s *memoryStore) Get(_ context.Context, id int64) (Expense, error) {
	item, ok := s.items[id]
	if !ok {
		return Expense{}, pgx.ErrNoRows
	}
	return item, nil
}

func (s *memoryStore) Update(_ context.Context, id int64, in ExpenseInput) (Expense, error) {
	item, ok := s.items[id]
	if !ok {
		return Expense{}, pgx.ErrNoRows
	}
	item.UserID = in.UserID
	item.Amount = in.Amount
	item.Category = in.Category
	item.SpentAt = in.SpentAt
	item.Comment = in.Comment
	item.UpdatedAt = time.Now()
	s.items[id] = item
	return item, nil
}

func (s *memoryStore) Delete(_ context.Context, id int64) error {
	if _, ok := s.items[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(s.items, id)
	return nil
}

func (s *memoryStore) List(_ context.Context, filters Filters) ([]Expense, int, error) {
	items := make([]Expense, 0)
	for _, item := range s.items {
		if filters.UserID > 0 && item.UserID != filters.UserID {
			continue
		}
		if filters.Category != "" && item.Category != filters.Category {
			continue
		}
		if filters.From != "" && item.SpentAt < filters.From {
			continue
		}
		if filters.To != "" && item.SpentAt > filters.To {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SpentAt == items[j].SpentAt {
			return items[i].ID > items[j].ID
		}
		return items[i].SpentAt > items[j].SpentAt
	})
	total := len(items)
	start := filters.Offset
	if start > len(items) {
		start = len(items)
	}
	end := len(items)
	if filters.Limit > 0 && start+filters.Limit < end {
		end = start + filters.Limit
	}
	return items[start:end], total, nil
}

func (s *memoryStore) Total(_ context.Context, filters Filters) (float64, int, error) {
	items, _, err := s.List(context.Background(), Filters{
		UserID:   filters.UserID,
		Category: filters.Category,
		From:     filters.From,
		To:       filters.To,
		Limit:    len(s.items),
	})
	var amount float64
	for _, item := range items {
		amount += item.Amount
	}
	return amount, len(items), err
}

func (s *memoryStore) Members(context.Context) ([]int64, error) {
	seen := make(map[int64]bool)
	ids := make([]int64, 0)
	for _, item := range s.items {
		if !seen[item.UserID] {
			seen[item.UserID] = true
			ids = append(ids, item.UserID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (s *memoryStore) LastByUser(_ context.Context, userID int64) (Expense, error) {
	var latest Expense
	for _, item := range s.items {
		if item.UserID == userID && item.ID > latest.ID {
			latest = item
		}
	}
	if latest.ID == 0 {
		return Expense{}, pgx.ErrNoRows
	}
	return latest, nil
}

func (s *memoryStore) DeleteLastMatching(
	_ context.Context,
	userID int64,
	category string,
	query string,
	amount float64,
	spentAt string,
) (Expense, error) {
	var candidate Expense
	for _, item := range s.items {
		if item.UserID != userID ||
			(category != "" && item.Category != category) ||
			(query != "" && !strings.Contains(strings.ToLower(item.Comment), strings.ToLower(query))) ||
			(amount > 0 && item.Amount != amount) ||
			(spentAt != "" && item.SpentAt != spentAt) {
			continue
		}
		if candidate.ID == 0 || item.SpentAt > candidate.SpentAt ||
			(item.SpentAt == candidate.SpentAt && item.ID > candidate.ID) {
			candidate = item
		}
	}
	if candidate.ID == 0 {
		return Expense{}, pgx.ErrNoRows
	}
	delete(s.items, candidate.ID)
	return candidate, nil
}

func TestAPIExpenseCRUD(t *testing.T) {
	store := newMemoryStore()
	handler := newAPIHandler(store)

	create := httptest.NewRequest(http.MethodPost, "/api/expenses", strings.NewReader(
		`{"userId":123,"amount":350,"category":"еда","spentAt":"2026-10-16","comment":"кофе"}`,
	))
	createdResponse := httptest.NewRecorder()
	handler.ServeHTTP(createdResponse, create)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d: %s", createdResponse.Code, http.StatusCreated, createdResponse.Body)
	}
	var created Expense
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created expense: %v", err)
	}
	if created.ID == 0 || created.Comment != "кофе" {
		t.Fatalf("created expense = %+v, want id and comment", created)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/expenses/1", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d: %s", getResponse.Code, http.StatusOK, getResponse.Body)
	}

	updateBody := strings.NewReader(`{"amount":450,"comment":"латте"}`)
	updateRequest := httptest.NewRequest(http.MethodPatch, "/api/expenses/1", updateBody)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d: %s", updateResponse.Code, http.StatusOK, updateResponse.Body)
	}
	updated, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get updated expense: %v", err)
	}
	if updated.Amount != 450 || updated.Comment != "латте" || updated.Category != "еда" {
		t.Fatalf("updated expense = %+v", updated)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/expenses/1", nil)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleteResponse.Code, http.StatusNoContent)
	}
	if _, err := store.Get(context.Background(), created.ID); err != pgx.ErrNoRows {
		t.Fatalf("get after delete error = %v, want ErrNoRows", err)
	}
}

func TestAPIListAppliesFiltersAndReturnsSummary(t *testing.T) {
	store := newMemoryStore()
	for _, item := range []ExpenseInput{
		{UserID: 10, Amount: 350, Category: "еда", SpentAt: "2026-10-16"},
		{UserID: 10, Amount: 1200, Category: "транспорт", SpentAt: "2026-10-15"},
		{UserID: 20, Amount: 500, Category: "еда", SpentAt: "2026-10-16"},
	} {
		if _, err := store.Create(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	handler := newAPIHandler(store)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/expenses?userId=10&category=еда&from=2026-10-16&to=2026-10-16",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	var payload struct {
		Data    []Expense `json:"data"`
		Summary struct {
			Amount float64 `json:"amount"`
			Count  int     `json:"count"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].UserID != 10 {
		t.Fatalf("filtered data = %+v, want one expense for user 10", payload.Data)
	}
	if payload.Summary.Amount != 350 || payload.Summary.Count != 1 {
		t.Fatalf("summary = %+v, want amount 350 and count 1", payload.Summary)
	}
}

func TestAPIPostRejectsInvalidExpense(t *testing.T) {
	handler := newAPIHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodPost, "/api/expenses", strings.NewReader(
		`{"userId":0,"amount":-1,"category":"неизвестно","spentAt":"2026-02-30"}`,
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
}

func TestAPIRejectsUnknownFields(t *testing.T) {
	handler := newAPIHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodPost, "/api/expenses", strings.NewReader(
		`{"userId":10,"amount":100,"category":"еда","spentAt":"2026-10-16","unexpected":true}`,
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func newAPIHandler(store *memoryStore) http.Handler {
	mux := http.NewServeMux()
	NewAPI(store).Register(mux)
	return mux
}

func TestParseIntUsesFallbackForInvalidInput(t *testing.T) {
	if got := parseInt("not-a-number", 20); got != 20 {
		t.Fatalf("parseInt() = %d, want fallback 20", got)
	}
	if got := parseInt("7", 20); got != 7 {
		t.Fatalf("parseInt() = %d, want 7", got)
	}
}

func TestAPIRejectsInvalidListFilters(t *testing.T) {
	handler := newAPIHandler(newMemoryStore())
	for _, query := range []url.Values{
		{"userId": {"zero"}},
		{"category": {"unknown"}},
		{"from": {"not-a-date"}},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/expenses?"+query.Encode(), nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Errorf("query %q status = %d, want %d", query.Encode(), response.Code, http.StatusUnprocessableEntity)
		}
	}
}

func TestAPIHealth(t *testing.T) {
	handler := newAPIHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("health response = %d %s", response.Code, response.Body)
	}
}

func TestAPIErrorEnvelopeContainsCodeAndMessage(t *testing.T) {
	response := httptest.NewRecorder()
	writeError(response, http.StatusBadRequest, "INVALID_JSON", "Некорректный запрос")
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "INVALID_JSON" || payload.Error.Message == "" {
		t.Fatalf("error envelope = %+v", payload.Error)
	}
}

func TestAPIMembersReturnsDistinctUserIDs(t *testing.T) {
	store := newMemoryStore()
	for _, userID := range []int64{20, 10, 20} {
		_, err := store.Create(context.Background(), ExpenseInput{
			UserID: userID, Amount: 100, Category: "еда", SpentAt: "2026-10-16",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := newAPIHandler(store)
	request := httptest.NewRequest(http.MethodGet, "/api/members", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("members status = %d, want %d", response.Code, http.StatusOK)
	}
	if got, want := response.Body.String(), `{"data":[10,20]}`+"\n"; got != want {
		t.Fatalf("members response = %q, want %q", got, want)
	}
}

func TestAPIGetMissingExpenseReturnsNotFound(t *testing.T) {
	handler := newAPIHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodGet, "/api/expenses/999", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestAPIGetExpenseRejectsMalformedID(t *testing.T) {
	handler := newAPIHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodGet, "/api/expenses/nope", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
