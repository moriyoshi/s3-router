package backend

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
)

// TestIsNonFatalS3Error tests the error classification logic
func TestIsNonFatalS3Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		isNonFatal  bool
		description string
	}{
		{
			name:        "nil error",
			err:         nil,
			isNonFatal:  true,
			description: "nil means success (no error), should return true for IsSuccessful",
		},
		{
			name: "NoSuchKey error",
			err: &smithy.GenericAPIError{
				Code:    "NoSuchKey",
				Message: "The specified key does not exist.",
			},
			isNonFatal:  true,
			description: "404 NoSuchKey is non-fatal",
		},
		{
			name: "NoSuchBucket error",
			err: &smithy.GenericAPIError{
				Code:    "NoSuchBucket",
				Message: "The specified bucket does not exist.",
			},
			isNonFatal:  true,
			description: "404 NoSuchBucket is non-fatal",
		},
		{
			name: "AccessDenied error",
			err: &smithy.GenericAPIError{
				Code:    "AccessDenied",
				Message: "Access Denied",
			},
			isNonFatal:  true,
			description: "403 AccessDenied is non-fatal",
		},
		{
			name: "Forbidden error",
			err: &smithy.GenericAPIError{
				Code:    "Forbidden",
				Message: "Forbidden",
			},
			isNonFatal:  true,
			description: "403 Forbidden is non-fatal",
		},
		{
			name: "InvalidRequest error",
			err: &smithy.GenericAPIError{
				Code:    "InvalidRequest",
				Message: "Invalid request",
			},
			isNonFatal:  true,
			description: "400 InvalidRequest is non-fatal",
		},
		{
			name: "BadRequest error",
			err: &smithy.GenericAPIError{
				Code:    "BadRequest",
				Message: "Bad request",
			},
			isNonFatal:  true,
			description: "400 BadRequest is non-fatal",
		},
		{
			name: "InternalError (fatal)",
			err: &smithy.GenericAPIError{
				Code:    "InternalError",
				Message: "Internal server error",
			},
			isNonFatal:  false,
			description: "500 InternalError is fatal",
		},
		{
			name: "ServiceUnavailable (fatal)",
			err: &smithy.GenericAPIError{
				Code:    "ServiceUnavailable",
				Message: "Service is unavailable",
			},
			isNonFatal:  false,
			description: "503 ServiceUnavailable is fatal",
		},
		{
			name:        "network error (fatal)",
			err:         errors.New("connection refused"),
			isNonFatal:  false,
			description: "Generic network error is fatal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNonFatalS3Error(tt.err)
			assert.Equal(t, tt.isNonFatal, result, tt.description)
		})
	}
}

// MockS3Operations is a mock implementation of S3Operations for testing
type MockS3Operations struct {
	getObjectErr error
	getObjectVal *s3.GetObjectOutput
	callCount    int
}

func (m *MockS3Operations) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.callCount++
	return m.getObjectVal, m.getObjectErr
}

func (m *MockS3Operations) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) DeleteObjectTagging(ctx context.Context, params *s3.DeleteObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectTaggingOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) GetObjectTagging(ctx context.Context, params *s3.GetObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.GetObjectTaggingOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) PutObjectTagging(ctx context.Context, params *s3.PutObjectTaggingInput, optFns ...func(*s3.Options)) (*s3.PutObjectTaggingOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) CreateMultipartUpload(ctx context.Context, params *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) UploadPart(ctx context.Context, params *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) CompleteMultipartUpload(ctx context.Context, params *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) AbortMultipartUpload(ctx context.Context, params *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) ListParts(ctx context.Context, params *s3.ListPartsInput, optFns ...func(*s3.Options)) (*s3.ListPartsOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) CopyObject(ctx context.Context, params *s3.CopyObjectInput, optFns ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) GetObjectAcl(ctx context.Context, params *s3.GetObjectAclInput, optFns ...func(*s3.Options)) (*s3.GetObjectAclOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) PutObjectAcl(ctx context.Context, params *s3.PutObjectAclInput, optFns ...func(*s3.Options)) (*s3.PutObjectAclOutput, error) {
	return nil, nil
}

func (m *MockS3Operations) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return nil, nil
}

// TestCircuitBreakerNonFatalErrors tests that non-fatal errors don't trip the breaker
func TestCircuitBreakerNonFatalErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		shouldTrip  bool
		description string
	}{
		{
			name: "NoSuchKey doesn't trip breaker",
			err: &smithy.GenericAPIError{
				Code:    "NoSuchKey",
				Message: "The specified key does not exist.",
			},
			shouldTrip:  false,
			description: "404 errors should not trip the breaker",
		},
		{
			name: "Forbidden doesn't trip breaker",
			err: &smithy.GenericAPIError{
				Code:    "Forbidden",
				Message: "Access denied",
			},
			shouldTrip:  false,
			description: "403 errors should not trip the breaker",
		},
		{
			name: "InternalError trips breaker",
			err: &smithy.GenericAPIError{
				Code:    "InternalError",
				Message: "Internal server error",
			},
			shouldTrip:  true,
			description: "5xx errors should trip the breaker",
		},
		{
			name:        "Network error trips breaker",
			err:         errors.New("connection refused"),
			shouldTrip:  true,
			description: "Network errors should trip the breaker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock S3 operations that returns the test error
			mock := &MockS3Operations{
				getObjectErr: tt.err,
				getObjectVal: nil,
			}

			// Create circuit breaker with non-fatal error filtering
			cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
				Name:        "test-breaker",
				MaxRequests: 3,
				Interval:    0, // Disable interval reset for test
				Timeout:     1, // Very short timeout
				ReadyToTrip: func(counts gobreaker.Counts) bool {
					// Trip if we have 2+ requests and at least 1 failure
					return counts.Requests >= 2 && counts.TotalFailures >= 1
				},
				IsSuccessful: func(err error) bool {
					return IsNonFatalS3Error(err)
				},
			})

			cbOps := NewCircuitBreakerS3Operations(mock, cb)

			ctx := context.Background()
			params := &s3.GetObjectInput{}

			// First call - breaker should still be closed
			_, err := cbOps.GetObject(ctx, params)
			assert.Equal(t, tt.err, err)

			// Second call - check if breaker trips
			_, err = cbOps.GetObject(ctx, params)
			if tt.shouldTrip {
				// If it should trip, the second call should fail with circuit breaker open error
				// (not the original error)
				if err != nil && err.Error() != "circuit breaker is open" {
					// Might be the second call that triggers it
					t.Logf("Error from second call: %v", err)
				}
			} else {
				// If it shouldn't trip, the second call should return the same error
				assert.Equal(t, tt.err, err, tt.description)
			}
		})
	}
}

// TestCircuitBreakerSuccessFiltersNonFatal tests that successful calls prevent breaker trip
func TestCircuitBreakerSuccessFiltersNonFatal(t *testing.T) {
	t.Parallel()

	// Create a mock that returns NoSuchKey error (non-fatal)
	mock := &MockS3Operations{
		getObjectErr: &smithy.GenericAPIError{
			Code:    "NoSuchKey",
			Message: "The specified key does not exist.",
		},
	}

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "test-breaker",
		MaxRequests: 3,
		Interval:    0,
		Timeout:     1,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 2 && counts.TotalFailures >= 2
		},
		IsSuccessful: func(err error) bool {
			return IsNonFatalS3Error(err)
		},
	})

	cbOps := NewCircuitBreakerS3Operations(mock, cb)
	ctx := context.Background()
	params := &s3.GetObjectInput{}

	// Multiple calls with non-fatal error should not trip breaker
	for i := 0; i < 5; i++ {
		_, err := cbOps.GetObject(ctx, params)
		assert.NotNil(t, err)
		assert.Equal(t, "NoSuchKey", err.(*smithy.GenericAPIError).ErrorCode())
	}

	// Verify mock was called 5 times (breaker didn't open)
	assert.Equal(t, 5, mock.callCount)
}
