// Package ai is the OPTIONAL intelligence layer. It is disabled by default
// and nothing in the core pipeline depends on it. Providers implement the
// same interface; the OpenAI-compatible client covers OpenAI, DeepSeek, GLM
// and any compatible endpoint, with Gemini/Anthropic/Ollama adapters to come.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"leadforge/internal/crawler"
)

// Request is a generation request.
type Request struct {
	System      string
	Prompt      string
	MaxTokens   int
	Temperature float64
	JSON        bool
}

// Response is a generation result with usage.
type Response struct {
	Text             string
	PromptTokens     int
	CompletionTokens int
}

// Provider generates text.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req Request) (Response, error)
}

// Disabled is the default provider: always errors, clearly.
type Disabled struct{}

func (Disabled) Name() string { return "disabled" }

func (Disabled) Generate(_ context.Context, _ Request) (Response, error) {
	return Response{}, fmt.Errorf("ai: disabled (set AI_ENABLED=true and configure a provider)")
}

// OpenAICompatible talks to any /v1/chat/completions endpoint
// (OpenAI, DeepSeek, GLM, Ollama via proxy, …).
type OpenAICompatible struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
	Guard   crawler.Guard
}

func (p *OpenAICompatible) Name() string { return "openai-compatible:" + p.Model }

func (p *OpenAICompatible) Generate(ctx context.Context, req Request) (Response, error) {
	if p.APIKey == "" {
		return Response{}, fmt.Errorf("ai: no API key configured")
	}
	model := p.Model
	if model == "" {
		return Response{}, fmt.Errorf("ai: model is not configured")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	msgs := []map[string]string{}
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": req.Prompt})
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": msgs,
		"max_tokens": maxTokens, "temperature": req.Temperature,
	})
	base, err := p.Guard.ValidateAndCheckURL(ctx, p.BaseURL)
	if err != nil {
		return Response{}, fmt.Errorf("ai: endpoint rejected: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base.String(), "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	client := p.Client
	if client == nil {
		client = p.Guard.NewClient(crawler.Options{Timeout: 60 * time.Second, MaxBodyBytes: 4 << 20})
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("ai: status %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, err
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("ai: empty response")
	}
	return Response{
		Text:             parsed.Choices[0].Message.Content,
		PromptTokens:     parsed.Usage.PromptTokens,
		CompletionTokens: parsed.Usage.CompletionTokens,
	}, nil
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
