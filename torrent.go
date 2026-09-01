package main

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// TFile is one entry from a torrent's file list.
type TFile struct {
	Path   []string
	Length int64
	Pad    bool // BEP 47 padding file, present only to align pieces
}

func (f TFile) String() string { return strings.Join(f.Path, "/") }

// Torrent is the interesting part of a .torrent, flattened.
type Torrent struct {
	Name        string
	PieceLength int64
	PieceCount  int
	SingleFile  bool
	Files       []TFile
	Total       int64 // content bytes, excluding padding files
	PadFiles    int
	PadBytes    int64

	Announce  string
	Trackers  []string
	WebSeeds  []string
	Comment   string
	CreatedBy string
	Created   int64 // unix seconds, 0 if absent
	Private   bool
	Source    string

	MetaVersion int64
	HasV1       bool
	HasV2       bool
	InfoHashV1  string // hex sha1 of the info dict's source bytes
	InfoHashV2  string // hex sha256, BEP 52

	Warnings []string
}

// ParseTorrent interprets an already-parsed document as torrent metadata.
//
// The infohash is a hash of the info dict exactly as it appears on disk, taken
// straight out of the source buffer. Re-serialising the parsed tree and
// hashing that gives the wrong answer for any file whose info dict is not
// canonically sorted, and those exist.
func ParseTorrent(d *Document) (*Torrent, error) {
	root := d.Root
	if root.Kind != KindDict {
		return nil, fmt.Errorf("not a torrent: top-level value is %s, want dict", root.Kind)
	}
	info := root.Get("info")
	if info == nil {
		return nil, fmt.Errorf("not a torrent: no `info` key at the top level")
	}
	if info.Kind != KindDict {
		return nil, fmt.Errorf("not a torrent: `info` is %s, want dict", info.Kind)
	}

	t := &Torrent{}
	raw := d.Raw(info)

	if pieces, ok := info.GetStr("pieces"); ok {
		t.HasV1 = true
		t.PieceCount = len(pieces) / sha1.Size
		if len(pieces)%sha1.Size != 0 {
			t.Warnings = append(t.Warnings, fmt.Sprintf("`pieces` is %d bytes, not a multiple of 20", len(pieces)))
		}
		sum := sha1.Sum(raw)
		t.InfoHashV1 = hex.EncodeToString(sum[:])
	}
	if v, ok := info.GetInt("meta version"); ok {
		t.MetaVersion = v
		if v == 2 {
			t.HasV2 = true
			sum := sha256.Sum256(raw)
			t.InfoHashV2 = hex.EncodeToString(sum[:])
		} else {
			t.Warnings = append(t.Warnings, fmt.Sprintf("`meta version` is %d; only 2 is defined", v))
		}
	}
	if !t.HasV1 && !t.HasV2 {
		return nil, fmt.Errorf("not a torrent: `info` has neither `pieces` (v1) nor `meta version` 2 (v2)")
	}

	if name, ok := info.GetStr("name"); ok {
		t.Name = decodeText(name)
	} else {
		t.Warnings = append(t.Warnings, "`info` has no `name`")
	}
	if pl, ok := info.GetInt("piece length"); ok {
		t.PieceLength = pl
	} else {
		t.Warnings = append(t.Warnings, "`info` has no `piece length`")
	}
	if v, ok := info.GetInt("private"); ok {
		t.Private = v == 1
	}
	if s, ok := info.GetStr("source"); ok {
		t.Source = decodeText(s)
	}

	if err := t.readFiles(info); err != nil {
		return nil, err
	}
	for _, f := range t.Files {
		if f.Pad {
			t.PadFiles++
			t.PadBytes += f.Length
			continue
		}
		t.Total += f.Length
	}

	if s, ok := root.GetStr("announce"); ok {
		t.Announce = decodeText(s)
	}
	t.Trackers = collectTrackers(root)
	t.WebSeeds = collectStrings(root.Get("url-list"))
	if s, ok := root.GetStr("comment"); ok {
		t.Comment = decodeText(s)
	}
	if s, ok := root.GetStr("created by"); ok {
		t.CreatedBy = decodeText(s)
	}
	if v, ok := root.GetInt("creation date"); ok {
		t.Created = v
	}
	if t.PieceLength > 0 && t.HasV1 && t.PieceCount > 0 {
		want := (t.Total + t.PadBytes + t.PieceLength - 1) / t.PieceLength
		if int64(t.PieceCount) != want {
			t.Warnings = append(t.Warnings, fmt.Sprintf("%d piece hashes but the file lengths need %d", t.PieceCount, want))
		}
	}
	return t, nil
}

func (t *Torrent) readFiles(info *Node) error {
	// v1 multi-file
	if files := info.Get("files"); files != nil {
		if files.Kind != KindList {
			return fmt.Errorf("`info/files` is %s, want list", files.Kind)
		}
		for i, f := range files.List {
			if f.Kind != KindDict {
				return fmt.Errorf("`info/files/%d` is %s, want dict", i, f.Kind)
			}
			length, ok := f.GetInt("length")
			if !ok {
				return fmt.Errorf("`info/files/%d` has no `length`", i)
			}
			pathNode := f.Get("path")
			if pathNode == nil || pathNode.Kind != KindList {
				return fmt.Errorf("`info/files/%d` has no `path` list", i)
			}
			var parts []string
			for _, p := range pathNode.List {
				if p.Kind != KindStr {
					return fmt.Errorf("`info/files/%d/path` contains a %s", i, p.Kind)
				}
				parts = append(parts, decodeText(p.Str))
			}
			pad := false
			if attr, ok := f.GetStr("attr"); ok && bytes.ContainsRune(attr, 'p') {
				pad = true
			}
			t.Files = append(t.Files, TFile{Path: parts, Length: length, Pad: pad})
		}
		return nil
	}
	// v1 single-file
	if length, ok := info.GetInt("length"); ok {
		t.SingleFile = true
		t.Files = []TFile{{Path: []string{t.Name}, Length: length}}
		return nil
	}
	// v2 file tree
	if tree := info.Get("file tree"); tree != nil {
		if tree.Kind != KindDict {
			return fmt.Errorf("`info/file tree` is %s, want dict", tree.Kind)
		}
		if err := walkFileTree(tree, nil, &t.Files); err != nil {
			return err
		}
		t.SingleFile = len(t.Files) == 1 && len(t.Files[0].Path) == 1
		return nil
	}
	return fmt.Errorf("`info` has no `length`, `files` or `file tree`")
}

// walkFileTree reads the BEP 52 file tree, where a directory is a dict of
// names and a file is marked by the empty-string key holding its metadata.
func walkFileTree(n *Node, prefix []string, out *[]TFile) error {
	for _, e := range n.Dict {
		if len(e.Key) == 0 {
			if len(prefix) == 0 {
				return fmt.Errorf("`info/file tree` has an empty-key leaf at the root")
			}
			if e.Val.Kind != KindDict {
				return fmt.Errorf("file tree leaf %q is %s, want dict", strings.Join(prefix, "/"), e.Val.Kind)
			}
			length, ok := e.Val.GetInt("length")
			if !ok {
				return fmt.Errorf("file tree leaf %q has no `length`", strings.Join(prefix, "/"))
			}
			path := make([]string, len(prefix))
			copy(path, prefix)
			*out = append(*out, TFile{Path: path, Length: length})
			continue
		}
		if e.Val.Kind != KindDict {
			return fmt.Errorf("file tree node %q is %s, want dict", strings.Join(append(prefix, string(e.Key)), "/"), e.Val.Kind)
		}
		child := append(append([]string{}, prefix...), decodeText(e.Key))
		if err := walkFileTree(e.Val, child, out); err != nil {
			return err
		}
	}
	return nil
}

// collectTrackers flattens `announce` and the `announce-list` tiers into one
// deduplicated list, keeping the order a client would try them in.
func collectTrackers(root *Node) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if s, ok := root.GetStr("announce"); ok {
		add(decodeText(s))
	}
	if al := root.Get("announce-list"); al != nil && al.Kind == KindList {
		for _, tier := range al.List {
			switch tier.Kind {
			case KindStr:
				add(decodeText(tier.Str))
			case KindList:
				for _, u := range tier.List {
					if u.Kind == KindStr {
						add(decodeText(u.Str))
					}
				}
			}
		}
	}
	return out
}

func collectStrings(n *Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == KindStr {
		return []string{decodeText(n.Str)}
	}
	var out []string
	if n.Kind == KindList {
		for _, item := range n.List {
			if item.Kind == KindStr {
				out = append(out, decodeText(item.Str))
			}
		}
	}
	return out
}

// Magnet builds a magnet URI. A hybrid torrent gets both xt parameters, v1
// first, which is what BEP 52 asks for so that v1-only clients still work.
func (t *Torrent) Magnet() string {
	var b strings.Builder
	b.WriteString("magnet:?")
	first := true
	raw := func(k, v string) {
		if !first {
			b.WriteByte('&')
		}
		first = false
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
	}
	param := func(k, v string) { raw(k, escapeMagnet(v)) }
	// xt is a URN of hex digits: percent-encoding its colons is legal but no
	// client writes it that way, and some parsers only match the literal form.
	if t.HasV1 {
		raw("xt", "urn:btih:"+t.InfoHashV1)
	}
	if t.HasV2 {
		raw("xt", "urn:btmh:1220"+t.InfoHashV2)
	}
	if t.Name != "" {
		param("dn", t.Name)
	}
	for _, tr := range t.Trackers {
		param("tr", tr)
	}
	for _, ws := range t.WebSeeds {
		param("ws", ws)
	}
	return b.String()
}

// escapeMagnet percent-encodes for a query string, but writes spaces as %20.
// url.Values would emit '+', which several magnet parsers hand back literally.
func escapeMagnet(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// decodeText keeps valid UTF-8 as-is and replaces anything else, so a torrent
// with Shift-JIS filenames prints something instead of corrupting the terminal.
func decodeText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	for _, c := range b {
		if c < utf8.RuneSelf {
			out.WriteByte(c)
		} else {
			out.WriteRune(utf8.RuneError)
		}
	}
	return out.String()
}
