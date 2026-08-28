package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"uuid"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/anthonyvsmuller/quire/internal/client"
)

// passwordVariable is where a password may be given instead of on the command
// line, which is visible to everything else running on the machine.
const passwordVariable = "QUIRECTL_PASSWORD" //nolint:gosec // G101: the name of a variable, not a credential.

// print writes one line of output.
//
// A failed write to the stream the program was given is not something a command
// can do anything about — the reader is looking at a terminal that stopped
// accepting output — so it is not carried back through every command.
func (a *app) print(format string, args ...any) {
	_, _ = fmt.Fprintf(a.out, format+"\n", args...)
}

// emit writes what the node returned as JSON, one object per line, and reports
// whether it did.
//
// A command that sees true has already printed its output and is done. One line
// per message rather than one array is what lets a shell pipe a list into jq
// and read it as it arrives, and it is the same shape for a list of one.
func (a *app) emit(messages ...proto.Message) (bool, error) {
	if !a.json {
		return false, nil
	}

	for _, message := range messages {
		encoded, err := protojson.Marshal(message)
		if err != nil {
			return true, err
		}

		_, _ = fmt.Fprintln(a.out, string(encoded))
	}

	return true, nil
}

// emitAll is [app.emit] over a slice of one kind of message.
func emitAll[T proto.Message](a *app, messages []T) (bool, error) {
	widened := make([]proto.Message, 0, len(messages))
	for _, message := range messages {
		widened = append(widened, message)
	}

	return a.emit(widened...)
}

// table writes rows under a header, aligned.
func (a *app) table(header []string, rows [][]string) {
	writer := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(writer, strings.Join(header, "\t"))

	for _, row := range rows {
		_, _ = fmt.Fprintln(writer, strings.Join(row, "\t"))
	}

	_ = writer.Flush()
}

// written reports what a change did, which is the one line every write command
// prints.
//
// It says which path the change took, because that is the only thing the reader
// cannot see: the record is the same record either way, and a queued change is
// waiting for a push rather than for the node.
//
// A write is the one answer here that is the client's rather than the node's —
// a change that was queued was answered by nobody — so under --json it is
// rendered as an object of this client's own, on the same one-object-per-line
// terms as everything else.
func (a *app) written(what string, change client.Written) {
	if a.json {
		encoded, err := json.Marshal(struct {
			Record string `json:"record"`
			Target string `json:"target"`
			Queued bool   `json:"queued"`
		}{Record: what, Target: change.Target.String(), Queued: change.Queued})
		if err != nil {
			return
		}

		_, _ = fmt.Fprintln(a.out, string(encoded))

		return
	}

	if change.Queued {
		a.print("%s %s queued", what, change.Target)

		return
	}

	a.print("%s %s", what, change.Target)
}

// identifier reads a uuid an argument names.
func identifier(value, what string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%s is not an identifier: %q", what, value)
	}

	return parsed, nil
}

// claimed returns a pointer to value when the flag was given, and nil when it
// was not.
//
// The difference is what a field mask carries: a field nobody named is left to
// whichever device wrote it last, and a field named as empty is one this change
// cleared.
func claimed(command *cobra.Command, name string, value *string) *string {
	if !command.Flags().Changed(name) {
		return nil
	}

	return value
}

// password returns the password the command was given, or the one in the
// environment when the flag was left out.
//
// A command line is visible to everything else running on the machine, so the
// variable is the way to give one that is not. Neither is a prompt that does
// not echo, and this client is a reference and a demonstration rather than the
// application a reader installs — which is a limitation worth stating rather
// than a dependency worth adding.
func password(given, flag string) (string, error) {
	if given != "" {
		return given, nil
	}

	if fromEnvironment := os.Getenv(passwordVariable); fromEnvironment != "" {
		return fromEnvironment, nil
	}

	return "", fmt.Errorf("no password: pass --%s or set %s", flag, passwordVariable)
}
