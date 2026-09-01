package main

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The expected hashes below were produced by an independent bencoder in
// Python (hashlib over its own serialisation), not by this code.
const (
	sampleHashV1   = "46ecfccd3b2239e20f1fd3e80cce12784f92fa64"
	unsortedHashV1 = "b53f2c0055cf7a2474fd34d98fd5e797eeec7861"
	hybridHashV1   = "7039eca6d2d2aaf93b2b48cb16c52f2d1185a307"
	hybridHashV2   = "2b0ce3980201393e3c2813275e28199b176d7c3256c22365781a4ce13dde9f9b"
)

func loadTorrent(t *testing.T, path string) *Torrent {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(buf)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	tor, err := ParseTorrent(doc)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return tor
}

// pieces returns n concatenated 20-byte hashes, so a synthetic torrent has a
// piece count that matches its file sizes.
func pieces(n int) string { return strings.Repeat(strings.Repeat("x", 20), n) }

func TestTorrentSingleFile(t *testing.T) {
	src := bd(
		bs("announce"), bs("http://t.example/announce"),
		bs("info"), bd(
			bs("length"), bi(2097152),
			bs("name"), bs("thing.iso"),
			bs("piece length"), bi(1048576),
			bs("pieces"), bs(pieces(2)),
		),
	)
	tor, err := ParseTorrent(mustParse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if !tor.SingleFile {
		t.Error("SingleFile = false")
	}
	if tor.Name != "thing.iso" || tor.Total != 2097152 || tor.PieceCount != 2 {
		t.Errorf("name=%q total=%d pieces=%d", tor.Name, tor.Total, tor.PieceCount)
	}
	if len(tor.Files) != 1 || tor.Files[0].String() != "thing.iso" {
		t.Errorf("Files = %v", tor.Files)
	}
	if !tor.HasV1 || tor.HasV2 {
		t.Errorf("HasV1=%v HasV2=%v", tor.HasV1, tor.HasV2)
	}
	if len(tor.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tor.Warnings)
	}
}

func TestTorrentFixtureMultiFile(t *testing.T) {
	tor := loadTorrent(t, "testdata/sample.torrent")
	if tor.InfoHashV1 != sampleHashV1 {
		t.Errorf("infohash = %s, want %s", tor.InfoHashV1, sampleHashV1)
	}
	if tor.Name != "pop-os_22.04_release" {
		t.Errorf("name = %q", tor.Name)
	}
	if tor.Total != 736073537 {
		t.Errorf("total = %d", tor.Total)
	}
	if len(tor.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(tor.Files))
	}
	if got := tor.Files[1].String(); got != "checksums/SHA256SUMS" {
		t.Errorf("Files[1] = %q, want the path joined with slashes", got)
	}
	if tor.PieceCount != 702 || tor.PieceLength != 1048576 {
		t.Errorf("pieces = %d x %d", tor.PieceCount, tor.PieceLength)
	}
	if len(tor.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tor.Warnings)
	}
	if len(tor.WebSeeds) != 1 || tor.WebSeeds[0] != "https://mirror.example.org/pop-os/" {
		t.Errorf("WebSeeds = %v", tor.WebSeeds)
	}
}

// The infohash covers the info dict's bytes as they appear in the file. This
// torrent's info dict has its keys out of order, so hashing a canonical
// re-encoding of the same data produces a different, wrong, infohash.
func TestTorrentHashesSourceBytesNotReEncoding(t *testing.T) {
	buf, err := os.ReadFile("testdata/unsorted.torrent")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(buf)
	if err != nil {
		t.Fatal(err)
	}
	tor, err := ParseTorrent(doc)
	if err != nil {
		t.Fatal(err)
	}
	if tor.InfoHashV1 != unsortedHashV1 {
		t.Errorf("infohash = %s, want %s", tor.InfoHashV1, unsortedHashV1)
	}
	reEncoded := sha1.Sum(Encode(doc.Root.Get("info")))
	if hex.EncodeToString(reEncoded[:]) == tor.InfoHashV1 {
		t.Fatal("this fixture is supposed to be non-canonical; the test proves nothing")
	}
	if f := Audit(doc); len(f) == 0 {
		t.Error("Audit should have flagged the key order")
	}
}

func TestTorrentHybridFixture(t *testing.T) {
	tor := loadTorrent(t, "testdata/hybrid.torrent")
	if !tor.HasV1 || !tor.HasV2 {
		t.Fatalf("HasV1=%v HasV2=%v, want both", tor.HasV1, tor.HasV2)
	}
	if tor.InfoHashV1 != hybridHashV1 {
		t.Errorf("v1 = %s, want %s", tor.InfoHashV1, hybridHashV1)
	}
	if tor.InfoHashV2 != hybridHashV2 {
		t.Errorf("v2 = %s, want %s", tor.InfoHashV2, hybridHashV2)
	}
	if tor.MetaVersion != 2 {
		t.Errorf("MetaVersion = %d", tor.MetaVersion)
	}
	// The v1 file list wins when both are present, padding files included.
	if len(tor.Files) != 3 {
		t.Fatalf("files = %v", tor.Files)
	}
	if !tor.Files[1].Pad {
		t.Error("Files[1] should be marked as padding (attr contains 'p')")
	}
	if tor.PadFiles != 1 || tor.PadBytes != 12768 {
		t.Errorf("padding = %d files, %d bytes", tor.PadFiles, tor.PadBytes)
	}
	if tor.Total != 25000 {
		t.Errorf("total = %d, want 25000 (padding excluded)", tor.Total)
	}
	if len(tor.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tor.Warnings)
	}
}

// A v2-only torrent has no `files` list; the file list has to come out of the
// BEP 52 file tree, where the empty key marks a leaf.
func TestTorrentV2FileTree(t *testing.T) {
	leaf := func(n int64) string {
		return bd(bs(""), bd(bs("length"), bi(n), bs("pieces root"), bs(strings.Repeat("r", 32))))
	}
	src := bd(bs("info"), bd(
		bs("file tree"), bd(
			bs("a.txt"), leaf(10),
			bs("empty.txt"), leaf(0),
			bs("sub"), bd(bs("deep"), bd(bs("b.bin"), leaf(500))),
		),
		bs("meta version"), bi(2),
		bs("name"), bs("v2only"),
		bs("piece length"), bi(16384),
	))
	tor, err := ParseTorrent(mustParse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if tor.HasV1 || !tor.HasV2 {
		t.Errorf("HasV1=%v HasV2=%v", tor.HasV1, tor.HasV2)
	}
	if tor.InfoHashV1 != "" {
		t.Errorf("a v2-only torrent has no v1 infohash, got %q", tor.InfoHashV1)
	}
	if len(tor.InfoHashV2) != 64 {
		t.Errorf("v2 infohash = %q, want 64 hex chars", tor.InfoHashV2)
	}
	want := []struct {
		path string
		size int64
	}{{"a.txt", 10}, {"empty.txt", 0}, {"sub/deep/b.bin", 500}}
	if len(tor.Files) != len(want) {
		t.Fatalf("files = %v", tor.Files)
	}
	for i, w := range want {
		if tor.Files[i].String() != w.path || tor.Files[i].Length != w.size {
			t.Errorf("Files[%d] = %s (%d), want %s (%d)", i, tor.Files[i], tor.Files[i].Length, w.path, w.size)
		}
	}
	if tor.Total != 510 {
		t.Errorf("total = %d, want 510", tor.Total)
	}
}

func TestTorrentErrors(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"not a dict", bl(bi(1)), "top-level value is list"},
		{"no info", bd(bs("announce"), bs("x")), "no `info` key"},
		{"info is not a dict", bd(bs("info"), bs("x")), "`info` is str"},
		{"no version markers", bd(bs("info"), bd(bs("name"), bs("x"))), "neither `pieces`"},
		{
			"no file information",
			bd(bs("info"), bd(bs("name"), bs("x"), bs("pieces"), bs(pieces(1)))),
			"no `length`, `files` or `file tree`",
		},
		{
			"files is not a list",
			bd(bs("info"), bd(bs("files"), bs("nope"), bs("pieces"), bs(pieces(1)))),
			"`info/files` is str",
		},
		{
			"file entry without length",
			bd(bs("info"), bd(bs("files"), bl(bd(bs("path"), bl(bs("a")))), bs("pieces"), bs(pieces(1)))),
			"`info/files/0` has no `length`",
		},
		{
			"file entry without a path list",
			bd(bs("info"), bd(bs("files"), bl(bd(bs("length"), bi(1))), bs("pieces"), bs(pieces(1)))),
			"`info/files/0` has no `path` list",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTorrent(mustParse(t, tc.src))
			if err == nil {
				t.Fatalf("ParseTorrent succeeded, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestTorrentWarnings(t *testing.T) {
	// pieces is not a multiple of 20, and there are too few of them.
	src := bd(bs("info"), bd(
		bs("length"), bi(10485760),
		bs("name"), bs("x"),
		bs("piece length"), bi(1048576),
		bs("pieces"), bs(strings.Repeat("z", 25)),
	))
	tor, err := ParseTorrent(mustParse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(tor.Warnings, "; ")
	for _, want := range []string{"not a multiple of 20", "piece hashes but the file lengths need 10"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings = %q, want one containing %q", joined, want)
		}
	}
}

func TestTrackersFlattenAndDedupe(t *testing.T) {
	src := bd(
		bs("announce"), bs("http://a/announce"),
		bs("announce-list"), bl(
			bl(bs("http://a/announce"), bs("http://b/announce")),
			bl(bs("udp://c:80/announce")),
			bs("http://d/announce"), // a bare string where a tier belongs
		),
		bs("info"), bd(bs("length"), bi(1), bs("name"), bs("x"), bs("piece length"), bi(1), bs("pieces"), bs(pieces(1))),
	)
	tor, err := ParseTorrent(mustParse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://a/announce", "http://b/announce", "udp://c:80/announce", "http://d/announce"}
	if len(tor.Trackers) != len(want) {
		t.Fatalf("Trackers = %v, want %v", tor.Trackers, want)
	}
	for i := range want {
		if tor.Trackers[i] != want[i] {
			t.Errorf("Trackers[%d] = %q, want %q", i, tor.Trackers[i], want[i])
		}
	}
}

func TestMagnet(t *testing.T) {
	tor := loadTorrent(t, "testdata/sample.torrent")
	got := tor.Magnet()
	if !strings.HasPrefix(got, "magnet:?xt=urn:btih:"+sampleHashV1) {
		t.Errorf("magnet = %s", got)
	}
	// Colons in xt stay literal; everything else is percent-encoded.
	if !strings.Contains(got, "&tr=http%3A%2F%2Ftracker.example.org%3A6969%2Fannounce") {
		t.Errorf("tracker is not percent-encoded: %s", got)
	}
	if !strings.Contains(got, "&dn=pop-os_22.04_release") {
		t.Errorf("missing display name: %s", got)
	}

	hybrid := loadTorrent(t, "testdata/hybrid.torrent").Magnet()
	if !strings.Contains(hybrid, "xt=urn:btih:"+hybridHashV1) {
		t.Errorf("hybrid magnet is missing the v1 urn: %s", hybrid)
	}
	// BEP 52 prefixes the v2 hash with the multihash header 1220.
	if !strings.Contains(hybrid, "xt=urn:btmh:1220"+hybridHashV2) {
		t.Errorf("hybrid magnet is missing the v2 urn: %s", hybrid)
	}
	if strings.Index(hybrid, "btih") > strings.Index(hybrid, "btmh") {
		t.Error("v1 xt should come first so v1-only clients read it")
	}
}

func TestMagnetEscapesSpaces(t *testing.T) {
	src := bd(bs("info"), bd(
		bs("length"), bi(1), bs("name"), bs("two words & more"),
		bs("piece length"), bi(1), bs("pieces"), bs(pieces(1)),
	))
	tor, err := ParseTorrent(mustParse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	// %20 rather than '+': some magnet parsers hand '+' back literally.
	if !strings.Contains(tor.Magnet(), "dn=two%20words%20%26%20more") {
		t.Errorf("magnet = %s", tor.Magnet())
	}
}

func TestDecodeTextKeepsTerminalSafe(t *testing.T) {
	if got := decodeText([]byte("plain ascii")); got != "plain ascii" {
		t.Errorf("got %q", got)
	}
	if got := decodeText([]byte("héllo")); got != "héllo" {
		t.Errorf("valid UTF-8 should pass through, got %q", got)
	}
	got := decodeText([]byte{'a', 0x83, 'b'})
	if got != "a�b" {
		t.Errorf("got %q, want the stray byte replaced", got)
	}
}
