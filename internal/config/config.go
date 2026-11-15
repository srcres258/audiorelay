// Package config holds the runtime configuration of a relay instance.
//
// The first version reads configuration from command-line flags. The Config
// type is intentionally decoupled from flag parsing so that later sources
// (file, environment, control API) can populate the same struct.
package config

// Defaults for values that are derived from the Opus setup chosen for the
// first demo (see docs/architecture.md).
const (
	DefaultBitrate    = "32k"
	DefaultChannels   = 2
	DefaultSampleRate = 48000
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

	// Bitrate is the Opus target bitrate in the syntax the backend expects,
	// e.g. "32k".
	Bitrate string

	// Channels is the number of output audio channels (1 = mono, 2 = stereo).
	Channels int

	// SampleRate is the output sample rate in Hz.
	SampleRate int
}

// Parse builds a Config from command-line arguments (without the program
// name). It returns an error for malformed or unsupported values.
func Parse(args []string) (Config, error) {
	// TODO: implement flag parsing, keeping -h/usage output stable.
	_ = args
	return Config{}, ErrNotImplemented
}

// Validate reports whether c is usable by the relay.
func Validate(c Config) error {
	// TODO: implement checks (non-empty input/output, supported bitrate,
	// channel count and sample rate ranges).
	_ = c
	return ErrNotImplemented
}
