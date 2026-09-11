package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"leadforge/internal/crawler"
)

func TestDisabled(t *testing.T) {
	if _, err := (Disabled{}).Generate(context.Background(), Request{Prompt: "hi"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenAICompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing auth")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "hello"}}},
			"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 1},
		})
	}))
	defer srv.Close()
	p := &OpenAICompatible{BaseURL: srv.URL, APIKey: "k", Model: "test", Guard: crawler.Guard{AllowPrivate: true}}
	res, err := p.Generate(context.Background(), Request{System: "s", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello" || res.PromptTokens != 3 || res.CompletionTokens != 1 {
		t.Fatalf("resp = %+v", res)
	}
}
