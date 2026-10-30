package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func integrationStore(t *testing.T) (*Store, User, string) {
	t.Helper()
	connection := os.Getenv("WEBHOOK_TEST_DB_URL")
	if connection == "" {
		t.Skip("WEBHOOK_TEST_DB_URL is required for isolated PostgreSQL integration tests")
	}
	parsed, err := url.Parse(connection)
	if err != nil || !strings.Contains(parsed.Path, "webhook") || !strings.Contains(parsed.Path, "test") || connection == os.Getenv("WEBHOOK_DB_URL") {
		t.Fatal("integration tests require a distinct webhook test database")
	}
	ctx := context.Background()
	store, err := Open(ctx, connection)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	t.Cleanup(store.Pool.Close)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool.Exec(ctx, `TRUNCATE audit,attempts,idempotency_keys,deliveries,events,receiver_effects,receipts,sessions,login_throttle,workers,users CASCADE; UPDATE queue_control SET paused=false,version=0; UPDATE destinations SET mode='HEALTHY',version=0;`)
	if err != nil {
		t.Fatal(err)
	}
	password := NewID()
	if err = store.Seed(ctx, password); err != nil {
		t.Fatal(err)
	}
	var user User
	if err = store.Pool.QueryRow(ctx, `SELECT id::text,email,name FROM users`).Scan(&user.ID, &user.Email, &user.Name); err != nil {
		t.Fatal(err)
	}
	return store, user, password
}

func createTestEvent(t *testing.T, store *Store, user User) Delivery {
	t.Helper()
	delivery, _, err := store.Create(context.Background(), user, NewID(), CreateInput{DestinationID: "warehouse", EventType: "order.created", Payload: `{"orderId":"SYNTHETIC-1","quantity":1}`})
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var failure *APIError
	if !errors.As(err, &failure) || failure.Status != status {
		t.Fatalf("wanted API status %d, got %v", status, err)
	}
}

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func dueNow(t *testing.T, store *Store, id string) {
	t.Helper()
	if _, err := store.Pool.Exec(context.Background(), `UPDATE deliveries SET next_attempt_at=now()-interval '1 second' WHERE event_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresAtomicCreationConcurrentIdempotencyAndCapacity(t *testing.T) {
	store, user, _ := integrationStore(t)
	ctx := context.Background()
	key := NewID()
	input := CreateInput{DestinationID: "warehouse", EventType: "order.created", Payload: ` { "large":9007199254740993,"amount":1.2300,"power":1e+09 } `}
	type result struct {
		delivery Delivery
		replay   bool
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			delivery, replay, err := store.Create(ctx, user, key, input)
			results <- result{delivery, replay, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.delivery.ID != b.delivery.ID || a.replay == b.replay {
		t.Fatalf("concurrent idempotency failed: %v / %v", a.err, b.err)
	}
	if a.delivery.Payload != `{"large":9007199254740993,"amount":1.2300,"power":1e+09}` {
		t.Fatal("source numeric representations changed")
	}
	if countRows(t, store, "events") != 1 || countRows(t, store, "deliveries") != 1 || countRows(t, store, "audit") != 1 {
		t.Fatal("outbox creation was duplicated")
	}
	changed := input
	changed.Payload = `{"large":1}`
	_, _, err := store.Create(ctx, user, key, changed)
	requireStatus(t, err, 409)
	changed.Payload = `[]`
	_, _, err = store.Create(ctx, user, NewID(), changed)
	requireStatus(t, err, 400)
	badActor := user
	badActor.ID = NewID()
	_, _, err = store.Create(ctx, badActor, NewID(), input)
	if err == nil || countRows(t, store, "events") != 1 || countRows(t, store, "deliveries") != 1 {
		t.Fatal("failed creation left a partial outbox")
	}
	_, err = store.Pool.Exec(ctx, `CREATE FUNCTION test_reject_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced test delivery failure'; END; $$; CREATE TRIGGER test_reject_delivery BEFORE INSERT ON deliveries FOR EACH ROW EXECUTE FUNCTION test_reject_delivery()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.Pool.Exec(ctx, `DROP FUNCTION IF EXISTS test_reject_delivery() CASCADE`)
	})
	_, _, err = store.Create(ctx, user, NewID(), input)
	if err == nil || countRows(t, store, "events") != 1 || countRows(t, store, "deliveries") != 1 || countRows(t, store, "idempotency_keys") != 1 || countRows(t, store, "audit") != 1 {
		t.Fatal("failure after the event insert left partial outbox data")
	}
	if _, err = store.Pool.Exec(ctx, `DROP FUNCTION test_reject_delivery() CASCADE`); err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool.Exec(ctx, `INSERT INTO events(id,actor_id,destination_id,event_type,payload,envelope) SELECT md5('capacity-'||g)::uuid,$1,'warehouse','order.created','{}','{}'::bytea FROM generate_series(1,499) g`, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool.Exec(ctx, `INSERT INTO deliveries(event_id,state,next_attempt_at) SELECT id,'QUEUED',now() FROM events WHERE id NOT IN(SELECT event_id FROM deliveries)`); err != nil {
		t.Fatal(err)
	}
	_, replay, err := store.Create(ctx, user, key, input)
	if err != nil || !replay {
		t.Fatal("committed replay failed at capacity")
	}
	_, _, err = store.Create(ctx, user, NewID(), input)
	requireStatus(t, err, 409)
	if countRows(t, store, "events") != 500 {
		t.Fatal("workspace cap was exceeded")
	}
}

func TestPostgresClaimsPauseRecoveryAndStaleFencing(t *testing.T) {
	store, user, _ := integrationStore(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		createTestEvent(t, store, user)
	}
	claims := make(chan *Claim, 4)
	errorsFound := make(chan error, 4)
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			claim, err := store.Claim(ctx, fmt.Sprintf("worker-%d", index))
			claims <- claim
			errorsFound <- err
		}(i)
	}
	group.Wait()
	close(claims)
	for i := 0; i < 4; i++ {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
	}
	unique := map[string]*Claim{}
	for claim := range claims {
		if claim == nil {
			t.Fatal("ready event was not claimed")
		}
		unique[claim.EventID] = claim
	}
	if len(unique) != 4 || countRows(t, store, "attempts") != 4 {
		t.Fatal("workers claimed the same event concurrently")
	}
	control, err := store.SetQueue(ctx, user, 0, true)
	if err != nil || !control.Paused {
		t.Fatal(err)
	}
	queued := createTestEvent(t, store, user)
	if claim, err := store.Claim(ctx, "paused-worker"); err != nil || claim != nil {
		t.Fatal("paused queue allowed a new claim")
	}
	var active []*Claim
	for _, claim := range unique {
		active = append(active, claim)
	}
	ok, err := store.Complete(ctx, "existing-worker", active[0], Outcome{Status: "SUCCEEDED", HTTPStatus: intPointer(200), DurationMS: 4})
	if err != nil || !ok {
		t.Fatal("pause blocked completion of an existing request")
	}
	expired := active[1]
	if _, err = store.Pool.Exec(ctx, `UPDATE deliveries SET lease_expires_at=now()-interval '1 second' WHERE event_id=$1`, expired.EventID); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.Recover(ctx, "recovery-worker"); err != nil || recovered != 1 {
		t.Fatal("expired lease did not recover while paused")
	}
	if ok, err = store.Complete(ctx, "stale-worker", expired, Outcome{Status: "SUCCEEDED", DurationMS: 1}); err != nil || ok {
		t.Fatal("expired worker completion was not fenced")
	}
	detail, err := store.Detail(ctx, expired.EventID)
	if err != nil || detail.Attempts[0].Status != "ABANDONED" || detail.Delivery.State != "RETRY_WAIT" {
		t.Fatal("recovery did not preserve unknown outcome")
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE deliveries SET next_attempt_at=now()+interval '1 hour' WHERE event_id=$1`, queued.ID); err != nil {
		t.Fatal(err)
	}
	dueNow(t, store, expired.EventID)
	if _, err = store.SetQueue(ctx, user, control.Version, false); err != nil {
		t.Fatal(err)
	}
	newClaim, err := store.Claim(ctx, "new-worker")
	if err != nil || newClaim == nil || newClaim.EventID != expired.EventID || newClaim.LeaseToken == expired.LeaseToken {
		t.Fatal("recovered event did not receive a new lease")
	}
	if ok, err = store.Complete(ctx, "stale-worker", expired, Outcome{Status: "SUCCEEDED", DurationMS: 1}); err != nil || ok {
		t.Fatal("stale result overwrote a newer claim")
	}
	if ok, err = store.Complete(ctx, "new-worker", newClaim, Outcome{Status: "SUCCEEDED", DurationMS: 1}); err != nil || !ok {
		t.Fatal("current lease could not finish")
	}
	locker, err := store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	blockedClaim := active[2]
	if _, err = locker.Exec(ctx, `UPDATE deliveries SET lease_expires_at=clock_timestamp()+interval '1 hour' WHERE event_id=$1`, blockedClaim.EventID); err != nil {
		t.Fatal(err)
	}
	type completion struct {
		ok  bool
		err error
	}
	completionResult := make(chan completion, 1)
	go func() {
		ok, err := store.Complete(ctx, "blocked-worker", blockedClaim, Outcome{Status: "SUCCEEDED", DurationMS: 1})
		completionResult <- completion{ok, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		err = store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT version,lease_token::text,state%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not reach the held row lock")
		}
	}
	if _, err = locker.Exec(ctx, `UPDATE deliveries SET lease_expires_at=clock_timestamp() WHERE event_id=$1`, blockedClaim.EventID); err != nil {
		t.Fatal(err)
	}
	if err = locker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	completed := <-completionResult
	if completed.err != nil || completed.ok {
		t.Fatal("completion used transaction-start time after waiting beyond lease expiry")
	}
	if err = store.Heartbeat(ctx, "worker-A"); err != nil {
		t.Fatal(err)
	}
	if err = store.Heartbeat(ctx, "worker-B"); err != nil {
		t.Fatal(err)
	}
	dashboard, err := store.Dashboard(ctx)
	if err != nil || dashboard.Workers.ActiveCount != 2 || dashboard.Workers.LastSeenAt == nil {
		t.Fatal("heartbeat status is not derived from stored workers")
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE workers SET last_seen_at=now()-interval '16 seconds'`); err != nil {
		t.Fatal(err)
	}
	dashboard, err = store.Dashboard(ctx)
	if err != nil || dashboard.Workers.ActiveCount != 0 {
		t.Fatal("stale heartbeats counted as active")
	}
}

func TestPostgresRetryBudgetRedriveRacesAndImmutableHistory(t *testing.T) {
	store, user, _ := integrationStore(t)
	ctx := context.Background()
	delivery := createTestEvent(t, store, user)
	var original []byte
	if err := store.Pool.QueryRow(ctx, `SELECT envelope FROM events WHERE id=$1`, delivery.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for cycle := 1; cycle <= 3; cycle++ {
		for attempt := 1; attempt <= 3; attempt++ {
			dueNow(t, store, delivery.ID)
			claim, err := store.Claim(ctx, "retry-worker")
			if err != nil || claim == nil {
				t.Fatal("retry was not claimed")
			}
			ok, err := store.Complete(ctx, "retry-worker", claim, Outcome{Status: "HTTP_ERROR", HTTPStatus: intPointer(503), DurationMS: 7, Preview: stringPointer("synthetic failure"), ErrorCategory: stringPointer("HTTP_STATUS")})
			if err != nil || !ok {
				t.Fatal(err)
			}
			detail, err := store.Detail(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			delivery = detail.Delivery
			if attempt < 3 {
				var delay float64
				if err = store.Pool.QueryRow(ctx, `SELECT extract(epoch FROM(next_attempt_at-updated_at))::float8 FROM deliveries WHERE event_id=$1`, delivery.ID).Scan(&delay); err != nil {
					t.Fatal(err)
				}
				want := 2.0
				if attempt == 2 {
					want = 5
				}
				if delay != want || delivery.State != "RETRY_WAIT" {
					t.Fatal("retry schedule differs from 2s then 5s")
				}
			} else if delivery.State != "DEAD_LETTER" {
				t.Fatal("exhausted cycle did not dead-letter")
			}
		}
		if cycle < 3 {
			if cycle == 1 {
				start := make(chan struct{})
				results := make(chan error, 2)
				for i := 0; i < 2; i++ {
					go func(expected int64) {
						<-start
						_, err := store.Redrive(ctx, user, delivery.ID, expected, "Investigated synthetic failure; retry this event.")
						results <- err
					}(delivery.Version)
				}
				close(start)
				a, b := <-results, <-results
				if (a == nil) == (b == nil) {
					t.Fatal("concurrent redrive did not have exactly one winner")
				}
				if a != nil {
					requireStatus(t, a, 409)
				} else {
					requireStatus(t, b, 409)
				}
			} else {
				if _, err := store.Redrive(ctx, user, delivery.ID, delivery.Version, "Investigated synthetic failure; final allowed redrive."); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := store.Redrive(ctx, user, delivery.ID, delivery.Version, "A third redrive must remain prohibited."); err == nil {
		t.Fatal("redrive budget exceeded")
	} else {
		requireStatus(t, err, 409)
	}
	detail, err := store.Detail(ctx, delivery.ID)
	if err != nil || len(detail.Attempts) != 9 || detail.Delivery.Redrives != 2 || detail.Delivery.Cycle != 3 {
		t.Fatal("attempt history or redrive budget changed")
	}
	for index, attempt := range detail.Attempts {
		if attempt.Number != index+1 || attempt.Status != "HTTP_ERROR" {
			t.Fatal("prior attempts were overwritten")
		}
	}
	var current []byte
	if err = store.Pool.QueryRow(ctx, `SELECT envelope FROM events WHERE id=$1`, delivery.ID).Scan(&current); err != nil || !bytes.Equal(current, original) {
		t.Fatal("source envelope changed during retries")
	}
	var redriveAudits int
	if err = store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit WHERE action='EVENT_REDRIVEN'`).Scan(&redriveAudits); err != nil || redriveAudits != 2 {
		t.Fatal("redrive audit was duplicated")
	}
}

func intPointer(value int) *int { return &value }

func signedRequest(t *testing.T, client *http.Client, address, destination, id string, body, secret []byte) *http.Response {
	t.Helper()
	response, err := sendSigned(client, address, destination, id, body, secret)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func sendSigned(client *http.Client, address, destination, id string, body, secret []byte) (*http.Response, error) {
	timestamp := fmt.Sprint(time.Now().Unix())
	request, err := http.NewRequest(http.MethodPost, address+"/hooks/"+destination, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Webhook-Id", id)
	request.Header.Set("Webhook-Timestamp", timestamp)
	request.Header.Set("Webhook-Signature", Signature(secret, destination, id, timestamp, body))
	return client.Do(request)
}

func TestPostgresReceiverConcurrentDedupeAndSignatureValidation(t *testing.T) {
	store, user, _ := integrationStore(t)
	ctx := context.Background()
	secret := []byte("test-only-signing-material-not-a-runtime-default")
	delivery := createTestEvent(t, store, user)
	var body []byte
	if err := store.Pool.QueryRow(ctx, `SELECT envelope FROM events WHERE id=$1`, delivery.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	receiver := NewReceiver(store, secret)
	server := httptest.NewServer(receiver)
	defer server.Close()
	var group sync.WaitGroup
	statuses := make(chan int, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			response, err := sendSigned(server.Client(), server.URL, "warehouse", delivery.ID, body, secret)
			if err != nil {
				t.Error("concurrent receiver request failed")
				statuses <- 0
				return
			}
			statuses <- response.StatusCode
			response.Body.Close()
		}()
	}
	group.Wait()
	for i := 0; i < 12; i++ {
		if <-statuses != 200 {
			t.Fatal("concurrent duplicate was not acknowledged")
		}
	}
	detail, err := store.Detail(ctx, delivery.ID)
	if err != nil || detail.Receipt == nil || detail.Receipt.ReceiptCount != 12 || detail.Receipt.EffectCount != 1 || countRows(t, store, "receiver_effects") != 1 {
		t.Fatal("receiver applied duplicate effects")
	}
	changed := bytes.Replace(body, []byte(`"quantity":1`), []byte(`"quantity":2`), 1)
	response := signedRequest(t, server.Client(), server.URL, "warehouse", delivery.ID, changed, secret)
	if response.StatusCode != 409 {
		t.Fatal("same event ID accepted different body")
	}
	response.Body.Close()
	request := httptest.NewRequest(http.MethodPost, "/hooks/warehouse", bytes.NewReader(body))
	request.Header.Set("Webhook-Id", delivery.ID)
	request.Header.Set("Webhook-Timestamp", fmt.Sprint(time.Now().Add(-301*time.Second).Unix()))
	request.Header.Set("Webhook-Signature", Signature(secret, "warehouse", delivery.ID, request.Header.Get("Webhook-Timestamp"), body))
	output := httptest.NewRecorder()
	receiver.ServeHTTP(output, request)
	if output.Code != 401 {
		t.Fatal("stale signature was accepted")
	}
	request = httptest.NewRequest(http.MethodPost, "/hooks/warehouse", bytes.NewReader(body))
	request.Header.Set("Webhook-Id", delivery.ID)
	request.Header.Set("Webhook-Timestamp", fmt.Sprint(time.Now().Unix()))
	request.Header.Set("Webhook-Signature", Signature(secret, "billing", delivery.ID, request.Header.Get("Webhook-Timestamp"), body))
	output = httptest.NewRecorder()
	receiver.ServeHTTP(output, request)
	if output.Code != 401 {
		t.Fatal("signature for another destination was accepted")
	}
	if _, err = store.SetDestination(ctx, user, "warehouse", 0, "FAIL"); err != nil {
		t.Fatal(err)
	}
	response = signedRequest(t, server.Client(), server.URL, "warehouse", delivery.ID, changed, secret)
	if response.StatusCode != 409 {
		t.Fatal("FAIL mode concealed a conflicting immutable event body")
	}
	response.Body.Close()
	failed := createTestEvent(t, store, user)
	if err = store.Pool.QueryRow(ctx, `SELECT envelope FROM events WHERE id=$1`, failed.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	response = signedRequest(t, server.Client(), server.URL, "warehouse", failed.ID, body, secret)
	if response.StatusCode != 503 {
		t.Fatal("FAIL mode did not fail")
	}
	response.Body.Close()
	if countRows(t, store, "receipts") != 1 || countRows(t, store, "receiver_effects") != 1 {
		t.Fatal("FAIL mode applied an effect")
	}
}

func TestPostgresTimeoutUnknownOutcomeThenRedriveDedupesEffect(t *testing.T) {
	store, user, _ := integrationStore(t)
	ctx := context.Background()
	secret := []byte("test-only-signing-material-not-a-runtime-default")
	delivery := createTestEvent(t, store, user)
	mode, err := store.SetDestination(ctx, user, "warehouse", 0, "TIMEOUT")
	if err != nil {
		t.Fatal(err)
	}
	receiver := NewReceiver(store, secret)
	var waits int
	receiver.Wait = func(_ context.Context, duration time.Duration) error {
		if duration != 3500*time.Millisecond {
			t.Fatal("receiver timeout differs from contract")
		}
		waits++
		if countRows(t, store, "receiver_effects") != 1 {
			t.Fatal("receiver waited before committing its effect")
		}
		return context.DeadlineExceeded
	}
	worker, _ := NewWorker(store, "timeout-worker", "http://receiver:8082", secret)
	worker.Client = doerFunc(func(request *http.Request) (*http.Response, error) {
		receiver.ServeHTTP(httptest.NewRecorder(), request)
		return nil, timeoutError{}
	})
	for i := 0; i < 3; i++ {
		dueNow(t, store, delivery.ID)
		claim, err := store.Claim(ctx, worker.ID)
		if err != nil || claim == nil {
			t.Fatal("timeout event was not claimed")
		}
		outcome := worker.Send(ctx, claim)
		if outcome.Status != "TIMEOUT" {
			t.Fatal("sender invented a known remote outcome")
		}
		if ok, err := store.Complete(ctx, worker.ID, claim, outcome); err != nil || !ok {
			t.Fatal(err)
		}
	}
	detail, err := store.Detail(ctx, delivery.ID)
	if err != nil || detail.Delivery.State != "DEAD_LETTER" || detail.Receipt == nil || detail.Receipt.ReceiptCount != 3 || waits != 3 {
		t.Fatal("timeout evidence was not preserved")
	}
	if _, err = store.SetDestination(ctx, user, "warehouse", mode.Version, "HEALTHY"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Redrive(ctx, user, delivery.ID, detail.Delivery.Version, "Receiver recovered; inspect duplicate-safe processing."); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(receiver)
	defer server.Close()
	worker, _ = NewWorker(store, "healthy-worker", server.URL, secret)
	claim, err := store.Claim(ctx, worker.ID)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	outcome := worker.Send(ctx, claim)
	if outcome.Status != "SUCCEEDED" {
		t.Fatal("healthy duplicate did not succeed")
	}
	if ok, err := store.Complete(ctx, worker.ID, claim, outcome); err != nil || !ok {
		t.Fatal(err)
	}
	detail, err = store.Detail(ctx, delivery.ID)
	if err != nil || detail.Delivery.State != "DELIVERED" || detail.Receipt.ReceiptCount != 4 || countRows(t, store, "receiver_effects") != 1 {
		t.Fatal("redrive applied a second effect")
	}
}

func TestPostgresAPIAuthenticationStrictInputsAndVersionedControls(t *testing.T) {
	store, _, password := integrationStore(t)
	api := &API{Store: store, Auth: NewAuth(store), Origin: "http://127.0.0.1:8097"}
	call := func(method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if csrf != "" {
			request.Header.Set("X-CSRF-Token", csrf)
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		output := httptest.NewRecorder()
		api.ServeHTTP(output, request)
		return output
	}
	for _, path := range []string{"/api/events", "/api;/events", "/api%3b/events", "/api/../api/events", "/api/auth/session", "/api/health/"} {
		if response := call("GET", path, "", nil, "", ""); response.Code != 401 {
			t.Fatalf("anonymous route bypassed auth: %s", path)
		}
	}
	if call("GET", "/api/health", "", nil, "", "").Code != 200 {
		t.Fatal("anonymous readiness failed")
	}
	loginBody := fmt.Sprintf(`{"email":"operator@webhooklab.test","password":%q}`, password)
	if call("POST", "/api/auth/login", loginBody, nil, "", "https://invalid.test").Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
	login := call("POST", "/api/auth/login", loginBody, nil, "", api.Origin)
	if login.Code != 200 {
		t.Fatal("valid login failed")
	}
	var session Session
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" || cookies[0].MaxAge != 43200 {
		t.Fatal("session cookie flags differ from contract")
	}
	cookie := cookies[0]
	var stored string
	if err := store.Pool.QueryRow(context.Background(), `SELECT digest FROM sessions`).Scan(&stored); err != nil || stored != digest([]byte(cookie.Value)) || stored == cookie.Value {
		t.Fatal("raw session token persisted")
	}
	if call("PUT", "/api/queue", `{"expectedVersion":0,"paused":true}`, cookie, "", api.Origin).Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if call("PUT", "/api/queue", `{"expectedVersion":0,"paused":true}`, cookie, session.CSRFToken, "https://invalid.test").Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	for _, body := range []string{`{"paused":true}`, `{"expectedVersion":0.5,"paused":true}`, `{"expectedVersion":"0","paused":true}`, `{"expectedVersion":0,"paused":true,"extra":1}`, `{"expectedVersion":0,"paused":true} {}`} {
		if call("PUT", "/api/queue", body, cookie, session.CSRFToken, api.Origin).Code != 400 {
			t.Fatal("invalid version or outer JSON accepted")
		}
	}
	if call("PUT", "/api/queue", `{"expectedVersion":0,"paused":true}`, cookie, session.CSRFToken, api.Origin).Code != 200 {
		t.Fatal("valid queue pause failed")
	}
	if call("PUT", "/api/queue", `{"expectedVersion":0,"paused":false}`, cookie, session.CSRFToken, api.Origin).Code != 409 {
		t.Fatal("stale queue settings overwrote current state")
	}
	if call("POST", "/api/auth/logout", "", cookie, session.CSRFToken, api.Origin).Code != 204 {
		t.Fatal("logout failed")
	}
	if call("GET", "/api/events", "", cookie, "", "").Code != 401 {
		t.Fatal("revoked session remained valid")
	}
	for i := 0; i < 5; i++ {
		if call("POST", "/api/auth/login", `{"email":"missing@webhooklab.test","password":"wrong-password"}`, nil, "", api.Origin).Code != 401 {
			t.Fatal("unexpected failed-login status")
		}
	}
	if call("POST", "/api/auth/login", loginBody, nil, "", api.Origin).Code != 429 {
		t.Fatal("persistent login throttle not enforced")
	}
}

func TestPostgresRecoveryAtExhaustedBudgetAndLiteralSearch(t *testing.T) {
	store, user, _ := integrationStore(t)
	ctx := context.Background()
	delivery := createTestEvent(t, store, user)
	for attempt := 1; attempt <= 3; attempt++ {
		dueNow(t, store, delivery.ID)
		claim, err := store.Claim(ctx, "crashing-worker")
		if err != nil || claim == nil {
			t.Fatal(err)
		}
		if _, err = store.Pool.Exec(ctx, `UPDATE deliveries SET lease_expires_at=now()-interval '1 second' WHERE event_id=$1`, delivery.ID); err != nil {
			t.Fatal(err)
		}
		if recovered, err := store.Recover(ctx, "recovery-worker"); err != nil || recovered != 1 {
			t.Fatal(err)
		}
	}
	detail, err := store.Detail(ctx, delivery.ID)
	if err != nil || detail.Delivery.State != "DEAD_LETTER" || detail.Delivery.NextAttemptAt != nil {
		t.Fatal("expired third attempt did not dead-letter")
	}
	for _, attempt := range detail.Attempts {
		if attempt.Status != "ABANDONED" || attempt.ErrorCategory == nil || *attempt.ErrorCategory != "OUTCOME_UNKNOWN" {
			t.Fatal("abandoned attempt pretended a known remote result")
		}
	}
	page, err := store.Events(ctx, 0, "ALL", "%' OR 1=1 --")
	if err != nil || page.Total != 0 {
		t.Fatal("search was interpreted as SQL or wildcard")
	}
	page, err = store.Events(ctx, 0, "ALL", "order.created")
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("literal event-type search failed")
	}
	var exists bool
	if err = store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM receipts WHERE event_id=$1)`, delivery.ID).Scan(&exists); err != nil || exists {
		t.Fatal("sender recovery invented receiver evidence")
	}
}
