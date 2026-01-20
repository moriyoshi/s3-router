package template

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAndExecuteNamedPlaceholders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		expected string
	}{
		{
			name:     "${foo} notation",
			template: "Value: ${foo}",
			vars:     map[string]string{"foo": "bar"},
			expected: "Value: bar",
		},
		{
			name:     "$foo notation",
			template: "Value: $foo",
			vars:     map[string]string{"foo": "bar"},
			expected: "Value: bar",
		},
		{
			name:     "multiple named placeholders",
			template: "${first}-${second}",
			vars:     map[string]string{"first": "hello", "second": "world"},
			expected: "hello-world",
		},
		{
			name:     "mixed notation",
			template: "$first and ${second}",
			vars:     map[string]string{"first": "hello", "second": "world"},
			expected: "hello and world",
		},
		{
			name:     "missing placeholder keeps original",
			template: "Value: ${missing}",
			vars:     map[string]string{},
			expected: "Value: ",
		},
		{
			name:     "underscore in name",
			template: "${my_var}",
			vars:     map[string]string{"my_var": "value"},
			expected: "value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			placeholders := NewPlaceholders().SetNamedMap(tt.vars)
			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseAndExecuteIndexedPlaceholders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		indexed  []string
		expected string
	}{
		{
			name:     "${1} notation",
			template: "First: ${1}",
			indexed:  []string{"", "hello"},
			expected: "First: hello",
		},
		{
			name:     "$1 notation",
			template: "First: $1",
			indexed:  []string{"", "hello"},
			expected: "First: hello",
		},
		{
			name:     "multiple indexed placeholders",
			template: "${1} and ${2}",
			indexed:  []string{"", "hello", "world"},
			expected: "hello and world",
		},
		{
			name:     "out of bounds keeps original",
			template: "${3}",
			indexed:  []string{"", "a", "b"},
			expected: "",
		},
		{
			name:     "zero index keeps original",
			template: "${0}",
			indexed:  []string{"a"},
			expected: "a",
		},
		{
			name:     "large indices",
			template: "${10}",
			indexed:  make([]string, 10),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			placeholders := NewPlaceholders().AddIndexedAll(tt.indexed...)
			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseAndExecuteDefaultPlaceholders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		expected string
	}{
		{
			name:     "default when not provided",
			template: "${foo:-default_value}",
			vars:     map[string]string{},
			expected: "default_value",
		},
		{
			name:     "uses value when provided",
			template: "${foo:-default_value}",
			vars:     map[string]string{"foo": "custom"},
			expected: "custom",
		},
		{
			name:     "empty default",
			template: "${foo:-}",
			vars:     map[string]string{},
			expected: "",
		},
		{
			name:     "default with spaces",
			template: "${foo:-default value with spaces}",
			vars:     map[string]string{},
			expected: "default value with spaces",
		},
		{
			name:     "multiple defaults",
			template: "${a:-first} and ${b:-second}",
			vars:     map[string]string{},
			expected: "first and second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			placeholders := NewPlaceholders().SetNamedMap(tt.vars)
			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNestedPlaceholders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		indexed  []string
		expected string
	}{
		{
			name:     "one level nesting",
			template: "${foo:-${bar}}",
			vars:     map[string]string{"bar": "value"},
			indexed:  []string{},
			expected: "value",
		},
		{
			name:     "one level nesting with fallback to named var",
			template: "${foo:-${bar}}",
			vars:     map[string]string{"foo": "explicit", "bar": "fallback"},
			indexed:  []string{},
			expected: "explicit",
		},
		{
			name:     "two levels nesting",
			template: "${a:-${b:-${c}}}",
			vars:     map[string]string{"c": "final"},
			indexed:  []string{},
			expected: "final",
		},
		{
			name:     "two levels nesting with intermediate value",
			template: "${a:-${b:-${c}}}",
			vars:     map[string]string{"b": "second", "c": "final"},
			indexed:  []string{},
			expected: "second",
		},
		{
			name:     "nested with literal in default",
			template: "${foo:-prefix_${bar}_suffix}",
			vars:     map[string]string{"bar": "middle"},
			indexed:  []string{},
			expected: "prefix_middle_suffix",
		},
		{
			name:     "nested indexed placeholders",
			template: "${1:-${2}}",
			vars:     map[string]string{},
			indexed:  []string{"", "first", "second"},
			expected: "first",
		},
		{
			name:     "three levels deep",
			template: "${a:-${b:-${c:-${d}}}}",
			vars:     map[string]string{"d": "deep"},
			indexed:  []string{},
			expected: "deep",
		},
		{
			name:     "complex nesting with multiple placeholders",
			template: "${x:-${y}}_${z:-${w}}_end",
			vars:     map[string]string{"y": "Y", "w": "W"},
			indexed:  []string{},
			expected: "Y_W_end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			placeholders := NewPlaceholders().SetNamedMap(tt.vars).AddIndexedAll(tt.indexed...)
			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMixedPlaceholders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		named    map[string]string
		indexed  []string
		expected string
	}{
		{
			name:     "named and indexed",
			template: "${bucket}/${1}/${key}",
			named:    map[string]string{"bucket": "my-bucket", "key": "path/to/file"},
			indexed:  []string{"", "prefix"},
			expected: "my-bucket/prefix/path/to/file",
		},
		{
			name:     "named with defaults and indexed",
			template: "${region:-us-east-1}/${1}/${key:-default}",
			named:    map[string]string{"key": "myfile"},
			indexed:  []string{"", "data"},
			expected: "us-east-1/data/myfile",
		},
		{
			name:     "all notation styles",
			template: "$bucket ${1} ${key} $undefined ${other:-fallback}",
			named:    map[string]string{"bucket": "b1", "key": "k1", "other": "o1"},
			indexed:  []string{"", "i1"},
			expected: "b1 i1 k1  o1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placeholders := NewPlaceholders().SetNamedMap(tt.named).AddIndexedAll(tt.indexed...)
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		indexed  []string
		expected string
	}{
		{
			name:     "dollar at end",
			template: "Price: $",
			vars:     map[string]string{},
			indexed:  []string{},
			expected: "Price: $",
		},
		{
			name:     "consecutive placeholders",
			template: "${a}${b}",
			vars:     map[string]string{"a": "hello", "b": "world"},
			indexed:  []string{},
			expected: "helloworld",
		},
		{
			name:     "placeholder in value",
			template: "${template}",
			vars:     map[string]string{"template": "${nested}"},
			indexed:  []string{},
			expected: "${nested}",
		},
		{
			name:     "empty template",
			template: "",
			vars:     map[string]string{},
			indexed:  []string{},
			expected: "",
		},
		{
			name:     "no placeholders",
			template: "plain text",
			vars:     map[string]string{},
			indexed:  []string{},
			expected: "plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placeholders := NewPlaceholders().SetNamedMap(tt.vars).AddIndexedAll(tt.indexed...)
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		template  string
		shouldErr bool
	}{
		{
			name:      "unclosed brace",
			template:  "${foo",
			shouldErr: true,
		},
		{
			name:      "unclosed default",
			template:  "${foo:-${bar}",
			shouldErr: true,
		},
		{
			name:      "invalid name after $",
			template:  "$-invalid",
			shouldErr: false, // $ followed by non-name is just literal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.template)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFluentAPI(t *testing.T) {
	t.Parallel()
	t.Run("chained setters", func(t *testing.T) {
		placeholders := NewPlaceholders().
			SetNamed("bucket", "my-bucket").
			SetNamed("region", "us-east-1").
			AddIndexed("").
			AddIndexed("path1").
			AddIndexed("path2")

		tmpl, err := Parse("${bucket}/${region}/${1}/${2}")
		assert.NoError(t, err)

		result, err := tmpl.Execute(placeholders)
		require.NoError(t, err)
		assert.Equal(t, "my-bucket/us-east-1/path1/path2", result)
	})

	t.Run("map setter", func(t *testing.T) {
		placeholders := NewPlaceholders().SetNamedMap(map[string]string{
			"bucket": "my-bucket",
			"region": "us-east-1",
		})

		tmpl, err := Parse("${bucket}/${region}")
		assert.NoError(t, err)

		result, err := tmpl.Execute(placeholders)
		require.NoError(t, err)
		assert.Equal(t, "my-bucket/us-east-1", result)
	})

	t.Run("indexed all", func(t *testing.T) {
		placeholders := NewPlaceholders().AddIndexedAll("", "a", "b", "c")

		tmpl, err := Parse("${1}-${2}-${3}")
		assert.NoError(t, err)

		result, err := tmpl.Execute(placeholders)
		require.NoError(t, err)
		assert.Equal(t, "a-b-c", result)
	})
}

func TestIfSetOperator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		indexed  []string
		expected string
	}{
		{
			name:     "expansion when variable is set",
			template: "${foo:+set}",
			vars:     map[string]string{"foo": "value"},
			indexed:  []string{},
			expected: "set",
		},
		{
			name:     "empty when variable is not set",
			template: "${foo:+expansion}",
			vars:     map[string]string{},
			indexed:  []string{},
			expected: "",
		},
		{
			name:     "empty when variable is empty string",
			template: "${foo:+expansion}",
			vars:     map[string]string{"foo": ""},
			indexed:  []string{},
			expected: "",
		},
		{
			name:     "expansion with placeholder reference",
			template: "${foo:+prefix_${foo}_suffix}",
			vars:     map[string]string{"foo": "value"},
			indexed:  []string{},
			expected: "prefix_value_suffix",
		},
		{
			name:     "set operator with indexed",
			template: "${1:+first}",
			vars:     map[string]string{},
			indexed:  []string{"", "value"},
			expected: "first",
		},
		{
			name:     "set operator indexed not set",
			template: "${1:+first}",
			vars:     map[string]string{},
			indexed:  []string{},
			expected: "",
		},
		{
			name:     "nested expansion in set operator",
			template: "${foo:+${bar}}",
			vars:     map[string]string{"foo": "set", "bar": "nested"},
			indexed:  []string{},
			expected: "nested",
		},
		{
			name:     "nested expansion in set operator unset inner",
			template: "${foo:+${bar}}",
			vars:     map[string]string{"foo": "set"},
			indexed:  []string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			placeholders := NewPlaceholders().SetNamedMap(tt.vars).AddIndexedAll(tt.indexed...)
			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMixedOperators(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		vars     map[string]string
		expected string
	}{
		{
			name:     "default and expansion in different placeholders",
			template: "${a:-default_a} ${b:+set_b}",
			vars:     map[string]string{"b": "value"},
			expected: "default_a set_b",
		},
		{
			name:     "multiple set expansions",
			template: "${a:+A} ${b:+B}",
			vars:     map[string]string{"a": "yes", "b": "yes"},
			expected: "A B",
		},
		{
			name:     "mixed set and unset",
			template: "${a:+A} ${b:+B}",
			vars:     map[string]string{"a": "yes"},
			expected: "A ",
		},
		{
			name:     "nested default in expansion",
			template: "${foo:+value_${bar:-fallback}}",
			vars:     map[string]string{"foo": "yes"},
			expected: "value_fallback",
		},
		{
			name:     "nested expansion in default",
			template: "${foo:-${bar:+expanded}}",
			vars:     map[string]string{"bar": "yes"},
			expected: "expanded",
		},
		{
			name:     "nested expansion in default with unset",
			template: "${foo:-${bar:+expanded}}",
			vars:     map[string]string{},
			expected: "",
		},
		{
			name:     "complex nesting with both operators",
			template: "${foo:+yes_${bar:-no}}",
			vars:     map[string]string{"foo": "set"},
			expected: "yes_no",
		},
		{
			name:     "complex nesting with both operators variant",
			template: "${foo:+yes_${bar:-no}}",
			vars:     map[string]string{"foo": "set", "bar": "exists"},
			expected: "yes_exists",
		},
		{
			name:     "three level nesting with operators",
			template: "${a:-${b:+${c}}}",
			vars:     map[string]string{"b": "yes", "c": "final"},
			expected: "final",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			assert.NoError(t, err)

			placeholders := NewPlaceholders().SetNamedMap(tt.vars)
			result, err := tmpl.Execute(placeholders)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
