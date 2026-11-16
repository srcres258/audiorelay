package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWithSourcePassword(t *testing.T) {
	t.Parallel()

	passwordFile := filepath.Join(t.TempDir(), "source-password")
	if err := os.WriteFile(passwordFile, []byte("s3cret value\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	cfg := Config{
		Output:             "icecast://source@[::1]:8000/tv.opus",
		SourcePasswordFile: passwordFile,
	}
	got, err := WithSourcePassword(cfg)
	if err != nil {
		t.Fatalf("WithSourcePassword() error = %v", err)
	}
	want := "icecast://source:s3cret%20value@[::1]:8000/tv.opus"
	if got.Output != want {
		t.Errorf("WithSourcePassword() output = %q, want %q", got.Output, want)
	}
	if cfg.Output != "icecast://source@[::1]:8000/tv.opus" {
		t.Errorf("WithSourcePassword() modified input config: %#v", cfg)
	}
}

func TestWithSourcePasswordWithoutFile(t *testing.T) {
	t.Parallel()

	cfg := Config{Output: "icecast://source@[::1]:8000/tv.opus"}
	got, err := WithSourcePassword(cfg)
	if err != nil {
		t.Fatalf("WithSourcePassword() error = %v", err)
	}
	if got != cfg {
		t.Errorf("WithSourcePassword() = %#v, want %#v", got, cfg)
	}
}

func TestWithSourcePasswordErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	emptyFile := filepath.Join(dir, "empty")
	passwordFile := filepath.Join(dir, "password")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatalf("write empty password file: %v", err)
	}
	if err := os.WriteFile(passwordFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	tests := []struct {
		name string
		cfg  Config
		want error
	}{
		{
			name: "missing file",
			cfg: Config{
				Output:             "icecast://source@localhost:8000/tv.opus",
				SourcePasswordFile: "/does/not/exist",
			},
		},
		{
			name: "empty file",
			cfg: Config{
				Output:             "icecast://source@localhost:8000/tv.opus",
				SourcePasswordFile: emptyFile,
			},
			want: ErrRequired,
		},
		{
			name: "missing username",
			cfg: Config{
				Output:             "icecast://localhost:8000/tv.opus",
				SourcePasswordFile: passwordFile,
			},
		},
		{
			name: "password already in URL",
			cfg: Config{
				Output:             "icecast://source:old@localhost:8000/tv.opus",
				SourcePasswordFile: passwordFile,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := WithSourcePassword(tt.cfg)
			if err == nil {
				t.Fatal("WithSourcePassword() error = nil, want non-nil")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("WithSourcePassword() error = %v, want error matching %v", err, tt.want)
			}
		})
	}
}
