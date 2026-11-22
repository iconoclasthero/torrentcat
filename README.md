# bencat

Read bencode and `.torrent` files from the command line: tree view, torrent summary, infohash, magnet link, JSON in and out.

Bencode is the format BitTorrent uses for metadata. It is four types (integers, byte strings, lists, dictionaries), no schema, and no way to tell text from binary. `.torrent` files are bencode, and so are BitTorrent session files, resume files, DHT dumps and a scattering of other things that never expected to be looked at by a human.

`bencat` opens them without a BitTorrent client.

## Why

Three specific annoyances:

A `.torrent` arrives and you want to know what is in it — how big, how many files, which trackers — before you hand it to anything that will start talking to the internet on your behalf. Opening a client to answer that is backwards.

You want the infohash. Every online snippet for this is four lines of Python that re-encodes the parsed `info` dictionary and hashes the result, which gives the wrong answer for any torrent whose `info` dictionary is not canonically sorted. Those torrents exist; see the design notes below.

You want to edit one field and hand the file back. `bencat json | jq | bencat encode` does that, and the round trip is byte-identical for a canonical file.

## Install

Go 1.22 or newer. No dependencies outside the standard library.

```
go install github.com/mangodistro/bencat@latest
```

Or from a clone:

```
go build -o bencat .
```

## Usage

```
bencat [command] [flags] [file]

show      print the structure as a tree (the default)
torrent   summarise a .torrent: size, files, infohash, magnet link
magnet    print just the magnet link
json      convert to JSON
encode    convert JSON back to canonical bencode
check     validate, and report anything non-canonical
get PATH  print one value, raw, for piping
```

The file may be `-` or omitted to read standard input. With no command, `bencat` shows the file; a file whose name collides with a command needs the command spelled out (`bencat show ./json`).

### Summarise a torrent

```
$ bencat torrent testdata/sample.torrent
name          pop-os_22.04_release
size          702.0 MiB in 3 files
pieces        702 x 1.0 MiB
format        v1
infohash v1   46ecfccd3b2239e20f1fd3e80cce12784f92fa64
created       2025-08-12T12:00:00Z
created by    mktorrent 1.1
comment       Pop!_OS 22.04 LTS, Intel/AMD build
trackers      http://tracker.example.org:6969/announce
              udp://open.tracker.example:1337/announce
web seeds     https://mirror.example.org/pop-os/
magnet        magnet:?xt=urn:btih:46ecfccd3b2239e20f1fd3e80cce12784f92fa64&dn=pop-os_22.04_release&tr=http%3A%2F%2Ftracker.example.org%3A6969%2Fannounce&tr=udp%3A%2F%2Fopen.tracker.example%3A1337%2Fannounce&ws=https%3A%2F%2Fmirror.example.org%2Fpop-os%2F

702.0 MiB  pop-os_22.04_amd64_intel_34.iso
  1.0 KiB  checksums/SHA256SUMS
    833 B  checksums/SHA256SUMS.gpg
```

BitTorrent v2 and hybrid torrents get both hashes, and the file list comes out of the BEP 52 file tree when there is no v1 `files` list. Padding files are counted separately from content:

```
$ bencat torrent testdata/hybrid.torrent
name          hybrid-demo
size          24.4 KiB in 2 files
padding       12.5 KiB in 1 padding file
pieces        3 x 16.0 KiB
format        v1 + v2 hybrid
infohash v1   7039eca6d2d2aaf93b2b48cb16c52f2d1185a307
infohash v2   2b0ce3980201393e3c2813275e28199b176d7c3256c22365781a4ce13dde9f9b
```

### Summarise a torrent as JSON

`-json` prints the same summary for scripts. `size` is the exact content size in bytes, padding files excluded; `-compact` makes it one line; `-a` adds a `file_list`.

```
$ bencat torrent -json -compact testdata/unsorted.torrent
{"name":"outoforder","size":1048576,"files":1,"padding_files":0,"padding_bytes":0,"piece_length":262144,"pieces":4,"format":"v1","infohash_v1":"b53f2c0055cf7a2474fd34d98fd5e797eeec7861","private":false,"trackers":["http://t.example/a"],"web_seeds":[],"magnet":"magnet:?xt=urn:btih:b53f2c0055cf7a2474fd34d98fd5e797eeec7861&dn=outoforder&tr=http%3A%2F%2Ft.example%2Fa","warnings":[]}

$ bencat torrent -json testdata/sample.torrent | jq .size
736073537
```

Keys every torrent has are always present; `infohash_v2`, `created`, `created_by`, `comment` and `source` are omitted when the file has none. Empty lists are `[]`, never `null`. Names and paths are the same lossy text the text view shows (invalid UTF-8 becomes U+FFFD); use `bencat json` or `bencat get` for exact bytes.

### Look at the structure

```
$ bencat testdata/sample.torrent
dict (7)
├─ "announce": "http://tracker.example.org:6969/announce"
├─ "announce-list": list (2)
│  ├─ [0]: list (1)
│  │  └─ [0]: "http://tracker.example.org:6969/announce"
│  └─ [1]: list (1)
│     └─ [0]: "udp://open.tracker.example:1337/announce"
├─ "comment": "Pop!_OS 22.04 LTS, Intel/AMD build"
...
│  ├─ "piece length": 1048576  # 1.0 MiB
│  └─ "pieces": <14040 bytes: 1cd0428d debfd0c5 8659d721 def290ef ac985346 983b5dc3 94b62435 18e693fb…>  # 702 SHA-1 piece hashes
└─ "url-list": list (1)
   └─ [0]: "https://mirror.example.org/pop-os/"
```

Byte strings are shown as hex and elided; `-full` prints them whole. The `#` comments come from the key: `creation date` becomes a timestamp, `piece length` becomes a size, `pieces` becomes a hash count.

### Pipe it around

`get` writes one value with nothing added — no newline, no quoting. Strings come out as their bytes, integers as digits, and lists and dictionaries as the exact bytes they were parsed from, which means you can compute the infohash the long way round:

```
$ bencat get info testdata/sample.torrent | sha1sum
46ecfccd3b2239e20f1fd3e80cce12784f92fa64  -

$ bencat json testdata/sample.torrent | jq -r '.info.files[] | "\(.length)\t\(.path|join("/"))"'
736071680	pop-os_22.04_amd64_intel_34.iso
1024	checksums/SHA256SUMS
833	checksums/SHA256SUMS.gpg
```

Paths are slash-separated, with decimal indexes into lists: `bencat get info/files/0/path/0 x.torrent`.

### Check a file

```
$ bencat check testdata/*.torrent
testdata/hybrid.torrent: ok, canonical
testdata/sample.torrent: ok, canonical
testdata/unsorted.torrent: valid, but not canonical
  info: key "length" sorts before "name" but comes after it
$ echo $?
3
```

Exit codes: `0` fine, `1` the file is bad or unreadable, `2` the command line is wrong, `3` (`check` only) the file parses but is not canonical.

## Design decisions

**The infohash is taken from the source bytes, not from a re-encoding.** A torrent's infohash is `SHA-1(info dictionary as it appears in the file)`. The tempting implementation — parse, re-serialise the `info` subtree canonically, hash that — agrees with the file only when the file was already canonical. `testdata/unsorted.torrent` has `name` written before `length` inside `info`, and the two approaches disagree:

```
source bytes:  b53f2c0055cf7a2474fd34d98fd5e797eeec7861   <- what every client uses
re-encoded:    a35af60f6d1195fe31745a2fe661541195206e7c   <- wrong
```

So every parsed node carries the byte offsets it came from, and `Document.Raw(node)` hands back the original slice. The cost is real: the whole file has to stay in memory, the parser can never become a streaming decoder, and `Node` is two `int`s bigger than it needs to be. For files that top out around a megabyte, that is a good trade. `TestTorrentHashesSourceBytesNotReEncoding` fails if anyone ever "simplifies" it back.

**Dictionaries are ordered slices, not maps.** Lookup is a linear scan, which would be indefensible for a general-purpose decoder and is invisible here — torrent dictionaries have single-digit key counts. What it buys: `show` prints keys in file order, `check` can see that they are out of order or duplicated, and a file with a duplicate key round-trips instead of quietly losing one of them.

**Integer syntax is enforced strictly.** `i-0e` and `i03e` are rejected with the offset. Plenty of decoders accept them and normalise to `0` and `3` — which means a file containing one hashes differently depending on who reads it, and a hash that depends on the reader is not a hash. The cost is that `bencat` refuses a small number of files other tools will open.

**Trailing data after the top-level value is a warning, not an error.** Some torrents carry padding or a stray newline. Refusing to show you the file because of two bytes at the end helps nobody, so it goes to stderr and `check` lists it.

**Non-UTF-8 byte strings become `{"_b64": "..."}` in JSON.** Bencode strings are bytes; JSON strings are text; the 20-bytes-per-piece SHA-1 blob in every torrent is not text. Handing it to Go's JSON encoder replaces every invalid byte with U+FFFD, so the round trip silently destroys the file.

The alternative I rejected was encoding every byte as a latin-1 code point (`Ã©` for `é`), which is exactly lossless and needs no marker. It also makes every non-ASCII filename in the output unreadable, and reading is what this command is for. The marker keeps ordinary text ordinary.

The marker has one ambiguity: a bencode dictionary whose only key is `_b64` with a string value is indistinguishable from an encoded byte string. Rather than guess, `bencat json` refuses that file and names the path. `-brief` swaps long blobs for `{"_b64_len": 14040}`, which is readable and deliberately cannot be encoded back — `bencat encode` says so instead of writing a torrent with the piece hashes missing.

**JSON output preserves key order; JSON input does not have to.** The writer is hand-rolled so that dictionary order survives into the output, because seeing the real order is the point. The reader uses `encoding/json` into a map, order and all thrown away, because `Encode` sorts keys canonically regardless. The asymmetry looks odd in the source and saves writing a second JSON parser.

**No colour.** Output is the same whether it goes to a terminal or a pipe, and it diffs cleanly. Byte strings containing control characters are always rendered as hex, so a hostile `.torrent` cannot smuggle escape sequences onto your terminal through the `comment` field.

**Go, for a format parser that is mostly byte slicing and hashing.** `crypto/sha1`, `crypto/sha256`, `encoding/base64` and `encoding/json` are all in the standard library, `[]byte` slices of the source cost nothing, and the result is one static binary with no runtime to install. The eventual annoyance is that Go's `flag` package has no real subcommand support, so the dispatch in `main.go` is written out by hand.

### Two things that were wrong first

The magnet link percent-encoded the whole `xt` parameter, producing `xt=urn%3Abtih%3A46ecf...`. That is a legal URI and no client writes it that way; some magnet parsers only match the literal `urn:btih:` prefix. The colons in `xt` are now left alone while trackers stay encoded, which is what real clients emit.

`show` ignored `-full` when the top-level value was a single string or integer, because the code that renders the root passed a hard-coded options struct instead of the caller's. `TestShowEliding` caught it. Nobody would have noticed on a torrent, where the root is always a dictionary — it only showed up when piping a bare bencoded string into `bencat`.

## What it does not do

Nothing on the network, and nothing on disk beyond the file you name. It will not verify piece hashes against downloaded data, create torrents, edit files in place, or check BEP 52 piece layers against the merkle roots in the file tree. It reports what the file says.

## Tests

```
go test ./...              # 69 tests, 95 including subtests
go test -fuzz=FuzzParse    # the parser, against arbitrary bytes
```

The suite covers integer and string grammar edge cases with their expected error offsets, the depth limit, every truncated prefix of a valid file (nothing may panic or read past the end), canonical re-encoding and key sort order, path lookup errors, the JSON marker rules in both directions, v1 single-file, v1 multi-file, v2 file-tree and hybrid torrents, padding files, tracker flattening, magnet formatting, and every exit code through the real `run()` entry point.

The expected infohashes in `torrent_test.go` were produced by an independent bencoder written in Python, not by this code, so a bug in `Encode` cannot make them agree with themselves. `FuzzParse` asserts that anything which parses re-encodes to something that parses back identically; 4.3 million executions have not found a case where it does not.

## Licence

MIT. See [LICENSE](LICENSE).
