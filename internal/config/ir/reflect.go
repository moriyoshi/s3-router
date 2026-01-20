package ir

import (
	"io"

	"fmt"
	"reflect"
	"strings"
)

type FieldOrIndex struct {
	Field string
	Key   string
	Ind   int
}

type CompositeTypePath []FieldOrIndex

func (path CompositeTypePath) AppendString(w io.Writer) error {
	var b [1]byte
	for i, c := range path {
		if c.Field != "" {
			if i > 0 {
				b[0] = '.'
				_, err := w.Write(b[:])
				if err != nil {
					return err
				}
			}
			_, err := io.WriteString(w, c.Field)
			if err != nil {
				return err
			}
		} else if c.Key != "" {
			b[0] = '['
			_, err := w.Write(b[:])
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, c.Key)
			if err != nil {
				return err
			}
			b[0] = ']'
			_, err = w.Write(b[:])
			if err != nil {
				return err
			}
		} else if c.Ind >= 0 {
			_, err := fmt.Fprintf(w, "[%d]", c.Ind)
			if err != nil {
				return err
			}
		} else {
			_, err := io.WriteString(w, "???")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func appendEscaped(w io.Writer, v string) (err error) {
	var b [1]byte
	s := 0
	for i := 0; i < len(v); {
		c := v[i]
		if c == '\\' || c == '\'' {
			_, err = io.WriteString(w, v[s:i])
			if err != nil {
				return
			}
			b[0] = '\\'
			_, err = w.Write(b[:])
			if err != nil {
				return
			}
			i++
			s = i
		} else {
			i++
		}
	}
	if s < len(v) {
		_, err = io.WriteString(w, v[s:])
	}
	return
}

func (path CompositeTypePath) AppendJSONPath(w io.Writer) error {
	var b [1]byte
	b[0] = '$'
	_, err := w.Write(b[:])
	if err != nil {
		return err
	}
	for _, c := range path {
		if c.Field != "" {
			b[0] = '.'
			_, err := w.Write(b[:])
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, c.Field)
			if err != nil {
				return err
			}
		} else if c.Key != "" {
			_, err := io.WriteString(w, "['")
			if err != nil {
				return err
			}
			err = appendEscaped(w, c.Key)
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, "']")
			if err != nil {
				return err
			}
		} else if c.Ind >= 0 {
			_, err := fmt.Fprintf(w, "[%d]", c.Ind)
			if err != nil {
				return err
			}
		} else {
			_, err := io.WriteString(w, "???")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (path CompositeTypePath) String() string {
	var w strings.Builder
	_ = path.AppendString(&w)
	return w.String()
}

var anyType = reflect.TypeOf((*any)(nil)).Elem()

func TranslateToUnmappedPath(t reflect.Type, path CompositeTypePath, tag string) (CompositeTypePath, error) {
	r := make(CompositeTypePath, 0, len(path))
	for i, c := range path {
		if c.Field != "" {
			if t.Kind() == reflect.Struct {
				f, ok := t.FieldByName(c.Field)
				if !ok {
					return nil, fmt.Errorf("item #%d of type %s does not have a field %s", i, t, c.Field)
				}
				tv, ok := f.Tag.Lookup(tag)
				if !ok {
					return nil, fmt.Errorf("field %s of item #%d of type %s does not have tag %s", c.Field, i, t, tag)
				}
				fn := f.Name
				telts := strings.Split(tv, ",")
				if len(telts) > 0 {
					fn = telts[0]
				}
				r = append(r, FieldOrIndex{Field: fn})
				t = f.Type
			} else if t.Kind() == reflect.Map && t.Key().Kind() == reflect.String {
				r = append(r, c)
				t = t.Elem()
			} else {
				return nil, fmt.Errorf("expecting item #%d to be a struct or string-keyed map, got %s", i, t)
			}
		} else if c.Key != "" {
			if t.Kind() != reflect.Map || t.Key().Kind() != reflect.String && (t.Key().Kind() != reflect.Interface || !t.Key().AssignableTo(anyType)) {
				return nil, fmt.Errorf("expecting item #%d to be a string-keyed or any-keyed map, got %s", i, t)
			}
			r = append(r, c)
			t = t.Elem()
		} else if c.Ind >= 0 {
			if t.Kind() != reflect.Slice || t.Key().Kind() != reflect.Array {
				return nil, fmt.Errorf("expecting item #%d to be a slice or array, got %s", i, t)
			}
			r = append(r, c)
			t = t.Elem()
		} else {
			return nil, fmt.Errorf("empty item #%d", i)
		}
	}
	return r, nil
}
