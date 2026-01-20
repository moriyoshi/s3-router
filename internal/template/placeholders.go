package template

// Placeholders provides placeholder values for template execution:
// - $1, ${1} - numbered placeholders (1-indexed)
// - $foo, ${foo} - named placeholders
// - ${foo:-default} - named placeholders with defaults
// - ${foo:+conditional} - named placeholders with conditionals
type Placeholders struct {
	named   map[string]string
	indexed []string
}

// NewPlaceholders creates a new Placeholders instance for template execution.
func NewPlaceholders() *Placeholders {
	return &Placeholders{
		named:   make(map[string]string),
		indexed: make([]string, 0),
	}
}

// SetNamed sets a named variable
func (p *Placeholders) SetNamed(name, value string) *Placeholders {
	p.named[name] = value
	return p
}

// SetNamedMap sets multiple named variables
func (p *Placeholders) SetNamedMap(vars map[string]string) *Placeholders {
	for k, v := range vars {
		p.named[k] = v
	}
	return p
}

// SetNamedMapBorrow sets multiple named variables borrowing the ownership of the map.
func (p *Placeholders) SetNamedMapBorrow(vars map[string]string) *Placeholders {
	p.named = vars
	return p
}

// AddIndexed adds an indexed variable (1-indexed)
func (p *Placeholders) AddIndexed(value string) *Placeholders {
	p.indexed = append(p.indexed, value)
	return p
}

// AddIndexedAll adds multiple indexed variables
func (p *Placeholders) AddIndexedAll(values ...string) *Placeholders {
	p.indexed = append(p.indexed, values...)
	return p
}

// SetIndexedAllBorrow sets multiple indexed variables borrowing the ownership of the slice.
func (p *Placeholders) SetIndexedAllBorrow(values []string) *Placeholders {
	p.indexed = values
	return p
}
