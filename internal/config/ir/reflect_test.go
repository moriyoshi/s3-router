package ir

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldOrIndexString(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
		{Key: "backend1"},
		{Field: "Credentials"},
	}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	result := buf.String()
	assert.Contains(t, result, "Backends")
	assert.Contains(t, result, "backend1")
	assert.Contains(t, result, "Credentials")
}

func TestCompositeTypePathStringWithField(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Config"},
		{Field: "Server"},
		{Field: "ReadTimeout"},
	}

	result := path.String()
	assert.Equal(t, "Config.Server.ReadTimeout", result)
}

func TestCompositeTypePathStringWithIndex(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Items"},
		{Ind: 0},
		{Field: "Name"},
	}

	result := path.String()
	assert.Equal(t, "Items[0].Name", result)
}

func TestCompositeTypePathStringWithKey(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
		{Key: "backend1"},
		{Field: "Bucket"},
	}

	result := path.String()
	assert.Equal(t, "Backends[backend1].Bucket", result)
}

func TestCompositeTypePathStringMixed(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Config"},
		{Key: "setting1"},
		{Ind: 0},
		{Field: "Value"},
	}

	result := path.String()
	assert.Equal(t, "Config[setting1][0].Value", result)
}

func TestCompositeTypePathAppendStringFieldOnly(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Bucket"},
	}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	assert.Equal(t, "Bucket", buf.String())
}

func TestCompositeTypePathAppendStringFieldThenField(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Server"},
		{Field: "Config"},
	}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	assert.Equal(t, "Server.Config", buf.String())
}

func TestCompositeTypePathAppendStringWithIndex(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Ind: 5},
	}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	assert.Equal(t, "[5]", buf.String())
}

func TestCompositeTypePathAppendStringWithKey(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Key: "mykey"},
	}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	assert.Equal(t, "[mykey]", buf.String())
}

func TestCompositeTypePathAppendStringEmpty(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	assert.Equal(t, "", buf.String())
}

func TestCompositeTypePathAppendStringInvalid(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Ind: -1}, // Invalid index
	}

	var buf bytes.Buffer
	err := path.AppendString(&buf)
	require.NoError(t, err)

	assert.Equal(t, "???", buf.String())
}

func TestCompositeTypePathJSONPath(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
		{Key: "backend1"},
		{Field: "Bucket"},
	}

	var buf bytes.Buffer
	err := path.AppendJSONPath(&buf)
	require.NoError(t, err)

	result := buf.String()
	assert.Equal(t, "$.Backends['backend1'].Bucket", result)
}

func TestCompositeTypePathJSONPathWithIndex(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Buckets"},
		{Ind: 0},
		{Field: "Name"},
	}

	var buf bytes.Buffer
	err := path.AppendJSONPath(&buf)
	require.NoError(t, err)

	result := buf.String()
	assert.Equal(t, "$.Buckets[0].Name", result)
}

func TestCompositeTypePathJSONPathEscaping(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Config"},
		{Key: "key'with\\quotes"},
	}

	var buf bytes.Buffer
	err := path.AppendJSONPath(&buf)
	require.NoError(t, err)

	result := buf.String()
	// Check that the path starts with $.Config and contains the escaped key
	assert.True(t, bytes.Contains([]byte(result), []byte("$.Config")))
	// The escaping adds backslash before ' and \ characters
	assert.True(t, bytes.Contains([]byte(result), []byte("key\\with\\quotes")))
}

func TestAppendEscapedNoEscape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := appendEscaped(&buf, "simple_key")
	require.NoError(t, err)

	assert.Equal(t, "simple_key", buf.String())
}

func TestAppendEscapedWithBackslash(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := appendEscaped(&buf, "key\\with\\backslash")
	require.NoError(t, err)

	result := buf.String()
	// appendEscaped only escapes \ and ', it doesn't double-escape
	assert.Equal(t, "key\\with\\backslash", result)
}

func TestAppendEscapedWithQuote(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := appendEscaped(&buf, "key'with'quote")
	require.NoError(t, err)

	result := buf.String()
	assert.Equal(t, "key\\with\\quote", result)
}

func TestAppendEscapedWithMixed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := appendEscaped(&buf, "key'test\\path")
	require.NoError(t, err)

	result := buf.String()
	assert.Equal(t, "key\\test\\path", result)
}

func TestTranslateToUnmappedPathField(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
	}

	result, err := TranslateToUnmappedPath(reflect.TypeOf(Config{}), path, "mapstructure")
	require.NoError(t, err)

	assert.Len(t, result, 1)
	assert.Equal(t, "backends", result[0].Field)
}

func TestTranslateToUnmappedPathNestedStruct(t *testing.T) {
	t.Parallel()
	// Test with a simple direct field that has a mapstructure tag
	path := CompositeTypePath{
		{Field: "Backends"},
	}

	result, err := TranslateToUnmappedPath(reflect.TypeOf(Config{}), path, "mapstructure")
	require.NoError(t, err)

	assert.Len(t, result, 1)
	assert.Equal(t, "backends", result[0].Field)
}

func TestTranslateToUnmappedPathWithMap(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
		{Key: "backend1"},
		{Field: "Bucket"},
	}

	result, err := TranslateToUnmappedPath(reflect.TypeOf(Config{}), path, "mapstructure")
	require.NoError(t, err)

	assert.Len(t, result, 3)
	assert.Equal(t, "backends", result[0].Field)
	assert.Equal(t, "backend1", result[1].Key)
	assert.Equal(t, "bucket", result[2].Field)
}

func TestTranslateToUnmappedPathInvalidField(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "NonExistentField"},
	}

	_, err := TranslateToUnmappedPath(reflect.TypeOf(Config{}), path, "mapstructure")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have a field")
}

func TestTranslateToUnmappedPathMissingTag(t *testing.T) {
	t.Parallel()
	// Create a simple struct without mapstructure tags
	type SimpleStruct struct {
		Field string
	}

	path := CompositeTypePath{
		{Field: "Field"},
	}

	_, err := TranslateToUnmappedPath(reflect.TypeOf(SimpleStruct{}), path, "mapstructure")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have tag")
}

func TestTranslateToUnmappedPathNotStruct(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "SomeField"},
	}

	// Pass a map type instead of struct - maps accept any field as a key
	// So we need a type that truly doesn't support fields
	type SimpleInt int

	_, err := TranslateToUnmappedPath(reflect.TypeOf(SimpleInt(0)), path, "mapstructure")
	assert.Error(t, err)
}

func TestTranslateToUnmappedPathArrayIndex(t *testing.T) {
	t.Parallel()
	// Test accessing a map value (which returns struct, not array)
	path := CompositeTypePath{
		{Field: "Backends"},
		{Key: "backend1"},
		{Field: "Bucket"},
	}

	result, err := TranslateToUnmappedPath(reflect.TypeOf(Config{}), path, "mapstructure")
	require.NoError(t, err)

	assert.Len(t, result, 3)
	assert.Equal(t, "backends", result[0].Field)
	assert.Equal(t, "backend1", result[1].Key)
	assert.Equal(t, "bucket", result[2].Field)
}

func TestTranslateToUnmappedPathWithOmitempty(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
	}

	result, err := TranslateToUnmappedPath(reflect.TypeOf(Config{}), path, "mapstructure")
	require.NoError(t, err)

	assert.Len(t, result, 1)
	assert.Equal(t, "backends", result[0].Field)
}

func TestCompositeTypePathStringComplex(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Backends"},
		{Key: "aws-prod"},
		{Field: "Credentials"},
		{Field: "AssumeRole"},
		{Field: "RoleARN"},
	}

	result := path.String()
	assert.Equal(t, "Backends[aws-prod].Credentials.AssumeRole.RoleARN", result)
}

func TestCompositeTypePathJSONPathComplex(t *testing.T) {
	t.Parallel()
	path := CompositeTypePath{
		{Field: "Buckets"},
		{Ind: 0},
		{Field: "Routes"},
		{Ind: 1},
		{Field: "Rewrites"},
	}

	var buf bytes.Buffer
	err := path.AppendJSONPath(&buf)
	require.NoError(t, err)

	assert.Equal(t, "$.Buckets[0].Routes[1].Rewrites", buf.String())
}
