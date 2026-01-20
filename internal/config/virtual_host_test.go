package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestVirtualHostConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		config         ir.VirtualHostConfig
		host           string
		expectedBucket string
		expectedOK     bool
	}{
		{
			name: "virtual hosts disabled",
			config: ir.VirtualHostConfig{
				Hosts: nil,
			},
			host:           "localhost",
			expectedBucket: "",
			expectedOK:     false,
		},
		{
			name: "exact host match with bucket mapping",
			config: ir.VirtualHostConfig{
				Hosts: []any{
					map[string]any{"localhost": "my-bucket"},
				},
			},
			host:           "localhost",
			expectedBucket: "my-bucket",
			expectedOK:     true,
		},
		{
			name: "exact host match with port",
			config: ir.VirtualHostConfig{
				Hosts: []any{
					map[string]any{"localhost": "my-bucket", "localhost:8080": "my-bucket-8080"},
				},
			},
			host:           "localhost:8080",
			expectedBucket: "my-bucket-8080",
			expectedOK:     true,
		},
		{
			name: "wildcard match - subdomain as bucket",
			config: ir.VirtualHostConfig{
				Hosts: []any{".localhost"},
			},
			host:           "mybucket.localhost",
			expectedBucket: "mybucket",
			expectedOK:     true,
		},
		{
			name: "virtual hosts enabled, no match",
			config: ir.VirtualHostConfig{
				Hosts: []any{
					map[string]any{"localhost": "my-bucket"},
				},
			},
			host:           "example.com",
			expectedBucket: "",
			expectedOK:     false,
		},
		{
			name:           "no virtual host config",
			config:         ir.VirtualHostConfig{},
			host:           "localhost",
			expectedBucket: "",
			expectedOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := buildVirtualHostConfigFromIR(NewContext(), &tt.config)
			bucket, ok := checker.GetBucketMapping(tt.host)
			assert.Equal(t, tt.expectedOK, ok)
			assert.Equal(t, tt.expectedBucket, bucket)
		})
	}
}

func TestVirtualHostConfigParsing(t *testing.T) {
	t.Parallel()
	cfg := ir.VirtualHostConfig{
		Hosts: []any{
			// Only hosts with explicit bucket mappings are treated as virtual hosts
			map[string]any{"localhost": "my-bucket"},
			map[string]any{"localhost:8080": "my-bucket-8080"},
			map[string]any{"example.com:443": "secure-bucket"},
			"*.s3.localhost", // Wildcard extracts subdomain as bucket
		},
	}

	ctx := NewContext()
	parsed := buildVirtualHostConfigFromIR(ctx, &cfg)
	assert.NoError(t, ctx.Errors())

	// Check that exactHosts map was created correctly with bucket names
	expectedBuckets := map[string]string{
		"localhost":       "my-bucket",
		"localhost:8080":  "my-bucket-8080",
		"example.com:443": "secure-bucket",
	}

	assert.NotNil(t, parsed.Hosts)
	for _, host := range parsed.Hosts {
		key := host.Suffix
		if host.Port != "" {
			key = host.Suffix + ":" + host.Port
		}
		if expected, ok := expectedBuckets[key]; ok {
			assert.Equal(t, expected, host.BucketName, "bucket for host %v", host)
		}
	}

	// Test the checker interface
	testCases := []struct {
		host           string
		expectedBucket string
		expectedOK     bool
		desc           string
	}{
		{"localhost", "my-bucket", true, "exact host match"},
		{"localhost:8080", "my-bucket-8080", true, "exact host with port"},
		{"localhost:9999", "my-bucket", true, "host with any port matches localhost"},
		{"example.com:443", "secure-bucket", true, "exact host with port"},
		{"example.com:80", "", false, "host with different port doesn't match example.com:443"},
		{"other.com", "", false, "unknown domain"},
		{"mybucket.s3.localhost", "mybucket", true, "wildcard extracts subdomain"},
		{"mybucket.s3.localhost:8080", "mybucket", true, "wildcard with port extracts subdomain"},
	}

	for _, tc := range testCases {
		bucket, ok := parsed.GetBucketMapping(tc.host)
		assert.Equal(t, tc.expectedOK, ok, tc.desc)
		assert.Equal(t, tc.expectedBucket, bucket, tc.desc)
	}
}

func TestWildcardVirtualHosts(t *testing.T) {
	t.Parallel()
	cfg := ir.VirtualHostConfig{
		Hosts: []any{
			"*.example.com",
			"*.api.example.com:8080",
			map[string]any{"exact.com": "exact-bucket"}, // Explicit mapping required
		},
	}

	checker := buildVirtualHostConfigFromIR(NewContext(), &cfg)

	// Test the checker interface with wildcard patterns
	testCases := []struct {
		host           string
		expectedBucket string
		expectedOK     bool
		desc           string
	}{
		// Exact matches with explicit bucket mapping
		{"exact.com", "exact-bucket", true, "exact host with explicit mapping"},
		{"exact.com:443", "exact-bucket", true, "exact host with port"},

		// Wildcard matches for *.example.com (subdomain as bucket)
		{"api.example.com", "api", true, "wildcard extracts subdomain"},
		{"www.example.com", "www", true, "wildcard extracts www"},
		{"mail.example.com", "mail", true, "wildcard extracts mail"},
		{"api.example.com:443", "api", true, "wildcard with port extracts subdomain"},

		// Wildcard matches for *.api.example.com:8080 (subdomain as bucket)
		{"v1.api.example.com:8080", "v1", true, "wildcard with specific port"},
		{"v2.api.example.com:8080", "v2", true, "wildcard with specific port"},
		{"v1.api.example.com:443", "", false, "wildcard with wrong port"},
		{"v1.api.example.com", "", false, "wildcard with missing required port"},

		// Non-matches
		{"example.com", "", false, "base domain should not match wildcard"},
		{"notexample.com", "", false, "different domain"},
		{"sub.sub.example.com", "", false, "multi-level subdomain should not match"},
	}

	for _, tc := range testCases {
		bucket, ok := checker.GetBucketMapping(tc.host)
		assert.Equal(t, tc.expectedOK, ok, tc.desc)
		assert.Equal(t, tc.expectedBucket, bucket, tc.desc)
	}
}

func TestBucketMappingExplicit(t *testing.T) {
	t.Parallel()
	cfg := ir.VirtualHostConfig{
		Hosts: []any{
			"localhost",                                    // string without mapping: falls through to path-style
			map[string]any{"api.local": "my-bucket"},       // map: bucket = "my-bucket"
			map[string]any{"*.s3.local": "default-bucket"}, // wildcard with mapping
			"*.s3.example.com",                             // wildcard without mapping (use subdomain)
		},
	}

	checker := buildVirtualHostConfigFromIR(NewContext(), &cfg)

	testCases := []struct {
		host           string
		expectedBucket string
		expectedOK     bool
		desc           string
	}{
		// String host without explicit mapping - falls through to path-style (no bucket mapping)
		{"localhost", "", false, "localhost without mapping falls through to path-style"},
		{"localhost:8080", "", false, "localhost with port falls through to path-style"},

		// Map host with explicit bucket mapping
		{"api.local", "my-bucket", true, "api.local maps to my-bucket"},
		{"api.local:3000", "my-bucket", true, "api.local with port maps to my-bucket"},

		// Wildcard with explicit bucket mapping
		{"mybucket.s3.local", "default-bucket", true, "wildcard with explicit mapping"},
		{"anybucket.s3.local", "default-bucket", true, "any subdomain uses explicit mapping"},

		// Wildcard without explicit mapping - subdomain is used as bucket
		{"mybucket.s3.example.com", "mybucket", true, "wildcard extracts subdomain"},
		{"anotherbucket.s3.example.com", "anotherbucket", true, "wildcard extracts different subdomain"},

		// No match
		{"unknown.host", "", false, "unknown host"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			bucket, ok := checker.GetBucketMapping(tc.host)
			assert.Equal(t, tc.expectedOK, ok, "GetBucketMapping ok")
			assert.Equal(t, tc.expectedBucket, bucket, "GetBucketMapping bucket")
		})
	}
}

func TestHostEntryParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		entry          any
		expectedHost   string
		expectedBucket string
		expectError    bool
	}{
		{
			name:           "string entry",
			entry:          "localhost",
			expectedHost:   "localhost",
			expectedBucket: "",
			expectError:    false,
		},
		{
			name:           "map entry with string interface",
			entry:          map[string]any{"api.local": "my-bucket"},
			expectedHost:   "api.local",
			expectedBucket: "my-bucket",
			expectError:    false,
		},
		{
			name:           "map entry with interface interface",
			entry:          map[any]any{"api.local": "my-bucket"},
			expectedHost:   "api.local",
			expectedBucket: "my-bucket",
			expectError:    false,
		},
		{
			name:        "invalid type",
			entry:       12345,
			expectError: true,
		},
		{
			name:        "map with multiple keys",
			entry:       map[string]any{"host1": "bucket1", "host2": "bucket2"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var host Host
			ctx := NewContext()
			populateHostEntryFromIR(ctx, &host, tt.entry)
			if tt.expectError {
				assert.Error(t, ctx.Errors())
			} else {
				assert.NoError(t, ctx.Errors())
				assert.Equal(t, tt.expectedHost, host.Suffix)
				assert.Equal(t, tt.expectedBucket, host.BucketName)
			}
		})
	}
}
