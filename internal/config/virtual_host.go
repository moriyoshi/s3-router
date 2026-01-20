package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// Host represents a parsed wildcard host pattern
type Host struct {
	Pattern       string // Original pattern like "*.example.com"
	Suffix        string // Suffix/Exact host name pattern either ".example.com" or "exact.example.com"
	Port          string // Port if hasPort is true
	BucketName    string // Explicit bucket name mapping (empty means use subdomain)
	extractorFunc func(*Host, string) (string, bool)
}

// GetBucketMapping retrieves the bucket name for a given host if a mapping exists.
func (h *Host) GetBucketMapping(host string) (string, bool) {
	return h.extractorFunc(h, host)
}

type VirtualHostConfig struct {
	Hosts []Host // List of wildcard patterns for pattern matching
}

// GetBucketMapping retrieves the bucket name for a given host by checking all configured virtual hosts.
func (vh *VirtualHostConfig) GetBucketMapping(host string) (string, bool) {
	for _, h := range vh.Hosts {
		bucket, ok := h.GetBucketMapping(host)
		if ok {
			return bucket, true
		}
	}
	return "", false
}

// isRFC1034Label reports whether r is a valid RFC 1034 label character (letter, digit, or hyphen).
func isRFC1034Label(r byte) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-'
}

// validateHostName validates that the host name follows RFC 1034 naming rules.
func validateHostName(v string) bool {
	i := 0
	s := 0
outer:
	for {
		for {
			if i >= len(v) {
				break outer
			}
			if v[i] == '.' {
				if s == i {
					// consecutive dots
					return false
				}
				break
			}
			if !isRFC1034Label(v[i]) {
				return false
			}
			i++
		}
		i++
		s = i
	}
	return true
}

// maybeSplitHostAndPort splits a host:port string, handling IPv6 addresses and validation.
func maybeSplitHostAndPort(v string) (host, port string, err error) {
	i := strings.IndexByte(v, ':')
	if i < 0 {
		return v, "", nil
	}
	if i == 0 {
		return "", "", fmt.Errorf("empty host name")
	}
	if i+1 >= len(v) {
		return "", "", fmt.Errorf("empty port")
	}
	return v[:i], v[i+1:], nil
}

// wildcardExtractor extracts bucket name from a host using wildcard pattern matching.
func wildcardExtractor(h *Host, name string) (string, bool) {
	host, port, err := maybeSplitHostAndPort(name)
	if err != nil {
		return "", false
	}
	if h.Port != "" && h.Port != port {
		return "", false
	}
	if len(host) < len(h.Suffix)+1 {
		return "", false
	}
	if host[len(host)-len(h.Suffix):] != h.Suffix {
		return "", false
	}
	// If explicit bucket mapping provided, use it; otherwise extract subdomain
	if h.BucketName != "" {
		return h.BucketName, true
	}
	bucket := host[:len(host)-len(h.Suffix)]
	if strings.IndexByte(bucket, '.') >= 0 {
		return "", false
	}
	return bucket, true
}

func handleWild(ctx *Context, dst *Host, hostPort string) {
	host, port, err := maybeSplitHostAndPort(hostPort)
	if err != nil {
		ctx.Append(err)
		return
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		ctx.Append("empty host not allowed")
		return
	}
	// Basic validation - host should not start/end with dots or contain invalid characters
	if host[0] == '.' {
		if !validateHostName(host[1:]) {
			ctx.Append("invalid host name: %s", host)
		}
		dst.Suffix = host
	} else if len(host) > 2 && host[0] == '*' {
		if host[1] != '.' {
			ctx.Append("invalid pattern: %s", host)
		} else {
			if !validateHostName(host[2:]) {
				ctx.Append("invalid host name: %s", host)
			}
			dst.Suffix = host[1:]
		}
	} else {
		// Exact host without bucket mapping - this entry never matches
		// (falls through to path-style resolution)
		if !validateHostName(host) {
			ctx.Append("invalid host name: %s", host)
		}
		dst.Suffix = host
		dst.Port = port
		dst.extractorFunc = func(h *Host, name string) (string, bool) {
			return "", false // Never match - fall through to path-style
		}
		return
	}
	dst.Port = port
	dst.extractorFunc = func(h *Host, name string) (string, bool) {
		host, port, err := maybeSplitHostAndPort(name)
		if err != nil {
			return "", false
		}
		if h.Port != "" && h.Port != port {
			return "", false
		}
		if len(host) < len(h.Suffix)+1 {
			return "", false
		}
		if host[len(host)-len(h.Suffix):] != h.Suffix {
			return "", false
		}
		bucket := host[:len(host)-len(h.Suffix)]
		if strings.IndexByte(bucket, '.') >= 0 {
			return "", false
		}
		return bucket, true
	}
}

// exactExtractor extracts bucket name from a host using exact host matching.
func exactExtractor(h *Host, host string) (string, bool) {
	host, port, err := maybeSplitHostAndPort(host)
	if err != nil {
		return "", false
	}
	if h.Port != "" && h.Port != port {
		return "", false
	}
	if h.Suffix != host {
		return "", false
	}
	return h.BucketName, true
}

func handleExactMapping(ctx *Context, dst *Host, hostPort string, maybeBucket any) {
	host, port, err := maybeSplitHostAndPort(hostPort)
	if err != nil {
		ctx.Append("invalid host name: %w", err)
	}
	bucket, ok := maybeBucket.(string)
	if !ok {
		ctx.Append("bucket name must be a string, got %T", maybeBucket)
	}
	// Check if this is a wildcard pattern
	if len(host) > 2 && host[0] == '*' && host[1] == '.' {
		if !validateHostName(host[2:]) {
			ctx.Append("invalid host name: %s", host)
		}
		dst.Suffix = host[1:] // Store ".s3.local" from "*.s3.local"
		dst.Port = port
		dst.BucketName = bucket
		dst.extractorFunc = wildcardExtractor
	} else {
		if !validateHostName(host) {
			ctx.Append("invalid host name: %s", host)
		}
		dst.Suffix = host
		dst.Port = port
		dst.BucketName = bucket
		dst.extractorFunc = exactExtractor
	}
}

// populateHostEntryFromIR populates a Host entry from its intermediate representation.
// It handles string entries like "localhost" or wildcard patterns like "*.example.com",
// as well as map entries like {"localhost": "my-bucket"} for explicit host-to-bucket mappings.
func populateHostEntryFromIR(ctx *Context, dst *Host, entry any) {
	switch v := entry.(type) {
	case string:
		handleWild(ctx, dst, v)
	case map[string]any:
		if len(v) != 1 {
			ctx.Append("host mapping must have exactly one key-value pair, got %d", len(v))
		}
		var hostPort string
		var val any
		for hostPort, val = range v {
		}
		handleExactMapping(ctx, dst, hostPort, val)
	case map[any]any:
		if len(v) != 1 {
			ctx.Append("host mapping must have exactly one key-value pair, got %d", len(v))
		}
		var maybeHostPort any
		var val any
		for maybeHostPort, val = range v {
		}
		var hostPort string
		hostPort, ok := maybeHostPort.(string)
		if !ok {
			ctx.Append("invalid type for the host name: %T", maybeHostPort)
			return
		}
		handleExactMapping(ctx, dst, hostPort, val)
	default:
		ctx.Append("invalid host entry type: %T (expected string or map)", entry)
	}
}

// buildVirtualHostConfigFromIR constructs virtual host configuration from its intermediate representation.
func buildVirtualHostConfigFromIR(ctx *Context, src *ir.VirtualHostConfig) *VirtualHostConfig {
	cfg := new(VirtualHostConfig)

	ctx = ctx.Enter("Hosts")

	hosts := make([]Host, len(src.Hosts))
	for i, entry := range src.Hosts {
		populateHostEntryFromIR(ctx.EnterIndex(i), &hosts[i], entry)
	}

	// Sort hosts by specificity:
	// 1. Hosts with port come before hosts without port (for same base hostname)
	// 2. Exact hosts come before wildcard hosts
	slices.SortStableFunc(hosts, func(a, b Host) int {
		// First, compare by whether they have a port (hosts with port are more specific)
		aHasPort := a.Port != ""
		bHasPort := b.Port != ""
		if aHasPort != bHasPort {
			if aHasPort {
				return -1 // a comes first
			}
			return 1 // b comes first
		}
		// Then, compare by suffix length (longer suffix = more specific)
		return -cmp.Compare(len(a.Suffix), len(b.Suffix))
	})

	// Store the parsed hosts in the config
	cfg.Hosts = hosts

	return cfg
}
