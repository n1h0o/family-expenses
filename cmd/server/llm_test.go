package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLLMParseReturnsStructuredIntent(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "test-model" {
			t.Errorf("model = %q, want test-model", request.Model)
		}
		if len(request.Messages) != 2 || request.Messages[1].Content != "кофе 350" {
			t.Errorf("messages = %+v", request.Messages)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"action\":\"add\",\"amount\":350,\"category\":\"еда\",\"spentAt\":\"2026-10-16\",\"comment\":\"кофе\"}"}}]}`)),
			Header:     make(http.Header),
		}, nil
	})}

	llm := &LLM{
		baseURL: "https://llm.example/v1",
		key:     "test-key",
		model:   "test-model",
		client:  client,
	}
	intent, err := llm.Parse(context.Background(), "кофе 350", time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if intent.Action != "add" || intent.Amount != 350 || intent.Category != "еда" || intent.Comment != "кофе" {
		t.Fatalf("Parse() = %+v", intent)
	}
}

func TestLLMParseReportsHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader("invalid key")),
			Header:     make(http.Header),
		}, nil
	})}
	llm := &LLM{baseURL: "https://llm.example", key: "bad-key", model: "test-model", client: client}
	_, err := llm.Parse(context.Background(), "кофе 350", time.Now())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("Parse() error = %v, want HTTP 401", err)
	}
}

func TestLLMParseRequiresAPIKey(t *testing.T) {
	llm := &LLM{baseURL: "http://unused.invalid", model: "test-model", client: http.DefaultClient}
	_, err := llm.Parse(context.Background(), "кофе 350", time.Now())
	if err == nil || !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("Parse() error = %v, want missing API key", err)
	}
}
