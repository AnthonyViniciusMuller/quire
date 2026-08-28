// Command quirectl is the reference Quire client: a device, driven from a
// terminal.
//
// It is what the end-to-end suites drive and what stands in for the Flutter
// application when the system is demonstrated (D05 in
// docs/tcc-corrections.md). Everything it can do, a device can do, and it does
// it the way a device does: it is bound to an origin server, it carries an
// identifier every vector clock entry is keyed by, and it keeps its state
// between two commands in a file — so two state files on one machine are two
// devices, which is what makes a demonstration of UC10 possible on a laptop.
//
// This file and the ones beside it decide nothing. They read flags, call one
// method of internal/client, and print what comes back; the client is where a
// change is stamped, queued or sent. That is the same division cmd/quired
// follows, and the same reason: the program at the edge should be the only
// part that knows about a terminal, and nothing below it should know there is
// one.
//
// The environment is read here and nowhere else, for the reason nothing below
// cmd/quired reads one either.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/anthonyvsmuller/quire/internal/client"
)

// The variables the flags default to, so that a shell that exports them once —
// a demonstration, a docker compose, an end-to-end run — does not repeat them
// on every command.
const (
	serverVariable = "QUIRECTL_SERVER"
	stateVariable  = "QUIRECTL_STATE"
)

// stateFileName is where a device keeps itself when it was not told otherwise.
const stateFileName = "quirectl.json"

func main() { os.Exit(exitCode()) }

// exitCode runs the program and reports what the process should exit with.
//
// It is separate from main because os.Exit runs no deferred function, and the
// signal handler has to be released: a program that exits without doing it
// leaves the terminal's interrupt going somewhere that no longer exists.
func exitCode() int {
	// SIGINT is how a reader stops `quirectl sync watch`, which is the one
	// command that does not end on its own.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "quirectl: "+describe(err))

		return 1
	}

	return 0
}

// run executes one command and returns what it failed with.
//
// It takes its arguments and its streams rather than reading the globals, so
// that a test can run the whole program and read what it printed.
func run(ctx context.Context, args []string, out, failures io.Writer) error {
	application := &app{out: out}

	// The context is cobra's own: ExecuteContext puts it there and
	// command.Context() takes it back, which the linter cannot follow.
	//nolint:contextcheck // the context reaches every command through cobra.
	root := newRootCommand(application)
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(failures)

	return root.ExecuteContext(ctx)
}

// app is what every command shares: how to reach the node, and where to write.
type app struct {
	options client.Options
	json    bool
	out     io.Writer
}

// newRootCommand builds the command tree.
//
// It is a function and not a package-level variable, which the project's linter
// requires and which is the better shape anyway: the tree is built once, from
// one place, and a command cannot reach into a neighbour's flags.
func newRootCommand(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:   "quirectl",
		Short: "The reference Quire client",
		Long: "quirectl is a Quire device driven from a terminal: it registers, reads, writes,\n" +
			"synchronizes, and remembers between two commands what a device has to remember.\n\n" +
			"Its state lives in one file, so a second --state is a second device.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// A run with no command prints the help rather than an error: somebody
		// who typed the name of the program is asking what it does.
		RunE: func(command *cobra.Command, _ []string) error { return command.Help() },
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			return a.resolve(command)
		},
	}

	flags := root.PersistentFlags()
	flags.String("server", "", "the node to call, as host:port ($"+serverVariable+")")
	flags.String("state", "", "the file this device keeps itself in ($"+stateVariable+")")
	flags.Bool("offline", false, "author changes into the local log instead of calling the node")
	flags.Bool("plaintext", false, "dial without TLS, which is what the local federation answers on")
	flags.String("ca", "", "a PEM file to verify the node against, instead of the system roots")
	flags.Bool("json", false, "print what the node returned, one JSON object per line")

	root.AddCommand(
		newRegisterCommand(a),
		newLoginCommand(a),
		newLogoutCommand(a),
		newWhoamiCommand(a),
		newUserCommand(a),
		newDeviceCommand(a),
		newEbookCommand(a),
		newCollectionCommand(a),
		newAnnotationCommand(a),
		newProgressCommand(a),
		newSyncCommand(a),
		newServerCommand(a),
		newReplicaCommand(a),
		newMigrateCommand(a),
	)

	return root
}

// resolve fills in what the flags did not say.
//
// A flag beats the environment and the environment beats the default, which is
// the order somebody typing a command expects — and the reason it is done here
// rather than in the flag definitions is that a default read at definition time
// would be read before the process could be told otherwise.
func (a *app) resolve(command *cobra.Command) error {
	flags := command.Root().PersistentFlags()

	address, err := flags.GetString("server")
	if err != nil {
		return err
	}

	if address == "" {
		address = os.Getenv(serverVariable)
	}

	statePath, err := flags.GetString("state")
	if err != nil {
		return err
	}

	if statePath == "" {
		statePath = os.Getenv(stateVariable)
	}

	if statePath == "" {
		statePath, err = defaultStatePath()
		if err != nil {
			return err
		}
	}

	offline, err := flags.GetBool("offline")
	if err != nil {
		return err
	}

	plaintext, err := flags.GetBool("plaintext")
	if err != nil {
		return err
	}

	authority, err := flags.GetString("ca")
	if err != nil {
		return err
	}

	a.json, err = flags.GetBool("json")
	if err != nil {
		return err
	}

	a.options = client.Options{
		Address:       address,
		StatePath:     statePath,
		Offline:       offline,
		Plaintext:     plaintext,
		CACertificate: authority,
	}

	return nil
}

// defaultStatePath is where a device keeps itself when nobody said.
func defaultStatePath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no configuration directory to keep the device state in: %w", err)
	}

	return filepath.Join(directory, "quire", stateFileName), nil
}

// with opens the client for one command, runs the body, and closes it.
//
// Every command needs the same three lines around its one call, so they are
// here instead: what is left in a command is its arguments and what it asks
// for.
func (a *app) with(command *cobra.Command, body func(context.Context, *client.Client) error) error {
	connection, err := client.Open(a.options)
	if err != nil {
		return err
	}

	defer func() { _ = connection.Close() }()

	return body(command.Context(), connection)
}

// describe renders an error for a person.
//
// A failure from the node arrives as a gRPC status carrying the two details the
// node attaches to everything it refuses: the machine-readable reason, and the
// fields that were wrong with a sentence about each. Printing only the message
// would throw away the half that says what to change.
func describe(err error) string {
	// A reader who interrupted `quirectl sync watch` is not being told about a
	// failure, and the cancellation reaches here as one.
	if errors.Is(err, context.Canceled) {
		return "stopped"
	}

	converted, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}

	lines := []string{converted.Message()}

	for _, detail := range converted.Details() {
		switch typed := detail.(type) {
		case *errdetails.ErrorInfo:
			lines = append(lines, "  reason: "+typed.GetReason())
		case *errdetails.BadRequest:
			for _, violation := range typed.GetFieldViolations() {
				lines = append(lines, "  "+violation.GetField()+": "+violation.GetDescription())
			}
		}
	}

	return strings.Join(lines, "\n")
}
