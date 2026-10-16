package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type Intent struct {
	Action   string  `json:"action"`
	Amount   float64 `json:"amount"`
	Category string  `json:"category"`
	SpentAt  string  `json:"spentAt"`
	Comment  string  `json:"comment"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Query    string  `json:"query"`
	Reply    string  `json:"reply"`
}
type LLM struct {
	baseURL, key, model string
	client              *http.Client
}

func NewLLM() *LLM {
	return &LLM{
		baseURL: strings.TrimRight(env("LLM_BASE_URL", "https://api.openai.com/v1"), "/"),
		key:     os.Getenv("LLM_API_KEY"),
		model:   env("LLM_MODEL", "gpt-4o-mini"),
		client:  &http.Client{Timeout: 35 * time.Second},
	}
}

// преобразует сообщение в действие, которое понимает бот.
func (l *LLM) Parse(ctx context.Context, text string, now time.Time) (Intent, error) {
	if l.key == "" {
		return Intent{}, fmt.Errorf("LLM_API_KEY не настроен")
	}
	system := fmt.Sprintf(`Ты парсер сообщений для семейного учёта расходов.
Сегодня %s.

Верни только JSON-объект с полями action, amount, category, spentAt, comment,
from, to, query и reply. Допустимые действия: add, list, update_last, delete,
clarify. Допустимые категории: еда, транспорт, жильё, развлечения, здоровье,
покупки, другое. Сумму указывай числом, даты — в формате YYYY-MM-DD.

Определи категорию по смыслу покупки. Не выдумывай сумму или дату.
Если дата траты не названа, используй сегодня.
Для add запиши в comment короткое название покупки без суммы и даты.
Например, для «кофе 350» comment должен быть «кофе».

Для запроса о тратах выбери list. Для исправления последней траты выбери
update_last, для удаления — delete. При удалении заполняй category, amount
и spentAt, если пользователь их назвал. Если названа категория и сумма,
не добавляй категорию в query: query предназначен только для названия покупки.
Если дата удаления не названа, используй сегодня. Если намерение неясно, задай короткий
вопрос. Для относительного периода заполни from и to. Под месяцем понимай
календарный месяц, под неделей — последние семь дней включительно.
В query укажи краткое название удаляемой траты.`, now.Format("2006-01-02"))
	payload := map[string]any{
		"model":           l.model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []any{
			map[string]string{"role": "system", "content": system},
			map[string]string{"role": "user", "content": text},
		},
	}
	body, _ := json.Marshal(payload)
	endpoint := l.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return Intent{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.key)
	resp, err := l.client.Do(req)
	if err != nil {
		return Intent{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Intent{}, fmt.Errorf("LLM вернул HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return Intent{}, err
	}
	if len(envelope.Choices) == 0 {
		return Intent{}, fmt.Errorf("LLM не вернул ответ")
	}
	var in Intent
	if err = json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &in); err != nil {
		return Intent{}, err
	}
	return in, nil
}
