package config

import (
	"errors"
	"fmt"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// Context tracks the path through configuration fields during population and collects validation errors.
type Context struct {
	errs   *Errors
	loc    ir.CompositeTypePath
	cowLoc ir.CompositeTypePath
}

// Append adds an error message or error to the context.
func (c *Context) Append(fmtOrError any, args ...any) *Context {
	if c.cowLoc == nil {
		loc := make(ir.CompositeTypePath, len(c.loc))
		copy(loc, c.loc)
		c.cowLoc = loc
	}
	switch fmtOrError := fmtOrError.(type) {
	case string:
		x := fmt.Errorf(fmtOrError, args...)
		*c.errs = append(*c.errs, Error{loc: c.cowLoc, msg: x.Error(), inner: errors.Unwrap(x)})
	case error:
		*c.errs = append(*c.errs, Error{loc: c.cowLoc, inner: fmtOrError})
	}
	return c
}

// Enter navigates to a named field within the current context path.
func (c *Context) Enter(field string) *Context {
	return &Context{
		errs: c.errs,
		loc:  append(c.loc, ir.FieldOrIndex{Field: field}),
	}
}

// EnterIndex navigates to an indexed element (string key or integer index) within the current context path.
func (c *Context) EnterIndex(i any) *Context {
	var fi ir.FieldOrIndex
	switch i := i.(type) {
	case string:
		fi = ir.FieldOrIndex{Key: i}
	case int:
		fi = ir.FieldOrIndex{Ind: i}
	default:
		panic("unknown index type")
	}
	return &Context{
		errs: c.errs,
		loc:  append(c.loc, fi),
	}
}

// Errors returns the accumulated errors or nil if there are none.
func (c *Context) Errors() error {
	if len(*c.errs) == 0 {
		return nil
	}
	return *c.errs
}

// NewContext creates a new Context for configuration population and error tracking.
func NewContext() *Context {
	return &Context{
		errs: new(Errors),
		loc:  make(ir.CompositeTypePath, 0, 10),
	}
}
