package pipeline

import "testing"

func TestRedactCredentials(t *testing.T) {
	t.Parallel()

	input := "connecting to icecast://source:secret@[::1]:8000/tv.opus"
	got := redactCredentials(input)
	want := "connecting to icecast://source:[REDACTED]@[::1]:8000/tv.opus"
	if got != want {
		t.Errorf("redactCredentials() = %q, want %q", got, want)
	}
}
