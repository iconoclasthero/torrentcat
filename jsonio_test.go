package main

import (
	"bytes"
	"strings"
	"testing"
)

func toJSON(t *testing.T, src string, opts JSONOpts) string {
	t.Helper()
	var b bytes.Buffer
	if err := WriteJSON(&b, mustParse(t, src).Root, opts); err != nil {
		t.Fatalf("WriteJSON(%q): %v", src, err)
	}
	return strings.TrimRight(b.String(), "\n")
}

func TestJSONCompact(t *testing.T) {
	tests := []struct{ in, want string }{
		{"i42e", "42"},
		{"i-42e", "-42"},
		{"0:", `""`},
		{"5:hello", `"hello"`},
		{"le", "[]"},
		{"de", "{}"},
		{bl(bi(1), bs("x")), `[1,"x"]`},
		{bd(bs("b"), bi(1), bs("a"), bi(2)), `{"b":1,"a":2}`}, // file order, not sorted
		{bs("<tag> & \"quote\""), `"<tag> & \"quote\""`},      // no HTML escaping
		{bs("tab\there"), `"tab\there"`},
		{bs("\x01"), `"\u0001"`}, // control bytes get escaped, not dropped
		{bs("héllo ☃"), `"héllo ☃"`},
	}
	for _, tc := range tests {
		if got := toJSON(t, tc.in, JSONOpts{Compact: true}); got != tc.want {
			t.Errorf("json(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestJSONIndented(t *testing.T) {
	got := toJSON(t, bd(bs("a"), bl(bi(1), bi(2))), JSONOpts{})
	want := "{\n  \"a\": [\n    1,\n    2\n  ]\n}"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestJSONBinaryStrings(t *testing.T) {
	raw := string([]byte{0xde, 0xad, 0xbe, 0xef})
	got := toJSON(t, bs(raw), JSONOpts{Compact: true})
	if got != `{"_b64":"3q2+7w=="}` {
		t.Fatalf("got %s", got)
	}
	// --brief drops the payload once it is over the threshold.
	long := string(bytes.Repeat([]byte{0xff}, 40))
	got = toJSON(t, bs(long), JSONOpts{Compact: true, Brief: true, Elide: 32})
	if got != `{"_b64_len":40}` {
		t.Fatalf("brief long = %s", got)
	}
	got = toJSON(t, bs(raw), JSONOpts{Compact: true, Brief: true, Elide: 32})
	if got != `{"_b64":"3q2+7w=="}` {
		t.Fatalf("brief short = %s, want the payload kept", got)
	}
}

// A dict shaped exactly like the byte-string marker cannot be written as JSON
// without becoming ambiguous, so bencat refuses instead of guessing.
func TestJSONRefusesMarkerCollision(t *testing.T) {
	var b bytes.Buffer
	err := WriteJSON(&b, mustParse(t, bd(bs("info"), bd(bs("_b64"), bs("hi")))).Root, JSONOpts{})
	if err == nil {
		t.Fatal("expected an error for a dict that mimics the marker")
	}
	if !strings.Contains(err.Error(), "indistinguishable") || !strings.Contains(err.Error(), "info") {
		t.Fatalf("error = %v, want it to name the path and the problem", err)
	}
	// Two keys is unambiguous, so it is allowed.
	b.Reset()
	if err := WriteJSON(&b, mustParse(t, bd(bs("_b64"), bs("hi"), bs("x"), bi(1))).Root, JSONOpts{Compact: true}); err != nil {
		t.Fatalf("two-key dict should be fine: %v", err)
	}
}

func TestJSONRefusesBinaryKey(t *testing.T) {
	var b bytes.Buffer
	err := WriteJSON(&b, mustParse(t, bd(bs(string([]byte{0xff})), bi(1))).Root, JSONOpts{})
	if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("err = %v, want a complaint about the key", err)
	}
}

func fromJSON(t *testing.T, s string) (string, error) {
	t.Helper()
	n, err := FromJSON(strings.NewReader(s))
	if err != nil {
		return "", err
	}
	return string(Encode(n)), nil
}

func TestFromJSON(t *testing.T) {
	tests := []struct{ in, want string }{
		{"42", "i42e"},
		{"-42", "i-42e"},
		{`"hi"`, "2:hi"},
		{`""`, "0:"},
		{"[]", "le"},
		{"{}", "de"},
		{`[1,"x"]`, bl(bi(1), bs("x"))},
		{`{"b":1,"a":2}`, bd(bs("a"), bi(2), bs("b"), bi(1))}, // sorted on the way out
		{`{"_b64":"3q2+7w=="}`, "4:" + string([]byte{0xde, 0xad, 0xbe, 0xef})},
		{`{"a":{"_b64":"aGk="}}`, bd(bs("a"), bs("hi"))},
		{`{"_b64":"aGk=","x":1}`, bd(bs("_b64"), bs("aGk="), bs("x"), bi(1))}, // two keys: a real dict
		{"9223372036854775807", "i9223372036854775807e"},
	}
	for _, tc := range tests {
		got, err := fromJSON(t, tc.in)
		if err != nil {
			t.Errorf("FromJSON(%s): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FromJSON(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFromJSONErrors(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "empty input"},
		{"1.5", "no floats"},
		{"1e3", "no floats"},
		{"99999999999999999999", "no floats or bignums"},
		{"true", "no booleans"},
		{"null", "no null"},
		{`{"info":{"files":[{"length":null}]}}`, "$.info.files[0].length"},
		{`{"_b64":"not base64!!"}`, "$._b64"},
		{`{"_b64":5}`, "expected a base64 string"},
		{`{"_b64_len":40}`, "only records a length"},
		{"1 2", "trailing data"},
		{"{", "unexpected EOF"},
	}
	for _, tc := range tests {
		_, err := fromJSON(t, tc.in)
		if err == nil {
			t.Errorf("FromJSON(%s) succeeded, want an error", tc.in)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("FromJSON(%s) = %v, want message containing %q", tc.in, err, tc.want)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	cases := []string{
		bd(bs("a"), bi(1), bs("b"), bl(bs("x"), bi(-2))),
		bd(bs("info"), bd(bs("name"), bs("f"), bs("pieces"), bs(string([]byte{0x00, 0xff, 0x10})))),
		"de", "le", "0:",
	}
	for _, in := range cases {
		var b bytes.Buffer
		if err := WriteJSON(&b, mustParse(t, in).Root, JSONOpts{}); err != nil {
			t.Fatalf("WriteJSON(%q): %v", in, err)
		}
		got, err := fromJSON(t, b.String())
		if err != nil {
			t.Fatalf("FromJSON of %s: %v", b.String(), err)
		}
		if got != in {
			t.Errorf("round trip of %q gave %q", in, got)
		}
	}
}
