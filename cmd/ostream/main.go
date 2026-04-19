// ostream is the command-line client for ostream.dev.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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
				Usage:     "push stdin to a stream",
				ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "eof", Aliases: []string{"e"},
						Usage: "on clean disconnect, mark the stream terminated (consumers exit)"},
					&cli.BoolFlag{Name: "tee", Aliases: []string{"t"},
						Usage: "also copy stdin to stdout (like UNIX tee)"},
					&cli.StringFlag{Name: "encrypt-with",
						Usage: "encrypt each line client-side with the named key before upload"},
				},
				Action: cmdPush,
			},
			{
				Name:      "tail",
				Usage:     "stream from a stream to stdout",
				ArgsUsage: "<path>",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "tail", Aliases: []string{"n"},
						Usage: "start from the last N buffered lines (default: all buffered)"},
					&cli.BoolFlag{Name: "no-kick",
						Usage: "refuse to take over from another connected consumer"},
					&cli.StringFlag{Name: "decrypt-with",
						Usage: "decrypt each line client-side with the named key"},
				},
				Action: cmdTail,
			},
			{
				Name:   "keygen",
				Usage:  "generate a symmetric encryption key stored locally",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "id", Usage: "key identifier (default: random hex)"},
				},
				Action: cmdKeygen,
			},
			{
				Name:   "keys",
				Usage:  "list local encryption key IDs",
				Action: cmdKeys,
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

	// Build the body reader. Order matters: tee must come before encrypt
	// so the user sees plaintext locally, not ciphertext.
	var body io.Reader = os.Stdin
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

	return c.Push(ctx, path, body, client.PushOpts{EOF: cmd.Bool("eof")})
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

	// With --decrypt-with, the response body is base64url ciphertext
	// (one per line). Route it through DecryptCopy instead of the
	// plain io.Copy inside client.Tail.
	if keyID := cmd.String("decrypt-with"); keyID != "" {
		key, err := crypto.LoadKey(keyID)
		if err != nil {
			return fmt.Errorf("load key %q: %w", keyID, err)
		}
		kb, err := key.Bytes()
		if err != nil {
			return err
		}
		body, err := c.TailReader(ctx, path, opts)
		if err != nil {
			return err
		}
		defer body.Close()
		return crypto.DecryptCopy(os.Stdout, body, kb)
	}

	return c.Tail(ctx, path, os.Stdout, opts)
}

func cmdKeygen(ctx context.Context, cmd *cli.Command) error {
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
	return nil
}

func cmdKeys(ctx context.Context, cmd *cli.Command) error {
	ids, err := crypto.ListKeys()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintln(os.Stderr, "No local keys. Generate one with `ostream keygen`.")
		return nil
	}
	for _, id := range ids {
		fmt.Println(id)
	}
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
