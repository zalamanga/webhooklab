package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type Receiver struct {
	Store  *Store
	Secret []byte
	Now    func() time.Time
	Wait   func(context.Context, time.Duration) error
}

func NewReceiver(store *Store, secret []byte) *Receiver {
	return &Receiver{Store: store, Secret: secret, Now: time.Now, Wait: waitContext}
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (receiver *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		if err := receiver.Store.Pool.Ping(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	destination := ""
	if r.Method == http.MethodPost && r.URL.Path == "/hooks/warehouse" {
		destination = "warehouse"
	}
	if r.Method == http.MethodPost && r.URL.Path == "/hooks/billing" {
		destination = "billing"
	}
	if destination == "" {
		writeError(w, missing())
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024+1))
	if err != nil || len(body) > 32*1024 || !utf8.Valid(body) {
		writeError(w, invalid("Webhook body must be UTF-8 JSON of at most 32 KiB."))
		return
	}
	id, timestamp := r.Header.Get("Webhook-Id"), r.Header.Get("Webhook-Timestamp")
	if !VerifySignature(receiver.Secret, destination, id, timestamp, r.Header.Get("Webhook-Signature"), body, receiver.Now()) {
		writeError(w, &APIError{401, "INVALID_SIGNATURE", "Webhook signature, ID or timestamp is invalid."})
		return
	}
	var envelope struct {
		ID        string          `json:"id"`
		EventType string          `json:"eventType"`
		CreatedAt time.Time       `json:"createdAt"`
		Payload   json.RawMessage `json:"payload"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&envelope); err != nil {
		writeError(w, invalid("Webhook envelope is invalid."))
		return
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF || !strings.EqualFold(envelope.ID, id) || !ValidID(envelope.ID) || !oneOf(envelope.EventType, "order.created", "order.cancelled") || envelope.CreatedAt.IsZero() {
		writeError(w, invalid("Webhook envelope does not match its headers."))
		return
	}
	if _, err = compactPayload(string(envelope.Payload)); err != nil {
		writeError(w, err)
		return
	}
	duplicate, mode, err := receiver.accept(r.Context(), destination, strings.ToLower(id), digest(body))
	if err != nil {
		writeError(w, err)
		return
	}
	if mode == "FAIL" {
		writeError(w, &APIError{503, "DEMO_FAILURE", "Demo receiver is configured to fail without applying an effect."})
		return
	}
	if mode == "TIMEOUT" {
		if err = receiver.Wait(r.Context(), 3500*time.Millisecond); err != nil {
			return
		}
	}
	writeJSON(w, 200, map[string]any{"received": true, "eventId": strings.ToLower(id), "duplicate": duplicate})
}

func (receiver *Receiver) accept(ctx context.Context, destination, id, bodyHash string) (bool, string, error) {
	tx, err := receiver.Store.Pool.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx)
	var mode string
	// A shared mode lock lets duplicate deliveries proceed concurrently and linearizes mode changes.
	if err = tx.QueryRow(ctx, `SELECT mode FROM destinations WHERE id=$1 FOR SHARE`, destination).Scan(&mode); err != nil {
		return false, "", err
	}
	if mode == "FAIL" {
		var previous string
		err = tx.QueryRow(ctx, `SELECT body_sha256 FROM receipts WHERE destination_id=$1 AND event_id=$2`, destination, id).Scan(&previous)
		if err == nil && previous != bodyHash {
			return false, mode, conflict("This event ID was already received with different immutable content.")
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, mode, err
		}
		return false, mode, nil
	}
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO receipts(destination_id,event_id,body_sha256) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING event_id::text`, destination, id, bodyHash).Scan(&inserted)
	duplicate := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !duplicate {
		return false, mode, err
	}
	if duplicate {
		var previous string
		if err = tx.QueryRow(ctx, `SELECT body_sha256 FROM receipts WHERE destination_id=$1 AND event_id=$2 FOR UPDATE`, destination, id).Scan(&previous); err != nil {
			return false, mode, err
		}
		if previous != bodyHash {
			return false, mode, conflict("This event ID was already received with different immutable content.")
		}
		if _, err = tx.Exec(ctx, `UPDATE receipts SET receipt_count=receipt_count+1,last_received_at=GREATEST(last_received_at,clock_timestamp()) WHERE destination_id=$1 AND event_id=$2`, destination, id); err != nil {
			return false, mode, err
		}
	} else {
		if _, err = tx.Exec(ctx, `INSERT INTO receiver_effects(destination_id,event_id) VALUES($1,$2)`, destination, id); err != nil {
			return false, mode, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, mode, err
	}
	return duplicate, mode, nil
}
