package backend

import (
	"context"
	"net/url"
	"testing"

	"github.com/moriyoshi/s3-router/internal/template"
	"github.com/stretchr/testify/assert"
)

func TestResolveEndpointURLWithTemplate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		template  string
		params    EndpointResolverParams
		expected  string
		shouldErr bool
	}{
		{
			name:     "virtual-host-style explicit template",
			template: "https://${bucket}.s3.${region}.amazonaws.com/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-east-1",
				Key:    "path/to/file.txt",
			},
			expected: "https://my-bucket.s3.us-east-1.amazonaws.com/path/to/file.txt",
		},
		{
			name:     "path-style template",
			template: "https://s3.${region}.amazonaws.com/${bucket}/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-west-2",
				Key:    "path/to/file.txt",
			},
			expected: "https://s3.us-west-2.amazonaws.com/my-bucket/path/to/file.txt",
		},
		{
			name:     "custom endpoint",
			template: "https://minio.example.com/${bucket}/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-east-1",
				Key:    "path/to/file.txt",
			},
			expected: "https://minio.example.com/my-bucket/path/to/file.txt",
		},
		{
			name:     "auto-add https scheme",
			template: "minio.example.com/${bucket}/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-east-1",
				Key:    "path/to/file.txt",
			},
			expected: "https://minio.example.com/my-bucket/path/to/file.txt",
		},
		{
			name:     "auto-add https scheme with region replacement",
			template: "${bucket}.s3.${region}.amazonaws.com/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "eu-west-1",
				Key:    "data.json",
			},
			expected: "https://my-bucket.s3.eu-west-1.amazonaws.com/data.json",
		},
		{
			name:     "http scheme preserved",
			template: "http://minio.example.com/${bucket}/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-east-1",
				Key:    "path/to/file.txt",
			},
			expected: "http://minio.example.com/my-bucket/path/to/file.txt",
		},
		{
			name:     "key with special characters",
			template: "https://${bucket}.s3.${region}.amazonaws.com/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-east-1",
				Key:    "path/with spaces/file&name.txt",
			},
			expected: "https://my-bucket.s3.us-east-1.amazonaws.com/path/with%20spaces/file&name.txt",
		},
		{
			name:     "bucket with special characters",
			template: "https://${bucket}.s3.${region}.amazonaws.com/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket-123",
				Region: "us-east-1",
				Key:    "file.txt",
			},
			expected: "https://my-bucket-123.s3.us-east-1.amazonaws.com/file.txt",
		},
		{
			name:     "key with multiple slashes",
			template: "https://s3.${region}.amazonaws.com/${bucket}/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "ap-northeast-1",
				Key:    "a/b/c/d/e.txt",
			},
			expected: "https://s3.ap-northeast-1.amazonaws.com/my-bucket/a/b/c/d/e.txt",
		},
		{
			name:     "template resolution returns url.URL",
			template: "https://${bucket}.s3.${region}.amazonaws.com/${key}",
			params: EndpointResolverParams{
				Bucket: "my-bucket",
				Region: "us-east-1",
				Key:    "path/to/file.txt",
			},
			expected: "https://my-bucket.s3.us-east-1.amazonaws.com/path/to/file.txt",
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse template into AST
			parsedTemplate, err := template.Parse(tt.template)
			assert.NoError(t, err)

			resolver := &EndpointResolver{
				template: parsedTemplate,
			}
			result, err := resolver.ResolveEndpointURL(ctx, tt.params)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.NotNil(t, result.URL)
			assert.Equal(t, tt.expected, result.URL.String())
			assert.NotNil(t, result.Headers)
		})
	}
}

func TestResolvedEndpointStructure(t *testing.T) {
	t.Parallel()
	// Test that ResolvedEndpoint has expected fields
	endpoint := &ResolvedEndpoint{
		URL:     &url.URL{Scheme: "https", Host: "example.com", Path: "/bucket/key"},
		Headers: map[string][]string{},
	}

	assert.NotNil(t, endpoint.URL)
	assert.NotNil(t, endpoint.Headers)
	assert.Equal(t, "https", endpoint.URL.Scheme)
	assert.Equal(t, "example.com", endpoint.URL.Host)
}

func TestEndpointResolverWithUsePathStyle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		template     string
		usePathStyle bool
		expected     string
	}{
		{
			name:         "path-style with template",
			template:     "https://s3.${region}.amazonaws.com/${bucket}/${key}",
			usePathStyle: true,
			expected:     "https://s3.us-east-1.amazonaws.com/my-bucket/path/to/file.txt",
		},
		{
			name:         "virtual-host style with template",
			template:     "https://${bucket}.s3.${region}.amazonaws.com/${key}",
			usePathStyle: false,
			expected:     "https://my-bucket.s3.us-east-1.amazonaws.com/path/to/file.txt",
		},
		{
			name:         "minio path-style endpoint",
			template:     "https://minio.example.com/${bucket}/${key}",
			usePathStyle: true,
			expected:     "https://minio.example.com/my-bucket/path/to/file.txt",
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedTemplate, err := template.Parse(tt.template)
			assert.NoError(t, err)

			resolver := &EndpointResolver{
				template: parsedTemplate,
			}

			params := EndpointResolverParams{
				Bucket:       "my-bucket",
				Region:       "us-east-1",
				Key:          "path/to/file.txt",
				UsePathStyle: tt.usePathStyle,
			}

			result, err := resolver.ResolveEndpointURL(ctx, params)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.NotNil(t, result.URL)
			assert.Equal(t, tt.expected, result.URL.String())
		})
	}
}
