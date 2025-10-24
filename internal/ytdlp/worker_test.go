package ytdlp

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected int64
	}{
		{"4.0MiB", 4 * 1024 * 1024},
		{"10MB", 10 * 1000 * 1000},
		{"~512KiB", 512 * 1024},
		{"Unknown", 0},
		{"1.5GiB", int64(1.5 * 1024 * 1024 * 1024)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			if got := parseSize(tt.input); got != tt.expected {
				t.Fatalf("parseSize(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseETA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"01:30", time.Minute + 30*time.Second},
		{"1:02:03", time.Hour + 2*time.Minute + 3*time.Second},
		{"Unknown", 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			if got := parseETA(tt.input); got != tt.expected {
				t.Fatalf("parseETA(%q) = %s, want %s", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseProgress(t *testing.T) {
	t.Parallel()

	pct, total, downloaded, speed, eta, ok := parseProgress("12.5% of 4.00MiB at 2.00MiB/s ETA 00:10")
	if !ok {
		t.Fatalf("parseProgress returned ok=false")
	}

	if pct <= 12 || pct >= 13 {
		t.Fatalf("unexpected pct %.2f", pct)
	}

	expectedTotal := int64(4 * 1024 * 1024)
	if total != expectedTotal {
		t.Fatalf("total = %d, want %d", total, expectedTotal)
	}

	if downloaded == 0 {
		t.Fatalf("downloaded should be > 0")
	}

	if speed != int64(2*1024*1024) {
		t.Fatalf("speed = %d, want %d", speed, 2*1024*1024)
	}

	expectedETA := 10 * time.Second
	if eta != expectedETA {
		t.Fatalf("eta = %s, want %s", eta, expectedETA)
	}
}

func TestCanHandle(t *testing.T) {
	t.Parallel()

	valid := []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
	}

	for _, url := range valid {
		url := url
		t.Run(url, func(t *testing.T) {
			t.Parallel()

			if !CanHandle(url) {
				t.Fatalf("expected CanHandle(%q) to be true", url)
			}
		})
	}

	invalid := []string{
		"https://example.com/watch?v=dQw4w9WgXcQ",
		"not a url",
	}

	for _, url := range invalid {
		url := url
		t.Run(url, func(t *testing.T) {
			t.Parallel()

			if CanHandle(url) {
				t.Fatalf("expected CanHandle(%q) to be false", url)
			}
		})
	}
}

func TestCalculatePercentage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		downloaded int64
		total      int64
		expected   float64
	}{
		{50, 100, 50},
		{0, 0, 0},
		{10, 0, 100},
		{200, 100, 100},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(fmt.Sprintf("%d_%d", tt.downloaded, tt.total), func(t *testing.T) {
			t.Parallel()

			got := calculatePercentage(tt.downloaded, tt.total)
			if math.Abs(got-tt.expected) > 1e-9 {
				t.Fatalf("calculatePercentage(%d, %d) = %f, want %f", tt.downloaded, tt.total, got, tt.expected)
			}
		})
	}
}
