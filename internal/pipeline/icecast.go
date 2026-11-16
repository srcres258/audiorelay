package pipeline

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// newIcecastPublisher creates an HTTP PUT source client for Icecast 2.4 and
// later. The password is used only for the in-memory Basic Authorization
// header; it is deliberately absent from the ffmpeg command line.
func newIcecastPublisher(output string) (publisher, error) {
	parsed, err := url.Parse(output)
	if err != nil {
		return nil, fmt.Errorf("parse Icecast output URL: %w", err)
	}

	scheme := parsed.Scheme
	switch scheme {
	case "icecast":
		scheme = "http"
	case "icecasts":
		scheme = "https"
	default:
		return nil, fmt.Errorf("unsupported output URL scheme %q: expected icecast or icecasts", parsed.Scheme)
	}
	if parsed.Host == "" || parsed.User == nil || parsed.User.Username() == "" {
		return nil, fmt.Errorf("invalid Icecast output URL: host and source username are required")
	}
	password, passwordSet := parsed.User.Password()
	if !passwordSet || password == "" {
		return nil, fmt.Errorf("invalid Icecast output URL: source password is required")
	}

	endpoint := *parsed
	endpoint.Scheme = scheme
	endpoint.User = nil
	if endpoint.Path == "" {
		endpoint.Path = "/"
	}

	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	username := parsed.User.Username()

	return func(ctx context.Context, stream io.ReadCloser, producerDone <-chan struct{}) error {
		defer stream.Close()

		request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), stream)
		if err != nil {
			return fmt.Errorf("create Icecast source request: %w", err)
		}
		request.SetBasicAuth(username, password)
		request.Header.Set("Content-Type", "audio/ogg")
		request.Header.Set("User-Agent", "audiorelay")
		request.TransferEncoding = []string{"chunked"}

		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("connect to Icecast: %w", err)
		}
		defer response.Body.Close()

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			return fmt.Errorf("Icecast source request failed: %s: %s", response.Status, strings.TrimSpace(string(body)))
		}

		select {
		case <-producerDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil
}
