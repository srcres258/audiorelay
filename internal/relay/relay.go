// Package relay is the control plane of audiorelay.
//
// It owns the lifecycle of a media pipeline: it validates configuration,
// builds a pipeline through a Backend, keeps it running across upstream
// failures, and shuts it down when the process context is cancelled.
// It deliberately knows nothing about HLS, Opus or Icecast internals.
package relay

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/srcres258/audiorelay/internal/config"
	"github.com/srcres258/audiorelay/internal/pipeline"
)

const (
	defaultRestartBackoff    = time.Second
	defaultMaxRestartBackoff = 30 * time.Second
	defaultStopTimeout       = 5 * time.Second
)

// restartPolicy controls the behaviour of the supervisor after a pipeline
// stops unexpectedly. It is kept private so the public API remains small;
// tests use newWithPolicy to avoid waiting for production backoff durations.
type restartPolicy struct {
	initialBackoff time.Duration
	maxBackoff     time.Duration
	stopTimeout    time.Duration
}

func defaultRestartPolicy() restartPolicy {
	return restartPolicy{
		initialBackoff: defaultRestartBackoff,
		maxBackoff:     defaultMaxRestartBackoff,
		stopTimeout:    defaultStopTimeout,
	}
}

// Relay supervises one pipeline for one configured source/sink pair.
type Relay struct {
	cfg     config.Config
	backend pipeline.Backend
	policy  restartPolicy
	logger  *slog.Logger
}

// New validates cfg and returns a Relay that will build its pipeline from
// backend when Run is called.
func New(cfg config.Config, backend pipeline.Backend) (*Relay, error) {
	return NewWithLogger(cfg, backend, slog.Default())
}

// NewWithLogger constructs a Relay using logger for lifecycle and retry
// messages. A nil logger uses slog.Default().
func NewWithLogger(cfg config.Config, backend pipeline.Backend, logger *slog.Logger) (*Relay, error) {
	return newWithPolicyAndLogger(cfg, backend, defaultRestartPolicy(), logger)
}

func newWithPolicy(cfg config.Config, backend pipeline.Backend, policy restartPolicy) (*Relay, error) {
	return newWithPolicyAndLogger(cfg, backend, policy, slog.Default())
}

func newWithPolicyAndLogger(cfg config.Config, backend pipeline.Backend, policy restartPolicy, logger *slog.Logger) (*Relay, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate relay configuration: %w", err)
	}
	if isNilBackend(backend) {
		return nil, ErrNilBackend
	}
	if policy.initialBackoff <= 0 {
		return nil, fmt.Errorf("initial restart backoff must be positive")
	}
	if policy.maxBackoff < policy.initialBackoff {
		return nil, fmt.Errorf("maximum restart backoff must not be less than initial backoff")
	}
	if policy.stopTimeout <= 0 {
		return nil, fmt.Errorf("stop timeout must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Relay{
		cfg:     cfg,
		backend: backend,
		policy:  policy,
		logger:  logger,
	}, nil
}

// Run starts the pipeline and keeps it alive until ctx is cancelled.
//
// A backend construction, start, or wait failure is treated as a transient
// pipeline failure. The supervisor retries with capped exponential backoff.
// Cancelling ctx stops the current pipeline and returns nil.
func (r *Relay) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("relay context must not be nil")
	}

	spec := pipeline.Spec{
		Input:      r.cfg.Input,
		Output:     r.cfg.Output,
		Bitrate:    r.cfg.Bitrate,
		Channels:   r.cfg.Channels,
		SampleRate: r.cfg.SampleRate,
	}

	backoff := r.policy.initialBackoff
	r.logger.Info("relay started")
	for {
		if err := ctx.Err(); err != nil {
			r.logger.Info("relay stopped", "reason", "context cancelled")
			return nil
		}

		current, err := r.backend.New(spec)
		if err == nil && isNilPipeline(current) {
			current = nil
			err = fmt.Errorf("backend returned a nil pipeline")
		}
		if err != nil {
			r.logger.Error("pipeline creation failed", "error", err)
		}
		if err == nil {
			r.logger.Info("starting pipeline")
			err = current.Start(ctx)
			if err != nil {
				r.logger.Error("pipeline start failed", "error", err)
			}
		}
		if err == nil {
			err = waitForPipeline(ctx, current)
			if err != nil {
				r.logger.Error("pipeline exited", "error", err)
			} else {
				r.logger.Warn("pipeline exited unexpectedly")
			}
		}

		if ctx.Err() != nil {
			if current != nil {
				if stopErr := stopPipeline(current, r.policy.stopTimeout); stopErr != nil {
					r.logger.Error("pipeline stop failed", "error", stopErr)
				}
			}
			r.logger.Info("relay stopped", "reason", "context cancelled")
			return nil
		}

		if err == nil {
			err = fmt.Errorf("pipeline exited unexpectedly")
		}
		r.logger.Warn("retrying pipeline", "backoff", backoff, "error", err)
		if err := waitForRestart(ctx, backoff); err != nil {
			r.logger.Info("relay stopped", "reason", "context cancelled")
			return nil
		}
		backoff = nextBackoff(backoff, r.policy.maxBackoff)
	}
}

func waitForPipeline(ctx context.Context, current pipeline.Pipeline) error {
	result := make(chan error, 1)
	go func() {
		result <- current.Wait()
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func stopPipeline(current pipeline.Pipeline, timeout time.Duration) error {
	stopCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return current.Stop(stopCtx)
}

func waitForRestart(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func isNilBackend(backend pipeline.Backend) bool {
	return isNilInterface(backend)
}

func isNilPipeline(current pipeline.Pipeline) bool {
	return isNilInterface(current)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
