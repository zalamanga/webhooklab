package lab

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type Auth struct {
	Store     *Store
	dummyHash []byte
}

func NewAuth(store *Store) *Auth {
	hash, err := bcrypt.GenerateFromPassword([]byte(NewID()), 12)
	if err != nil {
		panic("password hashing unavailable")
	}
	return &Auth{Store: store, dummyHash: hash}
}

func randomToken() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("secure randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func digest(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func remoteAddress(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return value
	}
	return host
}

func (a *Auth) Login(ctx context.Context, email, password, address string) (Session, string, error) {
	failure := &APIError{401, "LOGIN_FAILED", "Email or password is incorrect."}
	if len(email) > 254 || strings.ContainsRune(email, 0) || len(password) == 0 || len(password) > 72 {
		return Session{}, "", failure
	}
	email = strings.ToLower(strings.TrimSpace(email))
	key := digest([]byte(remoteAddress(address)))
	tx, err := a.Store.Pool.Begin(ctx)
	if err != nil {
		return Session{}, "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO login_throttle(address_digest) VALUES($1) ON CONFLICT DO NOTHING`, key); err != nil {
		return Session{}, "", err
	}
	var failures int
	var window, now time.Time
	var blocked *time.Time
	err = tx.QueryRow(ctx, `SELECT failures,window_start,blocked_until,now() FROM login_throttle WHERE address_digest=$1 FOR UPDATE`, key).Scan(&failures, &window, &blocked, &now)
	if err != nil {
		return Session{}, "", err
	}
	if blocked != nil && blocked.After(now) {
		return Session{}, "", &APIError{429, "LOGIN_THROTTLED", "Too many login attempts. Try again in 15 minutes."}
	}
	var user User
	var hash string
	err = tx.QueryRow(ctx, `SELECT id::text,email,name,password_hash FROM users WHERE email=$1`, email).Scan(&user.ID, &user.Email, &user.Name, &hash)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, "", err
	}
	passwordHash := []byte(hash)
	if !found {
		passwordHash = a.dummyHash
	}
	valid := bcrypt.CompareHashAndPassword(passwordHash, []byte(password)) == nil
	if !valid || !found {
		if !window.Add(15 * time.Minute).After(now) {
			failures = 0
			window = now
		}
		failures++
		blocked = nil
		if failures >= 5 {
			until := now.Add(15 * time.Minute)
			blocked = &until
		}
		if _, err = tx.Exec(ctx, `UPDATE login_throttle SET failures=$2,window_start=$3,blocked_until=$4 WHERE address_digest=$1`, key, failures, window, blocked); err != nil {
			return Session{}, "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return Session{}, "", err
		}
		return Session{}, "", failure
	}
	token, csrf := randomToken(), randomToken()
	session := Session{User: user, CSRFToken: csrf, Digest: digest([]byte(token))}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE expires_at<=now()`); err != nil {
		return Session{}, "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(digest,user_id,csrf_token,expires_at) VALUES($1,$2,$3,now()+interval '12 hours')`, session.Digest, user.ID, csrf); err != nil {
		return Session{}, "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM login_throttle WHERE address_digest=$1`, key); err != nil {
		return Session{}, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

func (a *Auth) Session(ctx context.Context, token string) (Session, error) {
	var session Session
	if len(token) != 43 {
		return session, &APIError{401, "SESSION_REQUIRED", "Sign in to continue."}
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return session, &APIError{401, "SESSION_REQUIRED", "Sign in to continue."}
	}
	session.Digest = digest([]byte(token))
	err := a.Store.Pool.QueryRow(ctx, `SELECT u.id::text,u.email,u.name,s.csrf_token FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.digest=$1 AND s.expires_at>now()`, session.Digest).Scan(&session.User.ID, &session.User.Email, &session.User.Name, &session.CSRFToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, &APIError{401, "SESSION_REQUIRED", "Sign in to continue."}
	}
	return session, err
}

func (a *Auth) Logout(ctx context.Context, session Session) error {
	_, err := a.Store.Pool.Exec(ctx, `DELETE FROM sessions WHERE digest=$1`, session.Digest)
	return err
}
