package lab

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Environment(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func RuntimeStore(ctx context.Context) (*Store, error) {
	connection := os.Getenv("WEBHOOK_DB_URL")
	if connection == "" {
		return nil, errors.New("WEBHOOK_DB_URL is required")
	}
	store, err := Open(ctx, connection)
	if err != nil {
		return nil, err
	}
	if err = store.Migrate(ctx); err != nil {
		store.Pool.Close()
		return nil, errors.New("database migration failed")
	}
	return store, nil
}

func SigningSecret() ([]byte, error) {
	secret := []byte(os.Getenv("WEBHOOK_SIGNING_SECRET"))
	if len(secret) < 32 {
		return nil, errors.New("WEBHOOK_SIGNING_SECRET must contain at least 32 bytes")
	}
	return secret, nil
}

func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func Serve(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func ExitFailure(message string) { log.Print(message); os.Exit(1) }
