package config

import (
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	t.Parallel()

	got, err := Parse([]string{
		"-input", "https://upstream.example/live.m3u8",
		"-output", "icecast://source:password@[fd12:3456::1]:8000/tv.opus",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Config{
		Input:      "https://upstream.example/live.m3u8",
		Output:     "icecast://source:password@[fd12:3456::1]:8000/tv.opus",
		Bitrate:    DefaultBitrate,
		Channels:   DefaultChannels,
		SampleRate: DefaultSampleRate,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseOverrides(t *testing.T) {
	t.Parallel()

	got, err := Parse([]string{
		"-input=https://upstream.example/live.m3u8",
		"-output=icecast://source:password@localhost:8000/tv.opus",
		"-bitrate=48k",
		"-channels=1",
		"-sample-rate=24000",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Config{
		Input:      "https://upstream.example/live.m3u8",
		Output:     "icecast://source:password@localhost:8000/tv.opus",
		Bitrate:    "48k",
		Channels:   1,
		SampleRate: 24000,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		is   error
	}{
		{
			name: "help",
			args: []string{"-h"},
			is:   flag.ErrHelp,
		},
		{
			name: "unknown flag",
			args: []string{"-unknown"},
		},
		{
			name: "invalid integer",
			args: []string{"-channels=stereo"},
		},
		{
			name: "positional argument",
			args: []string{"unexpected"},
			is:   ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args)
			if err == nil {
				t.Fatal("Parse() error = nil, want non-nil")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("Parse() error = %v, want error matching %v", err, tt.is)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := Config{
		Input:      "https://upstream.example/live.m3u8",
		Output:     "icecast://source:password@[fd12:3456::1]:8000/tv.opus",
		Bitrate:    "32k",
		Channels:   2,
		SampleRate: 48000,
	}
	if err := Validate(valid); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}

	tests := []struct {
		name string
		edit func(*Config)
		want error
	}{
		{
			name: "missing input",
			edit: func(c *Config) { c.Input = "" },
			want: ErrRequired,
		},
		{
			name: "relative output URL",
			edit: func(c *Config) { c.Output = "/tv.opus" },
			want: ErrInvalid,
		},
		{
			name: "bitrate without k suffix",
			edit: func(c *Config) { c.Bitrate = "32000" },
			want: ErrInvalid,
		},
		{
			name: "bitrate outside Opus range",
			edit: func(c *Config) { c.Bitrate = "511k" },
			want: ErrInvalid,
		},
		{
			name: "more than two channels",
			edit: func(c *Config) { c.Channels = 3 },
			want: ErrInvalid,
		},
		{
			name: "unsupported sample rate",
			edit: func(c *Config) { c.SampleRate = 44100 },
			want: ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valid
			tt.edit(&got)

			err := Validate(got)
			if !errors.Is(err, tt.want) {
				t.Errorf("Validate() error = %v, want error matching %v", err, tt.want)
			}
		})
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	Usage(&output)
	for _, want := range []string{
		"Usage: audiorelay -input URL -output URL [options]",
		"-input URL",
		"-output URL",
		"-bitrate RATE",
		"-channels COUNT",
		"-sample-rate HZ",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("Usage() output does not contain %q:\n%s", want, output.String())
		}
	}
}
