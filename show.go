package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ShowOpts controls eliding only. There is no colour: the output stays
// diffable and pipe-safe without anyone having to pass a flag.
type ShowOpts struct {
	Full     bool // print byte strings and long text in full
	MaxBytes int  // elide binary strings longer than this
	MaxText  int  // elide text longer than this
}

func defaultShowOpts() ShowOpts { return ShowOpts{MaxBytes: 32, MaxText: 96} }

// Show writes the document as an indented tree, keys in file order.
func Show(w io.Writer, n *Node, opts ShowOpts) error {
	b := &strings.Builder{}
	b.WriteString(header(n, opts))
	b.WriteByte('\n')
	showChildren(b, n, "", "", opts)
	_, err := io.WriteString(w, b.String())
	return err
}

func header(n *Node, opts ShowOpts) string {
	switch n.Kind {
	case KindList:
		return fmt.Sprintf("list (%d)", len(n.List))
	case KindDict:
		return fmt.Sprintf("dict (%d)", len(n.Dict))
	}
	return scalar(n, "", opts)
}

func showChildren(b *strings.Builder, n *Node, prefix, key string, opts ShowOpts) {
	type child struct {
		label string
		node  *Node
		key   string
	}
	var kids []child
	switch n.Kind {
	case KindList:
		for i, item := range n.List {
			// A list inherits its parent's key so that, for example, every
			// element of info/files/*/path is still annotated as a path.
			kids = append(kids, child{"[" + strconv.Itoa(i) + "]", item, key})
		}
	case KindDict:
		for _, e := range n.Dict {
			kids = append(kids, child{quoteKey(e.Key), e.Val, string(e.Key)})
		}
	default:
		return
	}
	for i, k := range kids {
		branch, indent := "├─ ", "│  "
		if i == len(kids)-1 {
			branch, indent = "└─ ", "   "
		}
		b.WriteString(prefix + branch + k.label + ": ")
		switch k.node.Kind {
		case KindList, KindDict:
			b.WriteString(header(k.node, opts))
			b.WriteByte('\n')
			showChildren(b, k.node, prefix+indent, k.key, opts)
		default:
			b.WriteString(scalar(k.node, k.key, opts))
			b.WriteByte('\n')
		}
	}
}

func quoteKey(k []byte) string {
	if utf8.Valid(k) {
		return strconv.Quote(string(k))
	}
	return fmt.Sprintf("<%d raw bytes: %s>", len(k), hexPrefix(k, 8))
}

// scalar renders one int or byte string, with a trailing comment when the key
// tells us what the number means.
func scalar(n *Node, key string, opts ShowOpts) string {
	if n.Kind == KindInt {
		s := strconv.FormatInt(n.Int, 10)
		if note := annotateInt(key, n.Int); note != "" {
			s += "  # " + note
		}
		return s
	}
	s := renderString(n.Str, opts)
	if note := annotateStr(key, n.Str); note != "" {
		s += "  # " + note
	}
	return s
}

func renderString(v []byte, opts ShowOpts) string {
	if utf8.Valid(v) && !hasControl(v) {
		if !opts.Full && utf8.RuneCount(v) > opts.MaxText {
			r := []rune(string(v))
			return strconv.Quote(string(r[:opts.MaxText])) + fmt.Sprintf(" … (%d bytes total)", len(v))
		}
		return strconv.Quote(string(v))
	}
	if opts.Full {
		return fmt.Sprintf("<%d bytes: %s>", len(v), hexPrefix(v, len(v)))
	}
	if len(v) <= opts.MaxBytes {
		return fmt.Sprintf("<%d bytes: %s>", len(v), hexPrefix(v, len(v)))
	}
	return fmt.Sprintf("<%d bytes: %s…>", len(v), hexPrefix(v, opts.MaxBytes))
}

func hasControl(v []byte) bool {
	for _, c := range v {
		if c < 0x20 && c != '\t' {
			return true
		}
	}
	return false
}

func hexPrefix(v []byte, n int) string {
	if n > len(v) {
		n = len(v)
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02x", v[i])
	}
	return b.String()
}

func annotateInt(key string, v int64) string {
	switch key {
	case "creation date":
		return time.Unix(v, 0).UTC().Format(time.RFC3339)
	case "piece length", "length":
		return humanBytes(v)
	case "private":
		if v == 1 {
			return "private: no DHT, no PEX"
		}
	case "meta version":
		if v == 2 {
			return "BitTorrent v2 (BEP 52)"
		}
	}
	return ""
}

func annotateStr(key string, v []byte) string {
	switch key {
	case "pieces":
		if len(v)%20 == 0 {
			return fmt.Sprintf("%d SHA-1 piece hashes", len(v)/20)
		}
		return "SHA-1 piece hashes, but not a multiple of 20 bytes"
	case "pieces root":
		if len(v) == 32 {
			return "SHA-256 merkle root"
		}
	case "ed2k":
		return "eD2k hash"
	}
	return ""
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ShowTorrent writes the summary. maxFiles caps the file listing; 0 means all.
func ShowTorrent(w io.Writer, t *Torrent, maxFiles int) error {
	b := &strings.Builder{}
	row := func(label, val string) {
		if val == "" {
			return
		}
		fmt.Fprintf(b, "%-13s %s\n", label, val)
	}

	row("name", t.Name)
	row("size", fmt.Sprintf("%s in %s", humanBytes(t.Total), plural(len(t.Files)-t.PadFiles, "file")))
	if t.PadFiles > 0 {
		row("padding", fmt.Sprintf("%s in %s", humanBytes(t.PadBytes), plural(t.PadFiles, "padding file")))
	}
	if t.PieceCount > 0 {
		row("pieces", fmt.Sprintf("%d x %s", t.PieceCount, humanBytes(t.PieceLength)))
	} else if t.PieceLength > 0 {
		row("piece size", humanBytes(t.PieceLength))
	}
	row("format", formatLabel(t))
	row("infohash v1", t.InfoHashV1)
	row("infohash v2", t.InfoHashV2)
	if t.Created != 0 {
		row("created", time.Unix(t.Created, 0).UTC().Format(time.RFC3339))
	}
	row("created by", t.CreatedBy)
	row("comment", t.Comment)
	row("source", t.Source)
	if t.Private {
		row("private", "yes (no DHT, no PEX)")
	}
	for i, tr := range t.Trackers {
		if i == 0 {
			row("trackers", tr)
		} else {
			row("", tr)
		}
	}
	for i, ws := range t.WebSeeds {
		if i == 0 {
			row("web seeds", ws)
		} else {
			row("", ws)
		}
	}
	row("magnet", t.Magnet())

	for _, warn := range t.Warnings {
		fmt.Fprintf(b, "%-13s %s\n", "warning", warn)
	}

	if len(t.Files) > 0 {
		b.WriteByte('\n')
		shown := t.Files
		if maxFiles > 0 && len(shown) > maxFiles {
			shown = shown[:maxFiles]
		}
		width := 0
		for _, f := range shown {
			if n := len(humanBytes(f.Length)); n > width {
				width = n
			}
		}
		for _, f := range shown {
			pad := ""
			if f.Pad {
				pad = "  (padding)"
			}
			fmt.Fprintf(b, "%*s  %s%s\n", width, humanBytes(f.Length), f.String(), pad)
		}
		if len(shown) < len(t.Files) {
			fmt.Fprintf(b, "… and %d more files (-a to list them all)\n", len(t.Files)-len(shown))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func formatLabel(t *Torrent) string {
	switch {
	case t.HasV1 && t.HasV2:
		return "v1 + v2 hybrid"
	case t.HasV2:
		return "v2 only"
	case t.HasV1:
		return "v1"
	}
	return ""
}
