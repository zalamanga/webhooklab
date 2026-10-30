package main

import (
	"context"
	"os"
	"strings"
	"time"
	"webhooklab/internal/lab"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		store, err := lab.Open(ctx, os.Getenv("WEBHOOK_DB_URL"))
		if err != nil {
			os.Exit(1)
		}
		defer store.Pool.Close()
		data, err := os.ReadFile("/tmp/worker-id")
		if err != nil || store.WorkerHealthy(ctx, strings.TrimSpace(string(data))) != nil {
			os.Exit(1)
		}
		return
	}
	ctx, cancel := lab.SignalContext()
	defer cancel()
	secret, err := lab.SigningSecret()
	if err != nil {
		lab.ExitFailure("Worker startup failed: configure the generated signing secret.")
	}
	store, err := lab.RuntimeStore(ctx)
	if err != nil {
		lab.ExitFailure("Worker startup failed: check database configuration and availability.")
	}
	defer store.Pool.Close()
	id := lab.NewID()
	if err = os.WriteFile("/tmp/worker-id", []byte(id), 0600); err != nil {
		lab.ExitFailure("Worker startup failed: its temporary directory is unavailable.")
	}
	worker, err := lab.NewWorker(store, id, lab.Environment("WEBHOOK_RECEIVER_BASE_URL", "http://receiver:8082"), secret)
	if err != nil {
		lab.ExitFailure("Worker startup failed: check the trusted demo receiver address.")
	}
	if err = worker.Run(ctx); err != nil {
		lab.ExitFailure("Worker stopped after a database error; unfinished claims will recover after lease expiry.")
	}
}
