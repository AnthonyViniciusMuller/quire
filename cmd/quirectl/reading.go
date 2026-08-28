package main

import (
	"context"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/anthonyvsmuller/quire/internal/client"
)

// newAnnotationCommand groups what the reader wrote in a work (UC04).
func newAnnotationCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:     "annotation",
		Aliases: []string{"mark"},
		Short:   "Maintain the marks the reader left in a work (UC04)",
		Args:    cobra.NoArgs,
		RunE:    func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newAnnotationCreateCommand(a),
		newAnnotationGetCommand(a),
		newAnnotationListCommand(a),
		newAnnotationUpdateCommand(a),
		newAnnotationDeleteCommand(a),
	)

	return command
}

// newAnnotationCreateCommand writes a mark.
func newAnnotationCreateCommand(a *app) *cobra.Command {
	var (
		in    client.AnnotationInput
		ebook string
	)

	command := &cobra.Command{
		Use:   "create",
		Short: "Write a note, a highlight or a bookmark",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			work, err := identifier(ebook, "the work")
			if err != nil {
				return err
			}

			in.Ebook = work

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.CreateAnnotation(ctx, &in)
				if err != nil {
					return err
				}

				a.written("annotation", written)

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&ebook, "ebook", "", "the work the mark is in")
	flags.StringVar(&in.Kind, "kind", "note", "note, highlight or bookmark")
	flags.StringVar(&in.Text, "text", "", "what the reader wrote, required for a note")
	flags.StringVar(&in.Locator, "locator", "",
		"the passage, expressed so that it survives the format: a CFI, a page")

	return command
}

// newAnnotationGetCommand reads one mark.
func newAnnotationGetCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "get <annotation-id>",
		Short: "Show one mark",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the mark")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				mark, err := connection.GetAnnotation(ctx, id)
				if err != nil {
					return err
				}

				if done, err := a.emit(mark); done || err != nil {
					return err
				}

				a.print("%s", mark.GetId())
				a.print("ebook     %s", mark.GetEbookId())
				a.print("kind      %s", client.AnnotationKindName(mark.GetKind()))
				a.print("locator   %s", mark.GetLocator())
				a.print("text      %s", mark.GetText())
				a.print("revision  %s", revisionOf(mark.GetRevision()))

				return nil
			})
		},
	}
}

// newAnnotationListCommand reads a page of what the reader wrote in one work.
func newAnnotationListCommand(a *app) *cobra.Command {
	var (
		ebook     string
		pageSize  int32
		pageToken string
	)

	command := &cobra.Command{
		Use:   "list",
		Short: "List the marks in one work",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			work, err := identifier(ebook, "the work")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				marks, next, err := connection.ListAnnotations(ctx, work, pageSize, pageToken)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, marks); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(marks))
				for _, mark := range marks {
					rows = append(rows, []string{
						mark.GetId(),
						client.AnnotationKindName(mark.GetKind()),
						mark.GetLocator(),
						mark.GetText(),
					})
				}

				a.table([]string{"ID", "KIND", "LOCATOR", "TEXT"}, rows)

				if next != "" {
					a.print("\nnext page: --page-token %s", next)
				}

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&ebook, "ebook", "", "the work whose marks to list")
	flags.Int32Var(&pageSize, "page-size", 0, "how many to return; zero asks the node to choose")
	flags.StringVar(&pageToken, "page-token", "", "the page to continue from")

	return command
}

// newAnnotationUpdateCommand writes the fields of a mark.
func newAnnotationUpdateCommand(a *app) *cobra.Command {
	var kind, text, locator string

	command := &cobra.Command{
		Use:   "update <annotation-id>",
		Short: "Change a mark",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the mark")
			if err != nil {
				return err
			}

			changes := client.AnnotationChanges{
				Kind:    claimed(command, "kind", &kind),
				Text:    claimed(command, "text", &text),
				Locator: claimed(command, "locator", &locator),
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.UpdateAnnotation(ctx, id, changes)
				if err != nil {
					return err
				}

				a.written("annotation", written)

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&kind, "kind", "", "note, highlight or bookmark")
	flags.StringVar(&text, "text", "", "what the reader wrote")
	flags.StringVar(&locator, "locator", "", "the passage it is attached to")

	return command
}

// newAnnotationDeleteCommand tombstones a mark.
func newAnnotationDeleteCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <annotation-id>",
		Short: "Remove a mark, as a tombstone rather than a deletion",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the mark")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.DeleteAnnotation(ctx, id)
				if err != nil {
					return err
				}

				a.written("deleted", written)

				return nil
			})
		},
	}
}

// newProgressCommand groups where the reader stopped (UC05).
func newProgressCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "progress",
		Short: "Report and read where a work was left off (UC05)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(newProgressSetCommand(a), newProgressListCommand(a))

	return command
}

// newProgressSetCommand records where this device has reached.
//
// The device is not a parameter, and cannot be: the row has one writer and it
// is the one the row names, which is C05 expressed in the contract rather than
// in a check somebody has to remember.
func newProgressSetCommand(a *app) *cobra.Command {
	var (
		ebook   string
		locator string
		percent float64
	)

	command := &cobra.Command{
		Use:   "set",
		Short: "Record where this device has reached in a work",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			work, err := identifier(ebook, "the work")
			if err != nil {
				return err
			}

			var proportion *float64
			if command.Flags().Changed("percent") {
				proportion = &percent
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.UpdateProgress(ctx, work, locator, proportion)
				if err != nil {
					return err
				}

				a.written("progress", written)

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&ebook, "ebook", "", "the work being read")
	flags.StringVar(&locator, "locator", "", "where the reader stopped")
	flags.Float64Var(&percent, "percent", 0, "how far through, 0 to 100")

	return command
}

// newProgressListCommand reads every device's position in one work (RN01).
//
// The node returns them all and picks none: which one to resume from is the
// client's decision, and this client shows them rather than making it.
func newProgressListCommand(a *app) *cobra.Command {
	var ebook string

	command := &cobra.Command{
		Use:   "list",
		Short: "Show every device's position in one work (RN01)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			work, err := identifier(ebook, "the work")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				positions, err := connection.ListProgress(ctx, work)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, positions); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(positions))
				for _, position := range positions {
					rows = append(rows, []string{
						position.GetDeviceId(),
						position.GetLocator(),
						strconv.FormatFloat(position.GetPercent(), 'f', 2, 64),
						strconv.FormatInt(position.GetUpdatedAt().GetUnixMicros(), 10),
					})
				}

				a.table([]string{"DEVICE", "LOCATOR", "PERCENT", "UPDATED"}, rows)

				return nil
			})
		},
	}

	command.Flags().StringVar(&ebook, "ebook", "", "the work whose positions to show")

	return command
}
