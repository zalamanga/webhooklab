package lab

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Session struct {
	User      User   `json:"user"`
	CSRFToken string `json:"csrfToken"`
	Digest    string `json:"-"`
}

type QueueControl struct {
	Paused  bool  `json:"paused"`
	Version int64 `json:"version"`
}

type WorkerStatus struct {
	ActiveCount       int        `json:"activeCount"`
	LastSeenAt        *time.Time `json:"lastSeenAt"`
	StaleAfterSeconds int        `json:"staleAfterSeconds"`
}

type Dashboard struct {
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"`
	Queue      QueueControl   `json:"queue"`
	Workers    WorkerStatus   `json:"workers"`
	ServerTime time.Time      `json:"serverTime"`
}

type Destination struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Route   string `json:"route"`
	Mode    string `json:"mode"`
	Version int64  `json:"version"`
}

type Delivery struct {
	ID              string     `json:"id"`
	EventType       string     `json:"eventType"`
	DestinationID   string     `json:"destinationId"`
	DestinationName string     `json:"destinationName"`
	Payload         string     `json:"payload"`
	State           string     `json:"state"`
	Version         int64      `json:"version"`
	Cycle           int        `json:"cycle"`
	CycleAttempts   int        `json:"cycleAttempts"`
	TotalAttempts   int        `json:"totalAttempts"`
	Redrives        int        `json:"redrives"`
	NextAttemptAt   *time.Time `json:"nextAttemptAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type Attempt struct {
	ID              string     `json:"id"`
	Number          int        `json:"number"`
	Cycle           int        `json:"cycle"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt"`
	HTTPStatus      *int       `json:"httpStatus"`
	DurationMS      *int64     `json:"durationMs"`
	ResponsePreview *string    `json:"responsePreview"`
	ErrorCategory   *string    `json:"errorCategory"`
	WorkerID        string     `json:"workerId"`
}

type Receipt struct {
	EventID         string    `json:"eventId"`
	DestinationID   string    `json:"destinationId"`
	FirstReceivedAt time.Time `json:"firstReceivedAt"`
	LastReceivedAt  time.Time `json:"lastReceivedAt"`
	ReceiptCount    int       `json:"receiptCount"`
	EffectCount     int       `json:"effectCount"`
	BodySHA256      string    `json:"bodySha256"`
}

type AuditItem struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	EventID   *string   `json:"eventId"`
	Note      string    `json:"note"`
}

type DeliveryDetail struct {
	Delivery Delivery    `json:"delivery"`
	Attempts []Attempt   `json:"attempts"`
	Receipt  *Receipt    `json:"receipt"`
	Audit    []AuditItem `json:"audit"`
}

type Page[T any] struct {
	Items    []T `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
}

type CreateInput struct {
	DestinationID string `json:"destinationId"`
	EventType     string `json:"eventType"`
	Payload       string `json:"payload"`
}

type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string   { return e.Code }
func invalid(message string) error  { return &APIError{400, "VALIDATION", message} }
func conflict(message string) error { return &APIError{409, "CONFLICT", message} }
func missing() error                { return &APIError{404, "NOT_FOUND", "Record not found."} }

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidID(id string) bool { return uuidPattern.MatchString(id) }

func NewID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("secure randomness unavailable")
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(data[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func apiError(err error) *APIError {
	var failure *APIError
	if errors.As(err, &failure) {
		return failure
	}
	return &APIError{500, "INTERNAL_ERROR", "The request could not be completed. Try again shortly."}
}
