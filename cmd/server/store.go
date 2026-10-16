package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var categories = map[string]bool{
	"еда":         true,
	"транспорт":   true,
	"жильё":       true,
	"развлечения": true,
	"здоровье":    true,
	"покупки":     true,
	"другое":      true,
}

type Expense struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userId"`
	Amount    float64   `json:"amount"`
	Category  string    `json:"category"`
	SpentAt   string    `json:"spentAt"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ExpenseInput struct {
	UserID   int64   `json:"userId"`
	Amount   float64 `json:"amount"`
	Category string  `json:"category"`
	SpentAt  string  `json:"spentAt"`
	Comment  string  `json:"comment"`
}

type Filters struct {
	UserID        int64
	Category      string
	From, To      string
	Limit, Offset int
}

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

const expenseColumns = `id,user_id,amount,category,spent_at::text,comment,created_at,updated_at`

// переносит значения строки базы данных в модель расхода.
func scanExpense(row pgx.Row) (Expense, error) {
	var e Expense
	err := row.Scan(&e.ID, &e.UserID, &e.Amount, &e.Category, &e.SpentAt, &e.Comment, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

// создаёт запись расхода из уже проверенных данных.
func (s *Store) Create(ctx context.Context, in ExpenseInput) (Expense, error) {
	query := `
		INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + expenseColumns
	return scanExpense(s.db.QueryRow(ctx, query,
		in.UserID,
		in.Amount,
		in.Category,
		in.SpentAt,
		in.Comment,
	))
}

func (s *Store) Get(ctx context.Context, id int64) (Expense, error) {
	query := `SELECT ` + expenseColumns + ` FROM expenses WHERE id = $1`
	return scanExpense(s.db.QueryRow(ctx, query, id))
}

func (s *Store) Update(ctx context.Context, id int64, in ExpenseInput) (Expense, error) {
	query := `
		UPDATE expenses
		SET user_id = $2,
		    amount = $3,
		    category = $4,
		    spent_at = $5,
		    comment = $6,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING ` + expenseColumns
	return scanExpense(s.db.QueryRow(ctx, query,
		id,
		in.UserID,
		in.Amount,
		in.Category,
		in.SpentAt,
		in.Comment,
	))
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM expenses WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// собирает фильтры только из параметров, безопасно переданных в запрос.
func (s *Store) List(ctx context.Context, f Filters) ([]Expense, int, error) {
	where := []string{"TRUE"}
	args := []any{}
	add := func(expr string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expr, len(args)))
	}
	if f.UserID > 0 {
		add("user_id=$%d", f.UserID)
	}
	if f.Category != "" {
		add("category=$%d", f.Category)
	}
	if f.From != "" {
		add("spent_at >= $%d::date", f.From)
	}
	if f.To != "" {
		add("spent_at <= $%d::date", f.To)
	}
	clause := strings.Join(where, " AND ")
	var total int
	countQuery := `SELECT count(*) FROM expenses WHERE ` + clause
	if err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	query := `SELECT ` + expenseColumns + ` FROM expenses WHERE ` + clause +
		fmt.Sprintf(` ORDER BY spent_at DESC, id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Expense{}
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}

// считает сумму и количество записей с теми же фильтрами, что и список.
func (s *Store) Total(ctx context.Context, f Filters) (float64, int, error) {
	where := []string{"TRUE"}
	args := []any{}
	add := func(expr string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expr, len(args)))
	}
	if f.UserID > 0 {
		add("user_id=$%d", f.UserID)
	}
	if f.Category != "" {
		add("category=$%d", f.Category)
	}
	if f.From != "" {
		add("spent_at >= $%d::date", f.From)
	}
	if f.To != "" {
		add("spent_at <= $%d::date", f.To)
	}
	var amount float64
	var count int
	query := `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM expenses
		WHERE ` + strings.Join(where, " AND ")
	err := s.db.QueryRow(ctx, query, args...).Scan(&amount, &count)
	return amount, count, err
}

func (s *Store) LastByUser(ctx context.Context, userID int64) (Expense, error) {
	query := `
		SELECT ` + expenseColumns + `
		FROM expenses
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`
	return scanExpense(s.db.QueryRow(ctx, query, userID))
}

// удаляет подходящую запись только из расходов этого пользователя.
func (s *Store) DeleteLastMatching(
	ctx context.Context,
	userID int64,
	category string,
	query string,
	amount float64,
	spentAt string,
) (Expense, error) {
	sql := `
		DELETE FROM expenses
		WHERE id = (
			SELECT id
			FROM expenses
			WHERE user_id = $1
			  AND ($2 = '' OR category = $2)
			  AND ($3 = '' OR comment ILIKE '%' || $3 || '%')
			  AND ($4 <= 0 OR amount = $4)
			  AND ($5 = '' OR spent_at = $5::date)
			ORDER BY spent_at DESC, id DESC
			LIMIT 1
		)
		RETURNING ` + expenseColumns
	return scanExpense(s.db.QueryRow(
		ctx,
		sql,
		userID,
		category,
		query,
		amount,
		spentAt,
	))
}
func (s *Store) Members(ctx context.Context) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT user_id FROM expenses ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
