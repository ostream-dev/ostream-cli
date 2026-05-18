# ostream-cli

Command-line client for [ostream.dev](https://ostream.dev).

Pipe output from one machine, tail it from another, over HTTP. Same as the
raw-`curl` API but with token management, friendlier flags, and more to come
(end-to-end encryption, reconnection on drop, `tee` semantics).

## Install

### Homebrew (macOS, Linux)

```sh
brew install ostream-dev/tap/ostream
```

### Prebuilt binary

Grab a release archive from
<https://github.com/ostream-dev/ostream-cli/releases> (darwin/linux/windows,
amd64/arm64). Extract and move the `ostream` binary somewhere on your PATH.

### From source

```sh
go install github.com/ostream-dev/ostream-cli/cmd/ostream@latest
```

## Usage

### First-time setup

Create an API key in the dashboard at <https://app.ostream.dev/keys>, then:

```sh
ostream login
# paste the token when prompted
```

Alternatively, set `OSTREAM_TOKEN` in your environment and skip the login
step, or pass `--token <token>` on any individual command (handy for one-off
use of a path-scoped key without overwriting the saved one). Precedence:
`--token` flag > `OSTREAM_TOKEN` env > saved config.

### Push and tail

```sh
# on producer machine
make 2>&1 | ostream push --eof build

# on consumer machine (or tab)
ostream tail build         # aliased as `ostream pull`
```

### Set per-stream retention

By default a stream is reaped after 30 minutes. To keep it longer
(useful for metrics charts), pass `--ttl`:

```sh
echo "a=12 b=34" | ostream push --ttl 7d metrics/demo
```

Accepts Go duration syntax plus `d` (days) and `w` (weeks): `30m`,
`12h`, `7d`, `2w`. The TTL is last-write-wins, so subsequent pushes
inherit it until you set a different one. Plan tiers cap the maximum.

### Post a metric

```sh
# Single datapoint with multiple series. Visit
# https://app.ostream.dev/charts/<path> to see the chart.
ostream metric metrics/cpu a=12 b=34 c=56

# Combined with --ttl so the chart spans more than 30 minutes.
ostream metric --ttl 7d metrics/sales total=42 region_us=20 region_eu=22
```

A thin wrapper over `push`: builds one whitespace-separated `key=value`
line and posts it. Each non-`t` key becomes a chart series. Optional
`t=<unix>` overrides the x-axis with your own timestamp.

### Push from a file

```sh
ostream push -f transcript.log meeting-notes
```

`--eof` defaults to on when `-f` is used (files end, so terminating
the stream fits). Pass `--no-eof` to keep the stream open after the
file is exhausted — useful for appending later with a second push.

### Tail to a file

```sh
ostream tail -f latest.log build
ostream tail -f latest.log --tee build   # also stream to stdout
```

`--file` appends rather than truncates, so successive runs add to
the same log.

### Tee — see locally while streaming

```sh
slow_job | ostream push --tee jobs/tonight
```

Output goes to both your stream AND local stdout.

### Listing and deleting streams

```sh
ostream ls
ostream rm some/stream
```

### Tail options

- `--tail=N` — start from the last N buffered lines (the rest are discarded).
- `--no-kick` — if another consumer is connected, refuse to take over.
- `--peek` — print the currently-buffered lines without draining them, then
  exit. Combine with `--tail=N` for "show me just the last N." Multiple
  `--peek` callers can run concurrently and won't disturb each other or an
  active consumer.

### End of stream

Producers can mark a stream terminated on clean disconnect:

```sh
build_and_test.sh | ostream push --eof releases/v1
```

When the producer finishes, any tailing consumer receives the remaining
lines and then exits. Handy for one-shot scripts.

### Encryption keys

Mint a local symmetric key and use it to encrypt lines client-side:

```sh
ostream key gen --id myproject                  # generate a fresh key
ostream key ls                                  # list ids
ostream key show myproject                      # print the JSON (for export)
echo hello | ostream push --encrypt-with myproject --eof secret-stream
ostream tail --decrypt-with myproject --once secret-stream
```

To share the key with someone else (so they can decrypt):

```sh
# on sender:
ostream key show myproject > myproject.key

# transfer myproject.key via a trusted channel (scp, Signal, ...)

# on receiver:
ostream key add -f myproject.key                # or  cat myproject.key | ostream key add
```

Keys never leave your machine through ostream — the relay stores
ciphertext only.

## Configuration

Everything lives under `~/.ostream/` (on Windows, `%USERPROFILE%\.ostream\`).

```
~/.ostream/
├── config.json         # { token, relay_url }  mode 0600
└── keys/               # encryption keys                 mode 0700
    └── <id>.json       # one per key                     mode 0600
```

Run `ostream path` to print the exact directory.

Environment overrides:

| Var | Meaning |
| --- | --- |
| `OSTREAM_TOKEN` | API token (overrides the saved one) |
| `OSTREAM_URL`   | Relay base URL (default `https://ostream.dev`) |

Also available as `--url` flag on the command line.

## License

MIT. See [LICENSE](LICENSE).
