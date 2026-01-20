package config

import (
	"reflect"
	"strings"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// Error represents a configuration validation error with location information.
type Error struct {
	loc   ir.CompositeTypePath
	msg   string
	inner error
}

var configType = reflect.TypeOf(ir.Config{})

// Error returns a formatted error message with the JSON path and message.
func (e *Error) Error() string {
	var b strings.Builder
	p, err := ir.TranslateToUnmappedPath(configType, e.loc, "mapstructure")
	if err == nil {
		_ = p.AppendJSONPath(&b)
	} else {
		_ = e.loc.AppendString(&b)
	}
	_, _ = b.WriteString(": ")
	if e.msg != "" {
		_, _ = b.WriteString(e.msg)
	} else {
		if e.inner != nil {
			_, _ = b.WriteString(e.inner.Error())
		} else {
			_, _ = b.WriteString("unknown error")
		}
	}
	return b.String()
}

// Unwrap returns the underlying error for errors.Unwrap support.
func (e *Error) Unwrap() error {
	return e.inner
}

// Errors is a collection of configuration validation errors.
type Errors []Error

// Unwrap returns the underlying errors for errors.Unwrap support.
func (e Errors) Unwrap() []error {
	r := make([]error, len(e))
	for i := range e {
		r[i] = &e[i]
	}
	return r
}

// Error returns a formatted string containing all errors joined by " / ".
func (e Errors) Error() string {
	var b strings.Builder
	for i, e := range e {
		if i != 0 {
			b.WriteString(" / ")
		}
		b.WriteString(e.Error())
	}
	return b.String()
}
