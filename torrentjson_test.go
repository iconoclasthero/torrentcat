package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeSummary runs `bencat torrent -json args...` and decodes the output
// generically, so the tests see exactly which keys are present.
func decodeSummary(t *testing.T, args ...string) map[string]any {
	t.Helper()
	r := invoke(append([]string{"torrent", "-json"}, args...), "")
	if r.code != exitOK {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, r.stdout)
	}
	return m
}

func TestTorrentJSONSample(t *testing.T) {
	m := decodeSummary(t, "testdata/sample.torrent")
	// float64 holds integers exactly up to 2^53, far above any torrent.
	if m["size"] != float64(736073537) || m["files"] != float64(3) {
		t.Errorf("size=%v files=%v", m["size"], m["files"])
	}
	if m["format"] != "v1" || m["infohash_v1"] != sampleHashV1 {
		t.Errorf("format=%v infohash_v1=%v", m["format"], m["infohash_v1"])
	}
	if _, ok := m["infohash_v2"]; ok {
		t.Error("v1 torrent has infohash_v2")
	}
	if m["pieces"] != float64(702) || m["piece_length"] != float64(1048576) {
		t.Errorf("pieces=%v piece_length=%v", m["pieces"], m["piece_length"])
	}
	if _, ok := m["file_list"]; ok {
		t.Error("file_list present without -a")
	}
	if m["private"] != false {
		t.Errorf("private=%v", m["private"])
	}
	if m["created"] != "2025-08-12T12:00:00Z" {
		t.Errorf("created=%v", m["created"])
	}
	if !strings.HasPrefix(m["magnet"].(string), "magnet:?xt=urn:btih:"+sampleHashV1) {
		t.Errorf("magnet=%v", m["magnet"])
	}
}

func TestTorrentJSONHybridPadding(t *testing.T) {
	m := decodeSummary(t, "testdata/hybrid.torrent")
	if m["size"] != float64(25000) || m["files"] != float64(2) {
		t.Errorf("size=%v files=%v", m["size"], m["files"])
	}
	if m["padding_files"] != float64(1) || m["padding_bytes"] != float64(12768) {
		t.Errorf("padding_files=%v padding_bytes=%v", m["padding_files"], m["padding_bytes"])
	}
	if m["format"] != "hybrid" || m["infohash_v1"] != hybridHashV1 || m["infohash_v2"] != hybridHashV2 {
		t.Errorf("format=%v v1=%v v2=%v", m["format"], m["infohash_v1"], m["infohash_v2"])
	}
}

func TestTorrentJSONFileList(t *testing.T) {
	m := decodeSummary(t, "-a", "testdata/hybrid.torrent")
	list, ok := m["file_list"].([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("file_list = %v", m["file_list"])
	}
	var content, padding float64
	for _, e := range list {
		f := e.(map[string]any)
		if f["padding"] == true {
			padding += f["size"].(float64)
		} else {
			content += f["size"].(float64)
		}
	}
	if content != m["size"] || padding != m["padding_bytes"] {
		t.Errorf("file_list sums content=%v padding=%v disagree with summary", content, padding)
	}
}

func TestTorrentJSONUnsortedKeepsSourceHash(t *testing.T) {
	m := decodeSummary(t, "testdata/unsorted.torrent")
	if m["infohash_v1"] != unsortedHashV1 || m["size"] != float64(1048576) {
		t.Errorf("infohash_v1=%v size=%v", m["infohash_v1"], m["size"])
	}
}

func TestTorrentJSONEmptyListsAreArrays(t *testing.T) {
	src := bd(bs("info"), bd(
		bs("length"), bi(10), bs("name"), bs("x"),
		bs("piece length"), bi(16384), bs("pieces"), bs(pieces(1)),
	))
	r := invoke([]string{"torrent", "-json", "-compact"}, src)
	if r.code != exitOK {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	for _, want := range []string{`"trackers":[]`, `"web_seeds":[]`, `"warnings":[]`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %s in %s", want, r.stdout)
		}
	}
	for _, absent := range []string{`"comment"`, `"created"`, `"created_by"`, `"source"`, `"infohash_v2"`} {
		if strings.Contains(r.stdout, absent) {
			t.Errorf("%s should be omitted: %s", absent, r.stdout)
		}
	}
}

func TestTorrentJSONLayout(t *testing.T) {
	pretty := invoke([]string{"torrent", "-json", "testdata/sample.torrent"}, "")
	compact := invoke([]string{"torrent", "-json", "-compact", "testdata/sample.torrent"}, "")
	if strings.Count(pretty.stdout, "\n") < 5 {
		t.Error("default output is not indented")
	}
	if strings.Count(compact.stdout, "\n") != 1 || !strings.HasSuffix(compact.stdout, "}\n") {
		t.Errorf("compact output is not one line: %q", compact.stdout)
	}
	// Magnet links are full of & and must not be HTML-escaped.
	if !strings.Contains(compact.stdout, "&dn=") || strings.Contains(compact.stdout, "\\u0026") {
		t.Error("ampersand was HTML-escaped")
	}
	if !strings.HasPrefix(compact.stdout, `{"name":"pop-os_22.04_release","size":736073537,`) {
		t.Errorf("key order changed: %.80s", compact.stdout)
	}
}

func TestTorrentJSONMatchesTextView(t *testing.T) {
	m := decodeSummary(t, "testdata/sample.torrent")
	text := invoke([]string{"torrent", "testdata/sample.torrent"}, "").stdout
	if !strings.Contains(text, humanBytes(int64(m["size"].(float64)))+" in 3 files") {
		t.Errorf("text view disagrees with JSON size:\n%s", text)
	}
}

func TestTorrentJSONUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"torrent", "-compact", "testdata/sample.torrent"},
		{"magnet", "-json", "testdata/sample.torrent"},
	} {
		if r := invoke(args, ""); r.code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, r.code, exitUsage)
		}
	}
}

func TestTorrentJSONNotATorrent(t *testing.T) {
	r := invoke([]string{"torrent", "-json"}, bd(bs("a"), bi(1)))
	if r.code != exitError || r.stdout != "" {
		t.Errorf("exit %d stdout %q", r.code, r.stdout)
	}
}
