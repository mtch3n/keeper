package main

import (
	"context"
	"os"

	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"

	"github.com/mtchen/keeper/internal/client"
)

// connectDaemon starts keeperd if needed and dials it, identifying this
// process as the "keeper" CLI client (SPEC R9.1's session attribution).
func connectDaemon(ctx context.Context) (*client.Client, error) {
	sock := client.DefaultSocketPath()
	if err := client.StartDaemon(ctx, sock); err != nil {
		return nil, err
	}
	return dial(ctx, sock)
}

// dialOnly connects to an already-running daemon without spawning one.
// `keeper doctor` uses this: starting a daemon to diagnose why it won't
// start would defeat the point.
func dialOnly(ctx context.Context, sock string) (*client.Client, error) {
	return dial(ctx, sock)
}

// dial connects as a human client: no session, which is how the daemon knows
// this is the CLI and not an agent (SPEC §6.3).
func dial(ctx context.Context, sock string) (*client.Client, error) {
	return client.DialHuman(ctx, sock)
}

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	// types.Limits.StatementTimeout is a time.Duration, which json/v2 refuses to
	// encode without a format. Nanoseconds match what the daemon sends.
	data, err := jsonv2.Marshal(v, jsonv2.Deterministic(true), jsonv1.FormatDurationAsNano(true))
	if err != nil {
		return err
	}
	val := jsontext.Value(data)
	if err := val.Indent(jsontext.WithIndent("  ")); err != nil {
		return err
	}
	_, err = os.Stdout.Write(append([]byte(val), '\n'))
	return err
}
