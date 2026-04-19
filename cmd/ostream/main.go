// ostream is the command-line client for ostream.dev.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/ostream-dev/ostream-cli/internal/client"
	"github.com/ostream-dev/ostream-cli/internal/config"
	"github.com/ostream-dev/ostream-cli/internal/crypto"
)

// Set via -ldflags "-X main.version=..." at release time.
var version = "dev"

func main() {
	app := &cli.Command{
		Name:    "ostream",
		Usage:   "stream a pipe over HTTP via ostream.dev",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "url",
				Usage:   "relay base URL (overrides config and OSTREAM_URL)",
				Sources: cli.EnvVars("OSTREAM_URL"),
			},
		},
		Commands: []*cli.Command{
			{
				Name:   "login",
				Usage:  "save an API token for subsequent commands",
				Action: cmdLogin,
			},
			{
				Name:   "logout",
				Usage:  "remove the saved API token",
				Action: cmdLogout,
			},
			{
				Name:   "token",
				Usage:  "print the saved API token",
				Action: cmdToken,
			},
			{
				Name:      "push",
				Usage:     "push stdin or a file to a stream",
				ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "file", Aliases: []string{"f"},
						Usage: "read input from this file instead of stdin (implies --eof by default)"},
					&cli.BoolFlag{Name: "eof", Aliases: []string{"e"},
						Usage: "mark the stream terminated on clean disconnect (default: true with --file, false with stdin)"},
					&cli.BoolFlag{Name: "no-eof",
						Usage: "explicitly disable end-of-stream marking (overrides --eof)"},
					&cli.BoolFlag{Name: "tee", Aliases: []string{"t"},
						Usage: "also copy input to stdout (like UNIX tee)"},
					&cli.StringFlag{Name: "encrypt-with",
						Usage: "encrypt each line client-side with the named key before upload"},
				},
				Action: cmdPush,
			},
			{
				Name:      "tail",
				Aliases:   []string{"pull"},
				Usage:     "stream from a stream to stdout (or a file)",
				ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "file", Aliases: []string{"f"},
						Usage: "append incoming lines to this file instead of stdout"},
					&cli.BoolFlag{Name: "tee", Aliases: []string{"t"},
						Usage: "with --file, also copy to stdout (like UNIX tee)"},
					&cli.IntFlag{Name: "tail", Aliases: []string{"n"},
						Usage: "start from the last N buffered lines (default: all buffered)"},
					&cli.BoolFlag{Name: "no-kick",
						Usage: "refuse to take over from another connected consumer"},
					&cli.StringFlag{Name: "decrypt-with",
						Usage: "decrypt each line client-side with the named key"},
					&cli.BoolFlag{Name: "once",
						Usage: "exit on first connection end (don't reconnect)"},
				},
				Action: cmdTail,
			},
			{
				Name:  "key",
				Usage: "manage local encryption keys",
				Commands: []*cli.Command{
					{
						Name:  "gen",
						Usage: "generate a new symmetric encryption key",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "id", Usage: "key identifier (default: random hex)"},
						},
						Action: cmdKeyGen,
					},
					{
						Name:   "ls",
						Usage:  "list local encryption key IDs",
						Action: cmdKeyLs,
					},
					{
						Name:      "show",
						Usage:     "print a key's file contents (JSON) to stdout",
						ArgsUsage: "<id>",
						Action:    cmdKeyShow,
					},
					{
						Name:  "add",
						Usage: "import a key from stdin or a file",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "file", Aliases: []string{"f"},
								Usage: "read the key JSON from this file (default: stdin)"},
						},
						Action: cmdKeyAdd,
					},
					{
						Name:      "rm",
						Usage:     "delete a key file",
						ArgsUsage: "<id>",
						Action:    cmdKeyRm,
					},
				},
			},
			{
				Name:   "path",
				Usage:  "print the config directory ($HOME/.ostream)",
				Action: cmdPath,
			},
			{
				Name:   "ls",
				Usage:  "list your active streams",
				Action: cmdLs,
			},
			{
				Name:      "rm",
				Usage:     "forcibly delete a stream (kicks any consumer)",
				ArgsUsage: "<path>",
				Action:    cmdRm,
			},
		},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := app.Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "ostream:", err)
		os.Exit(1)
	}
}

// buildClient loads the config, applies the --url override, and constructs
// an HTTP client. Returns an error if a token is required but missing.
func buildClient(cmd *cli.Command, requireToken bool) (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if v := cmd.String("url"); v != "" {
		cfg.RelayURL = v
	}
	if requireToken && cfg.Token == "" {
		return nil, errors.New("no API token — run `ostream login` or set OSTREAM_TOKEN")
	}
	return client.New(cfg.RelayURL, cfg.Token), nil
}

// ------- login / logout / token -------

func cmdLogin(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("Paste an API token from https://app.ostream.dev/keys and press Enter.")
	fmt.Print("Token: ")
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	token := strings.TrimSpace(line)
	if !strings.HasPrefix(token, "os_") {
		return errors.New("token must start with 'os_'")
	}

	cfg, _ := config.Load()
	cfg.Token = token
	if err := config.Save(cfg); err != nil {
		return err
	}

	// Sanity-check the token against the relay.
	cli2, err := buildClient(cmd, true)
	if err != nil {
		return err
	}
	qctx, cancel := client.QuickContext(ctx)
	defer cancel()
	if _, err := cli2.ListStreams(qctx); err != nil {
		return fmt.Errorf("token saved, but relay rejected it: %w", err)
	}
	p, _ := config.Path()
	fmt.Fprintf(os.Stderr, "Saved token to %s\n", p)
	return nil
}

func cmdLogout(ctx context.Context, cmd *cli.Command) error {
	if err := config.Clear(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Token removed.")
	return nil
}

func cmdToken(ctx context.Context, cmd *cli.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Token == "" {
		return errors.New("no token saved")
	}
	fmt.Println(cfg.Token)
	return nil
}

// ------- push / tail -------

func cmdPush(ctx context.Context, cmd *cli.Command) error {
	path, err := requireArg(cmd, "path")
	if err != nil {
		return err
	}
	c, err := buildClient(cmd, true)
	if err != nil {
		return err
	}

	// Input: --file, else stdin.
	var input io.Reader = os.Stdin
	filePath := cmd.String("file")
	if filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		input = f
	}

	// Resolve EOF: --no-eof wins, then explicit --eof, else default true
	// when reading a file (bounded input naturally ends) and false for
	// stdin (usually a live pipe).
	var eof bool
	switch {
	case cmd.Bool("no-eof"):
		eof = false
	case cmd.IsSet("eof"):
		eof = cmd.Bool("eof")
	default:
		eof = filePath != ""
	}

	// Build the body reader. Order matters: tee before encrypt so the
	// user sees plaintext locally, not ciphertext.
	body := input
	if cmd.Bool("tee") {
		body = io.TeeReader(body, os.Stdout)
	}
	if keyID := cmd.String("encrypt-with"); keyID != "" {
		key, err := crypto.LoadKey(keyID)
		if err != nil {
			return fmt.Errorf("load key %q: %w", keyID, err)
		}
		kb, err := key.Bytes()
		if err != nil {
			return err
		}
		body = crypto.EncryptingReader(body, kb)
	}

	return c.Push(ctx, path, body, client.PushOpts{EOF: eof})
}

func cmdTail(ctx context.Context, cmd *cli.Command) error {
	path, err := requireArg(cmd, "path")
	if err != nil {
		return err
	}
	c, err := buildClient(cmd, true)
	if err != nil {
		return err
	}

	opts := client.TailOpts{
		Tail:   cmd.Int("tail"),
		NoKick: cmd.Bool("no-kick"),
	}

	// Output destination: --file if set, else stdout. With --tee and
	// --file, write to both.
	var out io.Writer = os.Stdout
	filePath := cmd.String("file")
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if cmd.Bool("tee") {
			out = io.MultiWriter(f, os.Stdout)
		} else {
			out = f
		}
	}

	// Optional client-side decryption.
	var keyBytes []byte
	if keyID := cmd.String("decrypt-with"); keyID != "" {
		key, err := crypto.LoadKey(keyID)
		if err != nil {
			return fmt.Errorf("load key %q: %w", keyID, err)
		}
		keyBytes, err = key.Bytes()
		if err != nil {
			return err
		}
	}

	once := cmd.Bool("once")

	// Reconnection loop. Each iteration attempts one Tail request. A
	// successful attempt runs to clean EOF (producer sent --eof, the
	// server closed, or the connection dropped — we can't distinguish).
	// We restart unless --once was set or the error is permanent
	// (bad auth, conflict, etc).
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * 500 * time.Millisecond
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			fmt.Fprintf(os.Stderr, "tail: reconnecting in %s...\n", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil
			}
		}

		attemptErr := tailOnce(ctx, c, path, opts, out, keyBytes)
		if ctx.Err() != nil {
			return nil
		}
		if once {
			return attemptErr
		}
		if isPermanent(attemptErr) {
			return attemptErr
		}
	}
}

func tailOnce(ctx context.Context, c *client.Client, path string, opts client.TailOpts, out io.Writer, keyBytes []byte) error {
	if keyBytes == nil {
		return c.Tail(ctx, path, out, opts)
	}
	body, err := c.TailReader(ctx, path, opts)
	if err != nil {
		return err
	}
	defer body.Close()
	return crypto.DecryptCopy(out, body, keyBytes)
}

// isPermanent returns true for errors that a retry won't fix.
func isPermanent(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, client.ErrUnauthorized) ||
		errors.Is(err, client.ErrForbidden) ||
		errors.Is(err, client.ErrNotFound) ||
		errors.Is(err, client.ErrConflict)
}

func cmdKeyGen(ctx context.Context, cmd *cli.Command) error {
	id := cmd.String("id")
	if id == "" {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		id = hex.EncodeToString(b)
	}
	k, err := crypto.GenerateKey(id)
	if err != nil {
		return err
	}
	path, err := crypto.SaveKey(k)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Generated key %q at %s\n", id, path)
	fmt.Fprintln(os.Stderr, "Share the contents of that file out-of-band with anyone who needs to decrypt.")
	fmt.Fprintln(os.Stderr, "To export:  ostream key show "+id)
	return nil
}

func cmdKeyLs(ctx context.Context, cmd *cli.Command) error {
	ids, err := crypto.ListKeys()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "No local keys. Generate one with `ostream key gen`.")
		return nil
	}
	for _, id := range ids {
		fmt.Println(id)
	}
	return nil
}

func cmdKeyShow(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "id")
	if err != nil {
		return err
	}
	k, err := crypto.LoadKey(id)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func cmdKeyAdd(ctx context.Context, cmd *cli.Command) error {
	var r io.Reader = os.Stdin
	if f := cmd.String("file"); f != "" {
		fh, err := os.Open(f)
		if err != nil {
			return err
		}
		defer fh.Close()
		r = fh
	}
	var k crypto.Key
	if err := json.NewDecoder(r).Decode(&k); err != nil {
		return fmt.Errorf("parse key JSON: %w", err)
	}
	if k.ID == "" {
		return errors.New("key JSON is missing an id")
	}
	if k.Algo != crypto.Algo {
		return fmt.Errorf("key uses algo %q; only %q is supported", k.Algo, crypto.Algo)
	}
	// Round-trip the bytes to validate length.
	if _, err := k.Bytes(); err != nil {
		return err
	}
	path, err := crypto.SaveKey(&k)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Imported key %q at %s\n", k.ID, path)
	return nil
}

func cmdKeyRm(ctx context.Context, cmd *cli.Command) error {
	id, err := requireArg(cmd, "id")
	if err != nil {
		return err
	}
	path, err := crypto.KeyPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Removed key %q (%s).\n", id, path)
	return nil
}

func cmdPath(ctx context.Context, cmd *cli.Command) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	fmt.Println(dir)
	return nil
}

// ------- ls / rm -------

func cmdLs(ctx context.Context, cmd *cli.Command) error {
	c, err := buildClient(cmd, true)
	if err != nil {
		return err
	}
	qctx, cancel := client.QuickContext(ctx)
	defer cancel()
	streams, err := c.ListStreams(qctx)
	if err != nil {
		return err
	}
	if len(streams) == 0 {
		fmt.Fprintln(os.Stderr, "No active streams.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tLINES\tCONSUMER")
	for _, s := range streams {
		consumer := "—"
		if s.ConsumerConnected {
			consumer = "connected"
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", s.Path, s.Lines, consumer)
	}
	return w.Flush()
}

func cmdRm(ctx context.Context, cmd *cli.Command) error {
	path, err := requireArg(cmd, "path")
	if err != nil {
		return err
	}
	c, err := buildClient(cmd, true)
	if err != nil {
		return err
	}
	qctx, cancel := client.QuickContext(ctx)
	defer cancel()
	if err := c.DeleteStream(qctx, path); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deleted stream %q.\n", path)
	return nil
}

// requireArg returns cmd.Args().First() or an error if the first positional
// arg is missing.
func requireArg(cmd *cli.Command, name string) (string, error) {
	v := cmd.Args().First()
	if v == "" {
		return "", fmt.Errorf("missing <%s> argument", name)
	}
	return v, nil
}

// unused; keeps the imports honest in case I yank something.
var _ = time.Second
