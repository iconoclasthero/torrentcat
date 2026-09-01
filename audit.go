package main

import (
	"bytes"
	"fmt"
	"strconv"
)

// Finding is one thing about a file that is legal enough to parse but will
// make some other program disagree with you.
type Finding struct {
	Path string
	Msg  string
}

func (f Finding) String() string {
	p := f.Path
	if p == "" {
		p = "(root)"
	}
	return p + ": " + f.Msg
}

// Audit walks a parsed document looking for constructions that survive
// parsing but are not canonical: dict keys out of sort order, duplicate keys,
// and bytes after the top-level value. These matter because an infohash is a
// hash of bytes, so two encodings of the same data are two different torrents.
func Audit(d *Document) []Finding {
	var out []Finding
	walk(d.Root, "", &out)
	if len(d.Trailing) > 0 {
		out = append(out, Finding{"", fmt.Sprintf("%d bytes of trailing data after the top-level value", len(d.Trailing))})
	}
	return out
}

func walk(n *Node, path string, out *[]Finding) {
	switch n.Kind {
	case KindList:
		for i, item := range n.List {
			walk(item, join(path, strconv.Itoa(i)), out)
		}
	case KindDict:
		for i, e := range n.Dict {
			if i > 0 {
				prev := n.Dict[i-1].Key
				switch c := bytes.Compare(prev, e.Key); {
				case c == 0:
					*out = append(*out, Finding{path, fmt.Sprintf("duplicate key %q", e.Key)})
				case c > 0:
					*out = append(*out, Finding{path, fmt.Sprintf("key %q sorts before %q but comes after it", e.Key, prev)})
				}
			}
			walk(e.Val, join(path, string(e.Key)), out)
		}
	}
}

func join(path, elem string) string {
	if path == "" {
		return elem
	}
	return path + "/" + elem
}

// Lookup walks a slash-separated path: dict keys by name, list elements by
// decimal index. It returns the node and the path prefix that resolved, so an
// error can say where it ran out of road.
func Lookup(root *Node, path string) (*Node, error) {
	n := root
	done := ""
	for _, elem := range splitPath(path) {
		switch n.Kind {
		case KindDict:
			next := n.Get(elem)
			if next == nil {
				return nil, fmt.Errorf("no key %q in dict at %s", elem, orRoot(done))
			}
			n = next
		case KindList:
			i, err := strconv.Atoi(elem)
			if err != nil {
				return nil, fmt.Errorf("%s is a list; %q is not an index", orRoot(done), elem)
			}
			if i < 0 || i >= len(n.List) {
				return nil, fmt.Errorf("index %d is out of range for the %d-element list at %s", i, len(n.List), orRoot(done))
			}
			n = n.List[i]
		default:
			return nil, fmt.Errorf("%s is %s, so it has no member %q", orRoot(done), n.Kind, elem)
		}
		done = join(done, elem)
	}
	return n, nil
}

func splitPath(p string) []string {
	var out []string
	for _, s := range bytes.Split([]byte(p), []byte("/")) {
		if len(s) > 0 {
			out = append(out, string(s))
		}
	}
	return out
}

func orRoot(p string) string {
	if p == "" {
		return "the root"
	}
	return p
}
