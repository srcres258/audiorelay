// Package pipeline is the media plane boundary of audiorelay.
//
// A Pipeline takes a media source (Spec.Input), extracts and transcodes its
// audio track to Opus, and publishes the result to a sink (Spec.Output).
// Everything below that boundary (HLS fetching, demuxing, decoding, encoding,
// muxing, publishing) is backend specific and must not leak into the rest of
// the program.
//
// The first backend shells out to the ffmpeg executable. Later revisions may
// add in-process backends (cgo/libav, pure Go media libraries) or a native
// HTTP broadcast sink without changing this interface.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srcres258/audiorelay/internal/config"
)

const (
	ffmpegExecutable = "ffmpeg"
	stderrLimit      = 64 * 1024
	killWaitTimeout  = time.Second

	// inputStallTimeout bounds how long a single upstream read or connect
	// may make no progress. ffmpeg's http protocol option rw_timeout is
	// the only network option its HLS demuxer forwards to playlist and
	// segment requests (libavformat/aviobuf.c: ffio_copy_url_options), so a
	// dead connection is noticed here instead of hanging forever.
	inputStallTimeout = 10 * time.Second

	// inputReconnectDelayMax caps the delay of ffmpeg's own protocol-level
	// reconnect. It is best effort: the HLS demuxer does not propagate the
	// reconnect options to freshly opened playlist and segment requests, so
	// this only helps connections ffmpeg already owns.
	inputReconnectDelayMax = 4 * time.Second

	// segmentMaxRetry lets the HLS demuxer re-fetch a segment that failed
	// to open. The ffmpeg default of 0 gives up on the first error and
	// skips the segment, which is audible as a gap for a live stream.
	segmentMaxRetry = 3

	// defaultPublishStallTimeout is how long ffmpeg may be blocked handing
	// Ogg bytes to the sink before the pipeline is treated as dead. Without
	// it a sink that silently stops reading stalls decoding indefinitely.
	defaultPublishStallTimeout = 15 * time.Second

	// publishStallCheckInterval caps the watchdog polling period. The
	// watchdog polls four times per stall timeout for shorter timeouts.
	publishStallCheckInterval = time.Second
)

// Spec is a fully resolved pipeline description. It contains no knowledge of
// upstream playlists or downstream clients: both ends are just URLs.
type Spec struct {
	// Input is the upstream media URL handed to the backend.
	Input string

	// Output is the sink URL the encoded stream is published to.
	Output string

	// Bitrate is the Opus target bitrate, e.g. "32k".
	Bitrate string

	// Channels is the number of output channels.
	Channels int

	// SampleRate is the output sample rate in Hz.
	SampleRate int
}

// Pipeline is a single running source-to-sink media job.
//
// Lifecycle: Start brings the job up, Wait blocks until it ends on its own or
// is stopped, and Stop asks it to shut down. Stop must be safe to call more
// than once and after the job already exited. Cancelling the context passed to
// Start is equivalent to calling Stop.
type Pipeline interface {
	// Start begins processing. It must return promptly; the work continues
	// in the background.
	Start(ctx context.Context) error

	// Wait blocks until processing has ended and returns its result.
	Wait() error

	// Stop requests a graceful shutdown and waits for it to complete.
	Stop(ctx context.Context) error
}

// Backend constructs Pipelines for a given Spec. It exists so that the
// pipeline implementation can be selected (or replaced in tests) without the
// relay package importing a concrete backend.
type Backend interface {
	// New validates spec and prepares a Pipeline without starting it.
	New(spec Spec) (Pipeline, error)
}

// FFmpegBackend constructs pipelines backed by the ffmpeg executable found
// on PATH.
type FFmpegBackend struct {
	// Logger receives process lifecycle and ffmpeg stderr records. A nil
	// logger uses slog.Default().
	Logger *slog.Logger
}

// New constructs an ffmpeg-backed pipeline without starting a process.
func (b FFmpegBackend) New(spec Spec) (Pipeline, error) {
	return newFFmpegWithExecutableAndLogger(spec, ffmpegExecutable, b.Logger)
}

// NewFFmpeg returns a pipeline backed by the ffmpeg executable found on PATH.
// It validates the media settings but does not start a process until Start is
// called.
func NewFFmpeg(spec Spec) (Pipeline, error) {
	return newFFmpegWithExecutableAndLogger(spec, ffmpegExecutable, slog.Default())
}

type ffmpegPipeline struct {
	executable string
	args       []string
	logger     *slog.Logger
	publish    publisher

	// publishStallTimeout is the watchdog threshold in use for this
	// pipeline; zero disables the watchdog. Tests use a short value.
	publishStallTimeout time.Duration

	mu      sync.Mutex
	cmd     *exec.Cmd
	stderr  *tailBuffer
	done    chan struct{}
	started bool
	waitErr error
}

// pipelineRun carries the per-start state handed to the goroutines that
// supervise one ffmpeg process.
type pipelineRun struct {
	cmd           *exec.Cmd
	stderr        *processStderr
	stdout        *io.PipeWriter
	writer        *watchWriter
	producerDone  chan struct{}
	sourceDone    <-chan error
	stopWatch     chan struct{}
	cancelPublish context.CancelFunc
}

// publisher sends ffmpeg's Ogg byte stream to the configured downstream sink.
// producerDone is closed when ffmpeg closes the stream.
type publisher func(ctx context.Context, stream io.ReadCloser, producerDone <-chan struct{}) error

func newFFmpegWithExecutable(spec Spec, executable string) (Pipeline, error) {
	return newFFmpegWithExecutableAndLogger(spec, executable, slog.Default())
}

func newFFmpegWithExecutableAndLogger(spec Spec, executable string, logger *slog.Logger) (Pipeline, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	if strings.TrimSpace(executable) == "" {
		return nil, fmt.Errorf("ffmpeg executable must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}
	publish, err := newIcecastPublisher(spec.Output)
	if err != nil {
		return nil, err
	}

	return newFFmpegWithPublisher(executable, buildFFmpegArgs(spec), logger, publish), nil
}

func newFFmpegWithPublisher(executable string, args []string, logger *slog.Logger, publish publisher) *ffmpegPipeline {
	return &ffmpegPipeline{
		executable:          executable,
		args:                args,
		logger:              logger,
		publish:             publish,
		publishStallTimeout: defaultPublishStallTimeout,
	}
}

func (p *ffmpegPipeline) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("start context must not be nil")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return ErrAlreadyStarted
	}

	tail := &tailBuffer{max: stderrLimit}
	stderr := &processStderr{tail: tail, logger: p.logger}
	stdout, stdin := io.Pipe()
	watcher := &watchWriter{writer: stdin}
	publishCtx, cancelPublish := context.WithCancel(ctx)
	producerDone := make(chan struct{})
	sourceDone := make(chan error, 1)
	cmd := exec.CommandContext(ctx, p.executable, p.args...)
	cmd.Stdout = watcher
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancelPublish()
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	run := &pipelineRun{
		cmd:           cmd,
		stderr:        stderr,
		stdout:        stdin,
		writer:        watcher,
		producerDone:  producerDone,
		sourceDone:    sourceDone,
		stopWatch:     make(chan struct{}),
		cancelPublish: cancelPublish,
	}

	p.cmd = cmd
	p.stderr = tail
	p.done = make(chan struct{})
	p.started = true
	p.logger.Info("ffmpeg process started", "pid", cmd.Process.Pid)
	go func() {
		sourceDone <- p.publish(publishCtx, stdout, producerDone)
	}()
	if p.publishStallTimeout > 0 {
		go p.watchPublish(run)
	}
	go p.collectWait(run)
	return nil
}

// watchPublish aborts the publisher when ffmpeg has been blocked on a write
// for longer than the stall timeout, which means the sink stopped consuming
// the stream. The resulting publisher error makes the pipeline end instead of
// leaving decoding stalled behind a dead socket.
func (p *ffmpegPipeline) watchPublish(run *pipelineRun) {
	interval := p.publishStallTimeout / 4
	if interval > publishStallCheckInterval {
		interval = publishStallCheckInterval
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-run.stopWatch:
			return
		case <-ticker.C:
			blocked := run.writer.blockedFor()
			if blocked < p.publishStallTimeout {
				continue
			}
			run.writer.markStalled(blocked)
			p.logger.Error("downstream sink stopped consuming the stream", "blocked_for", blocked)
			run.cancelPublish()
			return
		}
	}
}

func (p *ffmpegPipeline) collectWait(run *pipelineRun) {
	processErr := run.cmd.Wait()
	run.stderr.Flush()
	_ = run.stdout.Close()
	close(run.producerDone)
	sourceErr := <-run.sourceDone
	close(run.stopWatch)
	run.cancelPublish()

	var err error
	switch {
	case processErr != nil && sourceErr != nil:
		err = errors.Join(
			formatProcessError(processErr, run.stderr.tail.String()),
			formatPublishError(run.writer, sourceErr),
		)
	case processErr != nil:
		err = formatProcessError(processErr, run.stderr.tail.String())
	case sourceErr != nil:
		err = formatPublishError(run.writer, sourceErr)
	}
	if err != nil {
		p.logger.Error("ffmpeg pipeline exited", "pid", run.cmd.Process.Pid, "error", err)
	} else {
		p.logger.Info("ffmpeg pipeline exited", "pid", run.cmd.Process.Pid)
	}

	p.mu.Lock()
	p.waitErr = err
	close(p.done)
	p.mu.Unlock()
}

func (p *ffmpegPipeline) Wait() error {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return ErrNotStarted
	}
	done := p.done
	p.mu.Unlock()

	<-done

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *ffmpegPipeline) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("stop context must not be nil")
	}

	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return ErrNotStarted
	}
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	select {
	case <-done:
		return nil
	default:
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		select {
		case <-done:
			return nil
		default:
			return fmt.Errorf("interrupt ffmpeg: %w", err)
		}
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if err := cmd.Process.Kill(); err != nil {
			select {
			case <-done:
				return ctx.Err()
			default:
				return fmt.Errorf("kill ffmpeg: %w", err)
			}
		}

		select {
		case <-done:
			return ctx.Err()
		case <-time.After(killWaitTimeout):
			return ctx.Err()
		}
	}
}

func validateSpec(spec Spec) error {
	if err := config.Validate(config.Config{
		Input:      spec.Input,
		Output:     spec.Output,
		Bitrate:    spec.Bitrate,
		Channels:   spec.Channels,
		SampleRate: spec.SampleRate,
		LogLevel:   config.DefaultLogLevel,
	}); err != nil {
		return fmt.Errorf("%w: invalid ffmpeg spec: %w", ErrPermanent, err)
	}
	return nil
}

func buildFFmpegArgs(spec Spec) []string {
	return []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
		// Input network options. ffmpeg applies these to the input protocol;
		// rw_timeout additionally reaches every HLS playlist and segment
		// request, while the reconnect options are best effort.
		"-rw_timeout", strconv.FormatInt(int64(inputStallTimeout/time.Microsecond), 10),
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", strconv.Itoa(int(inputReconnectDelayMax / time.Second)),
		"-seg_max_retry", strconv.Itoa(segmentMaxRetry),
		"-i", spec.Input,
		"-map", "0:a:0",
		"-vn",
		"-c:a", "libopus",
		"-application", "audio",
		"-b:a", spec.Bitrate,
		"-vbr", "on",
		"-ac", strconv.Itoa(spec.Channels),
		"-ar", strconv.Itoa(spec.SampleRate),
		"-f", "ogg",
		"pipe:1",
	}
}

// formatPublishError describes a publisher failure, making the difference
// between a sink that rejected the stream and a sink that silently stopped
// reading it visible in logs and in the restart reason.
func formatPublishError(writer *watchWriter, err error) error {
	if stalled := writer.stalledFor(); stalled > 0 {
		return fmt.Errorf("publish Ogg stream to Icecast: sink stalled for %s: %w", stalled.Round(time.Millisecond), err)
	}
	return fmt.Errorf("publish Ogg stream to Icecast: %w", err)
}

func formatProcessError(processErr error, stderr string) error {
	stderr = strings.TrimSpace(redactCredentials(stderr))
	if stderr == "" {
		return fmt.Errorf("ffmpeg: %w", processErr)
	}
	if isPermanentFFmpegError(stderr) {
		return fmt.Errorf("%w: ffmpeg: %w: %s", ErrPermanent, processErr, stderr)
	}
	return fmt.Errorf("ffmpeg: %w: %s", processErr, stderr)
}

func isPermanentFFmpegError(stderr string) bool {
	// A live HLS source can briefly return 404 for an individual segment.
	// Only classify HTTP 4xx as permanent when ffmpeg failed to open the
	// configured input itself, not when a later segment request failed.
	if !strings.Contains(strings.ToLower(stderr), "error opening input") {
		return false
	}

	matches := httpStatusPattern.FindAllStringSubmatch(stderr, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		status, err := strconv.Atoi(match[1])
		if err == nil && status >= http.StatusBadRequest && status < http.StatusInternalServerError &&
			status != http.StatusRequestTimeout && status != http.StatusTooManyRequests {
			return true
		}
	}
	return false
}

// watchWriter sits between ffmpeg's stdout and the publisher. It records when
// a write is in flight so the pipeline can distinguish an idle encoder from a
// sink that stopped reading: with an unbuffered pipe a blocked write means
// ffmpeg has stopped reading its upstream input as well.
type watchWriter struct {
	writer io.Writer

	mu            sync.Mutex
	pendingSince  time.Time
	stallDuration time.Duration
}

func (w *watchWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	w.pendingSince = time.Now()
	w.mu.Unlock()

	n, err := w.writer.Write(data)

	w.mu.Lock()
	w.pendingSince = time.Time{}
	w.mu.Unlock()
	return n, err
}

func (w *watchWriter) blockedFor() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pendingSince.IsZero() {
		return 0
	}
	return time.Since(w.pendingSince)
}

func (w *watchWriter) markStalled(blocked time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stallDuration = blocked
}

func (w *watchWriter) stalledFor() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stallDuration
}

type processStderr struct {
	tail   *tailBuffer
	logger *slog.Logger

	mu      sync.Mutex
	pending []byte
}

func (w *processStderr) Write(data []byte) (int, error) {
	if _, err := w.tail.Write(data); err != nil {
		return 0, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	for {
		index := strings.IndexByte(string(w.pending), '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(string(w.pending[:index]))
		w.pending = w.pending[index+1:]
		if line != "" {
			w.logger.Warn("ffmpeg stderr", "line", redactCredentials(line))
		}
	}
	return len(data), nil
}

func (w *processStderr) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	line := strings.TrimSpace(string(w.pending))
	w.pending = nil
	if line != "" {
		w.logger.Warn("ffmpeg stderr", "line", redactCredentials(line))
	}
}

var credentialsPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+:)[^@\s]+@`)
var httpStatusPattern = regexp.MustCompile(`(?i)\b(?:http error|server returned|status code)\s*[:=]?\s*(\d{3})\b`)

func redactCredentials(value string) string {
	return credentialsPattern.ReplaceAllString(value, `${1}[REDACTED]@`)
}

type tailBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, data...)
	if len(b.data) > b.max {
		b.data = b.data[len(b.data)-b.max:]
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
