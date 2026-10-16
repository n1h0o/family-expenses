package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type apiStore interface {
	Create(context.Context, ExpenseInput) (Expense, error)
	Get(context.Context, int64) (Expense, error)
	Update(context.Context, int64, ExpenseInput) (Expense, error)
	Delete(context.Context, int64) error
	List(context.Context, Filters) ([]Expense, int, error)
	Total(context.Context, Filters) (float64, int, error)
	Members(context.Context) ([]int64, error)
}

type API struct{ store apiStore }
type ExpensePatch struct {
	UserID   *int64   `json:"userId"`
	Amount   *float64 `json:"amount"`
	Category *string  `json:"category"`
	SpentAt  *string  `json:"spentAt"`
	Comment  *string  `json:"comment"`
}

func NewAPI(s apiStore) *API { return &API{store: s} }

// регистрирует публичные маршруты и связывает их с обработчиками.
func (a *API) Register(m *http.ServeMux) {
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/members", a.members)
	m.HandleFunc("/api/expenses", a.expenses)
	m.HandleFunc("/api/expenses/", a.expense)
}
func (a *API) members(w http.ResponseWriter, r *http.Request) {
	ids, err := a.store.Members(r.Context())
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "Не удалось получить участников")
		return
	}
	writeJSON(w, 200, map[string]any{"data": ids})
}
func (a *API) expenses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.list(w, r)
	case http.MethodPost:
		var in ExpenseInput
		if !decode(w, r, &in) {
			return
		}
		if in.SpentAt == "" {
			in.SpentAt = time.Now().Format("2006-01-02")
		}
		if err := validate(in); err != nil {
			writeError(w, 422, "VALIDATION_ERROR", err.Error())
			return
		}
		e, err := a.store.Create(r.Context(), in)
		if err != nil {
			writeError(w, 500, "INTERNAL_ERROR", "Не удалось сохранить трату")
			return
		}
		writeJSON(w, 201, e)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, 405, "METHOD_NOT_ALLOWED", "Метод не поддерживается")
	}
}
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pageSize := parseInt(q.Get("pageSize"), 50)
	page := parseInt(q.Get("page"), 1)
	f := Filters{
		Category: q.Get("category"),
		From:     q.Get("from"),
		To:       q.Get("to"),
		Limit:    pageSize,
		Offset:   (page - 1) * pageSize,
	}
	if v := q.Get("userId"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			writeError(w, 422, "VALIDATION_ERROR", "Некорректный userId")
			return
		}
		f.UserID = id
	}
	if f.Category != "" && !categories[f.Category] {
		writeError(w, 422, "VALIDATION_ERROR", "Неизвестная категория")
		return
	}
	if !validDateOrEmpty(f.From) || !validDateOrEmpty(f.To) {
		writeError(w, 422, "VALIDATION_ERROR", "Дата должна быть в формате YYYY-MM-DD")
		return
	}
	items, total, err := a.store.List(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "Не удалось получить траты")
		return
	}
	amount, count, err := a.store.Total(r.Context(), f)
	if err != nil {
		writeError(w, 500, "INTERNAL_ERROR", "Не удалось посчитать траты")
		return
	}
	if f.Limit < 1 || f.Limit > 100 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}
	writeJSON(w, 200, map[string]any{
		"data": items,
		"summary": map[string]any{
			"amount": amount,
			"count":  count,
		},
		"pagination": map[string]int{
			"page":       page,
			"pageSize":   pageSize,
			"totalItems": total,
			"totalPages": (total + pageSize - 1) / pageSize,
		},
	})
}
func (a *API) expense(w http.ResponseWriter, r *http.Request) {
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/expenses/"), "/")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		writeError(w, 404, "NOT_FOUND", "Трата не найдена")
		return
	}
	switch r.Method {
	case http.MethodGet:
		e, err := a.store.Get(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "Трата не найдена")
			return
		}
		if err != nil {
			writeError(w, 500, "INTERNAL_ERROR", "Не удалось получить трату")
			return
		}
		writeJSON(w, 200, e)
	case http.MethodPatch:
		var patch ExpensePatch
		if !decode(w, r, &patch) {
			return
		}
		current, err := a.store.Get(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "Трата не найдена")
			return
		}
		if err != nil {
			writeError(w, 500, "INTERNAL_ERROR", "Не удалось получить трату")
			return
		}
		if patch.UserID != nil {
			current.UserID = *patch.UserID
		}
		if patch.Amount != nil {
			current.Amount = *patch.Amount
		}
		if patch.Category != nil {
			current.Category = *patch.Category
		}
		if patch.SpentAt != nil {
			current.SpentAt = *patch.SpentAt
		}
		if patch.Comment != nil {
			current.Comment = *patch.Comment
		}
		in := ExpenseInput{
			UserID:   current.UserID,
			Amount:   current.Amount,
			Category: current.Category,
			SpentAt:  current.SpentAt,
			Comment:  current.Comment,
		}
		if err := validate(in); err != nil {
			writeError(w, 422, "VALIDATION_ERROR", err.Error())
			return
		}
		e, err := a.store.Update(r.Context(), id, in)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "Трата не найдена")
			return
		}
		if err != nil {
			writeError(w, 500, "INTERNAL_ERROR", "Не удалось обновить трату")
			return
		}
		writeJSON(w, 200, e)
	case http.MethodDelete:
		err := a.store.Delete(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "NOT_FOUND", "Трата не найдена")
			return
		}
		if err != nil {
			writeError(w, 500, "INTERNAL_ERROR", "Не удалось удалить трату")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		writeError(w, 405, "METHOD_NOT_ALLOWED", "Метод не поддерживается")
	}
}

// проверяет данные запроса до обращения к базе данных.
func validate(in ExpenseInput) error {
	if in.UserID < 1 {
		return errors.New("Укажите Telegram ID члена семьи")
	}
	if in.Amount <= 0 || in.Amount > 1e9 {
		return errors.New("Сумма должна быть больше нуля")
	}
	if !categories[in.Category] {
		return errors.New("Неизвестная категория")
	}
	if !validDateOrEmpty(in.SpentAt) {
		return errors.New("Дата должна быть в формате YYYY-MM-DD")
	}
	if in.SpentAt == "" {
		in.SpentAt = time.Now().Format("2006-01-02")
	}
	if len(in.Comment) > 500 {
		return errors.New("Комментарий длиннее 500 символов")
	}
	return nil
}
func validDateOrEmpty(s string) bool {
	if s == "" {
		return true
	}
	_, e := time.Parse("2006-01-02", s)
	return e == nil
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "INVALID_JSON", "Некорректное тело запроса")
		return false
	}
	return true
}
func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, e := strconv.Atoi(s)
	if e != nil {
		return fallback
	}
	return v
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
