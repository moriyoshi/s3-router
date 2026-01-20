package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextAppendString(t *testing.T) {
	t.Parallel()
	ctx := NewContext()

	ctx.Append("error message %s", "test")

	err := ctx.Errors()
	assert.Error(t, err)
}

func TestContextAppendError(t *testing.T) {
	t.Parallel()
	ctx := NewContext()
	baseErr := errors.New("base error")

	ctx.Append(baseErr)

	err := ctx.Errors()
	assert.Error(t, err)
	assert.True(t, errors.Is(err, baseErr))
}

func TestContextEnter(t *testing.T) {
	t.Parallel()
	ctx := NewContext()
	enterCtx := ctx.Enter("field1")

	enterCtx.Append("error in field")

	err := ctx.Errors()
	assert.Error(t, err)
}

func TestContextEnterNested(t *testing.T) {
	t.Parallel()
	ctx := NewContext()
	nested := ctx.Enter("parent").Enter("child")

	nested.Append("error in nested field")

	err := ctx.Errors()
	assert.Error(t, err)
}

func TestContextEnterIndex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		index any
	}{
		{
			name:  "integer index",
			index: 5,
		},
		{
			name:  "string key",
			index: "key1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			indexCtx := ctx.EnterIndex(tt.index)
			indexCtx.Append("error at index")

			err := ctx.Errors()
			assert.Error(t, err)
		})
	}
}

func TestContextNoErrors(t *testing.T) {
	t.Parallel()
	ctx := NewContext()

	err := ctx.Errors()
	assert.NoError(t, err)
}

func TestContextMultipleErrors(t *testing.T) {
	t.Parallel()
	ctx := NewContext()

	ctx.Append("error 1")
	ctx.Enter("field1").Append("error 2")
	ctx.Enter("field2").Append("error 3")

	err := ctx.Errors()
	assert.Error(t, err)
}

func TestNewContext(t *testing.T) {
	t.Parallel()
	ctx := NewContext()

	assert.NotNil(t, ctx)
	assert.NoError(t, ctx.Errors())
}

func TestContextErrorsReturnsNilWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := NewContext()

	err := ctx.Errors()
	assert.Nil(t, err)
}
