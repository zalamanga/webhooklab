package lab

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

func (s *Store) Detail(ctx context.Context, id string) (DeliveryDetail, error) {
	result := DeliveryDetail{Attempts: []Attempt{}, Audit: []AuditItem{}}
	if !ValidID(id) {
		return result, invalid("Event ID must be a canonical UUID.")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	result.Delivery, err = scanDelivery(tx.QueryRow(ctx, deliverySelect+`WHERE e.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, missing()
	}
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,number,cycle,status,started_at,finished_at,http_status,duration_ms,response_preview,error_category,worker_id FROM attempts WHERE event_id=$1 ORDER BY number`, id)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var attempt Attempt
		if err = rows.Scan(&attempt.ID, &attempt.Number, &attempt.Cycle, &attempt.Status, &attempt.StartedAt, &attempt.FinishedAt, &attempt.HTTPStatus, &attempt.DurationMS, &attempt.ResponsePreview, &attempt.ErrorCategory, &attempt.WorkerID); err != nil {
			rows.Close()
			return result, err
		}
		result.Attempts = append(result.Attempts, attempt)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	var receipt Receipt
	err = tx.QueryRow(ctx, `SELECT event_id::text,destination_id,first_received_at,last_received_at,receipt_count,effect_count,body_sha256 FROM receipts WHERE event_id=$1 AND destination_id=$2`, id, result.Delivery.DestinationID).Scan(&receipt.EventID, &receipt.DestinationID, &receipt.FirstReceivedAt, &receipt.LastReceivedAt, &receipt.ReceiptCount, &receipt.EffectCount, &receipt.BodySHA256)
	if err == nil {
		result.Receipt = &receipt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	result.Audit, err = auditItems(ctx, tx, `WHERE event_id=$1 ORDER BY created_at DESC,id DESC`, id)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

type queryRows interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func auditItems(ctx context.Context, source queryRows, suffix string, args ...any) ([]AuditItem, error) {
	rows, err := source.Query(ctx, `SELECT id::text,created_at,actor,action,event_id::text,note FROM audit `+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuditItem{}
	for rows.Next() {
		var item AuditItem
		if err = rows.Scan(&item.ID, &item.CreatedAt, &item.Actor, &item.Action, &item.EventID, &item.Note); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Audit(ctx context.Context, page int) (Page[AuditItem], error) {
	result := Page[AuditItem]{Items: []AuditItem{}, Page: page, PageSize: 25}
	if page < 0 || page > 100000 {
		return result, invalid("Page is out of range.")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit`).Scan(&result.Total); err != nil {
		return result, err
	}
	result.Items, err = auditItems(ctx, tx, `ORDER BY created_at DESC,id DESC LIMIT 25 OFFSET $1`, page*25)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) SetQueue(ctx context.Context, actor User, expected int64, paused bool) (QueueControl, error) {
	if expected < 0 {
		return QueueControl{}, invalid("expectedVersion must be a nonnegative integer.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return QueueControl{}, err
	}
	defer tx.Rollback(ctx)
	var control QueueControl
	if err = tx.QueryRow(ctx, `SELECT paused,version FROM queue_control WHERE id=1 FOR UPDATE`).Scan(&control.Paused, &control.Version); err != nil {
		return control, err
	}
	if control.Version != expected {
		return control, conflict("Queue settings changed. Refresh before submitting again.")
	}
	if control.Paused == paused {
		return control, conflict("The queue is already in that state.")
	}
	if err = tx.QueryRow(ctx, `UPDATE queue_control SET paused=$1,version=version+1 WHERE id=1 RETURNING paused,version`, paused).Scan(&control.Paused, &control.Version); err != nil {
		return control, err
	}
	action, note := "QUEUE_RESUMED", "Resumed new delivery claims."
	if paused {
		action, note = "QUEUE_PAUSED", "Paused new delivery claims; in-flight requests may still finish."
	}
	if err = audit(ctx, tx, actor.Email, action, nil, note); err != nil {
		return control, err
	}
	return control, tx.Commit(ctx)
}

func (s *Store) SetDestination(ctx context.Context, actor User, id string, expected int64, mode string) (Destination, error) {
	var destination Destination
	if expected < 0 || !oneOf(mode, "HEALTHY", "FAIL", "TIMEOUT") {
		return destination, invalid("Choose a valid receiver mode and nonnegative version.")
	}
	if !oneOf(id, "warehouse", "billing") {
		return destination, missing()
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return destination, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT id,name,route,mode,version FROM destinations WHERE id=$1 FOR UPDATE`, id).Scan(&destination.ID, &destination.Name, &destination.Route, &destination.Mode, &destination.Version)
	if err != nil {
		return destination, err
	}
	if destination.Version != expected {
		return destination, conflict("Receiver settings changed. Refresh before submitting again.")
	}
	if destination.Mode == mode {
		return destination, conflict("The receiver is already in that mode.")
	}
	if _, err = tx.Exec(ctx, `UPDATE destinations SET mode=$2,version=version+1 WHERE id=$1`, id, mode); err != nil {
		return destination, err
	}
	destination.Mode = mode
	destination.Version++
	if err = audit(ctx, tx, actor.Email, "RECEIVER_MODE_CHANGED", nil, "Set "+id+" demo receiver to "+mode+"."); err != nil {
		return destination, err
	}
	return destination, tx.Commit(ctx)
}

func (s *Store) Redrive(ctx context.Context, actor User, id string, expected int64, note string) (Delivery, error) {
	note = strings.TrimSpace(note)
	length := utf8.RuneCountInString(note)
	if !ValidID(id) || expected < 0 || length < 10 || length > 500 || strings.ContainsRune(note, 0) {
		return Delivery{}, invalid("Redrive requires an event UUID, a nonnegative version and a note of 10 to 500 characters.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer tx.Rollback(ctx)
	var state string
	var version int64
	var redrives int
	err = tx.QueryRow(ctx, `SELECT state,version,redrives FROM deliveries WHERE event_id=$1 FOR UPDATE`, id).Scan(&state, &version, &redrives)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, missing()
	}
	if err != nil {
		return Delivery{}, err
	}
	if version != expected {
		return Delivery{}, conflict("This delivery changed. Refresh before redriving.")
	}
	if state != "DEAD_LETTER" {
		return Delivery{}, conflict("Only a dead-letter delivery can be redriven.")
	}
	if redrives >= 2 {
		return Delivery{}, conflict("This event has used both redrives. Its evidence remains available.")
	}
	_, err = tx.Exec(ctx, `UPDATE deliveries SET state='QUEUED',version=version+1,cycle=cycle+1,cycle_attempts=0,redrives=redrives+1,next_attempt_at=now(),updated_at=now() WHERE event_id=$1`, id)
	if err != nil {
		return Delivery{}, err
	}
	if err = audit(ctx, tx, actor.Email, "EVENT_REDRIVEN", &id, note); err != nil {
		return Delivery{}, err
	}
	delivery, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+`WHERE e.id=$1`, id))
	if err != nil {
		return Delivery{}, err
	}
	return delivery, tx.Commit(ctx)
}
