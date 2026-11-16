package pipeline

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestIcecastPublisher(t *testing.T) {
	t.Parallel()

	requests := make(chan *http.Request, 1)
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requests <- r
		bodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	output := fmt.Sprintf("icecast://source:secret@%s/live.opus", serverURL.Host)
	publish, err := newIcecastPublisher(output)
	if err != nil {
		t.Fatalf("newIcecastPublisher() error = %v", err)
	}

	reader, writer := io.Pipe()
	producerDone := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- publish(context.Background(), reader, producerDone)
	}()

	if _, err := writer.Write([]byte("OggS test stream")); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	close(producerDone)

	if err := <-result; err != nil {
		t.Fatalf("publish() error = %v", err)
	}

	request := <-requests
	if request.Method != http.MethodPut {
		t.Errorf("method = %q, want PUT", request.Method)
	}
	if request.URL.Path != "/live.opus" {
		t.Errorf("path = %q, want /live.opus", request.URL.Path)
	}
	username, password, ok := request.BasicAuth()
	if !ok || username != "source" || password != "secret" {
		t.Errorf("BasicAuth() = (%q, %q, %t), want (source, secret, true)", username, password, ok)
	}
	if got := request.Header.Get("Content-Type"); got != "audio/ogg" {
		t.Errorf("Content-Type = %q, want audio/ogg", got)
	}
	if got := string(<-bodies); got != "OggS test stream" {
		t.Errorf("body = %q, want OggS test stream", got)
	}
}

func TestNewIcecastPublisherValidation(t *testing.T) {
	t.Parallel()

	for _, output := range []string{
		"https://source:password@example.com/live.opus",
		"icecast://example.com/live.opus",
		"icecast://source@example.com/live.opus",
	} {
		if _, err := newIcecastPublisher(output); err == nil {
			t.Errorf("newIcecastPublisher(%q) error = nil, want non-nil", output)
		}
	}
}

func TestFFmpegArgsDoNotContainOutputCredentials(t *testing.T) {
	t.Parallel()

	spec := testSpec()
	args := strings.Join(buildFFmpegArgs(spec), " ")
	if strings.Contains(args, "password") || strings.Contains(args, spec.Output) {
		t.Errorf("ffmpeg arguments expose output credentials: %q", args)
	}
	if !strings.HasSuffix(args, "pipe:1") {
		t.Errorf("ffmpeg arguments = %q, want pipe:1 output", args)
	}
}
