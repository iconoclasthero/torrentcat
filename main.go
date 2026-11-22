package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

const version = "1.0.0"

// Exit codes: 0 fine, 1 the file is bad or unreadable, 2 you asked wrong,
// 3 (check only) the file parses but is not canonical.
const (
	exitOK       = 0
	exitError    = 1
	exitUsage    = 2
	exitFindings = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `bencat %s — read bencode and .torrent files

usage: bencat [command] [flags] [file]

commands:
  show      print the structure as a tree (the default)
  torrent   summarise a .torrent: size, files, infohash, magnet link
  magnet    print just the magnet link
  json      convert to JSON
  encode    convert JSON back to canonical bencode
  check     validate, and report anything non-canonical
  get PATH  print one value, raw, for piping

With no command, bencat shows the file. A file whose name collides with a
command needs the command spelled out: bencat show ./json

The file argument may be "-" or omitted to read stdin.
Run bencat <command> -h for that command's flags.

examples:
  bencat ubuntu.torrent
  bencat torrent ubuntu.torrent
  bencat json ubuntu.torrent | jq '.info.name'
  bencat torrent -json ubuntu.torrent | jq .size     # total size in bytes
  bencat get info ubuntu.torrent | sha1sum      # the infohash, the long way
  bencat check *.torrent

exit codes: 0 ok, 1 bad file, 2 bad usage, 3 check found something
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, version)
		return exitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintf(stdout, usage, version)
		return exitOK
	case "-V", "--version", "version":
		fmt.Fprintf(stdout, "bencat %s\n", version)
		return exitOK
	}

	cmd, rest := "show", args
	switch args[0] {
	case "show", "torrent", "magnet", "json", "encode", "check", "get":
		cmd, rest = args[0], args[1:]
	}

	c := &cli{stdin: stdin, stdout: stdout, stderr: stderr}
	switch cmd {
	case "show":
		return c.show(rest)
	case "torrent":
		return c.torrent(rest, false)
	case "magnet":
		return c.torrent(rest, true)
	case "json":
		return c.json(rest)
	case "encode":
		return c.encode(rest)
	case "check":
		return c.check(rest)
	case "get":
		return c.get(rest)
	}
	return exitUsage
}

type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (c *cli) fail(format string, a ...any) int {
	fmt.Fprintf(c.stderr, "bencat: "+format+"\n", a...)
	return exitError
}

// errUsage marks a mistake in the command line rather than in the file, so it
// gets exit code 2. The message has already been printed.
var errUsage = errors.New("usage")

func (c *cli) failErr(err error) int {
	if errors.Is(err, errUsage) {
		return exitUsage
	}
	return c.fail("%v", err)
}

func (c *cli) flags(name, argHint string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.Usage = func() {
		fmt.Fprintf(c.stderr, "usage: bencat %s [flags] %s\n", name, argHint)
		fs.PrintDefaults()
	}
	return fs
}

// input reads the one optional file argument, or stdin.
func (c *cli) input(fs *flag.FlagSet) ([]byte, string, error) {
	args := fs.Args()
	if len(args) > 1 {
		fmt.Fprintf(c.stderr, "bencat: expected at most one file, got %d\n", len(args))
		return nil, "", errUsage
	}
	if len(args) == 0 || args[0] == "-" {
		b, err := io.ReadAll(c.stdin)
		return b, "stdin", err
	}
	b, err := os.ReadFile(args[0])
	return b, args[0], err
}

// parse reads and decodes, warning about trailing junk without failing on it.
func (c *cli) parse(fs *flag.FlagSet) (*Document, string, error) {
	buf, name, err := c.input(fs)
	if err != nil {
		return nil, name, err
	}
	doc, err := Parse(buf)
	if err != nil {
		return nil, name, fmt.Errorf("%s: %w", name, err)
	}
	if n := len(doc.Trailing); n > 0 {
		fmt.Fprintf(c.stderr, "bencat: %s: %d bytes of trailing data after the top-level value\n", name, n)
	}
	return doc, name, nil
}

func (c *cli) show(args []string) int {
	fs := c.flags("show", "[file]")
	full := fs.Bool("full", false, "print byte strings and long text in full")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	doc, _, err := c.parse(fs)
	if err != nil {
		return c.failErr(err)
	}
	opts := defaultShowOpts()
	opts.Full = *full
	if err := Show(c.stdout, doc.Root, opts); err != nil {
		return c.fail("%v", err)
	}
	return exitOK
}

func (c *cli) torrent(args []string, magnetOnly bool) int {
	name := "torrent"
	if magnetOnly {
		name = "magnet"
	}
	fs := c.flags(name, "[file]")
	all := fs.Bool("a", false, "list every file, not just the first 20 (with -json: include file_list)")
	var asJSON, compact *bool
	if !magnetOnly {
		asJSON = fs.Bool("json", false, "print the summary as JSON instead of text")
		compact = fs.Bool("compact", false, "with -json, one line, no indentation")
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if compact != nil && *compact && !*asJSON {
		fmt.Fprintln(c.stderr, "bencat: -compact only applies with -json")
		return exitUsage
	}
	doc, src, err := c.parse(fs)
	if err != nil {
		return c.failErr(err)
	}
	t, err := ParseTorrent(doc)
	if err != nil {
		return c.fail("%s: %v", src, err)
	}
	if magnetOnly {
		fmt.Fprintln(c.stdout, t.Magnet())
		return exitOK
	}
	if *asJSON {
		if err := WriteTorrentJSON(c.stdout, t, TorrentJSONOpts{Compact: *compact, FileList: *all}); err != nil {
			return c.fail("%v", err)
		}
		return exitOK
	}
	maxFiles := 20
	if *all {
		maxFiles = 0
	}
	if err := ShowTorrent(c.stdout, t, maxFiles); err != nil {
		return c.fail("%v", err)
	}
	return exitOK
}

func (c *cli) json(args []string) int {
	fs := c.flags("json", "[file]")
	compact := fs.Bool("compact", false, "one line, no indentation")
	brief := fs.Bool("brief", false, "replace long binary strings with their length (not re-encodable)")
	elide := fs.Int("elide", 32, "with -brief, the length above which binary strings are elided")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	doc, _, err := c.parse(fs)
	if err != nil {
		return c.failErr(err)
	}
	opts := JSONOpts{Compact: *compact, Brief: *brief, Elide: *elide}
	if err := WriteJSON(c.stdout, doc.Root, opts); err != nil {
		return c.fail("%v", err)
	}
	return exitOK
}

func (c *cli) encode(args []string) int {
	fs := c.flags("encode", "[file.json]")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	buf, name, err := c.input(fs)
	if err != nil {
		return c.failErr(err)
	}
	node, err := FromJSON(bytesReader(buf))
	if err != nil {
		return c.fail("%s: %v", name, err)
	}
	if _, err := c.stdout.Write(Encode(node)); err != nil {
		return c.fail("%v", err)
	}
	return exitOK
}

func (c *cli) check(args []string) int {
	fs := c.flags("check", "[file...]")
	quiet := fs.Bool("q", false, "print nothing, just set the exit code")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	names := fs.Args()
	if len(names) == 0 {
		names = []string{"-"}
	}
	worst := exitOK
	for _, name := range names {
		var buf []byte
		var err error
		if name == "-" {
			buf, err = io.ReadAll(c.stdin)
			name = "stdin"
		} else {
			buf, err = os.ReadFile(name)
		}
		if err != nil {
			c.fail("%v", err)
			worst = exitError
			continue
		}
		doc, err := Parse(buf)
		if err != nil {
			if !*quiet {
				fmt.Fprintf(c.stdout, "%s: invalid: %v\n", name, err)
			}
			worst = exitError
			continue
		}
		findings := Audit(doc)
		if len(findings) == 0 {
			if !*quiet {
				fmt.Fprintf(c.stdout, "%s: ok, canonical\n", name)
			}
			continue
		}
		if !*quiet {
			fmt.Fprintf(c.stdout, "%s: valid, but not canonical\n", name)
			for _, f := range findings {
				fmt.Fprintf(c.stdout, "  %s\n", f)
			}
		}
		if worst == exitOK {
			worst = exitFindings
		}
	}
	return worst
}

// get prints one value with nothing added: strings as their bytes, integers as
// digits, and lists or dicts as the exact source bytes they were parsed from.
// That last part is what makes `bencat get info x.torrent | sha1sum` work.
func (c *cli) get(args []string) int {
	fs := c.flags("get", "PATH [file]")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(c.stderr, "bencat: get needs a path, for example: bencat get info/name x.torrent")
		return exitUsage
	}
	path := fs.Arg(0)
	sub := flag.NewFlagSet("get", flag.ContinueOnError)
	sub.SetOutput(c.stderr)
	if err := sub.Parse(fs.Args()[1:]); err != nil {
		return exitUsage
	}
	doc, src, err := c.parse(sub)
	if err != nil {
		return c.failErr(err)
	}
	node, err := Lookup(doc.Root, path)
	if err != nil {
		return c.fail("%s: %v", src, err)
	}
	var out []byte
	switch node.Kind {
	case KindStr:
		out = node.Str
	case KindInt:
		out = []byte(strconv.FormatInt(node.Int, 10))
	default:
		out = doc.Raw(node)
	}
	if _, err := c.stdout.Write(out); err != nil {
		return c.fail("%v", err)
	}
	return exitOK
}
