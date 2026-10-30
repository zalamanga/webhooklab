package lab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type Claim struct {
	EventID       string
	DestinationID string
	Route         string
	Envelope      []byte
	LeaseToken    string
	Version       int64
	AttemptNumber int
	CycleAttempts int
	DatabaseTime  time.Time
}

type Outcome struct {
	Status        string
	HTTPStatus    *int
	DurationMS    int64
	Preview       *string
	ErrorCategory *string
}

func (s *Store) Heartbeat(ctx context.Context, workerID string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO workers(id,last_seen_at) VALUES($1,now()) ON CONFLICT(id) DO UPDATE SET last_seen_at=now()`, workerID)
	return err
}

func (s *Store) WorkerHealthy(ctx context.Context, workerID string) error {
	var active bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workers WHERE id=$1 AND last_seen_at>now()-interval '15 seconds')`, workerID).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("worker heartbeat is stale")
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, workerID string) (*Claim, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var paused bool
	if err = tx.QueryRow(ctx, `SELECT paused FROM queue_control WHERE id=1 FOR SHARE`).Scan(&paused); err != nil {
		return nil, err
	}
	if paused {
		return nil, nil
	}
	claim := &Claim{LeaseToken: NewID()}
	err = tx.QueryRow(ctx, `SELECT d.event_id::text,e.destination_id,t.route,e.envelope,d.cycle_attempts,clock_timestamp() FROM deliveries d JOIN events e ON e.id=d.event_id JOIN destinations t ON t.id=e.destination_id WHERE d.state IN ('QUEUED','RETRY_WAIT') AND d.next_attempt_at<=clock_timestamp() ORDER BY d.next_attempt_at,d.event_id FOR UPDATE OF d SKIP LOCKED LIMIT 1`).Scan(&claim.EventID, &claim.DestinationID, &claim.Route, &claim.Envelope, &claim.CycleAttempts, &claim.DatabaseTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cycle int
	err = tx.QueryRow(ctx, `UPDATE deliveries SET state='SENDING',version=version+1,cycle_attempts=cycle_attempts+1,total_attempts=total_attempts+1,next_attempt_at=NULL,lease_token=$2,lease_expires_at=statement_timestamp()+interval '10 seconds',updated_at=statement_timestamp() WHERE event_id=$1 RETURNING version,total_attempts,cycle_attempts,cycle`, claim.EventID, claim.LeaseToken).Scan(&claim.Version, &claim.AttemptNumber, &claim.CycleAttempts, &cycle)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO attempts(id,event_id,number,cycle,status,worker_id,started_at) VALUES($1,$2,$3,$4,'STARTED',$5,statement_timestamp())`, NewID(), claim.EventID, claim.AttemptNumber, cycle, workerID)
	if err != nil {
		return nil, err
	}
	if err = audit(ctx, tx, "worker:"+workerID, "ATTEMPT_STARTED", &claim.EventID, "Started delivery attempt "+strconv.Itoa(claim.AttemptNumber)+"."); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claim, nil
}

func nextState(attempts int, success bool) (string, int) {
	if success {
		return "DELIVERED", 0
	}
	if attempts >= 3 {
		return "DEAD_LETTER", 0
	}
	if attempts == 1 {
		return "RETRY_WAIT", 2
	}
	return "RETRY_WAIT", 5
}

func (s *Store) Complete(ctx context.Context, workerID string, claim *Claim, outcome Outcome) (bool, error) {
	if !oneOf(outcome.Status, "SUCCEEDED", "HTTP_ERROR", "TIMEOUT", "NETWORK_ERROR") || outcome.DurationMS < 0 {
		return false, invalid("Invalid worker outcome.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var version int64
	var token *string
	var state string
	var attempts int
	var active bool
	err = tx.QueryRow(ctx, `SELECT version,lease_token::text,state,cycle_attempts FROM deliveries WHERE event_id=$1 FOR UPDATE`, claim.EventID).Scan(&version, &token, &state, &attempts)
	if err != nil {
		return false, err
	}
	if state != "SENDING" || token == nil || *token != claim.LeaseToken || version != claim.Version {
		return false, nil
	}
	if err = tx.QueryRow(ctx, `SELECT lease_expires_at>clock_timestamp() FROM deliveries WHERE event_id=$1`, claim.EventID).Scan(&active); err != nil {
		return false, err
	}
	if !active {
		return false, nil
	}
	state, delay := nextState(attempts, outcome.Status == "SUCCEEDED")
	_, err = tx.Exec(ctx, `UPDATE attempts SET status=$3,finished_at=statement_timestamp(),http_status=$4,duration_ms=$5,response_preview=$6,error_category=$7 WHERE event_id=$1 AND number=$2 AND status='STARTED'`, claim.EventID, claim.AttemptNumber, outcome.Status, outcome.HTTPStatus, outcome.DurationMS, outcome.Preview, outcome.ErrorCategory)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE deliveries SET state=$2,version=version+1,next_attempt_at=CASE WHEN $2='RETRY_WAIT' THEN statement_timestamp()+($3::integer*interval '1 second') ELSE NULL END,lease_token=NULL,lease_expires_at=NULL,updated_at=statement_timestamp() WHERE event_id=$1`, claim.EventID, state, delay)
	if err != nil {
		return false, err
	}
	if err = audit(ctx, tx, "worker:"+workerID, "ATTEMPT_"+outcome.Status, &claim.EventID, "Attempt "+strconv.Itoa(claim.AttemptNumber)+" ended as "+outcome.Status+"; delivery is "+state+"."); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Recover(ctx context.Context, workerID string) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT event_id::text,cycle_attempts,total_attempts FROM deliveries WHERE state='SENDING' AND lease_expires_at<=clock_timestamp() ORDER BY lease_expires_at,event_id FOR UPDATE SKIP LOCKED LIMIT 50`)
	if err != nil {
		return 0, err
	}
	type expired struct {
		id                   string
		cycleAttempts, total int
	}
	claims := []expired{}
	for rows.Next() {
		var claim expired
		if err = rows.Scan(&claim.id, &claim.cycleAttempts, &claim.total); err != nil {
			rows.Close()
			return 0, err
		}
		claims = append(claims, claim)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, claim := range claims {
		state, delay := nextState(claim.cycleAttempts, false)
		_, err = tx.Exec(ctx, `UPDATE attempts SET status='ABANDONED',finished_at=statement_timestamp(),duration_ms=GREATEST(0,extract(epoch FROM (statement_timestamp()-started_at))*1000)::bigint,error_category='OUTCOME_UNKNOWN' WHERE event_id=$1 AND number=$2 AND status='STARTED'`, claim.id, claim.total)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE deliveries SET state=$2,version=version+1,next_attempt_at=CASE WHEN $2='RETRY_WAIT' THEN statement_timestamp()+($3::integer*interval '1 second') ELSE NULL END,lease_token=NULL,lease_expires_at=NULL,updated_at=statement_timestamp() WHERE event_id=$1`, claim.id, state, delay)
		if err != nil {
			return 0, err
		}
		if err = audit(ctx, tx, "worker:"+workerID, "LEASE_RECOVERED", &claim.id, "The request outcome is unknown after lease expiry; delivery is "+state+"."); err != nil {
			return 0, err
		}
	}
	return len(claims), tx.Commit(ctx)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Worker struct {
	Store   *Store
	ID      string
	BaseURL string
	Secret  []byte
	Client  HTTPDoer
	Now     func() time.Time
}

func NewWorker(store *Store, id, baseURL string, secret []byte) (*Worker, error) {
	base, err := url.Parse(baseURL)
	if err != nil || !oneOf(base.Scheme, "http", "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" && base.Path != "/" {
		return nil, errors.New("invalid trusted receiver configuration")
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Worker{Store: store, ID: id, BaseURL: strings.TrimRight(baseURL, "/"), Secret: secret, Client: client, Now: time.Now}, nil
}

func (worker *Worker) Send(ctx context.Context, claim *Claim) Outcome {
	started := worker.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, worker.BaseURL+claim.Route, bytes.NewReader(claim.Envelope))
	if err != nil {
		return Outcome{Status: "NETWORK_ERROR", ErrorCategory: stringPointer("NETWORK_ERROR")}
	}
	timestamp := strconv.FormatInt(claim.DatabaseTime.Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Webhook-Id", claim.EventID)
	request.Header.Set("Webhook-Timestamp", timestamp)
	request.Header.Set("Webhook-Signature", Signature(worker.Secret, claim.DestinationID, claim.EventID, timestamp, claim.Envelope))
	response, err := worker.Client.Do(request)
	duration := worker.Now().Sub(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	if err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return Outcome{Status: "TIMEOUT", DurationMS: duration, ErrorCategory: stringPointer("TIMEOUT")}
		}
		return Outcome{Status: "NETWORK_ERROR", DurationMS: duration, ErrorCategory: stringPointer("NETWORK_ERROR")}
	}
	defer response.Body.Close()
	preview, readErr := io.ReadAll(io.LimitReader(response.Body, 2048))
	duration = worker.Now().Sub(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	if readErr != nil && (response.StatusCode < 200 || response.StatusCode > 299) {
		var networkError net.Error
		if errors.As(readErr, &networkError) && networkError.Timeout() {
			return Outcome{Status: "TIMEOUT", HTTPStatus: &response.StatusCode, DurationMS: duration, ErrorCategory: stringPointer("TIMEOUT")}
		}
		return Outcome{Status: "NETWORK_ERROR", HTTPStatus: &response.StatusCode, DurationMS: duration, ErrorCategory: stringPointer("NETWORK_ERROR")}
	}
	text := strings.ToValidUTF8(string(preview), "\uFFFD")
	if len(text) > 2048 {
		text = text[:2048]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	outcome := Outcome{Status: "HTTP_ERROR", HTTPStatus: &response.StatusCode, DurationMS: duration, Preview: &text, ErrorCategory: stringPointer("HTTP_STATUS")}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		outcome.Status = "SUCCEEDED"
		outcome.ErrorCategory = nil
	}
	return outcome
}

func stringPointer(value string) *string { return &value }

func (worker *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := worker.Store.Heartbeat(ctx, worker.ID); err != nil {
		return err
	}
	heartbeat := time.NewTicker(3 * time.Second)
	defer heartbeat.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				_ = worker.Store.Heartbeat(ctx, worker.ID)
			}
		}
	}()
	for ctx.Err() == nil {
		if _, err := worker.Store.Recover(ctx, worker.ID); err != nil {
			return err
		}
		claim, err := worker.Store.Claim(ctx, worker.ID)
		if err != nil {
			return err
		}
		if claim != nil {
			outcome := worker.Send(ctx, claim)
			if _, err = worker.Store.Complete(ctx, worker.ID, claim, outcome); err != nil && ctx.Err() == nil {
				return err
			}
		}
		if err = waitContext(ctx, 200*time.Millisecond); err != nil {
			return nil
		}
	}
	return nil
}
