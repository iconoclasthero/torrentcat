package main

import (
	"strings"
	"testing"
)

func TestAuditClean(t *testing.T) {
	d := mustParse(t, bd(bs("a"), bi(1), bs("b"), bl(bd(bs("x"), bi(0), bs("y"), bi(0)))))
	if f := Audit(d); len(f) != 0 {
		t.Fatalf("Audit found %v on a canonical document", f)
	}
}

func TestAuditFindings(t *testing.T) {
	tests := []struct {
		name string
		in   string
		path string
		want string
	}{
		{
			"unsorted keys",
			bd(bs("b"), bi(1), bs("a"), bi(2)),
			"(root)",
			`key "a" sorts before "b"`,
		},
		{
			"duplicate keys",
			bd(bs("a"), bi(1), bs("a"), bi(2)),
			"(root)",
			`duplicate key "a"`,
		},
		{
			"nested, with the path",
			bd(bs("info"), bd(bs("name"), bs("x"), bs("length"), bi(1))),
			"info",
			`key "length" sorts before "name"`,
		},
		{
			"inside a list element",
			bd(bs("files"), bl(bd(bs("path"), bs("p"), bs("length"), bi(1)))),
			"files/0",
			`key "length" sorts before "path"`,
		},
		{
			"trailing data",
			bd(bs("a"), bi(1)) + "junk",
			"(root)",
			"4 bytes of trailing data",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := Audit(mustParse(t, tc.in))
			if len(f) != 1 {
				t.Fatalf("Audit = %v, want exactly one finding", f)
			}
			got := f[0].String()
			if !strings.HasPrefix(got, tc.path+": ") {
				t.Errorf("finding %q does not start with path %q", got, tc.path)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("finding = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// Duplicate keys are reported once per extra copy, and sorting is by raw
// bytes rather than by anything locale-aware.
func TestAuditByteOrderNotLexicalOrder(t *testing.T) {
	// "Z" < "a" in bytes, so this is canonical even though it looks backwards.
	if f := Audit(mustParse(t, bd(bs("Z"), bi(1), bs("a"), bi(2)))); len(f) != 0 {
		t.Fatalf("Audit = %v, want nothing: 0x5a sorts before 0x61", f)
	}
	if f := Audit(mustParse(t, bd(bs("a"), bi(1), bs("Z"), bi(2)))); len(f) != 1 {
		t.Fatalf("Audit = %v, want one finding", f)
	}
}

func TestLookup(t *testing.T) {
	src := bd(
		bs("info"), bd(
			bs("files"), bl(bd(bs("length"), bi(10), bs("path"), bl(bs("dir"), bs("f.txt")))),
			bs("name"), bs("thing"),
		),
	)
	d := mustParse(t, src)

	tests := []struct {
		path string
		want string // rendered as source bytes
	}{
		{"info/name", bs("thing")},
		{"info/files/0/length", "i10e"},
		{"info/files/0/path/1", bs("f.txt")},
		{"/info/name/", bs("thing")},
		{"", src},
	}
	for _, tc := range tests {
		n, err := Lookup(d.Root, tc.path)
		if err != nil {
			t.Errorf("Lookup(%q): %v", tc.path, err)
			continue
		}
		if got := string(d.Raw(n)); got != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestLookupErrors(t *testing.T) {
	d := mustParse(t, bd(bs("info"), bd(bs("files"), bl(bi(1)), bs("name"), bs("x"))))
	tests := []struct{ path, want string }{
		{"nope", `no key "nope" in dict at the root`},
		{"info/nope", `no key "nope" in dict at info`},
		{"info/files/9", "index 9 is out of range for the 1-element list at info/files"},
		{"info/files/x", `info/files is a list; "x" is not an index`},
		{"info/name/more", `info/name is str, so it has no member "more"`},
	}
	for _, tc := range tests {
		_, err := Lookup(d.Root, tc.path)
		if err == nil {
			t.Errorf("Lookup(%q) succeeded, want an error", tc.path)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.path, err, tc.want)
		}
	}
}
