package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Builders, so no test has to count string lengths by hand.
func bs(s string) string    { return fmt.Sprintf("%d:%s", len(s), s) }
func bi(n int64) string     { return fmt.Sprintf("i%de", n) }
func bl(v ...string) string { return "l" + strings.Join(v, "") + "e" }
func bd(v ...string) string { return "d" + strings.Join(v, "") + "e" }

func mustParse(t *testing.T, s string) *Document {
	t.Helper()
	d, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return d
}

func TestParseScalars(t *testing.T) {
	tests := []struct {
		in   string
		kind Kind
		i    int64
		s    string
	}{
		{"i0e", KindInt, 0, ""},
		{"i42e", KindInt, 42, ""},
		{"i-42e", KindInt, -42, ""},
		{"i9223372036854775807e", KindInt, 1<<63 - 1, ""},
		{"i-9223372036854775808e", KindInt, -1 << 63, ""},
		{"0:", KindStr, 0, ""},
		{"5:hello", KindStr, 0, "hello"},
		{"3:i3e", KindStr, 0, "i3e"},
		{"4:a:be", KindStr, 0, "a:be"},
	}
	for _, tc := range tests {
		d := mustParse(t, tc.in)
		got := d.Root
		if got.Kind != tc.kind {
			t.Errorf("%q: kind = %v, want %v", tc.in, got.Kind, tc.kind)
			continue
		}
		if got.Kind == KindInt && got.Int != tc.i {
			t.Errorf("%q: int = %d, want %d", tc.in, got.Int, tc.i)
		}
		if got.Kind == KindStr && string(got.Str) != tc.s {
			t.Errorf("%q: str = %q, want %q", tc.in, got.Str, tc.s)
		}
		if len(d.Trailing) != 0 {
			t.Errorf("%q: unexpected trailing %q", tc.in, d.Trailing)
		}
	}
}

func TestParseBinaryString(t *testing.T) {
	raw := []byte{0x00, 0xff, 0x0a, 0x80}
	buf := append([]byte("4:"), raw...)
	d, err := Parse(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.Root.Str, raw) {
		t.Fatalf("got % x, want % x", d.Root.Str, raw)
	}
}

func TestParseContainers(t *testing.T) {
	d := mustParse(t, bl("le", "de", bi(1), bs("two")))
	root := d.Root
	if root.Kind != KindList || len(root.List) != 4 {
		t.Fatalf("root = %v with %d items", root.Kind, len(root.List))
	}
	if root.List[0].Kind != KindList || len(root.List[0].List) != 0 {
		t.Error("first item should be an empty list")
	}
	if root.List[1].Kind != KindDict || len(root.List[1].Dict) != 0 {
		t.Error("second item should be an empty dict")
	}

	d = mustParse(t, bd(bs("a"), bi(1), bs("b"), bl(bs("x"))))
	if v, ok := d.Root.GetInt("a"); !ok || v != 1 {
		t.Errorf(`Get("a") = %d, %v`, v, ok)
	}
	if v := d.Root.Get("b"); v == nil || v.Kind != KindList {
		t.Errorf(`Get("b") = %v`, v)
	}
	if v := d.Root.Get("missing"); v != nil {
		t.Errorf(`Get("missing") = %v, want nil`, v)
	}
	if _, ok := d.Root.GetInt("b"); ok {
		t.Error("GetInt on a list should report not-ok")
	}
	if _, ok := d.Root.GetStr("a"); ok {
		t.Error("GetStr on an int should report not-ok")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		in     string
		offset int
		want   string // substring of the message
	}{
		{"", 0, "empty input"},
		{"i", 0, "no terminating 'e'"},
		{"ie", 1, "no digits"},
		{"i-e", 1, "just a minus sign"},
		{"i-0e", 1, "negative zero"},
		{"i-01e", 1, "negative zero"},
		{"i03e", 1, "leading zero"},
		{"i00e", 1, "leading zero"},
		{"i1 2e", 1, "unexpected \" \" in integer"},
		{"i9223372036854775808e", 1, "does not fit in 64 bits"},
		{"3", 0, "no terminating ':'"},
		{"03:abc", 0, "string length has a leading zero"},
		{"5:abc", 0, "claims 5 bytes but only 3 remain"},
		{"99999999999999999999:x", 0, "does not fit in 64 bits"},
		{"1x:a", 0, "unexpected \"x\" in string length"},
		{"l", 0, "list has no terminating 'e'"},
		{"l1:a", 0, "list has no terminating 'e'"},
		{"d", 0, "dict has no terminating 'e'"},
		{"d1:a", 4, "unexpected end of input"},
		{"di1e1:ae", 1, "dict key must be a string"},
		{"e", 0, "expected 'i', 'l', 'd' or a digit"},
		{"\x00", 0, "byte 0x00"},
	}
	for _, tc := range tests {
		_, err := Parse([]byte(tc.in))
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want error containing %q", tc.in, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want message containing %q", tc.in, err, tc.want)
			continue
		}
		se, ok := err.(*SyntaxError)
		if !ok {
			t.Errorf("Parse(%q) returned %T, want *SyntaxError", tc.in, err)
			continue
		}
		if se.Offset != tc.offset {
			t.Errorf("Parse(%q): offset = %d, want %d (%v)", tc.in, se.Offset, tc.offset, err)
		}
	}
}

func TestParseTrailingData(t *testing.T) {
	d := mustParse(t, "i1e\n\n")
	if string(d.Trailing) != "\n\n" {
		t.Fatalf("Trailing = %q, want two newlines", d.Trailing)
	}
	if d.Root.Int != 1 {
		t.Fatalf("root = %d", d.Root.Int)
	}
}

func TestParseDepthLimit(t *testing.T) {
	deep := strings.Repeat("l", maxDepth+2) + strings.Repeat("e", maxDepth+2)
	if _, err := Parse([]byte(deep)); err == nil {
		t.Fatal("deeply nested input parsed without error")
	} else if !strings.Contains(err.Error(), "nested deeper") {
		t.Fatalf("got %v, want a depth error", err)
	}
	ok := strings.Repeat("l", 20) + strings.Repeat("e", 20)
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatalf("20 levels should be fine: %v", err)
	}
}

// A truncated length header must not be trusted: parsing 8 bytes should never
// make the parser reach past the end of the buffer.
func TestParseTruncatedDoesNotPanic(t *testing.T) {
	full := bd(bs("info"), bd(bs("name"), bs("thing"), bs("pieces"), bs(strings.Repeat("x", 40))))
	for i := 0; i < len(full); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix of length %d: %v", i, r)
				}
			}()
			Parse([]byte(full[:i]))
		}()
	}
}

func TestNodeOffsets(t *testing.T) {
	src := bd(bs("a"), bi(7), bs("info"), bd(bs("k"), bs("v")))
	d := mustParse(t, src)
	info := d.Root.Get("info")
	if got, want := string(d.Raw(info)), bd(bs("k"), bs("v")); got != want {
		t.Fatalf("Raw(info) = %q, want %q", got, want)
	}
	if got, want := string(d.Raw(d.Root.Get("a"))), "i7e"; got != want {
		t.Fatalf("Raw(a) = %q, want %q", got, want)
	}
	if got, want := string(d.Raw(d.Root)), src; got != want {
		t.Fatalf("Raw(root) = %q, want the whole buffer", got)
	}
}

func TestEncodeRoundTripCanonical(t *testing.T) {
	cases := []string{
		"i0e", "i-1e", "0:", "5:hello", "le", "de",
		bl(bi(1), bi(2), bl(bs("x"))),
		bd(bs("a"), bi(1), bs("b"), bs("two")),
		bd(bs("info"), bd(bs("length"), bi(10), bs("name"), bs("f.txt"))),
	}
	for _, in := range cases {
		d := mustParse(t, in)
		if got := string(Encode(d.Root)); got != in {
			t.Errorf("round trip of %q gave %q", in, got)
		}
	}
}

func TestEncodeSortsKeys(t *testing.T) {
	// Keys sort by raw bytes, so "Z" (0x5a) comes before "a" (0x61) and a
	// prefix comes before the longer string that starts with it.
	d := mustParse(t, bd(bs("a"), bi(1), bs("Z"), bi(2), bs("ab"), bi(3), bs("aa"), bi(4)))
	want := bd(bs("Z"), bi(2), bs("a"), bi(1), bs("aa"), bi(4), bs("ab"), bi(3))
	if got := string(Encode(d.Root)); got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
	// The parsed tree keeps the original order for display.
	if string(d.Root.Dict[0].Key) != "a" {
		t.Fatalf("parse reordered the dict: first key is %q", d.Root.Dict[0].Key)
	}
}

func TestEncodeIsIdempotent(t *testing.T) {
	d := mustParse(t, bd(bs("b"), bi(1), bs("a"), bl(bd(bs("z"), bi(0), bs("y"), bi(0)))))
	once := Encode(d.Root)
	d2, err := Parse(once)
	if err != nil {
		t.Fatal(err)
	}
	if twice := Encode(d2.Root); !bytes.Equal(once, twice) {
		t.Fatalf("encode is not idempotent: %q then %q", once, twice)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("d4:infod4:name5:helloee"))
	f.Add([]byte("li1ei-1e0:le"))
	f.Add([]byte("i-0e"))
	f.Add([]byte("5:abc"))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Parse(data)
		if err != nil {
			return
		}
		// Whatever parsed must re-encode to something that parses to the same
		// bytes again; anything else means the tree and the grammar disagree.
		once := Encode(d.Root)
		d2, err := Parse(once)
		if err != nil {
			t.Fatalf("re-parse of encoded output failed: %v (input %q, encoded %q)", err, data, once)
		}
		if twice := Encode(d2.Root); !bytes.Equal(once, twice) {
			t.Fatalf("not idempotent: %q vs %q (input %q)", once, twice, data)
		}
		if len(d2.Trailing) != 0 {
			t.Fatalf("encoded output has trailing bytes: %q", d2.Trailing)
		}
	})
}
