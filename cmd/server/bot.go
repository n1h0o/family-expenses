package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Bot struct {
	store    botStore
	llm      intentParser
	token    string
	location *time.Location
	client   *http.Client
}

type botStore interface {
	apiStore
	LastByUser(context.Context, int64) (Expense, error)
	DeleteLastMatching(context.Context, int64, string, string, float64, string) (Expense, error)
}

type intentParser interface {
	Parse(context.Context, string, time.Time) (Intent, error)
}

// возвращает название покупки из сообщения, если модель не заполнила комментарий.
func purchaseComment(text string) string {
	amountPattern := regexp.MustCompile(`(?i)\d+(?:[.,]\d+)?\s*(?:₽|руб(?:\.|лей|ля|ль)?|р(?:уб)?\.?)?`)
	comment := amountPattern.ReplaceAllString(text, " ")
	datePattern := regexp.MustCompile(`(?i)(сегодня|вчера|позавчера)`)
	comment = datePattern.ReplaceAllString(comment, " ")
	return strings.Trim(strings.Join(strings.Fields(comment), " "), " ,.;:!?—-«»\"'")
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// создаёт обработчик бота с выбранным часовым поясом.
func NewBot(s botStore, l intentParser, token, tz string) *Bot {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return &Bot{
		store:    s,
		llm:      l,
		token:    token,
		location: loc,
		client:   &http.Client{Timeout: 45 * time.Second},
	}
}

// получает обновления Telegram и отвечает пользователям в личных чатах.
func (b *Bot) Run(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=25&offset=%d", b.token, offset)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		resp, err := b.client.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("telegram polling: %v", err)
				time.Sleep(time.Second)
			}
			continue
		}
		var result struct {
			OK     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil || !result.OK {
			log.Printf("telegram polling response invalid: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, u := range result.Result {
			offset = u.UpdateID + 1
			if u.Message == nil || strings.TrimSpace(u.Message.Text) == "" {
				continue
			}
			if u.Message.Chat.ID != u.Message.From.ID {
				if err := b.send(
					ctx,
					u.Message.Chat.ID,
					"Для приватности напишите мне в личном чате.",
				); err != nil {
					log.Printf("telegram update %d: %v", u.UpdateID, err)
				}
				continue
			}
			if err := b.handle(ctx, u.Message.From.ID, u.Message.Chat.ID, u.Message.Text); err != nil {
				log.Printf("telegram update %d: %v", u.UpdateID, err)
			}
		}
	}
}

// выполняет действие от имени автора сообщения в Telegram.
func (b *Bot) handle(ctx context.Context, userID, chatID int64, text string) error {
	intent, err := b.llm.Parse(ctx, text, time.Now().In(b.location))
	if err != nil {
		message := "Не удалось обработать сообщение. Проверьте настройки ИИ " +
			"и попробуйте ещё раз."
		return b.send(ctx, chatID, message)
	}
	if intent.Action != "add" &&
		intent.Action != "list" &&
		intent.Action != "update_last" &&
		intent.Action != "delete" &&
		intent.Action != "clarify" {
		message := "Не понял запрос. Попробуйте уточнить его " +
			"другими словами."
		return b.send(ctx, chatID, message)
	}
	if intent.Action == "list" && (!validDateOrEmpty(intent.From) || !validDateOrEmpty(intent.To)) {
		message := "Не смог определить период. Напишите дату " +
			"или период точнее."
		return b.send(ctx, chatID, message)
	}
	if intent.Action == "delete" && !validDateOrEmpty(intent.SpentAt) {
		return b.send(ctx, chatID, "Не смог определить дату траты. Уточните её, пожалуйста.")
	}
	if intent.Category != "" &&
		!categories[intent.Category] &&
		(intent.Action == "add" || intent.Action == "delete") {
		message := "Не смог определить категорию. " +
			"Уточните покупку, пожалуйста."
		return b.send(ctx, chatID, message)
	}
	switch intent.Action {
	case "add":
		in := ExpenseInput{
			UserID:   userID,
			Amount:   intent.Amount,
			Category: intent.Category,
			SpentAt:  intent.SpentAt,
			Comment:  intent.Comment,
		}
		if in.SpentAt == "" {
			in.SpentAt = time.Now().In(b.location).Format("2006-01-02")
		}
		if strings.TrimSpace(in.Comment) == "" {
			in.Comment = purchaseComment(text)
		}
		if err := validate(in); err != nil {
			message := "Не удалось распознать сумму или категорию. " +
				"Напишите, например: «кофе 350»."
			return b.send(ctx, chatID, message)
		}
		e, err := b.store.Create(ctx, in)
		if err != nil {
			return err
		}
		return b.send(ctx, chatID, fmt.Sprintf("Записал: %.2f ₽ · %s · %s", e.Amount, e.Category, e.SpentAt))
	case "list":
		filters := Filters{
			UserID:   userID,
			Category: intent.Category,
			From:     intent.From,
			To:       intent.To,
			Limit:    20,
		}
		items, total, err := b.store.List(ctx, filters)
		if err != nil {
			return err
		}
		if total == 0 {
			return b.send(ctx, chatID, "За этот период трат нет.")
		}
		filters.Limit = 0
		filters.Offset = 0
		sum, _, err := b.store.Total(ctx, filters)
		if err != nil {
			return err
		}
		lines := []string{}
		for _, e := range items {
			lines = append(lines, fmt.Sprintf("• %s — %.2f ₽ (%s)", e.SpentAt, e.Amount, e.Category))
		}
		message := fmt.Sprintf(
			"Трат: %d · всего %.2f ₽\n%s",
			total,
			sum,
			strings.Join(lines, "\n"),
		)
		return b.send(ctx, chatID, message)
	case "update_last":
		e, err := b.store.LastByUser(ctx, userID)
		if err == pgx.ErrNoRows {
			return b.send(ctx, chatID, "Пока нечего менять.")
		}
		if err != nil {
			return err
		}
		if intent.Amount > 0 {
			e.Amount = intent.Amount
		}
		if categories[intent.Category] {
			e.Category = intent.Category
		}
		if validDateOrEmpty(intent.SpentAt) && intent.SpentAt != "" {
			e.SpentAt = intent.SpentAt
		}
		if intent.Comment != "" {
			e.Comment = intent.Comment
		}
		updated, err := b.store.Update(ctx, e.ID, ExpenseInput{
			UserID:   userID,
			Amount:   e.Amount,
			Category: e.Category,
			SpentAt:  e.SpentAt,
			Comment:  e.Comment,
		})
		if err != nil {
			return err
		}
		message := fmt.Sprintf(
			"Исправил последнюю трату: %.2f ₽ · %s · %s",
			updated.Amount,
			updated.Category,
			updated.SpentAt,
		)
		return b.send(ctx, chatID, message)
	case "delete":
		if intent.Category == "" &&
			strings.TrimSpace(intent.Query) == "" &&
			intent.Amount <= 0 &&
			intent.SpentAt == "" {
			message := "Какую именно трату удалить? " +
				"Назовите покупку или категорию."
			return b.send(ctx, chatID, message)
		}
		spentAt := intent.SpentAt
		if spentAt == "" {
			spentAt = time.Now().In(b.location).Format("2006-01-02")
		}
		query := strings.TrimSpace(intent.Query)
		if strings.EqualFold(query, intent.Category) {
			query = ""
		}
		e, err := b.store.DeleteLastMatching(
			ctx,
			userID,
			intent.Category,
			query,
			intent.Amount,
			spentAt,
		)
		if err == pgx.ErrNoRows {
			return b.send(ctx, chatID, "Не нашёл такую трату.")
		}
		if err != nil {
			return err
		}
		message := fmt.Sprintf(
			"Удалил: %.2f ₽ · %s · %s",
			e.Amount,
			e.Category,
			e.SpentAt,
		)
		return b.send(ctx, chatID, message)
	default:
		msg := intent.Reply
		if msg == "" {
			msg = "Уточните, пожалуйста: записать трату, показать расходы, " +
				"изменить или удалить запись?"
		}
		return b.send(ctx, chatID, msg)
	}
}
func (b *Bot) send(ctx context.Context, chatID int64, text string) error {
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	endpoint := "https://api.telegram.org/bot" + url.PathEscape(b.token) + "/sendMessage"
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Telegram sendMessage HTTP %d", resp.StatusCode)
	}
	return nil
}
