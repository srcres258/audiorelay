// Package config holds the runtime configuration of a relay instance.
//
// The first version reads configuration from command-line flags. The Config
// type is intentionally decoupled from flag parsing so that later sources
// (file, environment, control API) can populate the same struct.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/srcres258/audiorelay/internal/logging"
)

// Defaults for values that are derived from the Opus setup chosen for the
// first demo (see docs/architecture.md).
const (
	DefaultBitrate    = "32k"
	DefaultChannels   = 2
	DefaultSampleRate = 48000
	DefaultLogLevel   = logging.DefaultLevel

	minBitrateKbps = 6
	maxBitrateKbps = 510
)

// Config describes a single source-to-sink relay.
type Config struct {
	// Input is the upstream media URL. It may point at an HLS playlist
	// (m3u8), a plain HTTP stream, or any other URL the media backend
	// understands. Meaning is ultimately defined by the pipeline backend.
	Input string

	// Output is the downstream sink URL. For the first version this is an
	// Icecast source URL such as
	// icecast://source@[::1]:8000/tv.opus. Use SourcePasswordFile rather
	// than embedding a password in this URL.
	Output string

	// Bitrate is the Opus target bitrate in kilobits per second, such as
	// "32k".
	Bitrate string

	// Channels is the number of output audio channels (1 = mono, 2 = stereo).
	Channels int

	// SampleRate is the output sample rate in Hz.
	SampleRate int

	// LogLevel controls structured log verbosity: debug, info, warn, or error.
	LogLevel string

	// SourcePasswordFile is an optional file whose contents are used as the
	// password in Output. It keeps the secret out of the process command line.
	SourcePasswordFile string
}

// Parse builds a Config from command-line arguments (without the program
// name). It returns flag.ErrHelp when args requests help. Parse only checks
// command-line syntax; callers must call Validate before using the result.
func Parse(args []string) (Config, error) {
	var c Config

	flags := flag.NewFlagSet("audiorelay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&c.Input, "input", "", "upstream media URL")
	flags.StringVar(&c.Output, "output", "", "downstream sink URL")
	flags.StringVar(&c.Bitrate, "bitrate", DefaultBitrate, "Opus bitrate in kbps (for example, 32k)")
	flags.IntVar(&c.Channels, "channels", DefaultChannels, "output audio channels (1 or 2)")
	flags.IntVar(&c.SampleRate, "sample-rate", DefaultSampleRate, "output sample rate in Hz")
	flags.StringVar(&c.LogLevel, "log-level", DefaultLogLevel, "log level: debug, info, warn, or error")
	flags.StringVar(&c.SourcePasswordFile, "source-password-file", "", "file containing the output source password")

	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("parse flags: %w: unexpected argument %q", ErrInvalid, flags.Arg(0))
	}

	return c, nil
}

// Usage writes the command-line usage text to w.
func Usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: audiorelay -input URL -output URL [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Relay an upstream audio track as a low-bitrate Ogg Opus stream.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Required:")
	fmt.Fprintln(w, "  -input URL          upstream media URL")
	fmt.Fprintln(w, "  -output URL         downstream sink URL")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintf(w, "  -bitrate RATE       Opus bitrate in kbps (default %q)\n", DefaultBitrate)
	fmt.Fprintf(w, "  -channels COUNT     output audio channels, 1 or 2 (default %d)\n", DefaultChannels)
	fmt.Fprintf(w, "  -sample-rate HZ     output sample rate in Hz (default %d)\n", DefaultSampleRate)
	fmt.Fprintf(w, "  -log-level LEVEL    log level: debug, info, warn, or error (default %q)\n", DefaultLogLevel)
	fmt.Fprintln(w, "  -source-password-file PATH")
	fmt.Fprintln(w, "                      file containing the output source password")
	fmt.Fprintln(w, "  -h, -help           show this help")
}

// Validate reports whether c is usable by the relay.
func Validate(c Config) error {
	if err := validateURL("input", c.Input); err != nil {
		return err
	}
	if err := validateURL("output", c.Output); err != nil {
		return err
	}
	if err := validateBitrate(c.Bitrate); err != nil {
		return err
	}
	if c.Channels != 1 && c.Channels != 2 {
		return fmt.Errorf("channels: %w: must be 1 or 2", ErrInvalid)
	}
	if !isSupportedSampleRate(c.SampleRate) {
		return fmt.Errorf("sample-rate: %w: must be one of 8000, 12000, 16000, 24000, or 48000 Hz", ErrInvalid)
	}
	if _, err := logging.ParseLevel(c.LogLevel); err != nil {
		return fmt.Errorf("log-level: %w: %v", ErrInvalid, err)
	}

	return nil
}

// WithSourcePassword reads c.SourcePasswordFile, if configured, and returns a
// copy whose Output URL contains the password. The secret is never passed as a
// command-line argument. The output URL must already contain a source username
// but must not contain a password of its own.
func WithSourcePassword(c Config) (Config, error) {
	if c.SourcePasswordFile == "" {
		return c, nil
	}

	contents, err := os.ReadFile(c.SourcePasswordFile)
	if err != nil {
		return Config{}, fmt.Errorf("read source password file: %w", err)
	}
	password := strings.TrimRight(string(contents), "\r\n")
	if password == "" {
		return Config{}, fmt.Errorf("source password file: %w", ErrRequired)
	}

	output, err := url.Parse(c.Output)
	if err != nil {
		return Config{}, fmt.Errorf("parse output URL: %w", err)
	}
	if output.User == nil || output.User.Username() == "" {
		return Config{}, fmt.Errorf("output: %w: source-password-file requires a username", ErrInvalid)
	}
	if _, set := output.User.Password(); set {
		return Config{}, fmt.Errorf("output: %w: password must not be set when source-password-file is used", ErrInvalid)
	}

	output.User = url.UserPassword(output.User.Username(), password)
	c.Output = output.String()
	return c, nil
}

func validateURL(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s: %w", name, ErrRequired)
	}

	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s: %w: must be an absolute URL", name, ErrInvalid)
	}

	return nil
}

func validateBitrate(value string) error {
	kbps, ok := strings.CutSuffix(value, "k")
	if !ok || kbps == "" {
		return fmt.Errorf("bitrate: %w: must use the form <number>k", ErrInvalid)
	}

	rate, err := strconv.Atoi(kbps)
	if err != nil || rate < minBitrateKbps || rate > maxBitrateKbps {
		return fmt.Errorf("bitrate: %w: must be between %dk and %dk", ErrInvalid, minBitrateKbps, maxBitrateKbps)
	}

	return nil
}

func isSupportedSampleRate(rate int) bool {
	switch rate {
	case 8000, 12000, 16000, 24000, 48000:
		return true
	default:
		return false
	}
}

// IsHelp reports whether err was returned because the command-line arguments
// requested help.
func IsHelp(err error) bool {
	return errors.Is(err, flag.ErrHelp)
}
