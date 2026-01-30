package backend

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyendpoints "github.com/aws/smithy-go/endpoints"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/moriyoshi/s3-router/internal/template"
)

type EndpointResolver struct {
	template    *template.Template
	awsResolver s3.EndpointResolverV2
}

// ResolvedEndpoint represents a resolved S3 endpoint with URL and headers.
// It also implements s3.EndpointResolverV2 to be used directly with the AWS SDK.
type ResolvedEndpoint struct {
	URL     *url.URL
	Headers http.Header
}

type EndpointResolverParams struct {
	Bucket            string
	Key               string
	Region            string
	UseFIPS           bool
	UseGlobalEndpoint bool
	UseDualStack      bool
	Accelerate        bool
	UsePathStyle      bool
}

// ResolveEndpointURL resolves an endpoint URL for a given bucket and key.
// If a template is provided, it uses template-based resolution with placeholders.
// If template is empty, it uses the AWS SDK V2's EndpointResolverV2 to get the default endpoint.
//
// Supported template placeholders:
// - ${bucket} or $bucket - bucket name
// - ${region} or $region - AWS region
// - ${key} or $key - URL-encoded key (preserving /)
// - ${foo:-default} - with default values
//
// If the template doesn't contain a scheme (http://, https://), https:// is prepended.
func (resolver *EndpointResolver) ResolveEndpointURL(ctx context.Context, params EndpointResolverParams) (*ResolvedEndpoint, error) {
	logger := observability.GetLoggerFromContext(ctx)
	if logger != nil {
		logger = logger.With(
			"bucket", params.Bucket,
			"region", params.Region,
			"fips", params.UseFIPS,
			"global_endpoint", params.UseGlobalEndpoint,
			"accelerate", params.Accelerate,
			"dualstack", params.UseDualStack,
			"use_path_style", params.UsePathStyle,
			"key", params.Key,
		)
	}

	if resolver.template != nil {
		// Use template-based resolution
		url, err := resolver.resolveTemplateEndpoint(params)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint URL after template resolution: %w", err)
		}
		if logger != nil {
			logger.Debug("resolved endpoint using template", "endpoint", url.String())
		}
		return &ResolvedEndpoint{
			URL:     url,
			Headers: http.Header{},
		}, nil
	}

	// Use AWS SDK's endpoint resolver
	endpoint, err := resolver.resolveSDKEndpointWithOptions(ctx, params)
	if err != nil {
		return nil, err
	}

	// Append the key to the resolved endpoint
	if params.Key != "" {
		endpointPath := endpoint.URL.EscapedPath()
		if !strings.HasSuffix(endpointPath, "/") {
			endpointPath += "/"
		}
		endpoint.URL.RawPath = endpointPath + params.Key
		// Also update Path with decoded version for consistency
		endpoint.URL.Path, _ = url.PathUnescape(endpoint.URL.RawPath)
	}

	if logger != nil {
		logger.Debug("resolved endpoint using AWS SDK's endpoint resolver", "endpoint", endpoint.URL.String())
	}
	return endpoint, nil
}

func emptyOrOne(v bool) string {
	if v {
		return "1"
	}
	return ""
}

// resolveTemplateEndpoint resolves a template-based endpoint with placeholder substitution
func (resolver *EndpointResolver) resolveTemplateEndpoint(params EndpointResolverParams) (*url.URL, error) {
	// Use the unified placeholders container
	placeholders := template.NewPlaceholders().
		SetNamed("bucket", params.Bucket).
		SetNamed("region", params.Region).
		SetNamed("fips", emptyOrOne(params.UseFIPS)).
		SetNamed("globalEndpoint", emptyOrOne(params.UseGlobalEndpoint)).
		SetNamed("accelerate", emptyOrOne(params.Accelerate)).
		SetNamed("dualStack", emptyOrOne(params.UseDualStack)).
		SetNamed("usePathStyle", emptyOrOne(params.UsePathStyle)).
		SetNamed("key", params.Key)

	endpoint, err := resolver.template.Execute(placeholders)
	if err != nil {
		return nil, fmt.Errorf("failed to execute endpoint template: %w", err)
	}

	// Auto-add https:// if no scheme present
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}

	return url.Parse(endpoint)
}

// resolveSDKEndpointWithOptions resolves endpoint with additional endpoint parameters
func (resolver *EndpointResolver) resolveSDKEndpointWithOptions(ctx context.Context, params EndpointResolverParams) (*ResolvedEndpoint, error) {
	// Prepare the endpoint parameters with all options
	s3Params := s3.EndpointParameters{
		Bucket:            &params.Bucket,
		Key:               &params.Key,
		Region:            &params.Region,
		UseFIPS:           &params.UseFIPS,
		UseGlobalEndpoint: &params.UseGlobalEndpoint,
		UseDualStack:      &params.UseDualStack,
		Accelerate:        &params.Accelerate,
		ForcePathStyle:    &params.UsePathStyle,
	}

	// Resolve the endpoint
	endpoint, err := resolver.awsResolver.ResolveEndpoint(ctx, s3Params)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve endpoint using SDK V2 with options: %w", err)
	}

	// Convert headers from map to http.Header
	headers := http.Header{}
	for k, vv := range endpoint.Headers {
		if len(vv) > 0 {
			headers.Set(k, vv[0])
		}
	}

	return &ResolvedEndpoint{
		URL:     &endpoint.URI,
		Headers: headers,
	}, nil
}

// ResolveEndpoint implements s3.EndpointResolverV2.
// It returns the pre-resolved URL and headers.
func (resolver *EndpointResolver) ResolveEndpoint(ctx context.Context, params s3.EndpointParameters) (smithyendpoints.Endpoint, error) {
	var ourParams EndpointResolverParams
	// ourParams.Key is not populated intentinoally
	if params.Bucket != nil {
		ourParams.Bucket = *params.Bucket
	}
	if params.Region != nil {
		ourParams.Region = *params.Region
	}
	if params.UseFIPS != nil {
		ourParams.UseFIPS = *params.UseFIPS
	}
	if params.UseGlobalEndpoint != nil {
		ourParams.UseGlobalEndpoint = *params.UseGlobalEndpoint
	}
	if params.UseDualStack != nil {
		ourParams.UseDualStack = *params.UseDualStack
	}
	if params.Accelerate != nil {
		ourParams.Accelerate = *params.Accelerate
	}
	if params.ForcePathStyle != nil {
		ourParams.UsePathStyle = *params.ForcePathStyle
	}
	r, err := resolver.ResolveEndpointURL(ctx, ourParams)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	return smithyendpoints.Endpoint{
		URI:     *r.URL,
		Headers: r.Headers,
	}, nil
}

func newEndpointResolver(bcfg *config.BackendConfig) (*EndpointResolver, error) {
	var tmpl *template.Template
	var awsResolver s3.EndpointResolverV2
	if bcfg.Endpoint != "" {
		var err error
		// Parse the template into an AST
		tmpl, err = template.Parse(bcfg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template: %w", err)
		}
	} else {
		awsResolver = s3.NewDefaultEndpointResolverV2()
	}
	return &EndpointResolver{
		template:    tmpl,
		awsResolver: awsResolver,
	}, nil
}
