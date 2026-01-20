package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestErrorFormatting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		loc         ir.CompositeTypePath
		msg         string
		inner       error
		expectError bool
		contains    string
	}{
		{
			name: "simple error with message",
			loc: ir.CompositeTypePath{
				ir.FieldOrIndex{Field: "test"},
			},
			msg:         "test message",
			expectError: true,
			contains:    "test message",
		},
		{
			name: "error without message but with inner",
			loc: ir.CompositeTypePath{
				ir.FieldOrIndex{Field: "test"},
			},
			inner:       errors.New("inner error"),
			expectError: true,
			contains:    "inner error",
		},
		{
			name: "nested field path",
			loc: ir.CompositeTypePath{
				ir.FieldOrIndex{Field: "parent"},
				ir.FieldOrIndex{Field: "child"},
			},
			msg:         "nested error",
			expectError: true,
			contains:    "nested error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &Error{
				loc:   tt.loc,
				msg:   tt.msg,
				inner: tt.inner,
			}

			errorStr := err.Error()
			assert.NotEmpty(t, errorStr)
			if tt.contains != "" {
				assert.Contains(t, errorStr, tt.contains)
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	t.Parallel()
	innerErr := errors.New("inner error")
	err := &Error{
		loc:   ir.CompositeTypePath{},
		inner: innerErr,
	}

	unwrapped := err.Unwrap()
	assert.Equal(t, innerErr, unwrapped)
}

func TestErrorsUnwrap(t *testing.T) {
	t.Parallel()
	errs := Errors{
		{msg: "error 1"},
		{msg: "error 2"},
		{msg: "error 3"},
	}

	unwrapped := errs.Unwrap()
	assert.Len(t, unwrapped, 3)
	for _, err := range unwrapped {
		assert.NotNil(t, err)
		assert.IsType(t, &Error{}, err)
	}
}

func TestErrorsError(t *testing.T) {
	t.Parallel()
	errs := Errors{
		{msg: "error 1"},
		{msg: "error 2"},
	}

	errorStr := errs.Error()
	assert.NotEmpty(t, errorStr)
	assert.Contains(t, errorStr, "error 1")
	assert.Contains(t, errorStr, "error 2")
	assert.Contains(t, errorStr, " / ")
}

func TestErrorsSingleError(t *testing.T) {
	t.Parallel()
	errs := Errors{
		{msg: "single error"},
	}

	errorStr := errs.Error()
	assert.Contains(t, errorStr, "single error")
	assert.NotContains(t, errorStr, " / ")
}

func TestErrorsEmpty(t *testing.T) {
	t.Parallel()
	errs := Errors{}

	errorStr := errs.Error()
	assert.Empty(t, errorStr)
}

func TestErrorInterface(t *testing.T) {
	t.Parallel()
	var e interface{} = &Error{msg: "test"}
	_, ok := e.(error)
	assert.True(t, ok)

	var es interface{} = Errors{{msg: "test"}}
	_, ok = es.(error)
	assert.True(t, ok)
}

func TestErrorsInterface(t *testing.T) {
	t.Parallel()
	errs := Errors{
		{msg: "error 1"},
	}

	var e error = errs
	assert.NotNil(t, e)

	unwrappedMulti := errors.Unwrap(e)
	if unwrappedMulti != nil {
		multiErr, ok := unwrappedMulti.(interface{ Unwrap() []error })
		assert.True(t, ok)
		errorList := multiErr.Unwrap()
		assert.Len(t, errorList, 1)
	}
}
