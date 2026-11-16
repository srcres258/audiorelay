// Command audiorelay relays the audio track of an upstream live stream to a
// low-bitrate Ogg Opus HTTP endpoint.
//
// Command status: configuration parsing, pipeline supervision and the ffmpeg
// backend are implemented. See docs/architecture.md for the intended shape of
// the program.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/srcres258/audiorelay/internal/config"
	"github.com/srcres258/audiorelay/internal/logging"
	"github.com/srcres258/audiorelay/internal/pipeline"
	"github.com/srcres258/audiorelay/internal/relay"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		if config.IsHelp(err) {
			config.Usage(os.Stdout)
			return
		}
		log.Fatalf("audiorelay: %v", err)
	}

	if err := config.Validate(cfg); err != nil {
		log.Fatalf("audiorelay: invalid configuration: %v", err)
	}
	cfg, err = config.WithSourcePassword(cfg)
	if err != nil {
		log.Fatalf("audiorelay: %v", err)
	}

	logger, err := logging.New(cfg.LogLevel, os.Stderr)
	if err != nil {
		log.Fatalf("audiorelay: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Select the ffmpeg backend directly until additional media backends exist.
	r, err := relay.NewWithLogger(cfg, pipeline.FFmpegBackend{Logger: logger}, logger)
	if err != nil {
		log.Fatalf("audiorelay: %v", err)
	}

	if err := r.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("audiorelay: %v", err)
	}
}
