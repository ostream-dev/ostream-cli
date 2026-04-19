# ostream-cli

Command-line client for [ostream.dev](https://ostream.dev).

Pipe output from one machine, tail it from another, over HTTP. Same as the
raw-`curl` API but with token management, friendlier flags, and more to come
(end-to-end encryption, reconnection on drop, `tee` semantics).

## Install

```sh
go install github.com/ostream-dev/ostream-cli/cmd/ostream@latest
```

Pre-built binaries and Homebrew formula TBD.

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

## Configuration

Stored at `~/.config/ostream/config.json` (mode 0600).

Environment overrides:

| Var | Meaning |
| --- | --- |
| `OSTREAM_TOKEN` | API token (overrides the saved one) |
| `OSTREAM_URL`   | Relay base URL (default `https://ostream.dev`) |

Also available as `--url` flag on the command line.

## Roadmap

- `--encrypt-with <key-id>` / `--decrypt-with <key-id>` — symmetric
  end-to-end encryption (ChaCha20-Poly1305) so the relay never sees plaintext
- `ostream keygen` — mint local encryption keys
- Reconnection on transient drop (for `tail`)
- Homebrew formula and release binaries

## License

TBD.
