package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Bencode strings are byte strings; JSON strings are text. Anything that is
// not valid UTF-8 is emitted as a one-key object instead:
//
//	{"_b64": "MjAgcmF3IGJ5dGVz"}
//
// and read back the same way, so `bencat json | jq | bencat encode` survives a
// torrent's 20-byte-per-piece SHA-1 blob. --brief swaps the payload for a
// length, which is readable and deliberately not round-trippable.
const (
	b64Key    = "_b64"
	b64LenKey = "_b64_len"
)

// JSONOpts controls the shape of the JSON, not its meaning.
type JSONOpts struct {
	Compact bool
	Brief   bool
	Elide   int // brief: byte strings longer than this become {"_b64_len": n}
}

// WriteJSON renders a node as JSON. Dict keys keep their original file order
// rather than being sorted, because seeing the real order is the point of an
// inspector; re-encoding sorts them again.
func WriteJSON(w io.Writer, n *Node, opts JSONOpts) error {
	e := &jsonWriter{opts: opts}
	if err := e.value(n, 0, ""); err != nil {
		return err
	}
	e.buf.WriteByte('\n')
	_, err := w.Write(e.buf.Bytes())
	return err
}

type jsonWriter struct {
	buf  bytes.Buffer
	opts JSONOpts
}

func (e *jsonWriter) newline(depth int) {
	if e.opts.Compact {
		return
	}
	e.buf.WriteByte('\n')
	for i := 0; i < depth; i++ {
		e.buf.WriteString("  ")
	}
}

func (e *jsonWriter) colon() {
	if e.opts.Compact {
		e.buf.WriteByte(':')
	} else {
		e.buf.WriteString(": ")
	}
}

func (e *jsonWriter) value(n *Node, depth int, path string) error {
	switch n.Kind {
	case KindInt:
		e.buf.WriteString(strconv.FormatInt(n.Int, 10))
	case KindStr:
		e.str(n.Str)
	case KindList:
		if len(n.List) == 0 {
			e.buf.WriteString("[]")
			return nil
		}
		e.buf.WriteByte('[')
		for i, item := range n.List {
			if i > 0 {
				e.buf.WriteByte(',')
			}
			e.newline(depth + 1)
			if err := e.value(item, depth+1, join(path, strconv.Itoa(i))); err != nil {
				return err
			}
		}
		e.newline(depth)
		e.buf.WriteByte(']')
	case KindDict:
		if len(n.Dict) == 0 {
			e.buf.WriteString("{}")
			return nil
		}
		if len(n.Dict) == 1 && string(n.Dict[0].Key) == b64Key && n.Dict[0].Val.Kind == KindStr {
			return fmt.Errorf("dict at %s has the single key %q and would be indistinguishable from a byte string in JSON; use `show` or `get` instead",
				orRoot(path), b64Key)
		}
		e.buf.WriteByte('{')
		for i, ent := range n.Dict {
			if !utf8.Valid(ent.Key) {
				return fmt.Errorf("dict key %q at %s is not valid UTF-8 and cannot be a JSON key; use `show` or `get` instead",
					ent.Key, orRoot(path))
			}
			if i > 0 {
				e.buf.WriteByte(',')
			}
			e.newline(depth + 1)
			e.str(ent.Key)
			e.colon()
			if err := e.value(ent.Val, depth+1, join(path, string(ent.Key))); err != nil {
				return err
			}
		}
		e.newline(depth)
		e.buf.WriteByte('}')
	}
	return nil
}

func (e *jsonWriter) str(s []byte) {
	if utf8.Valid(s) {
		writeJSONString(&e.buf, s)
		return
	}
	if e.opts.Brief && len(s) > e.opts.Elide {
		e.buf.WriteString(`{"` + b64LenKey + `"`)
		e.colon()
		e.buf.WriteString(strconv.Itoa(len(s)))
		e.buf.WriteByte('}')
		return
	}
	e.buf.WriteString(`{"` + b64Key + `"`)
	e.colon()
	e.buf.WriteByte('"')
	e.buf.WriteString(base64.StdEncoding.EncodeToString(s))
	e.buf.WriteString(`"}`)
}

// writeJSONString escapes s, which must already be valid UTF-8. Unlike
// encoding/json it leaves <, > and & alone: this output is for terminals and
// jq, not for embedding in HTML.
func writeJSONString(b *bytes.Buffer, s []byte) {
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

// FromJSON reads JSON and builds a bencode tree. Object key order is
// discarded, because Encode sorts keys anyway.
func FromJSON(r io.Reader) (*Node, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty input")
		}
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the top-level JSON value")
	}
	return fromJSONValue(v, "$")
}

func fromJSONValue(v any, path string) (*Node, error) {
	switch t := v.(type) {
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return nil, fmt.Errorf("%s: bencode has no floats or bignums, got %s", path, t.String())
		}
		return &Node{Kind: KindInt, Int: i}, nil
	case string:
		return &Node{Kind: KindStr, Str: []byte(t)}, nil
	case []any:
		list := make([]*Node, 0, len(t))
		for i, item := range t {
			c, err := fromJSONValue(item, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			list = append(list, c)
		}
		return &Node{Kind: KindList, List: list}, nil
	case map[string]any:
		if s, ok := t[b64Key]; ok && len(t) == 1 {
			enc, ok := s.(string)
			if !ok {
				return nil, fmt.Errorf("%s.%s: expected a base64 string", path, b64Key)
			}
			raw, err := base64.StdEncoding.DecodeString(enc)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %v", path, b64Key, err)
			}
			return &Node{Kind: KindStr, Str: raw}, nil
		}
		if _, ok := t[b64LenKey]; ok && len(t) == 1 {
			return nil, fmt.Errorf("%s: %q only records a length; --brief output cannot be encoded back", path, b64LenKey)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		entries := make([]Entry, 0, len(keys))
		for _, k := range keys {
			c, err := fromJSONValue(t[k], path+"."+k)
			if err != nil {
				return nil, err
			}
			entries = append(entries, Entry{Key: []byte(k), Val: c})
		}
		return &Node{Kind: KindDict, Dict: entries}, nil
	case bool:
		return nil, fmt.Errorf("%s: bencode has no booleans; use 0 or 1", path)
	case nil:
		return nil, fmt.Errorf("%s: bencode has no null", path)
	}
	return nil, fmt.Errorf("%s: unsupported JSON value", path)
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
