package template

import (
	"fmt"
	"strconv"
	"strings"
)

// Node represents a node in the template AST
type Node interface {
	node()
}

// LiteralNode represents literal text
type LiteralNode struct {
	Value string
}

func (l *LiteralNode) node() {}

// PlaceholderNode represents a variable reference with optional default or expansion
type PlaceholderNode struct {
	Name  string // variable name or numeric index
	Inner Node   // nil if no default/expansion, can be SequenceNode for nested
	IfSet bool   // true for :+ (if-set expansion), false for :- (if-not-set default)
}

func (p *PlaceholderNode) node() {}

// SequenceNode represents a sequence of nodes
type SequenceNode struct {
	Nodes []Node
}

func (s *SequenceNode) node() {}

// Template represents a parsed template AST
type Template struct {
	input    string
	root     Node
	analysis *TemplateAnalysisResult
}

// PlaceholderTailPair represents a placeholder followed by its tail literal
type PlaceholderTailPair struct {
	Placeholder *PlaceholderNode
	Tail        string
}

// TemplateAnalysisResult contains the analysis of a template structure
type TemplateAnalysisResult struct {
	Prefix               string
	Rest                 []PlaceholderTailPair
	ContainsConditionals bool
}

// Parse parses a template string into a Template AST
func Parse(input string) (*Template, error) {
	parser := &parser{
		input: input,
		pos:   0,
	}
	root, err := parser.parseTemplate()
	if err != nil {
		return nil, err
	}
	t := &Template{input: input, root: root}
	// Eager analysis - compute at parse time to avoid nil checks later
	t.analysis = analyzeTemplate(root)
	return t, nil
}

// MustParse parses a template string into a Template AST. Panics on failure
func MustParse(input string) *Template {
	t, err := Parse(input)
	if err != nil {
		panic(err)
	}
	return t
}

// templateAnalyzerContext is used to traverse and analyze a template AST.
type templateAnalyzerContext struct {
	prefixB              []byte
	tailB                []byte
	ln                   *PlaceholderNode
	rest                 []PlaceholderTailPair
	containsConditionals bool
}

func (a *templateAnalyzerContext) traverse(n Node) {
	switch n := n.(type) {
	case *PlaceholderNode:
		// Only append if we already have a previous placeholder
		if a.ln != nil {
			a.rest = append(a.rest, PlaceholderTailPair{a.ln, string(a.tailB)})
			a.tailB = a.tailB[:0]
		}
		a.containsConditionals = a.containsConditionals || n.Inner != nil
		a.ln = n
	case *LiteralNode:
		if a.ln == nil {
			// Before any placeholder - this is prefix
			a.prefixB = append(a.prefixB, n.Value...)
		} else {
			// After a placeholder - this is tail
			a.tailB = append(a.tailB, n.Value...)
		}
	case *SequenceNode:
		for _, nn := range n.Nodes {
			a.traverse(nn)
		}
	}
}

func (a *templateAnalyzerContext) finalize() {
	if a.ln != nil {
		a.rest = append(a.rest, PlaceholderTailPair{a.ln, string(a.tailB)})
	}
}

func analyzeTemplate(root Node) *TemplateAnalysisResult {
	ctx := &templateAnalyzerContext{}
	ctx.traverse(root)
	ctx.finalize()
	return &TemplateAnalysisResult{
		Prefix:               string(ctx.prefixB),
		Rest:                 ctx.rest,
		ContainsConditionals: ctx.containsConditionals,
	}
}

// Analysis returns the analysis of the template structure
func (t *Template) Analysis() *TemplateAnalysisResult {
	return t.analysis
}

// String returns the original template string
func (t *Template) String() string {
	return t.input
}

// Execute evaluates the template against Placeholders
func (t *Template) Execute(p *Placeholders) (string, error) {
	result, err := t.executeNode(p, t.root)
	if err != nil {
		return "", err
	}
	return result, nil
}

// executeNode recursively evaluates a node
func (t *Template) executeNode(p *Placeholders, node Node) (string, error) {
	switch n := node.(type) {
	case *LiteralNode:
		return n.Value, nil
	case *PlaceholderNode:
		return t.executePlaceholder(p, n)
	case *SequenceNode:
		var result strings.Builder
		for _, child := range n.Nodes {
			val, err := t.executeNode(p, child)
			if err != nil {
				return "", err
			}
			result.WriteString(val)
		}
		return result.String(), nil
	default:
		return "", fmt.Errorf("unknown node type: %T", node)
	}
}

// executePlaceholder evaluates a placeholder node
func (t *Template) executePlaceholder(p *Placeholders, ph *PlaceholderNode) (string, error) {
	// Try named lookup
	value, exists := p.named[ph.Name]

	// Try indexed lookup if not found (if name is numeric)
	if !exists {
		if index, err := strconv.Atoi(ph.Name); err == nil && index >= 0 {
			if index < len(p.indexed) {
				value = p.indexed[index]
				exists = true
			}
		}
	}

	// Handle operators: :- (if-not-set default) vs :+ (if-set expansion)
	if ph.Inner != nil {
		if ph.IfSet {
			// :+ operator - expand if variable is set (non-empty)
			if exists && value != "" {
				return t.executeNode(p, ph.Inner)
			}
			// Variable is not set or empty - return empty string
			return "", nil
		} else {
			// :- operator - use default if variable is unset or empty
			if exists && value != "" {
				return value, nil
			}
			return t.executeNode(p, ph.Inner)
		}
	}

	// No operator - just return the value or empty string
	if exists {
		return value, nil
	}

	// No value and no default/expansion - return empty string
	return "", nil
}

// parser is an internal parser for template strings
type parser struct {
	input string
	pos   int
}

// parseTemplate parses a full template (sequence of literals and placeholders)
func (p *parser) parseTemplate() (Node, error) {
	nodes := []Node{}

	for p.pos < len(p.input) {
		if p.current() == '$' {
			ph, err := p.parsePlaceholder()
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, ph)
		} else {
			lit := p.parseLiteral()
			nodes = append(nodes, lit)
		}
	}

	if len(nodes) == 0 {
		return &LiteralNode{Value: ""}, nil
	}
	if len(nodes) == 1 {
		return nodes[0], nil
	}
	return &SequenceNode{Nodes: nodes}, nil
}

// current returns the current character without advancing
func (p *parser) current() byte {
	if p.pos < len(p.input) {
		return p.input[p.pos]
	}
	return 0
}

// peek returns the next character without advancing
func (p *parser) peek() byte {
	if p.pos+1 < len(p.input) {
		return p.input[p.pos+1]
	}
	return 0
}

// advance moves forward by n positions
func (p *parser) advance(n int) {
	p.pos += n
}

// parseLiteral parses a literal (non-placeholder) sequence
func (p *parser) parseLiteral() *LiteralNode {
	start := p.pos
	for p.pos < len(p.input) && p.current() != '$' {
		p.pos++
	}
	return &LiteralNode{Value: p.input[start:p.pos]}
}

// parsePlaceholder parses a placeholder: $name, ${name}, ${name:-default}, etc.
func (p *parser) parsePlaceholder() (Node, error) {
	if p.current() != '$' {
		return nil, fmt.Errorf("expected '$' at position %d", p.pos)
	}
	p.advance(1) // consume '$'

	if p.current() == '{' {
		// ${...} style
		return p.parseBracedPlaceholder()
	}

	// $name style
	name := p.parseName()
	if name == "" {
		// Not a valid placeholder, return '$' as literal
		return &LiteralNode{Value: "$"}, nil
	}
	return &PlaceholderNode{Name: name}, nil
}

// parseBracedPlaceholder parses ${...} style placeholders
func (p *parser) parseBracedPlaceholder() (Node, error) {
	if p.current() != '{' {
		return nil, fmt.Errorf("expected '{{' at position %d", p.pos)
	}
	p.advance(1) // consume '{'

	name := p.parseName()
	if name == "" {
		return nil, fmt.Errorf("invalid placeholder name at position %d", p.pos)
	}

	// Check for operator: :- (if-not-set default) or :+ (if-set expansion)
	if p.current() == ':' {
		nextChar := p.peek()
		if nextChar == '-' || nextChar == '+' {
			p.advance(2) // consume ':' and operator character
			isPositive := nextChar == '+'

			// Parse expansion/default (can contain nested placeholders)
			expansionNode, err := p.parseDefaultValue()
			if err != nil {
				return nil, err
			}

			if p.current() != '}' {
				return nil, fmt.Errorf("expected '}}' at position %d", p.pos)
			}
			p.advance(1) // consume '}'

			return &PlaceholderNode{Name: name, Inner: expansionNode, IfSet: isPositive}, nil
		}
	}

	if p.current() != '}' {
		return nil, fmt.Errorf("expected '}}' or ':-' at position %d", p.pos)
	}
	p.advance(1) // consume '}'

	return &PlaceholderNode{Name: name}, nil
}

// parseDefaultValue parses the default value (everything until the closing })
// This needs to handle nested placeholders and find the correct closing brace
func (p *parser) parseDefaultValue() (Node, error) {
	start := p.pos
	depth := 1 // we're inside one brace

	for p.pos < len(p.input) && depth > 0 {
		ch := p.current()
		if ch == '$' && p.peek() == '{' {
			// Count nested braces
			p.advance(2)
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				break
			}
			p.advance(1)
		} else {
			p.advance(1)
		}
	}

	if depth > 0 {
		return nil, fmt.Errorf("unclosed placeholder at position %d", start)
	}

	defaultStr := p.input[start:p.pos]

	// Recursively parse the default value (which may contain nested placeholders)
	subParser := &parser{input: defaultStr, pos: 0}
	defaultNode, err := subParser.parseTemplate()
	if err != nil {
		return nil, err
	}

	return defaultNode, nil
}

// parseName parses a valid placeholder name (identifier or numeric)
func (p *parser) parseName() string {
	start := p.pos

	// First check if it's numeric
	for p.pos < len(p.input) && isDigit(p.current()) {
		p.pos++
	}
	if p.pos > start {
		return p.input[start:p.pos]
	}

	// Otherwise, parse as identifier
	if !isIdentifierStart(p.current()) {
		return ""
	}

	p.advance(1)
	for p.pos < len(p.input) && isIdentifierPart(p.current()) {
		p.advance(1)
	}

	return p.input[start:p.pos]
}

// isDigit reports whether ch is an ASCII digit.
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

// isIdentifierStart reports whether ch is a valid identifier start character (letter or underscore).
func isIdentifierStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// isIdentifierPart reports whether ch is a valid identifier character (letter, digit, or underscore).
func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || isDigit(ch)
}
