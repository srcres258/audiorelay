package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/srcres258/audiorelay/internal/pipeline"
)

func TestExitStatus(t *testing.T) {
	t.Parallel()

	permanent := fmt.Errorf("invalid upstream: %w", pipeline.ErrPermanent)
	joined := errors.Join(errors.New("ffmpeg exited"), pipeline.ErrPermanent)
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "permanent failure", err: permanent, want: permanentFailureExitStatus},
		{name: "joined permanent failure", err: joined, want: permanentFailureExitStatus},
		{name: "transient failure", err: errors.New("temporary failure"), want: 1},
		{name: "nil", err: nil, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitStatus(tt.err); got != tt.want {
				t.Errorf("exitStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
