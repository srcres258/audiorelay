package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildFFmpegArgs(t *testing.T) {
	t.Parallel()

	spec := testSpec()
	got := buildFFmpegArgs(spec)
	want := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
		"-i", spec.Input,
		"-map", "0:a:0",
		"-vn",
		"-c:a", "libopus",
		"-application", "audio",
		"-b:a", "32k",
		"-vbr", "on",
		"-ac", "2",
		"-ar", "48000",
		"-f", "ogg",
		"-content_type", "audio/ogg",
		spec.Output,
	}

	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("buildFFmpegArgs() = %#v, want %#v", got, want)
	}
}

func TestNewFFmpegValidatesSpec(t *testing.T) {
	t.Parallel()

	if _, err := NewFFmpeg(testSpec()); err != nil {
		t.Fatalf("NewFFmpeg(valid spec) error = %v", err)
	}
	if _, err := NewFFmpeg(Spec{}); err == nil {
		t.Fatal("NewFFmpeg(empty spec) error = nil, want error")
	}
}

func TestFFmpegPipelineLifecycle(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
exit 0
`)

	p, err := newFFmpegWithExecutable(testSpec(), script)
	if err != nil {
		t.Fatalf("newFFmpegWithExecutable() error = %v", err)
	}
	if err := p.Wait(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Wait() before Start error = %v, want %v", err, ErrNotStarted)
	}

	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := p.Start(ctx); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("second Start() error = %v, want %v", err, ErrAlreadyStarted)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want nil", err)
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Errorf("Stop() after exit error = %v, want nil", err)
	}
}

func TestFFmpegPipelineReportsProcessError(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
printf 'synthetic ffmpeg failure\n' >&2
exit 7
`)

	p, err := newFFmpegWithExecutable(testSpec(), script)
	if err != nil {
		t.Fatalf("newFFmpegWithExecutable() error = %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err = p.Wait()
	if err == nil {
		t.Fatal("Wait() error = nil, want process error")
	}
	if !strings.Contains(err.Error(), "synthetic ffmpeg failure") {
		t.Errorf("Wait() error = %v, want stderr context", err)
	}
}

func TestFFmpegPipelineStopTerminatesProcess(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
trap '' INT TERM
while :; do :; done
`)

	p, err := newFFmpegWithExecutable(testSpec(), script)
	if err != nil {
		t.Fatalf("newFFmpegWithExecutable() error = %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	if err := p.Wait(); err == nil {
		t.Fatal("Wait() after Stop() error = nil, want process termination error")
	}
}

func testSpec() Spec {
	return Spec{
		Input:      "https://upstream.example/live.m3u8",
		Output:     "icecast://source:password@localhost:8000/tv.opus",
		Bitrate:    "32k",
		Channels:   2,
		SampleRate: 48000,
	}
}

func writeExecutable(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake-ffmpeg")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}
