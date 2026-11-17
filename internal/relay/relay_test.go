package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/srcres258/audiorelay/internal/config"
	"github.com/srcres258/audiorelay/internal/pipeline"
)

var errPipelineFailed = errors.New("pipeline failed")

func TestNew(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	backend := &fakeBackend{}

	relay, err := New(cfg, backend)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if relay == nil {
		t.Fatal("New() returned nil relay")
	}
	if relay.backend != backend {
		t.Fatal("New() did not retain backend")
	}

	if _, err := New(config.Config{}, backend); err == nil {
		t.Fatal("New(invalid config) error = nil, want error")
	}
	if _, err := New(cfg, nil); !errors.Is(err, ErrNilBackend) {
		t.Errorf("New(nil backend) error = %v, want %v", err, ErrNilBackend)
	}
}

func TestRunMapsConfigAndStopsOnCancellation(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	p := newBlockingPipeline()
	backend := &fakeBackend{pipelines: []pipeline.Pipeline{p}}
	relay, err := newWithPolicy(cfg, backend, testPolicy())
	if err != nil {
		t.Fatalf("newWithPolicy() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- relay.Run(ctx)
	}()

	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not start")
	}

	wantSpec := pipeline.Spec{
		Input:      cfg.Input,
		Output:     cfg.Output,
		Bitrate:    cfg.Bitrate,
		Channels:   cfg.Channels,
		SampleRate: cfg.SampleRate,
	}
	if got := backend.specs(); len(got) != 1 || !reflect.DeepEqual(got[0], wantSpec) {
		t.Fatalf("backend specs = %#v, want [%#v]", got, wantSpec)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Run() after cancellation error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after context cancellation")
	}

	select {
	case <-p.stopped:
	case <-time.After(time.Second):
		t.Fatal("pipeline Stop() was not called")
	}
}

func TestRunRestartsAfterPipelineFailure(t *testing.T) {
	t.Parallel()

	first := &fakePipeline{waitErr: errPipelineFailed}
	second := newBlockingPipeline()
	backend := &fakeBackend{
		pipelines: []pipeline.Pipeline{first, second},
		newCalls:  make(chan struct{}, 16),
	}
	relay, err := newWithPolicy(testConfig(), backend, testPolicy())
	if err != nil {
		t.Fatalf("newWithPolicy() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- relay.Run(ctx)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-backend.newCalls:
		case <-time.After(time.Second):
			t.Fatalf("backend New() call %d did not happen", i+1)
		}
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Run() after cancellation error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after restarting pipeline")
	}

	if first.stopCount() != 0 {
		t.Errorf("failed pipeline Stop() calls = %d, want 0", first.stopCount())
	}
}

func TestRunDoesNotRetryPermanentFailure(t *testing.T) {
	t.Parallel()

	permanent := fmt.Errorf("invalid credentials: %w", pipeline.ErrPermanent)
	backend := &fakeBackend{newErrs: []error{permanent}}
	relay, err := newWithPolicy(testConfig(), backend, testPolicy())
	if err != nil {
		t.Fatalf("newWithPolicy() error = %v", err)
	}

	err = relay.Run(context.Background())
	if !errors.Is(err, pipeline.ErrPermanent) {
		t.Errorf("Run() error = %v, want error wrapping %v", err, pipeline.ErrPermanent)
	}
	if got := len(backend.specs()); got != 1 {
		t.Errorf("backend New() calls = %d, want 1", got)
	}
}

func TestRunResetsBackoffAfterHealthyPipeline(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	first := &fakePipeline{waitErr: errPipelineFailed}
	healthy := &delayedFailurePipeline{delay: 5 * time.Millisecond, waitErr: errPipelineFailed}
	third := newBlockingPipeline()
	backend := &fakeBackend{
		pipelines: []pipeline.Pipeline{first, healthy, third},
		newCalls:  make(chan struct{}, 16),
	}
	policy := testPolicy()
	policy.backoffResetAfter = 2 * time.Millisecond
	relay, err := newWithPolicyAndLogger(testConfig(), backend, policy, slog.New(handler))
	if err != nil {
		t.Fatalf("newWithPolicyAndLogger() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- relay.Run(ctx)
	}()

	select {
	case <-third.started:
	case <-time.After(time.Second):
		t.Fatal("third pipeline did not start")
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Run() after cancellation error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}

	if got, want := handler.retryBackoffs(), []time.Duration{time.Millisecond, time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Errorf("retry backoffs = %v, want %v", got, want)
	}
}

func TestNextBackoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		current time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{current: time.Second, max: 30 * time.Second, want: 2 * time.Second},
		{current: 8 * time.Second, max: 30 * time.Second, want: 16 * time.Second},
		{current: 16 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
		{current: 30 * time.Second, max: 30 * time.Second, want: 30 * time.Second},
	}

	for _, tt := range tests {
		if got := nextBackoff(tt.current, tt.max); got != tt.want {
			t.Errorf("nextBackoff(%s, %s) = %s, want %s", tt.current, tt.max, got, tt.want)
		}
	}
}

func testConfig() config.Config {
	return config.Config{
		Input:      "https://upstream.example/live.m3u8",
		Output:     "icecast://source:password@localhost:8000/tv.opus",
		Bitrate:    "32k",
		Channels:   2,
		SampleRate: 48000,
		LogLevel:   config.DefaultLogLevel,
	}
}

func testPolicy() restartPolicy {
	return restartPolicy{
		initialBackoff:    time.Millisecond,
		maxBackoff:        4 * time.Millisecond,
		backoffResetAfter: time.Millisecond,
		stopTimeout:       time.Second,
	}
}

type fakeBackend struct {
	mu        sync.Mutex
	pipelines []pipeline.Pipeline
	newErrs   []error
	calls     int
	recorded  []pipeline.Spec
	newCalls  chan struct{}
}

func (b *fakeBackend) New(spec pipeline.Spec) (pipeline.Pipeline, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.newCalls == nil {
		b.newCalls = make(chan struct{}, 16)
	}
	b.recorded = append(b.recorded, spec)
	index := b.calls
	b.calls++
	b.newCalls <- struct{}{}
	if index < len(b.newErrs) && b.newErrs[index] != nil {
		return nil, b.newErrs[index]
	}
	if index >= len(b.pipelines) {
		return b.pipelines[len(b.pipelines)-1], nil
	}
	return b.pipelines[index], nil
}

func (b *fakeBackend) specs() []pipeline.Spec {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]pipeline.Spec(nil), b.recorded...)
}

type fakePipeline struct {
	waitErr error

	started chan struct{}
	stopped chan struct{}
	done    chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	mu        sync.Mutex
	stops     int
}

func newBlockingPipeline() *fakePipeline {
	return &fakePipeline{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (p *fakePipeline) Start(context.Context) error {
	if p.started != nil {
		p.startOnce.Do(func() { close(p.started) })
	}
	return nil
}

func (p *fakePipeline) Wait() error {
	if p.waitErr != nil {
		return p.waitErr
	}
	<-p.done
	return nil
}

func (p *fakePipeline) Stop(context.Context) error {
	p.mu.Lock()
	p.stops++
	p.mu.Unlock()
	p.stopOnce.Do(func() {
		if p.done != nil {
			close(p.done)
		}
		if p.stopped != nil {
			close(p.stopped)
		}
	})
	return nil
}

type delayedFailurePipeline struct {
	delay   time.Duration
	waitErr error
}

func (p *delayedFailurePipeline) Start(context.Context) error {
	return nil
}

func (p *delayedFailurePipeline) Wait() error {
	time.Sleep(p.delay)
	return p.waitErr
}

func (p *delayedFailurePipeline) Stop(context.Context) error {
	return nil
}

type recordingHandler struct {
	mu       sync.Mutex
	backoffs []time.Duration
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message != "retrying pipeline" {
		return nil
	}

	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "backoff" {
			h.mu.Lock()
			h.backoffs = append(h.backoffs, attr.Value.Duration())
			h.mu.Unlock()
		}
		return true
	})
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *recordingHandler) WithGroup(string) slog.Handler {
	return h
}

func (h *recordingHandler) retryBackoffs() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Duration(nil), h.backoffs...)
}

func (p *fakePipeline) stopCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stops
}
