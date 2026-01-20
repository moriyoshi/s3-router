package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parseBytes parses a size value with optional unit suffix (B, KB, MB, GB, KiB, MiB, GiB).
// Supports both numeric values and strings with units.
// Examples: "4GB", "512MB", "100KB", "1GiB", "512MiB", "100KiB", "1024" (bytes), "4294967296"
// Note: KB/MB/GB use 1024 multipliers (binary), while KiB/MiB/GiB are explicit binary units.
func parseBytes(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		if v == "" {
			return 0, fmt.Errorf("empty value")
		}

		// Try parsing as raw number first
		if num, err := strconv.ParseInt(v, 10, 64); err == nil {
			return num, nil
		}

		// Parse with units
		v = strings.TrimSpace(v)
		re := regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([a-zA-Z]*)$`)
		matches := re.FindStringSubmatch(v)
		if len(matches) != 3 {
			return 0, fmt.Errorf("invalid size format: %q (use format like '4GB', '512MB', '100KB', '1GiB', '512MiB', '100KiB', or a number in bytes)", v)
		}

		numStr, unit := matches[1], strings.ToUpper(matches[2])
		num, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number in size: %w", err)
		}

		var multiplier int64 = 1
		switch unit {
		case "", "B":
		case "KB":
			multiplier = 1024
		case "MB":
			multiplier = 1024 * 1024
		case "GB":
			multiplier = 1024 * 1024 * 1024
		case "KIB":
			multiplier = 1024
		case "MIB":
			multiplier = 1024 * 1024
		case "GIB":
			multiplier = 1024 * 1024 * 1024
		default:
			return 0, fmt.Errorf("unknown size unit: %q (supported: B, KB, MB, GB, KiB, MiB, GiB)", unit)
		}

		result := int64(num * float64(multiplier))
		if result < 0 {
			return 0, fmt.Errorf("size must be non-negative")
		}
		return result, nil
	default:
		return 0, fmt.Errorf("invalid type for size: %T", value)
	}
}

// parseCount parses a count value with optional unit suffix (k for thousands, m for millions).
// Supports both numeric values and strings with units.
// Examples: "1k", "1m", "1000", "1000000"
func parseCount(value any) (int, error) {
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0, fmt.Errorf("count must be non-negative")
		}
		return v, nil
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("count must be non-negative")
		}
		return int(v), nil
	case string:
		if v == "" {
			return 0, fmt.Errorf("empty value")
		}

		// Try parsing as raw number first
		if num, err := strconv.ParseInt(v, 10, 64); err == nil {
			if num < 0 {
				return 0, fmt.Errorf("count must be non-negative")
			}
			return int(num), nil
		}

		// Parse with units
		v = strings.TrimSpace(v)
		re := regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([a-zA-Z]*)$`)
		matches := re.FindStringSubmatch(v)
		if len(matches) != 3 {
			return 0, fmt.Errorf("invalid count format: %q (use format like '1k', '1m', or a plain number)", v)
		}

		numStr, unit := matches[1], strings.ToLower(matches[2])
		num, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number in count: %w", err)
		}

		var multiplier float64 = 1
		switch unit {
		case "":
		case "k":
			multiplier = 1000
		case "m":
			multiplier = 1000000
		default:
			return 0, fmt.Errorf("unknown count unit: %q (supported: k, m, or no unit)", unit)
		}

		result := int(num * multiplier)
		if result < 0 {
			return 0, fmt.Errorf("count must be non-negative")
		}
		return result, nil
	default:
		return 0, fmt.Errorf("invalid type for count: %T", value)
	}
}

// parseDuration parses a duration value using Go's time.ParseDuration format.
// Also supports raw seconds as a fallback for backward compatibility.
// Examples: "30s", "2m", "1h", "15m", "900" (seconds)
func parseDuration(value any) (time.Duration, error) {
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0, fmt.Errorf("duration must be non-negative")
		}
		return time.Duration(v) * time.Second, nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("duration must be non-negative")
		}
		return time.Duration(v) * time.Second, nil
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("duration must be non-negative")
		}
		return time.Duration(int64(v)) * time.Second, nil
	case string:
		if v == "" {
			return 0, fmt.Errorf("empty value")
		}

		v = strings.TrimSpace(v)

		// Try parsing as raw number (interpret as seconds) first for backward compatibility
		if num, err := strconv.ParseInt(v, 10, 64); err == nil {
			if num < 0 {
				return 0, fmt.Errorf("duration must be non-negative")
			}
			return time.Duration(num) * time.Second, nil
		}

		// Try parsing as Go duration format
		dur, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("invalid duration format: %q (use Go duration format like '30s', '2m', '1h', or a number in seconds): %w", v, err)
		}

		if dur < 0 {
			return 0, fmt.Errorf("duration must be non-negative")
		}
		return dur, nil
	default:
		return 0, fmt.Errorf("invalid type for duration: %T", value)
	}
}
