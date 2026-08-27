package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/chromenative"
)

func main() {
	if err := run(os.Stdin, os.Stdout, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "codex-router-chrome-host: %v\n", err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer, args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	config, err := chromenative.LoadConfig(filepath.Join(filepath.Dir(executable), chromenative.ConfigFileName))
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return errors.New("Chrome caller origin is missing")
	}
	if err := chromenative.ValidateOrigin(args[1], config.ExtensionID); err != nil {
		return err
	}
	bridge, err := chromenative.NewBridge(config)
	if err != nil {
		return err
	}
	for {
		payload, err := chromenative.ReadFrame(input)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		request, err := chromenative.ParseRequest(payload)
		if err != nil {
			response := chromenative.Failure(extractID(payload), "invalid_request", err.Error())
			if writeErr := chromenative.WriteFrame(output, response); writeErr != nil {
				return writeErr
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		response := bridge.Handle(ctx, request)
		cancel()
		if err := chromenative.WriteFrame(output, response); err != nil {
			return err
		}
	}
}

func extractID(payload []byte) string {
	var envelope struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.ID) > 128 {
		return ""
	}
	return envelope.ID
}
