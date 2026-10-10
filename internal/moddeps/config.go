// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"fmt"
	"strings"
)

// Value is one property's right-hand side: either a scalar (a string or
// number literal, stored as its raw text with quotes stripped) or an
// array of scalars.
type Value struct {
	IsArray bool
	Scalar  string
	Array   []string
}

// Class is one "class Name [: Parent] { ... };" block, or the synthetic
// root Class ParseConfig returns holding every top-level statement.
type Class struct {
	Name       string
	Parent     string
	Properties map[string]Value
	Classes    map[string]*Class
}

func newClass(name, parent string) *Class {
	return &Class{Name: name, Parent: parent, Properties: map[string]Value{}, Classes: map[string]*Class{}}
}

// ParseConfig parses the plain-text "raw" Arma/Enfusion config grammar
// (config.cpp source, not a rapified config.bin) into a tree of Class
// nodes. Preprocessor lines (#define, #include, ...) are not expanded;
// they are skipped as unrecognised, which is enough to find CfgPatches in
// a config.cpp that does not rely on macros for its structure - real mod
// configs that do are a documented gap (see the package doc comment).
func ParseConfig(data []byte) (*Class, error) {
	toks, err := tokenize(string(data))
	if err != nil {
		return nil, fmt.Errorf("moddeps: tokenize: %w", err)
	}
	p := &parser{toks: toks}
	root := newClass("", "")
	if err := p.parseBody(root); err != nil {
		return nil, fmt.Errorf("moddeps: parse: %w", err)
	}
	return root, nil
}

type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokNumber
	tokPunct // one of { } ; : = [ ] , += (kept in text)
	tokEOF
)

type token struct {
	kind tokenKind
	text string
}

func tokenize(src string) ([]token, error) {
	var toks []token
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += 2 + end + 2
		case c == '#':
			// Preprocessor line - skip to end of line, best-effort (see
			// the package doc comment on macro-dependent configs).
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"':
			s, next, err := readString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{tokString, s})
			i = next
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			toks = append(toks, token{tokIdent, src[start:i]})
		case isDigit(c) || (c == '-' && i+1 < n && isDigit(src[i+1])):
			start := i
			i++
			for i < n && (isDigit(src[i]) || src[i] == '.') {
				i++
			}
			if i < n && isIdentStart(src[i]) && src[start] != '-' { // a class name may start with digits: 416M4_Sounds
				for i < n && isIdentPart(src[i]) {
					i++
				}
				toks = append(toks, token{tokIdent, src[start:i]})
				break
			}
			toks = append(toks, token{tokNumber, src[start:i]})
		case c == '+' && i+1 < n && src[i+1] == '=':
			toks = append(toks, token{tokPunct, "+="})
			i += 2
		case strings.ContainsRune("{};:=[],", rune(c)):
			toks = append(toks, token{tokPunct, string(c)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", c, i)
		}
	}
	toks = append(toks, token{tokEOF, ""})
	return toks, nil
}

func readString(src string, start int) (string, int, error) {
	var b strings.Builder
	i := start + 1
	n := len(src)
	for i < n {
		if src[i] == '"' {
			if i+1 < n && src[i+1] == '"' { // "" is an escaped quote
				b.WriteByte('"')
				i += 2
				continue
			}
			return b.String(), i + 1, nil
		}
		b.WriteByte(src[i])
		i++
	}
	return "", 0, fmt.Errorf("unterminated string literal")
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// parser is a small recursive-descent parser over the token stream.
type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) advance() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) expectPunct(s string) error {
	t := p.advance()
	if t.kind != tokPunct || t.text != s {
		return fmt.Errorf("expected %q, got %q", s, t.text)
	}
	return nil
}

// parseBody parses statements until '}' or EOF, adding classes/properties
// to into.
func (p *parser) parseBody(into *Class) error {
	for {
		t := p.peek()
		if t.kind == tokEOF {
			return nil
		}
		if t.kind == tokPunct && t.text == "}" {
			return nil
		}
		if t.kind == tokPunct && t.text == ";" {
			p.advance() // stray semicolon
			continue
		}
		if t.kind != tokIdent {
			return fmt.Errorf("expected an identifier, got %q", t.text)
		}
		if t.text == "class" {
			if err := p.parseClass(into); err != nil {
				return err
			}
			continue
		}
		if err := p.parseAssignment(into); err != nil {
			return err
		}
	}
}

func (p *parser) parseClass(into *Class) error {
	p.advance() // "class"
	nameTok := p.advance()
	if nameTok.kind != tokIdent {
		return fmt.Errorf("expected a class name, got %q", nameTok.text)
	}
	parent := ""
	if p.peek().kind == tokPunct && p.peek().text == ":" {
		p.advance()
		parentTok := p.advance()
		if parentTok.kind != tokIdent {
			return fmt.Errorf("expected a parent class name, got %q", parentTok.text)
		}
		parent = parentTok.text
	}
	cls := newClass(nameTok.text, parent)
	if p.peek().kind == tokPunct && p.peek().text == "{" {
		p.advance()
		if err := p.parseBody(cls); err != nil {
			return err
		}
		if err := p.expectPunct("}"); err != nil {
			return err
		}
	}
	if err := p.expectPunct(";"); err != nil {
		return err
	}
	into.Classes[cls.Name] = cls
	return nil
}

func (p *parser) parseAssignment(into *Class) error {
	nameTok := p.advance()
	isArray := false
	if p.peek().kind == tokPunct && p.peek().text == "[" {
		p.advance()
		if err := p.expectPunct("]"); err != nil {
			return err
		}
		isArray = true
	}
	op := p.advance()
	if op.kind != tokPunct || (op.text != "=" && op.text != "+=") {
		return fmt.Errorf("expected '=' or '+=', got %q", op.text)
	}
	if isArray {
		items, err := p.parseArrayLiteral()
		if err != nil {
			return err
		}
		if err := p.expectPunct(";"); err != nil {
			return err
		}
		into.Properties[nameTok.text] = Value{IsArray: true, Array: items}
		return nil
	}
	val := p.advance()
	if val.kind != tokString && val.kind != tokNumber && val.kind != tokIdent {
		return fmt.Errorf("expected a scalar value, got %q", val.text)
	}
	if err := p.expectPunct(";"); err != nil {
		return err
	}
	into.Properties[nameTok.text] = Value{Scalar: val.text}
	return nil
}

func (p *parser) parseArrayLiteral() ([]string, error) {
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	var items []string
	for {
		if p.peek().kind == tokPunct && p.peek().text == "}" {
			p.advance()
			return items, nil
		}
		t := p.advance()
		switch {
		case t.kind == tokString || t.kind == tokNumber || t.kind == tokIdent:
			items = append(items, t.text)
		case t.kind == tokPunct && t.text == "{":
			// Nested array literal: flatten its scalars in (rare in
			// practice for requiredAddons, but valid grammar elsewhere).
			p.pos-- // put back the '{' for the recursive call
			nested, err := p.parseArrayLiteral()
			if err != nil {
				return nil, err
			}
			items = append(items, nested...)
		default:
			return nil, fmt.Errorf("expected an array item, got %q", t.text)
		}
		if p.peek().kind == tokPunct && p.peek().text == "," {
			p.advance()
			continue
		}
	}
}
