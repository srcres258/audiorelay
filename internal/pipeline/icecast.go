package pipeline

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// icecastConnectTimeout bounds dialing and the source handshake. A sink
	// that accepts the TCP connection but never answers must fail the
	// pipeline quickly instead of blocking decoding behind a stalled write.
	icecastConnectTimeout = 10 * time.Second

	// icecastKeepAlivePeriod keeps NAT and firewall state alive between
	// writes of a long-lived source upload.
	icecastKeepAlivePeriod = 30 * time.Second
)

// newIcecastPublisher creates an HTTP PUT source client for Icecast 2.4 and
// later. The password is used only for the in-memory Basic Authorization
// header; it is deliberately absent from the ffmpeg command line.
func newIcecastPublisher(output string) (publisher, error) {
	parsed, err := url.Parse(output)
	if err != nil {
		return nil, fmt.Errorf("%w: parse Icecast output URL: %w", ErrPermanent, err)
	}

	scheme := parsed.Scheme
	switch scheme {
	case "icecast":
		scheme = "http"
	case "icecasts":
		scheme = "https"
	default:
		return nil, fmt.Errorf("%w: unsupported output URL scheme %q: expected icecast or icecasts", ErrPermanent, parsed.Scheme)
	}
	if parsed.Host == "" || parsed.User == nil || parsed.User.Username() == "" {
		return nil, fmt.Errorf("%w: invalid Icecast output URL: host and source username are required", ErrPermanent)
	}
	password, passwordSet := parsed.User.Password()
	if !passwordSet || password == "" {
		return nil, fmt.Errorf("%w: invalid Icecast output URL: source password is required", ErrPermanent)
	}

	endpoint := *parsed
	endpoint.Scheme = scheme
	endpoint.User = nil
	if endpoint.Path == "" {
		endpoint.Path = "/"
	}

	client := newIcecastClient()
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
			err := fmt.Errorf("Icecast source request failed: %s: %s", response.Status, strings.TrimSpace(string(body)))
			if isPermanentIcecastStatus(response.StatusCode) {
				return fmt.Errorf("%w: %w", ErrPermanent, err)
			}
			return err
		}

		select {
		case <-producerDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil
}

// newIcecastClient builds the HTTP client used for the source upload.
//
// The upload is a single long-lived request, so the transport needs explicit
// bounds: a dial or handshake that never completes, or an idle connection
// whose peer disappeared, would otherwise block the publisher (and therefore
// ffmpeg's writes) for as long as the kernel keeps the socket around.
func newIcecastClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   icecastConnectTimeout,
				KeepAlive: icecastKeepAlivePeriod,
			}).DialContext,
			ResponseHeaderTimeout: icecastConnectTimeout,
			ExpectContinueTimeout: time.Second,
			// Ogg is already compressed; gzip only adds buffering.
			DisableCompression: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isPermanentIcecastStatus(statusCode int) bool {
	return statusCode >= http.StatusBadRequest &&
		statusCode < http.StatusInternalServerError &&
		statusCode != http.StatusRequestTimeout &&
		statusCode != http.StatusTooManyRequests
}
