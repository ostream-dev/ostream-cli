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

Alternatively, set `OSTREAM_TOKEN` in your environment and skip the login step.

### Push and tail

```sh
# on producer machine
make 2>&1 | ostream push --eof build

# on consumer machine (or tab)
ostream tail build
```

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
