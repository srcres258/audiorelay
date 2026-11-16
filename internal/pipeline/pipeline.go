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
	"fmt"
	"io"
	"log/slog"
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

	mu      sync.Mutex
	cmd     *exec.Cmd
	stderr  *tailBuffer
	done    chan struct{}
	started bool
	waitErr error
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
		executable: executable,
		args:       args,
		logger:     logger,
		publish:    publish,
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
	producerDone := make(chan struct{})
	sourceDone := make(chan error, 1)
	cmd := exec.CommandContext(ctx, p.executable, p.args...)
	cmd.Stdout = stdin
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	p.cmd = cmd
	p.stderr = tail
	p.done = make(chan struct{})
	p.started = true
	p.logger.Info("ffmpeg process started", "pid", cmd.Process.Pid)
	go func() {
		sourceDone <- p.publish(ctx, stdout, producerDone)
	}()
	go p.collectWait(cmd, stderr, stdin, producerDone, sourceDone)
	return nil
}

func (p *ffmpegPipeline) collectWait(
	cmd *exec.Cmd,
	stderr *processStderr,
	stdout *io.PipeWriter,
	producerDone chan struct{},
	sourceDone <-chan error,
) {
	processErr := cmd.Wait()
	stderr.Flush()
	_ = stdout.Close()
	close(producerDone)
	sourceErr := <-sourceDone

	var err error
	switch {
	case sourceErr != nil:
		err = fmt.Errorf("publish Ogg stream to Icecast: %w", sourceErr)
	case processErr != nil:
		err = formatProcessError(processErr, stderr.tail.String())
	}
	if err != nil {
		p.logger.Error("ffmpeg pipeline exited", "pid", cmd.Process.Pid, "error", err)
	} else {
		p.logger.Info("ffmpeg pipeline exited", "pid", cmd.Process.Pid)
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
		return fmt.Errorf("invalid ffmpeg spec: %w", err)
	}
	return nil
}

func buildFFmpegArgs(spec Spec) []string {
	return []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
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

func formatProcessError(processErr error, stderr string) error {
	stderr = strings.TrimSpace(redactCredentials(stderr))
	if stderr == "" {
		return fmt.Errorf("ffmpeg: %w", processErr)
	}
	return fmt.Errorf("ffmpeg: %w: %s", processErr, stderr)
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
