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
	"strconv"
	"strings"
)

// Defaults for values that are derived from the Opus setup chosen for the
// first demo (see docs/architecture.md).
const (
	DefaultBitrate    = "32k"
	DefaultChannels   = 2
	DefaultSampleRate = 48000

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
	// icecast://source:PASSWORD@[::1]:8000/tv.opus
	Output string

	// Bitrate is the Opus target bitrate in kilobits per second, such as
	// "32k".
	Bitrate string

	// Channels is the number of output audio channels (1 = mono, 2 = stereo).
	Channels int

	// SampleRate is the output sample rate in Hz.
	SampleRate int
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

	return nil
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
