package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     any
		expected  int64
		expectErr bool
	}{
		// Raw numbers
		{name: "raw int", value: 1024, expected: 1024, expectErr: false},
		{name: "raw int64", value: int64(2048), expected: 2048, expectErr: false},
		{name: "raw float64", value: float64(512), expected: 512, expectErr: false},

		// String formats with units
		{name: "bytes with B", value: "100B", expected: 100, expectErr: false},
		{name: "kilobytes", value: "1KB", expected: 1024, expectErr: false},
		{name: "megabytes", value: "1MB", expected: 1024 * 1024, expectErr: false},
		{name: "gigabytes", value: "1GB", expected: 1024 * 1024 * 1024, expectErr: false},
		{name: "4GB", value: "4GB", expected: 4 * 1024 * 1024 * 1024, expectErr: false},
		{name: "512MB", value: "512MB", expected: 512 * 1024 * 1024, expectErr: false},

		// Binary units (KiB, MiB, GiB)
		{name: "kibibytes", value: "1KiB", expected: 1024, expectErr: false},
		{name: "mebibytes", value: "1MiB", expected: 1024 * 1024, expectErr: false},
		{name: "gibibytes", value: "1GiB", expected: 1024 * 1024 * 1024, expectErr: false},
		{name: "4GiB", value: "4GiB", expected: 4 * 1024 * 1024 * 1024, expectErr: false},
		{name: "512MiB", value: "512MiB", expected: 512 * 1024 * 1024, expectErr: false},
		{name: "100KiB", value: "100KiB", expected: 100 * 1024, expectErr: false},

		// Case variations for binary units
		{name: "lowercase kib", value: "1kib", expected: 1024, expectErr: false},
		{name: "lowercase mib", value: "1mib", expected: 1024 * 1024, expectErr: false},
		{name: "lowercase gib", value: "1gib", expected: 1024 * 1024 * 1024, expectErr: false},

		// Raw number strings
		{name: "number string", value: "1024", expected: 1024, expectErr: false},
		{name: "large number string", value: "4294967296", expected: 4294967296, expectErr: false},

		// Decimal values
		{name: "decimal MB", value: "1.5MB", expected: int64(1.5 * 1024 * 1024), expectErr: false},

		// With whitespace
		{name: "with spaces", value: "100 MB", expected: 100 * 1024 * 1024, expectErr: false},
		{name: "with trim", value: "  1GB  ", expected: 1024 * 1024 * 1024, expectErr: false},

		// Error cases
		{name: "empty string", value: "", expectErr: true},
		{name: "invalid unit", value: "1TB", expectErr: true},
		{name: "invalid format", value: "not a number", expectErr: true},
		{name: "negative", value: "-1GB", expectErr: true},
		{name: "invalid type", value: []byte{}, expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBytes(tt.value)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     any
		expected  int
		expectErr bool
	}{
		// Raw numbers
		{name: "raw int", value: 1000, expected: 1000, expectErr: false},
		{name: "raw float64", value: float64(500), expected: 500, expectErr: false},

		// String formats with units
		{name: "thousands", value: "1k", expected: 1000, expectErr: false},
		{name: "millions", value: "1m", expected: 1000000, expectErr: false},
		{name: "5k", value: "5k", expected: 5000, expectErr: false},
		{name: "2m", value: "2m", expected: 2000000, expectErr: false},

		// Case insensitivity
		{name: "uppercase K", value: "1K", expected: 1000, expectErr: false},
		{name: "uppercase M", value: "1M", expected: 1000000, expectErr: false},

		// Raw number strings
		{name: "number string", value: "1000", expected: 1000, expectErr: false},
		{name: "large number string", value: "1000000", expected: 1000000, expectErr: false},

		// Decimal values
		{name: "decimal k", value: "1.5k", expected: 1500, expectErr: false},

		// With whitespace
		{name: "with spaces", value: "100 k", expected: 100000, expectErr: false},
		{name: "with trim", value: "  1m  ", expected: 1000000, expectErr: false},

		// Error cases
		{name: "empty string", value: "", expectErr: true},
		{name: "invalid unit", value: "1g", expectErr: true},
		{name: "invalid format", value: "not a number", expectErr: true},
		{name: "negative", value: "-1k", expectErr: true},
		{name: "invalid type", value: []byte{}, expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseCount(tt.value)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     any
		expected  time.Duration
		expectErr bool
	}{
		// Raw numbers (interpreted as seconds)
		{name: "raw int", value: 30, expected: 30 * time.Second, expectErr: false},
		{name: "raw int64", value: int64(60), expected: 60 * time.Second, expectErr: false},
		{name: "raw float64", value: float64(15), expected: 15 * time.Second, expectErr: false},

		// Go duration format
		{name: "30s", value: "30s", expected: 30 * time.Second, expectErr: false},
		{name: "2m", value: "2m", expected: 2 * time.Minute, expectErr: false},
		{name: "1h", value: "1h", expected: 1 * time.Hour, expectErr: false},
		{name: "15m", value: "15m", expected: 15 * time.Minute, expectErr: false},
		{name: "complex", value: "1h30m", expected: 1*time.Hour + 30*time.Minute, expectErr: false},

		// Raw number strings (interpreted as seconds)
		{name: "number string", value: "30", expected: 30 * time.Second, expectErr: false},
		{name: "large number string", value: "3600", expected: 3600 * time.Second, expectErr: false},

		// With whitespace
		{name: "with spaces", value: "  30s  ", expected: 30 * time.Second, expectErr: false},

		// Error cases
		{name: "empty string", value: "", expectErr: true},
		{name: "invalid format", value: "not a duration", expectErr: true},
		{name: "negative int", value: -1, expectErr: true},
		{name: "negative duration", value: "-30s", expectErr: true},
		{name: "invalid type", value: []byte{}, expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseDuration(tt.value)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
