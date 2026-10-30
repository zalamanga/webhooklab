package lab

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const sessionCookie = "webhooklab_session"

type API struct {
	Store        *Store
	Auth         *Auth
	Origin       string
	CookieSecure bool
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.ContentLength > 32*1024 {
		writeError(w, invalid("Request body must be at most 32 KiB."))
		return
	}
	mutation := r.Method != http.MethodGet && r.Method != http.MethodHead
	if mutation && r.Header.Get("Origin") != a.Origin {
		writeError(w, &APIError{403, "ORIGIN", "Request origin is not allowed."})
		return
	}
	path := r.URL.Path
	if r.Method == http.MethodGet && path == "/api/health" {
		if err := a.Store.Pool.Ping(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodPost && path == "/api/auth/login" {
		a.login(w, r)
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, &APIError{401, "SESSION_REQUIRED", "Sign in to continue."})
		return
	}
	session, err := a.Auth.Session(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	if mutation && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) != 1 {
		writeError(w, &APIError{403, "CSRF", "CSRF token is missing or invalid."})
		return
	}
	if r.Method == http.MethodGet && path == "/api/auth/session" {
		writeJSON(w, 200, session)
		return
	}
	if r.Method == http.MethodPost && path == "/api/auth/logout" {
		if r.ContentLength != 0 {
			var input struct{}
			if err = decodeJSON(r, &input); err != nil {
				writeError(w, err)
				return
			}
		}
		if err = a.Auth.Logout(r.Context(), session); err != nil {
			writeError(w, err)
			return
		}
		a.cookie(w, "", -1)
		w.WriteHeader(204)
		return
	}
	a.workspace(w, r, session.User)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	session, token, err := a.Auth.Login(r.Context(), input.Email, input.Password, r.RemoteAddr)
	if err != nil {
		writeError(w, err)
		return
	}
	a.cookie(w, token, 12*60*60)
	writeJSON(w, 200, session)
}

func (a *API) cookie(w http.ResponseWriter, value string, age int) {
	cookie := &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: a.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: age}
	if age > 0 {
		cookie.Expires = time.Now().Add(time.Duration(age) * time.Second).UTC()
	} else {
		cookie.Expires = time.Unix(1, 0).UTC()
	}
	http.SetCookie(w, cookie)
}

func (a *API) workspace(w http.ResponseWriter, r *http.Request, user User) {
	ctx, path := r.Context(), r.URL.Path
	page, err := parsePage(r)
	if err != nil {
		writeError(w, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/api/dashboard":
		result, err := a.Store.Dashboard(ctx)
		respond(w, 200, result, err)
	case r.Method == http.MethodGet && path == "/api/destinations":
		result, err := a.Store.Destinations(ctx)
		respond(w, 200, result, err)
	case r.Method == http.MethodPut && path == "/api/queue":
		var input struct {
			ExpectedVersion *int64 `json:"expectedVersion"`
			Paused          *bool  `json:"paused"`
		}
		if err = decodeJSON(r, &input); err != nil {
			writeError(w, err)
			return
		}
		if input.ExpectedVersion == nil || input.Paused == nil {
			writeError(w, invalid("expectedVersion and paused are required."))
			return
		}
		result, err := a.Store.SetQueue(ctx, user, *input.ExpectedVersion, *input.Paused)
		respond(w, 200, result, err)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/api/destinations/"):
		id := strings.TrimPrefix(path, "/api/destinations/")
		var input struct {
			ExpectedVersion *int64 `json:"expectedVersion"`
			Mode            string `json:"mode"`
		}
		if err = decodeJSON(r, &input); err != nil {
			writeError(w, err)
			return
		}
		if input.ExpectedVersion == nil {
			writeError(w, invalid("expectedVersion is required."))
			return
		}
		result, err := a.Store.SetDestination(ctx, user, id, *input.ExpectedVersion, input.Mode)
		respond(w, 200, result, err)
	case r.Method == http.MethodGet && path == "/api/events":
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "ALL"
		}
		result, err := a.Store.Events(ctx, page, state, r.URL.Query().Get("search"))
		respond(w, 200, result, err)
	case r.Method == http.MethodPost && path == "/api/events":
		var input CreateInput
		if err = decodeJSON(r, &input); err != nil {
			writeError(w, err)
			return
		}
		result, replay, err := a.Store.Create(ctx, user, r.Header.Get("Idempotency-Key"), input)
		status := 201
		if replay {
			status = 200
		}
		respond(w, status, result, err)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/events/"):
		id := strings.TrimPrefix(path, "/api/events/")
		result, err := a.Store.Detail(ctx, id)
		respond(w, 200, result, err)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/events/") && strings.HasSuffix(path, "/redrive"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/events/"), "/redrive")
		var input struct {
			ExpectedVersion *int64 `json:"expectedVersion"`
			Note            string `json:"note"`
		}
		if err = decodeJSON(r, &input); err != nil {
			writeError(w, err)
			return
		}
		if input.ExpectedVersion == nil {
			writeError(w, invalid("expectedVersion is required."))
			return
		}
		result, err := a.Store.Redrive(ctx, user, id, *input.ExpectedVersion, input.Note)
		respond(w, 200, result, err)
	case r.Method == http.MethodGet && path == "/api/audit":
		result, err := a.Store.Audit(ctx, page)
		respond(w, 200, result, err)
	default:
		writeError(w, missing())
	}
}

func parsePage(r *http.Request) (int, error) {
	text := r.URL.Query().Get("page")
	if text == "" {
		return 0, nil
	}
	page, err := strconv.Atoi(text)
	if err != nil || page < 0 || page > 100000 {
		return 0, invalid("Page must be a nonnegative integer within range.")
	}
	return page, nil
}

func decodeJSON(r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid("This request requires application/json.")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 32*1024+1))
	if err != nil || len(data) > 32*1024 || !utf8.Valid(data) {
		return invalid("Request must be valid UTF-8 JSON of at most 32 KiB.")
	}
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return invalid("Request must contain a JSON object.")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return invalid("Request fields are missing, unknown or have an invalid type.")
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return invalid("Request must contain exactly one JSON object.")
	}
	return nil
}

func respond(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, value)
}

func writeError(w http.ResponseWriter, err error) {
	failure := apiError(err)
	writeJSON(w, failure.Status, failure)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
