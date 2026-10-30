package lab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPayloadPreservesNumericRepresentations(t *testing.T) {
	input := ` { "large":9007199254740993, "decimal":1.2300, "power":1e+09 } `
	want := `{"large":9007199254740993,"decimal":1.2300,"power":1e+09}`
	got, err := compactPayload(input)
	if err != nil || got != want {
		t.Fatalf("payload changed numeric text: %q (%v)", got, err)
	}
	for _, invalidPayload := range []string{"", "[]", "null", "1", "{} {}", `{"a":}`, "{\"x\":\"" + string([]byte{0xff}) + "\"}", "{\"x\":\"" + strings.Repeat("a", 16*1024) + "\"}"} {
		if _, err = compactPayload(invalidPayload); err == nil {
			t.Errorf("invalid payload accepted: length %d", len(invalidPayload))
		}
	}
	if fingerprint("a", "bc") == fingerprint("ab", "c") {
		t.Fatal("fingerprint has ambiguous boundaries")
	}
}

func TestSignaturesBindDestinationTimestampIDAndExactBody(t *testing.T) {
	secret := []byte("test-only-signing-material-not-a-runtime-default")
	id := "12345678-1234-4234-9234-123456789abc"
	now := time.Unix(1800000000, 0)
	body := []byte(`{"id":"12345678-1234-4234-9234-123456789abc","payload":{"n":1.00}}`)
	timestamp := "1800000000"
	signature := Signature(secret, "warehouse", id, timestamp, body)
	if signature != "v1=4c066aaa088af5823d42561714746f226a5b2a5f28f54a0cbf009dfd7f4c6d26" {
		t.Fatal("signature differs from the independently calculated HMAC-SHA256 vector")
	}
	if !VerifySignature(secret, "warehouse", id, timestamp, signature, body, now) {
		t.Fatal("valid signature rejected")
	}
	cases := []struct {
		destination, id, timestamp, signature string
		body                                  []byte
		now                                   time.Time
	}{
		{"billing", id, timestamp, signature, body, now},
		{"warehouse", NewID(), timestamp, signature, body, now},
		{"warehouse", id, timestamp, signature, append(append([]byte{}, body...), byte(' ')), now},
		{"warehouse", id, timestamp, signature, body, now.Add(301 * time.Second)},
		{"warehouse", id, timestamp, signature, body, now.Add(-301 * time.Second)},
		{"warehouse", id, timestamp, "v1=invalid", body, now},
		{"warehouse", "1-1-1-1-1", timestamp, signature, body, now},
		{"warehouse", id, "+1800000000", signature, body, now},
	}
	for i, item := range cases {
		if VerifySignature(secret, item.destination, item.id, item.timestamp, item.signature, item.body, item.now) {
			t.Errorf("tampered signature accepted in case %d", i)
		}
	}
	if !VerifySignature(secret, "warehouse", id, timestamp, signature, body, now.Add(300*time.Second)) {
		t.Fatal("inclusive timestamp boundary rejected")
	}
}

func TestStrictJSONBoundary(t *testing.T) {
	for _, body := range []string{`{"expectedVersion":1.5}`, `{"expectedVersion":"1"}`, `{"expectedVersion":0,"extra":true}`, `{"expectedVersion":0} {}`, `[]`, `null`, strings.Repeat(" ", 32*1024) + "{}"} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var input struct {
			ExpectedVersion *int64 `json:"expectedVersion"`
		}
		if err := decodeJSON(r, &input); err == nil {
			t.Errorf("invalid JSON boundary accepted: length %d", len(body))
		}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"expectedVersion":0}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	var input struct {
		ExpectedVersion *int64 `json:"expectedVersion"`
	}
	if err := decodeJSON(r, &input); err != nil || input.ExpectedVersion == nil || *input.ExpectedVersion != 0 {
		t.Fatal("valid zero version rejected")
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (function doerFunc) Do(request *http.Request) (*http.Response, error) { return function(request) }

type timeoutError struct{}

func (timeoutError) Error() string   { return "test timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingBody) Close() error             { return nil }

func TestWorkerTransportSignsStoredBytesBoundsPreviewAndClassifiesFailures(t *testing.T) {
	secret := []byte("test-only-signing-material-not-a-runtime-default")
	worker, err := NewWorker(nil, "worker-test", "http://receiver:8082", secret)
	if err != nil {
		t.Fatal(err)
	}
	client := worker.Client.(*http.Client)
	if client.Timeout != 2*time.Second {
		t.Fatal("worker request timeout differs from contract")
	}
	claim := &Claim{EventID: NewID(), DestinationID: "warehouse", Route: "/hooks/warehouse", Envelope: []byte(`{"payload":{"n":1.2300}}`), DatabaseTime: time.Unix(1800000000, 0)}
	worker.Client = doerFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		if !bytes.Equal(body, claim.Envelope) {
			t.Error("stored envelope changed before send")
		}
		if !VerifySignature(secret, "warehouse", claim.EventID, request.Header.Get("Webhook-Timestamp"), request.Header.Get("Webhook-Signature"), body, claim.DatabaseTime) {
			t.Error("worker signature does not verify")
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(strings.Repeat("🙂", 1000)))}, nil
	})
	outcome := worker.Send(context.Background(), claim)
	if outcome.Status != "SUCCEEDED" || outcome.ErrorCategory != nil || outcome.Preview == nil || len(*outcome.Preview) > 2048 || !utf8.ValidString(*outcome.Preview) {
		t.Fatal("invalid success outcome or unbounded preview")
	}
	worker.Client = doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("synthetic failure"))}, nil
	})
	if got := worker.Send(context.Background(), claim); got.Status != "HTTP_ERROR" || *got.HTTPStatus != 503 {
		t.Fatal("HTTP failure not classified")
	}
	worker.Client = doerFunc(func(*http.Request) (*http.Response, error) { return nil, timeoutError{} })
	if worker.Send(context.Background(), claim).Status != "TIMEOUT" {
		t.Fatal("timeout not classified")
	}
	worker.Client = doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("private network detail must not be persisted")
	})
	if got := worker.Send(context.Background(), claim); got.Status != "NETWORK_ERROR" || *got.ErrorCategory != "NETWORK_ERROR" || got.Preview != nil {
		t.Fatal("network error leaked raw details")
	}
	worker.Client = doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{0xff}, 2048)))}, nil
	})
	if got := worker.Send(context.Background(), claim); got.Preview == nil || len(*got.Preview) > 2048 || !utf8.ValidString(*got.Preview) {
		t.Fatal("invalid UTF-8 response exceeded preview byte limit")
	}
	worker.Client = doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 202, Body: failingBody{}}, nil
	})
	if got := worker.Send(context.Background(), claim); got.Status != "SUCCEEDED" || got.HTTPStatus == nil || *got.HTTPStatus != 202 || got.ErrorCategory != nil {
		t.Fatal("known 2xx acknowledgement was lost when best-effort preview failed")
	}
}

func TestWorkerRefusesRedirectsAndUntrustedURLComponents(t *testing.T) {
	for _, address := range []string{"file:///tmp/hook", "http://user:pass@receiver", "http://receiver/path", "http://receiver?secret=value"} {
		if _, err := NewWorker(nil, "id", address, nil); err == nil {
			t.Error("invalid trusted deployment URL accepted")
		}
	}
	worker, _ := NewWorker(nil, "id", "http://receiver", nil)
	if worker.Client.(*http.Client).CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("worker follows redirects")
	}
}
