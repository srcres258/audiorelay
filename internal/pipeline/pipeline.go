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

import "context"

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

// NewFFmpeg returns the ffmpeg-based backend.
//
// TODO: implement. The backend is expected to run the ffmpeg executable with
// an Ogg/Opus output targeting spec.Output, supervise the process, and map
// exit codes and stderr into errors.
func NewFFmpeg(spec Spec) (Pipeline, error) {
	_ = spec
	return nil, ErrNotImplemented
}
