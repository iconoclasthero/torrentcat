package main

import (
	"strings"
	"testing"
)

func render(t *testing.T, src string, opts ShowOpts) string {
	t.Helper()
	var b strings.Builder
	if err := Show(&b, mustParse(t, src).Root, opts); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{833, "833 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048575, "1024.0 KiB"},
		{1048576, "1.0 MiB"},
		{736073537, "702.0 MiB"},
		{1 << 30, "1.0 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1.0 PiB"},
		{1<<60 + 1, "1.0 EiB"},
	}
	for _, tc := range tests {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "file"); got != "1 file" {
		t.Errorf("got %q", got)
	}
	if got := plural(0, "file"); got != "0 files" {
		t.Errorf("got %q", got)
	}
	if got := plural(3, "padding file"); got != "3 padding files" {
		t.Errorf("got %q", got)
	}
}

func TestShowTree(t *testing.T) {
	got := render(t, bd(bs("a"), bi(1), bs("b"), bl(bs("x"), bd(bs("c"), bi(2)))), defaultShowOpts())
	want := strings.Join([]string{
		`dict (2)`,
		`├─ "a": 1`,
		`└─ "b": list (2)`,
		`   ├─ [0]: "x"`,
		`   └─ [1]: dict (1)`,
		`      └─ "c": 2`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestShowScalarRoot(t *testing.T) {
	if got := render(t, "i7e", defaultShowOpts()); got != "7\n" {
		t.Errorf("got %q", got)
	}
	if got := render(t, bs("hi"), defaultShowOpts()); got != "\"hi\"\n" {
		t.Errorf("got %q", got)
	}
}

func TestShowAnnotations(t *testing.T) {
	src := bd(
		bs("creation date"), bi(1755000000),
		bs("info"), bd(
			bs("length"), bi(1048576),
			bs("meta version"), bi(2),
			bs("piece length"), bi(262144),
			bs("pieces"), bs(strings.Repeat("p", 40)),
			bs("private"), bi(1),
		),
	)
	got := render(t, src, defaultShowOpts())
	for _, want := range []string{
		"2025-08-12T12:00:00Z",
		"# 1.0 MiB",
		"# 256.0 KiB",
		"2 SHA-1 piece hashes",
		"private: no DHT, no PEX",
		"BitTorrent v2 (BEP 52)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	// private: 0 says nothing, because it means nothing.
	if strings.Contains(render(t, bd(bs("private"), bi(0)), defaultShowOpts()), "no DHT") {
		t.Error("private: 0 should not be annotated as private")
	}
}

func TestShowEliding(t *testing.T) {
	binary := strings.Repeat("\x00\xff", 40) // 80 bytes, not UTF-8
	opts := defaultShowOpts()
	got := render(t, bs(binary), opts)
	if !strings.Contains(got, "<80 bytes:") || !strings.HasSuffix(strings.TrimSpace(got), "…>") {
		t.Errorf("binary string should be elided, got %s", got)
	}
	full := render(t, bs(binary), ShowOpts{Full: true})
	if strings.Contains(full, "…") {
		t.Errorf("-full should print every byte, got %s", full)
	}
	if len(full) < 160 {
		t.Errorf("-full output is too short to hold 80 hex bytes: %s", full)
	}

	long := strings.Repeat("a", 200)
	got = render(t, bs(long), opts)
	if !strings.Contains(got, "(200 bytes total)") {
		t.Errorf("long text should be truncated with a total, got %s", got)
	}
	if strings.Contains(render(t, bs(long), ShowOpts{Full: true}), "total)") {
		t.Error("-full should not truncate text")
	}
}

// Text with control characters is shown as hex, so a file cannot smuggle
// escape sequences onto the terminal through bencat.
func TestShowTreatsControlCharactersAsBinary(t *testing.T) {
	got := render(t, bs("\x1b[31mred"), defaultShowOpts())
	if strings.Contains(got, "\x1b") {
		t.Fatalf("escape byte reached the output: %q", got)
	}
	if !strings.Contains(got, "1b5b3331") {
		t.Fatalf("got %q, want the bytes in hex", got)
	}
}

func TestShowBinaryKey(t *testing.T) {
	got := render(t, bd(bs(string([]byte{0xff, 0xfe})), bi(1)), defaultShowOpts())
	if !strings.Contains(got, "<2 raw bytes: fffe>") {
		t.Fatalf("got %q", got)
	}
}

func TestShowTorrentSummary(t *testing.T) {
	tor := loadTorrent(t, "testdata/sample.torrent")
	var b strings.Builder
	if err := ShowTorrent(&b, tor, 20); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"name          pop-os_22.04_release",
		"size          702.0 MiB in 3 files",
		"pieces        702 x 1.0 MiB",
		"format        v1",
		"infohash v1   " + sampleHashV1,
		"created       2025-08-12T12:00:00Z",
		"created by    mktorrent 1.1",
		"702.0 MiB  pop-os_22.04_amd64_intel_34.iso",
		"    833 B  checksums/SHA256SUMS.gpg",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "infohash v2") {
		t.Error("a v1 torrent should not print a v2 infohash")
	}
	if strings.Contains(got, "private") {
		t.Error("a public torrent should not print a private line")
	}
}

func TestShowTorrentFileLimit(t *testing.T) {
	tor := loadTorrent(t, "testdata/sample.torrent")
	var b strings.Builder
	if err := ShowTorrent(&b, tor, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "… and 2 more files") {
		t.Errorf("expected a truncation notice:\n%s", b.String())
	}
}

func TestShowTorrentHybridAndPadding(t *testing.T) {
	tor := loadTorrent(t, "testdata/hybrid.torrent")
	var b strings.Builder
	if err := ShowTorrent(&b, tor, 0); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"format        v1 + v2 hybrid",
		"infohash v1   " + hybridHashV1,
		"infohash v2   " + hybridHashV2,
		"padding       12.5 KiB in 1 padding file",
		".pad/12768  (padding)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
}
