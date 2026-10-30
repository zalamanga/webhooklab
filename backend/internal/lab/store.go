package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed schema.sql
var schema string

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, connection string) (*Store, error) {
	config, err := pgxpool.ParseConfig(connection)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	config.MaxConns = 6
	config.MinConns = 1
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.AfterConnect = func(_ context.Context, connection *pgx.Conn) error {
		connection.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("database initialization failed")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database unavailable")
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72819402)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY)`); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=1)`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err = tx.Exec(ctx, schema); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations VALUES(1)`); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Seed(ctx context.Context, password string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM workspace_guard WHERE id=1 FOR UPDATE`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if len(password) < 16 || len(password) > 72 {
			return errors.New("WEBHOOK_DEMO_PASSWORD must contain 16 to 72 UTF-8 bytes")
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), 12)
		if hashErr != nil {
			return hashErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO users(id,email,name,password_hash) VALUES($1,'operator@webhooklab.test','Demo Operator',$2)`, NewID(), string(hash))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func audit(ctx context.Context, tx pgx.Tx, actor, action string, eventID *string, note string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit(id,actor,action,event_id,note,created_at) VALUES($1,$2,$3,$4,$5,statement_timestamp())`, NewID(), actor, action, eventID, note)
	return err
}

func compactPayload(payload string) (string, error) {
	if len(payload) == 0 || len(payload) > 16*1024 || !utf8.ValidString(payload) {
		return "", invalid("Payload must be a UTF-8 JSON object of at most 16 KiB.")
	}
	trimmed := bytes.TrimSpace([]byte(payload))
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return "", invalid("Payload must contain one valid JSON object.")
	}
	var output bytes.Buffer
	if err := json.Compact(&output, trimmed); err != nil {
		return "", invalid("Payload must contain one valid JSON object.")
	}
	return output.String(), nil
}

func fingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		hash.Write(size[:])
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

const deliverySelect = `SELECT e.id::text,e.event_type,e.destination_id,t.name,e.payload,d.state,d.version,d.cycle,d.cycle_attempts,d.total_attempts,d.redrives,d.next_attempt_at,e.created_at,d.updated_at FROM events e JOIN deliveries d ON d.event_id=e.id JOIN destinations t ON t.id=e.destination_id `

type rowScanner interface{ Scan(...any) error }

func scanDelivery(row rowScanner) (Delivery, error) {
	var d Delivery
	err := row.Scan(&d.ID, &d.EventType, &d.DestinationID, &d.DestinationName, &d.Payload, &d.State, &d.Version, &d.Cycle, &d.CycleAttempts, &d.TotalAttempts, &d.Redrives, &d.NextAttemptAt, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (s *Store) Create(ctx context.Context, actor User, key string, input CreateInput) (Delivery, bool, error) {
	if !ValidID(key) {
		return Delivery{}, false, invalid("Idempotency-Key must be a canonical UUID.")
	}
	if input.DestinationID != "warehouse" && input.DestinationID != "billing" {
		return Delivery{}, false, invalid("Choose a demo destination.")
	}
	if input.EventType != "order.created" && input.EventType != "order.cancelled" {
		return Delivery{}, false, invalid("Choose a supported event type.")
	}
	payload, err := compactPayload(input.Payload)
	if err != nil {
		return Delivery{}, false, err
	}
	hash := fingerprint(input.DestinationID, input.EventType, payload)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Delivery{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM workspace_guard WHERE id=1 FOR UPDATE`); err != nil {
		return Delivery{}, false, err
	}
	var previousHash, previousID string
	err = tx.QueryRow(ctx, `SELECT fingerprint,event_id::text FROM idempotency_keys WHERE actor_id=$1 AND key=$2`, actor.ID, strings.ToLower(key)).Scan(&previousHash, &previousID)
	if err == nil {
		if previousHash != hash {
			return Delivery{}, false, conflict("This idempotency key belongs to different event content.")
		}
		delivery, readErr := scanDelivery(tx.QueryRow(ctx, deliverySelect+`WHERE e.id=$1`, previousID))
		return delivery, true, readErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, false, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&count); err != nil {
		return Delivery{}, false, err
	}
	if count >= 500 {
		return Delivery{}, false, conflict("The workspace has reached its 500-event limit. Existing events can still be inspected and redriven.")
	}
	id := NewID()
	var created time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&created); err != nil {
		return Delivery{}, false, err
	}
	envelope := []byte(fmt.Sprintf(`{"id":%q,"eventType":%q,"createdAt":%q,"payload":%s}`, id, input.EventType, created.UTC().Format(time.RFC3339Nano), payload))
	_, err = tx.Exec(ctx, `INSERT INTO events(id,actor_id,destination_id,event_type,payload,envelope,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, actor.ID, input.DestinationID, input.EventType, payload, envelope, created)
	if err != nil {
		return Delivery{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO deliveries(event_id,state,next_attempt_at) VALUES($1,'QUEUED',now())`, id); err != nil {
		return Delivery{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO idempotency_keys(actor_id,key,fingerprint,event_id) VALUES($1,$2,$3,$4)`, actor.ID, strings.ToLower(key), hash, id); err != nil {
		return Delivery{}, false, err
	}
	if err = audit(ctx, tx, actor.Email, "EVENT_CREATED", &id, "Created a synthetic event and queued its delivery."); err != nil {
		return Delivery{}, false, err
	}
	delivery, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+`WHERE e.id=$1`, id))
	if err != nil {
		return Delivery{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Delivery{}, false, err
	}
	return delivery, false, nil
}

func (s *Store) Destinations(ctx context.Context) ([]Destination, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,name,route,mode,version FROM destinations ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Destination{}
	for rows.Next() {
		var d Destination
		if err = rows.Scan(&d.ID, &d.Name, &d.Route, &d.Mode, &d.Version); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) Dashboard(ctx context.Context) (Dashboard, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Dashboard{}, err
	}
	defer tx.Rollback(ctx)
	d := Dashboard{Counts: map[string]int{"QUEUED": 0, "SENDING": 0, "RETRY_WAIT": 0, "DELIVERED": 0, "DEAD_LETTER": 0}}
	rows, err := tx.Query(ctx, `SELECT state,count(*) FROM deliveries GROUP BY state`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var state string
		var count int
		if err = rows.Scan(&state, &count); err != nil {
			rows.Close()
			return d, err
		}
		d.Counts[state] = count
		d.Total += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	if err = tx.QueryRow(ctx, `SELECT paused,version FROM queue_control WHERE id=1`).Scan(&d.Queue.Paused, &d.Queue.Version); err != nil {
		return d, err
	}
	d.Workers.StaleAfterSeconds = 15
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE last_seen_at>now()-interval '15 seconds'),max(last_seen_at),now() FROM workers`).Scan(&d.Workers.ActiveCount, &d.Workers.LastSeenAt, &d.ServerTime); err != nil {
		return d, err
	}
	return d, tx.Commit(ctx)
}

func (s *Store) Events(ctx context.Context, page int, state, search string) (Page[Delivery], error) {
	result := Page[Delivery]{Items: []Delivery{}, Page: page, PageSize: 25}
	if page < 0 || page > 100000 {
		return result, invalid("Page is out of range.")
	}
	if !oneOf(state, "ALL", "QUEUED", "SENDING", "RETRY_WAIT", "DELIVERED", "DEAD_LETTER") {
		return result, invalid("Unknown delivery state.")
	}
	search = strings.TrimSpace(search)
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 64 || strings.ContainsRune(search, 0) {
		return result, invalid("Search must contain at most 64 characters.")
	}
	escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
	where := `WHERE ($1='ALL' OR d.state=$1) AND (e.id::text ILIKE $2 ESCAPE '!' OR e.event_type ILIKE $2 ESCAPE '!') `
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM events e JOIN deliveries d ON d.event_id=e.id `+where, state, "%"+escaped+"%").Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, deliverySelect+where+`ORDER BY e.created_at DESC,e.id DESC LIMIT 25 OFFSET $3`, state, "%"+escaped+"%", page*25)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		d, readErr := scanDelivery(rows)
		if readErr != nil {
			return result, readErr
		}
		result.Items = append(result.Items, d)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	return result, tx.Commit(ctx)
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
