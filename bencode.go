package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// Kind is the bencode type of a Node.
type Kind uint8

const (
	KindInt Kind = iota
	KindStr
	KindList
	KindDict
)

func (k Kind) String() string {
	switch k {
	case KindInt:
		return "int"
	case KindStr:
		return "str"
	case KindList:
		return "list"
	case KindDict:
		return "dict"
	}
	return "invalid"
}

// Entry is one key/value pair of a dict, held in the order it appeared in the
// source. Dicts are slices rather than maps so that a file which breaks the
// key-ordering rule still round-trips, still prints in its original order, and
// still hashes to the value every other client agrees on.
type Entry struct {
	Key []byte
	Val *Node
}

// Node is a parsed bencode value. Start and End bracket the value in the
// source buffer; Document.Raw hands back those exact bytes, which is how
// infohashes stay correct for files that are not canonically encoded.
type Node struct {
	Kind Kind
	Int  int64  // KindInt
	Str  []byte // KindStr, aliasing the source buffer
	List []*Node
	Dict []Entry

	Start, End int
}

// Get returns the value for key, or nil if n is not a dict or has no such key.
// On a dict with duplicate keys it returns the first.
func (n *Node) Get(key string) *Node {
	if n == nil || n.Kind != KindDict {
		return nil
	}
	for i := range n.Dict {
		if string(n.Dict[i].Key) == key {
			return n.Dict[i].Val
		}
	}
	return nil
}

// GetInt returns the integer at key. ok is false if the key is missing or the
// value is some other type.
func (n *Node) GetInt(key string) (int64, bool) {
	v := n.Get(key)
	if v == nil || v.Kind != KindInt {
		return 0, false
	}
	return v.Int, true
}

// GetStr returns the byte string at key.
func (n *Node) GetStr(key string) ([]byte, bool) {
	v := n.Get(key)
	if v == nil || v.Kind != KindStr {
		return nil, false
	}
	return v.Str, true
}

// Document is a parsed buffer plus whatever followed the top-level value.
type Document struct {
	Root     *Node
	Buf      []byte
	Trailing []byte
}

// Raw returns the source bytes n was parsed from.
func (d *Document) Raw(n *Node) []byte { return d.Buf[n.Start:n.End] }

// SyntaxError reports where in the buffer parsing gave up.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg) }

// maxDepth stops a buffer of nothing but "lllll..." from exhausting the stack.
// Real torrents nest four or five deep; v2 file trees follow directory depth.
const maxDepth = 128

// Parse decodes one bencode value from buf. Bytes after that value are not an
// error: some torrents in the wild carry padding. They are returned in
// Document.Trailing so callers can complain if they care.
func Parse(buf []byte) (*Document, error) {
	if len(buf) == 0 {
		return nil, &SyntaxError{0, "empty input"}
	}
	p := &parser{buf: buf}
	root, err := p.value(0)
	if err != nil {
		return nil, err
	}
	return &Document{Root: root, Buf: buf, Trailing: buf[p.pos:]}, nil
}

type parser struct {
	buf []byte
	pos int
}

func (p *parser) value(depth int) (*Node, error) {
	if depth > maxDepth {
		return nil, &SyntaxError{p.pos, fmt.Sprintf("nested deeper than %d levels", maxDepth)}
	}
	if p.pos >= len(p.buf) {
		return nil, &SyntaxError{p.pos, "unexpected end of input"}
	}
	switch c := p.buf[p.pos]; {
	case c == 'i':
		return p.integer()
	case c == 'l':
		return p.list(depth)
	case c == 'd':
		return p.dict(depth)
	case c >= '0' && c <= '9':
		return p.bytestring()
	default:
		return nil, &SyntaxError{p.pos, fmt.Sprintf("expected 'i', 'l', 'd' or a digit, got %s", showByte(c))}
	}
}

func (p *parser) integer() (*Node, error) {
	start := p.pos
	p.pos++ // 'i'
	rel := bytes.IndexByte(p.buf[p.pos:], 'e')
	if rel < 0 {
		return nil, &SyntaxError{start, "integer has no terminating 'e'"}
	}
	digits := p.buf[p.pos : p.pos+rel]
	if msg := checkIntSyntax(digits); msg != "" {
		return nil, &SyntaxError{p.pos, msg}
	}
	v, err := strconv.ParseInt(string(digits), 10, 64)
	if err != nil {
		return nil, &SyntaxError{p.pos, fmt.Sprintf("integer %q does not fit in 64 bits", digits)}
	}
	p.pos += rel + 1
	return &Node{Kind: KindInt, Int: v, Start: start, End: p.pos}, nil
}

// checkIntSyntax enforces the parts of the grammar that most decoders skip.
// i-0e and i03e have no single agreed meaning, so a file containing them
// hashes differently depending on who reads it; that is worth rejecting.
func checkIntSyntax(d []byte) string {
	if len(d) == 0 {
		return "integer has no digits"
	}
	body := d
	if d[0] == '-' {
		body = d[1:]
		if len(body) == 0 {
			return "integer is just a minus sign"
		}
		if body[0] == '0' {
			return "negative zero and negative leading zeros are not allowed"
		}
	}
	if len(body) > 1 && body[0] == '0' {
		return "integer has a leading zero"
	}
	for _, c := range body {
		if c < '0' || c > '9' {
			return fmt.Sprintf("unexpected %s in integer", showByte(c))
		}
	}
	return ""
}

func (p *parser) bytestring() (*Node, error) {
	start := p.pos
	rel := bytes.IndexByte(p.buf[p.pos:], ':')
	if rel < 0 {
		return nil, &SyntaxError{start, "string length has no terminating ':'"}
	}
	digits := p.buf[p.pos : p.pos+rel]
	if len(digits) > 1 && digits[0] == '0' {
		return nil, &SyntaxError{start, "string length has a leading zero"}
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return nil, &SyntaxError{start, fmt.Sprintf("unexpected %s in string length", showByte(c))}
		}
	}
	n, err := strconv.ParseInt(string(digits), 10, 64)
	if err != nil {
		return nil, &SyntaxError{start, fmt.Sprintf("string length %q does not fit in 64 bits", digits)}
	}
	body := p.pos + rel + 1
	if avail := int64(len(p.buf) - body); n > avail {
		return nil, &SyntaxError{start, fmt.Sprintf("string claims %d bytes but only %d remain", n, avail)}
	}
	p.pos = body + int(n)
	return &Node{Kind: KindStr, Str: p.buf[body:p.pos], Start: start, End: p.pos}, nil
}

func (p *parser) list(depth int) (*Node, error) {
	start := p.pos
	p.pos++ // 'l'
	items := []*Node{}
	for {
		if p.pos >= len(p.buf) {
			return nil, &SyntaxError{start, "list has no terminating 'e'"}
		}
		if p.buf[p.pos] == 'e' {
			p.pos++
			return &Node{Kind: KindList, List: items, Start: start, End: p.pos}, nil
		}
		item, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
}

func (p *parser) dict(depth int) (*Node, error) {
	start := p.pos
	p.pos++ // 'd'
	entries := []Entry{}
	for {
		if p.pos >= len(p.buf) {
			return nil, &SyntaxError{start, "dict has no terminating 'e'"}
		}
		if p.buf[p.pos] == 'e' {
			p.pos++
			return &Node{Kind: KindDict, Dict: entries, Start: start, End: p.pos}, nil
		}
		if c := p.buf[p.pos]; c < '0' || c > '9' {
			return nil, &SyntaxError{p.pos, fmt.Sprintf("dict key must be a string, got %s", showByte(c))}
		}
		key, err := p.bytestring()
		if err != nil {
			return nil, err
		}
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Key: key.Str, Val: val})
	}
}

func showByte(c byte) string {
	if c >= 0x20 && c < 0x7f {
		return fmt.Sprintf("%q", string(c))
	}
	return fmt.Sprintf("byte 0x%02x", c)
}

// Encode writes n in canonical form: dict keys sorted by raw byte value.
// Encoding a Document parsed from a canonical file reproduces it exactly; for
// a file with out-of-order keys it will not, which is the whole reason
// infohashes are taken from the source bytes instead.
func Encode(n *Node) []byte {
	var b bytes.Buffer
	encodeTo(&b, n)
	return b.Bytes()
}

func encodeTo(b *bytes.Buffer, n *Node) {
	switch n.Kind {
	case KindInt:
		b.WriteByte('i')
		b.WriteString(strconv.FormatInt(n.Int, 10))
		b.WriteByte('e')
	case KindStr:
		b.WriteString(strconv.Itoa(len(n.Str)))
		b.WriteByte(':')
		b.Write(n.Str)
	case KindList:
		b.WriteByte('l')
		for _, item := range n.List {
			encodeTo(b, item)
		}
		b.WriteByte('e')
	case KindDict:
		sorted := make([]Entry, len(n.Dict))
		copy(sorted, n.Dict)
		sort.SliceStable(sorted, func(i, j int) bool {
			return bytes.Compare(sorted[i].Key, sorted[j].Key) < 0
		})
		b.WriteByte('d')
		for _, e := range sorted {
			b.WriteString(strconv.Itoa(len(e.Key)))
			b.WriteByte(':')
			b.Write(e.Key)
			encodeTo(b, e.Val)
		}
		b.WriteByte('e')
	}
}
