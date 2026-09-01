package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type result struct {
	code   int
	stdout string
	stderr string
}

func invoke(args []string, stdin string) result {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return result{code, out.String(), errb.String()}
}

func TestCLIHelpAndVersion(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		r := invoke([]string{arg}, "")
		if r.code != exitOK {
			t.Errorf("%s: exit %d, want 0", arg, r.code)
		}
		if !strings.Contains(r.stdout, "usage: bencat") || !strings.Contains(r.stdout, "get PATH") {
			t.Errorf("%s: help text looks wrong:\n%s", arg, r.stdout)
		}
	}
	r := invoke([]string{"--version"}, "")
	if r.code != exitOK || !strings.Contains(r.stdout, version) {
		t.Errorf("--version: exit %d, out %q", r.code, r.stdout)
	}
	// No arguments is a usage error, and the usage goes to stderr.
	r = invoke(nil, "")
	if r.code != exitUsage {
		t.Errorf("no args: exit %d, want %d", r.code, exitUsage)
	}
	if r.stdout != "" || !strings.Contains(r.stderr, "usage: bencat") {
		t.Errorf("no args: stdout %q, stderr %q", r.stdout, r.stderr)
	}
}

func TestCLIDefaultsToShow(t *testing.T) {
	withCmd := invoke([]string{"show", "testdata/sample.torrent"}, "")
	without := invoke([]string{"testdata/sample.torrent"}, "")
	if withCmd.code != exitOK || without.code != exitOK {
		t.Fatalf("exit codes %d and %d", withCmd.code, without.code)
	}
	if withCmd.stdout != without.stdout {
		t.Error("omitting the command should be the same as saying show")
	}
	if !strings.HasPrefix(without.stdout, "dict (7)") {
		t.Errorf("output starts %q", firstLine(without.stdout))
	}
}

func TestCLIReadsStdin(t *testing.T) {
	r := invoke([]string{"show"}, bd(bs("a"), bi(1)))
	if r.code != exitOK || !strings.Contains(r.stdout, `"a": 1`) {
		t.Fatalf("exit %d, out %q, err %q", r.code, r.stdout, r.stderr)
	}
	r = invoke([]string{"show", "-"}, bd(bs("a"), bi(1)))
	if r.code != exitOK {
		t.Fatalf(`"-" should mean stdin: exit %d, err %q`, r.code, r.stderr)
	}
}

func TestCLIExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"clean file", []string{"show", "testdata/sample.torrent"}, exitOK},
		{"missing file", []string{"show", "testdata/does-not-exist"}, exitError},
		{"unknown flag", []string{"show", "-nope", "testdata/sample.torrent"}, exitUsage},
		{"two files", []string{"show", "testdata/sample.torrent", "testdata/hybrid.torrent"}, exitUsage},
		{"get without a path", []string{"get"}, exitUsage},
		{"get a missing key", []string{"get", "nope", "testdata/sample.torrent"}, exitError},
		{"not a torrent", []string{"torrent", "testdata/plain.bencode"}, exitError},
		{"non-canonical", []string{"check", "testdata/unsorted.torrent"}, exitFindings},
		{"canonical", []string{"check", "testdata/sample.torrent"}, exitOK},
	}
	if err := os.WriteFile("testdata/plain.bencode", []byte(bd(bs("a"), bi(1))), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove("testdata/plain.bencode")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := invoke(tc.args, "")
			if r.code != tc.want {
				t.Fatalf("exit %d, want %d (stderr %q)", r.code, tc.want, r.stderr)
			}
		})
	}
}

func TestCLIParseErrorGoesToStderr(t *testing.T) {
	r := invoke([]string{"show"}, "i-0e")
	if r.code != exitError {
		t.Fatalf("exit %d, want %d", r.code, exitError)
	}
	if r.stdout != "" {
		t.Errorf("stdout should be empty, got %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "offset 1") || !strings.Contains(r.stderr, "negative zero") {
		t.Errorf("stderr = %q, want the offset and the reason", r.stderr)
	}
}

func TestCLITrailingDataWarns(t *testing.T) {
	r := invoke([]string{"show"}, "i1e\n")
	if r.code != exitOK {
		t.Fatalf("exit %d: trailing data is a warning, not a failure", r.code)
	}
	if !strings.Contains(r.stderr, "1 bytes of trailing data") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "1" {
		t.Errorf("stdout = %q", r.stdout)
	}
}

// `bencat get info x.torrent | sha1sum` has to reproduce the infohash, which
// only works if get writes the original bytes and adds nothing to them.
func TestCLIGetWritesRawBytes(t *testing.T) {
	r := invoke([]string{"get", "info", "testdata/sample.torrent"}, "")
	if r.code != exitOK {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if !strings.HasPrefix(r.stdout, "d5:files") || !strings.HasSuffix(r.stdout, "e") {
		t.Errorf("get info returned %.20q…%.10q", r.stdout, r.stdout[len(r.stdout)-10:])
	}
	sum := sha1Hex([]byte(r.stdout))
	if sum != sampleHashV1 {
		t.Errorf("sha1 of `get info` = %s, want the infohash %s", sum, sampleHashV1)
	}

	r = invoke([]string{"get", "info/name", "testdata/sample.torrent"}, "")
	if r.stdout != "pop-os_22.04_release" {
		t.Errorf("get info/name = %q, want no trailing newline", r.stdout)
	}
	r = invoke([]string{"get", "info/piece length", "testdata/sample.torrent"}, "")
	if r.stdout != "1048576" {
		t.Errorf("get of an int = %q, want decimal digits", r.stdout)
	}
}

func TestCLIRoundTripThroughJSON(t *testing.T) {
	original, err := os.ReadFile("testdata/sample.torrent")
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"json"}, {"json", "-compact"}} {
		j := invoke(append(flags, "testdata/sample.torrent"), "")
		if j.code != exitOK {
			t.Fatalf("%v: exit %d: %s", flags, j.code, j.stderr)
		}
		back := invoke([]string{"encode"}, j.stdout)
		if back.code != exitOK {
			t.Fatalf("encode: exit %d: %s", back.code, back.stderr)
		}
		if back.stdout != string(original) {
			t.Errorf("%v: round trip changed the file (%d bytes vs %d)", flags, len(back.stdout), len(original))
		}
	}
}

// --brief is readable and lossy; encode should say so rather than silently
// producing a torrent with the piece hashes missing.
func TestCLIBriefIsNotReEncodable(t *testing.T) {
	j := invoke([]string{"json", "-brief", "testdata/sample.torrent"}, "")
	if j.code != exitOK {
		t.Fatal(j.stderr)
	}
	if !strings.Contains(j.stdout, `"_b64_len": 14040`) {
		t.Errorf("expected the pieces blob to be elided:\n%.400s", j.stdout)
	}
	back := invoke([]string{"encode"}, j.stdout)
	if back.code != exitError {
		t.Fatalf("encode of brief JSON: exit %d, want %d", back.code, exitError)
	}
	if !strings.Contains(back.stderr, "only records a length") {
		t.Errorf("stderr = %q", back.stderr)
	}
}

func TestCLICheckMultipleFiles(t *testing.T) {
	r := invoke([]string{"check", "testdata/sample.torrent", "testdata/unsorted.torrent"}, "")
	if r.code != exitFindings {
		t.Fatalf("exit %d, want %d: one bad file should decide the exit code", r.code, exitFindings)
	}
	if !strings.Contains(r.stdout, "sample.torrent: ok, canonical") {
		t.Errorf("stdout = %q", r.stdout)
	}
	if !strings.Contains(r.stdout, `key "length" sorts before "name"`) {
		t.Errorf("stdout = %q", r.stdout)
	}

	q := invoke([]string{"check", "-q", "testdata/unsorted.torrent"}, "")
	if q.code != exitFindings {
		t.Errorf("-q exit %d", q.code)
	}
	if q.stdout != "" {
		t.Errorf("-q printed %q", q.stdout)
	}
}

func TestCLICheckInvalidFileBeatsFindings(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.torrent")
	if err := os.WriteFile(bad, []byte("d3:abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := invoke([]string{"check", "testdata/unsorted.torrent", bad}, "")
	if r.code != exitError {
		t.Fatalf("exit %d, want %d", r.code, exitError)
	}
	if !strings.Contains(r.stdout, "invalid:") {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestCLITorrentAndMagnet(t *testing.T) {
	r := invoke([]string{"torrent", "testdata/sample.torrent"}, "")
	if r.code != exitOK {
		t.Fatal(r.stderr)
	}
	if !strings.Contains(r.stdout, "infohash v1   "+sampleHashV1) {
		t.Errorf("summary:\n%s", r.stdout)
	}

	m := invoke([]string{"magnet", "testdata/sample.torrent"}, "")
	if m.code != exitOK {
		t.Fatal(m.stderr)
	}
	lines := strings.Split(strings.TrimSpace(m.stdout), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "magnet:?xt=urn:btih:") {
		t.Errorf("magnet output = %q, want exactly one magnet URI", m.stdout)
	}
}

func TestCLITorrentAllFiles(t *testing.T) {
	limited := invoke([]string{"torrent", "testdata/sample.torrent"}, "")
	all := invoke([]string{"torrent", "-a", "testdata/sample.torrent"}, "")
	if limited.code != exitOK || all.code != exitOK {
		t.Fatal(limited.stderr, all.stderr)
	}
	if strings.Contains(all.stdout, "and 0 more") {
		t.Error("-a should not print a truncation notice")
	}
}

func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
