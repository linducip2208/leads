// Package webhookstore dispatches signed outbound webhooks via Asynq.
package webhookstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/crawler"
	"leadforge/internal/crypto"
	"leadforge/internal/metrics"
)

// Emit finds active webhooks subscribed to event and enqueues deliveries.
// enqueue is injected (asynq client) to keep this package queue-agnostic.
func Emit(ctx context.Context, pool *pgxpool.Pool, tenantID, event string, payload map[string]any, enqueue func(webhookID, event string, body []byte) error) error {
	rows, err := pool.Query(ctx, `SELECT id::text FROM webhooks WHERE tenant_id=$1 AND is_active AND $2 = ANY(events)`, tenantID, event)
	if err != nil {
		return err
	}
	defer rows.Close()
	body, _ := json.Marshal(map[string]any{"event": event, "data": payload, "at": time.Now().UTC().Format(time.RFC3339)})
	var firstErr error
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			if err := enqueue(id, event, body); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := rows.Err(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// RandomSecret generates a signing secret.
func RandomSecret() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Sign returns the hex HMAC-SHA256 of ts.body.
func Sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// EventID is stable for the logical webhook delivery. Retries therefore carry
// the same identifier and consumers can safely deduplicate them.
func EventID(webhookID, event string, body []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(webhookID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(event))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	return "evt_" + hex.EncodeToString(h.Sum(nil)[:16])
}

// ValidateTarget applies the outbound webhook SSRF and HTTPS policy before a
// request is built. allowInsecure is intended only for local development.
func ValidateTarget(ctx context.Context, target string, allowInsecure bool) (string, error) {
	guard := crawler.Guard{AllowPrivate: allowInsecure}
	u, err := guard.ValidateAndCheckURL(ctx, target)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" && !allowInsecure {
		return "", fmt.Errorf("webhook: https required")
	}
	return u.String(), nil
}

// Deliver POSTs one webhook with signature headers and records the outcome.
func Deliver(ctx context.Context, pool *pgxpool.Pool, appSecret, webhookID, event string, body []byte, allowInsecure ...bool) error {
	var tenantID, targetURL string
	var secretEnc []byte
	err := pool.QueryRow(ctx, `SELECT tenant_id::text, url, secret_enc FROM webhooks WHERE id=$1 AND is_active`,
		webhookID).Scan(&tenantID, &targetURL, &secretEnc)
	if err != nil {
		return nil // gone or disabled: drop
	}
	secret := ""
	if len(secretEnc) > 0 {
		if s, err := crypto.Decrypt(appSecret, string(secretEnc)); err == nil {
			secret = s
		}
	}
	unsafeLocal := len(allowInsecure) > 0 && allowInsecure[0]
	validatedTarget, err := ValidateTarget(ctx, targetURL, unsafeLocal)
	if err != nil {
		record(ctx, pool, webhookID, event, body, "failed", 0, "webhook target rejected")
		return fmt.Errorf("webhook target rejected: %w", err)
	}
	guard := crawler.Guard{AllowPrivate: unsafeLocal}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, validatedTarget, bytes.NewReader(body))
	if err != nil {
		record(ctx, pool, webhookID, event, body, "failed", 0, err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	eventID := EventID(webhookID, event, body)
	req.Header.Set("X-LeadForge-Event-ID", eventID)
	req.Header.Set("X-LeadForge-Event", event)
	req.Header.Set("X-LeadForge-Timestamp", ts)
	// Legacy names remain for existing consumers during the hardening rollout.
	req.Header.Set("X-Event", event)
	req.Header.Set("X-Timestamp", ts)
	if secret != "" {
		sig := Sign(secret, ts, body)
		req.Header.Set("X-LeadForge-Signature", sig)
		req.Header.Set("X-Signature-256", sig)
	}
	client := guard.NewClient(crawler.Options{Timeout: 10 * time.Second, MaxBodyBytes: 1 << 20})
	resp, err := client.Do(req)
	if err != nil {
		record(ctx, pool, webhookID, event, body, "failed", 0, trunc(err.Error()))
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("status %d", resp.StatusCode)
		record(ctx, pool, webhookID, event, body, "failed", resp.StatusCode, msg)
		return fmt.Errorf("webhook: %s", msg)
	}
	record(ctx, pool, webhookID, event, body, "success", resp.StatusCode, "")
	_ = tenantID
	return nil
}

func record(ctx context.Context, pool *pgxpool.Pool, webhookID, event string, body []byte, status string, code int, errMsg string) {
	if status == "failed" {
		metrics.WebhooksFailed.Add(1)
	}
	payload := map[string]any{"event": event}
	_ = json.Unmarshal(body, &payload)
	_, _ = pool.Exec(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, event, payload, status, response_code, attempts, error)
		VALUES ($1,$2,$3,$4,$5,1,$6)`, webhookID, event, string(body), status, code, errMsg)
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
