package main

import (
	"os"
	"webhooklab/internal/lab"
)

func main() {
	ctx, cancel := lab.SignalContext()
	defer cancel()
	store, err := lab.RuntimeStore(ctx)
	if err != nil {
		lab.ExitFailure("API startup failed: check database configuration and availability.")
	}
	defer store.Pool.Close()
	if err = store.Seed(ctx, os.Getenv("WEBHOOK_DEMO_PASSWORD")); err != nil {
		lab.ExitFailure("Account initialization failed: check the generated seed password.")
	}
	api := &lab.API{Store: store, Auth: lab.NewAuth(store), Origin: lab.Environment("WEBHOOK_APP_ORIGIN", "http://127.0.0.1:8097"), CookieSecure: os.Getenv("WEBHOOK_COOKIE_SECURE") == "true"}
	if err = lab.Serve(ctx, lab.Environment("WEBHOOK_API_ADDR", ":8080"), api); err != nil {
		lab.ExitFailure("API listener stopped unexpectedly.")
	}
}
