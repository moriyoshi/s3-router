package backend

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/sony/gobreaker"
)

// S3Operations defines the S3 operations used by the proxy executor.
// This interface allows for mocking and decorator patterns like circuit breaking.
type S3Operations interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	DeleteObjectTagging(ctx context.Context, params *s3.DeleteObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectTaggingOutput, error)
	GetObjectTagging(ctx context.Context, params *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error)
	PutObjectTagging(ctx context.Context, params *s3.PutObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error)
	CreateMultipartUpload(ctx context.Context, params *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	UploadPart(ctx context.Context, params *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(ctx context.Context, params *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(ctx context.Context, params *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
	ListParts(ctx context.Context, params *s3.ListPartsInput, optFns ...func(*s3.Options)) (*s3.ListPartsOutput, error)
	CopyObject(ctx context.Context, params *s3.CopyObjectInput, optFns ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	GetObjectAcl(ctx context.Context, params *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error)
	PutObjectAcl(ctx context.Context, params *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// IsNonFatalS3Error determines if an error is a non-fatal S3 client error that should not
// trigger the circuit breaker. Non-fatal errors include:
// - 404 NoSuchKey (object doesn't exist)
// - 403 Forbidden (access denied, but S3 is operational)
// - 400 BadRequest (malformed request, but S3 is operational)
// - Other 4xx errors (client errors, not backend failures)
//
// Fatal errors that should trip the breaker:
// - 5xx server errors
// - Network/connectivity errors
// - Timeouts
// - Service unavailable (503)
func IsNonFatalS3Error(err error) bool {
	if err == nil {
		return false
	}

	var ae smithy.APIError
	if errors.As(err, &ae) {
		code := ae.ErrorCode()
		switch code {
		case "NoSuchKey", "NoSuchBucket", "NotFound":
			return true // 404 - object/bucket doesn't exist
		case "AccessDenied", "Forbidden":
			return true // 403 - forbidden
		case "InvalidRequest", "BadRequest":
			return true // 400 - bad request
		default:
			// If the API error isn't explicitly mapped, treat as fatal
			return false
		}
	}

	// Non-API errors (network, timeout, etc.) are fatal
	return false
}

// CircuitBreakerS3Operations wraps an S3Operations implementation with a circuit breaker.
// Failures are tracked and the breaker will reject requests if the failure rate is too high.
// Non-fatal S3 errors (4xx client errors) do not count toward circuit breaker failures.
type CircuitBreakerS3Operations struct {
	inner   S3Operations
	breaker *gobreaker.CircuitBreaker
}

// NewCircuitBreakerS3Operations creates a new circuit-breaker-backed S3 operations wrapper.
func NewCircuitBreakerS3Operations(inner S3Operations, cb *gobreaker.CircuitBreaker) *CircuitBreakerS3Operations {
	return &CircuitBreakerS3Operations{
		inner:   inner,
		breaker: cb,
	}
}

// GetObject delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.GetObject(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.GetObjectOutput), nil
}

// HeadObject delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.HeadObject(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.HeadObjectOutput), nil
}

// PutObject delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.PutObject(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.PutObjectOutput), nil
}

// DeleteObject delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.DeleteObject(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.DeleteObjectOutput), nil
}

// DeleteObjects delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.DeleteObjects(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.DeleteObjectsOutput), nil
}

// DeleteObjectTagging delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) DeleteObjectTagging(ctx context.Context, params *s3.DeleteObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectTaggingOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.DeleteObjectTagging(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.DeleteObjectTaggingOutput), nil
}

// GetObjectTagging delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) GetObjectTagging(ctx context.Context, params *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.GetObjectTagging(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.GetObjectTaggingOutput), nil
}

// PutObjectTagging delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) PutObjectTagging(ctx context.Context, params *s3.PutObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.PutObjectTagging(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.PutObjectTaggingOutput), nil
}

// CreateMultipartUpload delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) CreateMultipartUpload(ctx context.Context, params *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.CreateMultipartUpload(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.CreateMultipartUploadOutput), nil
}

// UploadPart delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) UploadPart(ctx context.Context, params *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.UploadPart(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.UploadPartOutput), nil
}

// CompleteMultipartUpload delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) CompleteMultipartUpload(ctx context.Context, params *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.CompleteMultipartUpload(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.CompleteMultipartUploadOutput), nil
}

// AbortMultipartUpload delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) AbortMultipartUpload(ctx context.Context, params *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.AbortMultipartUpload(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.AbortMultipartUploadOutput), nil
}

// ListParts delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) ListParts(ctx context.Context, params *s3.ListPartsInput, optFns ...func(*s3.Options)) (*s3.ListPartsOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.ListParts(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.ListPartsOutput), nil
}

// CopyObject delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) CopyObject(ctx context.Context, params *s3.CopyObjectInput, optFns ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.CopyObject(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.CopyObjectOutput), nil
}

// GetObjectAcl delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) GetObjectAcl(ctx context.Context, params *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.GetObjectAcl(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.GetObjectAclOutput), nil
}

// PutObjectAcl delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) PutObjectAcl(ctx context.Context, params *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.PutObjectAcl(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.PutObjectAclOutput), nil
}

// ListObjectsV2 delegates to the inner implementation with circuit breaker protection.
func (c *CircuitBreakerS3Operations) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	result, err := c.breaker.Execute(func() (any, error) {
		return c.inner.ListObjectsV2(ctx, params, optFns...)
	})
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	return result.(*s3.ListObjectsV2Output), nil
}
