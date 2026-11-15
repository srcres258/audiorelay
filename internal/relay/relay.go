// Package relay is the control plane of audiorelay.
//
// It owns the lifecycle of a media pipeline: it validates configuration,
// builds a pipeline through a Backend, keeps it running across upstream
// failures, and shuts it down when the process context is cancelled.
// It deliberately knows nothing about HLS, Opus or Icecast internals.
package relay

import (
	"context"

	"github.com/srcres258/audiorelay/internal/config"
	"github.com/srcres258/audiorelay/internal/pipeline"
)

// Relay supervises one pipeline for one configured source/sink pair.
type Relay struct {
	cfg     config.Config
	backend pipeline.Backend
}

// New validates cfg and returns a Relay that will build its pipeline from
// backend when Run is called.
func New(cfg config.Config, backend pipeline.Backend) (*Relay, error) {
	// TODO: validate cfg here (or at the CLI boundary) and reject a nil
	// backend.
	_ = cfg
	_ = backend
	return nil, ErrNotImplemented
}

// Run starts the pipeline and keeps it alive until ctx is cancelled.
//
// TODO: implement the supervision loop:
//   - map cfg to pipeline.Spec;
//   - start the pipeline and wait for it;
//   - on unexpected exit, restart with capped exponential backoff;
//   - on ctx cancellation, stop the pipeline and return nil.
//
// Restart policy, backoff bounds and logging are intentionally left as open
// design points for the next iteration (see docs/architecture.md).
func (r *Relay) Run(ctx context.Context) error {
	_ = ctx
	return ErrNotImplemented
}
