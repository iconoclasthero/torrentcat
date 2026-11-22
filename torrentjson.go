package main

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// TorrentJSONOpts controls the shape of the torrent summary JSON.
type TorrentJSONOpts struct {
	Compact  bool // one line, no indentation
	FileList bool // include every file; off by default, a torrent can have thousands
}

// torrentJSON is the machine-readable twin of ShowTorrent. Field order is the
// output order, so it is deliberate: size sits near the top where a shell
// script that does not want to start jq can still find it.
//
// Keys that every torrent has are always present. Keys that only some have are
// omitted rather than written as null or "", so `jq -r .comment` prints "null"
// only for a file that really has no comment.
type torrentJSON struct {
	Name         string   `json:"name"`
	Size         int64    `json:"size"`  // content bytes, padding files excluded
	Files        int      `json:"files"` // content files, padding files excluded
	PaddingFiles int      `json:"padding_files"`
	PaddingBytes int64    `json:"padding_bytes"`
	PieceLength  int64    `json:"piece_length"`
	Pieces       int      `json:"pieces"`
	Format       string   `json:"format"` // "v1", "v2" or "hybrid"
	InfoHashV1   string   `json:"infohash_v1,omitempty"`
	InfoHashV2   string   `json:"infohash_v2,omitempty"`
	Created      string   `json:"created,omitempty"` // RFC 3339, UTC
	CreatedBy    string   `json:"created_by,omitempty"`
	Comment      string   `json:"comment,omitempty"`
	Source       string   `json:"source,omitempty"`
	Private      bool     `json:"private"`
	Trackers     []string `json:"trackers"`
	WebSeeds     []string `json:"web_seeds"`
	Magnet       string   `json:"magnet"`
	Warnings     []string `json:"warnings"`

	FileList []fileJSON `json:"file_list,omitempty"`
}

type fileJSON struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Padding bool   `json:"padding"`
}

// jsonFormat names the torrent flavour the way the text view does, minus the
// decoration, so a script can compare against a fixed set of strings.
func jsonFormat(t *Torrent) string {
	switch {
	case t.HasV1 && t.HasV2:
		return "hybrid"
	case t.HasV2:
		return "v2"
	case t.HasV1:
		return "v1"
	}
	return ""
}

// nonNil keeps empty lists as [] rather than null, so `jq '.trackers[]'` and
// `.trackers | length` work on a torrent with none.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// WriteTorrentJSON writes the summary of t as JSON followed by a newline.
//
// Text is whatever ParseTorrent produced, which is the same lossy view the
// text summary gives: invalid UTF-8 in a name or path becomes U+FFFD. Use
// `bencat json` or `bencat get` when you need the exact bytes.
func WriteTorrentJSON(w io.Writer, t *Torrent, opts TorrentJSONOpts) error {
	out := torrentJSON{
		Name:         t.Name,
		Size:         t.Total,
		Files:        len(t.Files) - t.PadFiles,
		PaddingFiles: t.PadFiles,
		PaddingBytes: t.PadBytes,
		PieceLength:  t.PieceLength,
		Pieces:       t.PieceCount,
		Format:       jsonFormat(t),
		InfoHashV1:   t.InfoHashV1,
		InfoHashV2:   t.InfoHashV2,
		CreatedBy:    t.CreatedBy,
		Comment:      t.Comment,
		Source:       t.Source,
		Private:      t.Private,
		Trackers:     nonNil(t.Trackers),
		WebSeeds:     nonNil(t.WebSeeds),
		Magnet:       t.Magnet(),
		Warnings:     nonNil(t.Warnings),
	}
	if t.Created != 0 {
		out.Created = time.Unix(t.Created, 0).UTC().Format(time.RFC3339)
	}
	if opts.FileList {
		out.FileList = make([]fileJSON, 0, len(t.Files))
		for _, f := range t.Files {
			out.FileList = append(out.FileList, fileJSON{Path: f.String(), Size: f.Length, Padding: f.Pad})
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Like the bencode JSON writer: this is for terminals and jq, not HTML, so
	// <, > and & stay as they are. Magnet links are full of &.
	enc.SetEscapeHTML(false)
	if !opts.Compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(out); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
