package main

import "webhooklab/internal/lab"

func main() {
	ctx, cancel := lab.SignalContext()
	defer cancel()
	secret, err := lab.SigningSecret()
	if err != nil {
		lab.ExitFailure("Receiver startup failed: configure the generated signing secret.")
	}
	store, err := lab.RuntimeStore(ctx)
	if err != nil {
		lab.ExitFailure("Receiver startup failed: check database configuration and availability.")
	}
	defer store.Pool.Close()
	if err = lab.Serve(ctx, lab.Environment("WEBHOOK_RECEIVER_ADDR", ":8082"), lab.NewReceiver(store, secret)); err != nil {
		lab.ExitFailure("Receiver listener stopped unexpectedly.")
	}
}
