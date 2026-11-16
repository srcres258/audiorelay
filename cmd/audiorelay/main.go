// Command audiorelay relays the audio track of an upstream live stream to a
// low-bitrate Ogg Opus HTTP endpoint.
//
// Skeleton status: command-line configuration and pipeline supervision are
// implemented, but the ffmpeg backend is still a stub. See
// docs/architecture.md for the intended shape of the program.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/srcres258/audiorelay/internal/config"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// TODO: select the backend from configuration once more than one exists.
	r, err := relay.New(cfg, ffmpegBackend{})
	if err != nil {
		log.Fatalf("audiorelay: %v", err)
	}

	if err := r.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("audiorelay: %v", err)
	}
}

// ffmpegBackend adapts pipeline.NewFFmpeg to the pipeline.Backend interface.
//
// TODO: move this adapter into internal/pipeline once the backend has real
// implementation; it lives here only to keep the wiring visible while the
// skeleton is being filled in.
type ffmpegBackend struct{}

func (ffmpegBackend) New(spec pipeline.Spec) (pipeline.Pipeline, error) {
	return pipeline.NewFFmpeg(spec)
}
