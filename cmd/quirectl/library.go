package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"uuid"

	"github.com/spf13/cobra"

	"github.com/anthonyvsmuller/quire/internal/client"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// newEbookCommand groups the works in the reader's collection (UC01, UC02).
func newEbookCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "ebook",
		Short: "Maintain the works in the collection (UC01, UC02)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newEbookCreateCommand(a),
		newEbookGetCommand(a),
		newEbookListCommand(a),
		newEbookUpdateCommand(a),
		newEbookDeleteCommand(a),
		newEbookUploadCommand(a),
		newEbookDownloadCommand(a),
	)

	return command
}

// newEbookCreateCommand registers a work and, when it was given the file,
// imports it.
//
// The two are one command because they are one act to a reader: UC02 is
// importing a book, and the metadata and the bytes travelling separately is how
// the contract carries it rather than something a reader should have to know.
// The file is read to be described whether or not it is uploaded — the digest
// is what names the work across the whole federation, so a work registered
// without one names nothing.
func newEbookCreateCommand(a *app) *cobra.Command {
	var (
		in   client.EbookInput
		file string
		size int64
		meta string
	)

	command := &cobra.Command{
		Use:   "create",
		Short: "Register a work, and import its file when one is given (UC01, UC02)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			in.Size = size

			if file != "" {
				fingerprint, err := client.Digest(file)
				if err != nil {
					return err
				}

				in.ContentHash = fingerprint.ContentHash
				in.Size = fingerprint.Size

				if in.Format == "" {
					in.Format = fingerprint.Format
				}
			}

			if meta != "" {
				if err := json.Unmarshal([]byte(meta), &in.Extra); err != nil {
					return fmt.Errorf("--extra is not a JSON object: %w", err)
				}
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.CreateEbook(ctx, &in)
				if err != nil {
					return err
				}

				a.written("ebook", written)

				// A device that authored the work offline holds the bytes and
				// cannot hand them over: the log carries changes to records and
				// not files, so the import finishes when the device is next
				// connected.
				if file != "" && written.Queued {
					a.print("the file stays here until: quirectl ebook upload %s", file)

					return nil
				}

				if file == "" || !written.ContentMissing {
					return nil
				}

				content, err := connection.UploadContent(ctx, file)
				if err != nil {
					return err
				}

				a.print("stored %d bytes under %s", content.GetSizeBytes(), content.GetContentHash())

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&in.Title, "title", "", "the title of the work")
	flags.StringVar(&in.Author, "author", "", "who wrote it")
	flags.StringVar(&in.Publisher, "publisher", "", "who published it")
	flags.StringVar(&in.Language, "language", "", "what it is written in")
	flags.StringVar(&in.Format, "format", "", "epub, pdf, mobi, djvu or cbz")
	flags.StringVar(&in.ContentHash, "content-hash", "", "the sha-256 of the file, when it is not given")
	flags.Int64Var(&size, "size", 0, "the length of the file, when it is not given")
	flags.StringVar(&file, "file", "", "the file to describe and import")
	flags.StringVar(&meta, "extra", "", "the metadata this contract does not name, as a JSON object")

	return command
}

// newEbookGetCommand reads one work.
func newEbookGetCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "get <ebook-id>",
		Short: "Show one work",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the work")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				work, err := connection.GetEbook(ctx, id)
				if err != nil {
					return err
				}

				if done, err := a.emit(work); done || err != nil {
					return err
				}

				a.print("%s", work.GetId())
				a.print("title      %s", work.GetTitle())
				a.print("author     %s", work.GetAuthor())
				a.print("format     %s", client.FormatName(work.GetFormat()))
				a.print("hash       %s", work.GetContentHash())
				a.print("size       %d", work.GetSizeBytes())
				a.print("revision   %s", revisionOf(work.GetRevision()))

				return nil
			})
		},
	}
}

// newEbookListCommand reads a page of the collection.
func newEbookListCommand(a *app) *cobra.Command {
	var (
		collection string
		pageSize   int32
		pageToken  string
	)

	command := &cobra.Command{
		Use:   "list",
		Short: "List the works, optionally the ones filed under one grouping",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var narrowed *uuid.UUID

			if collection != "" {
				id, err := identifier(collection, "the grouping")
				if err != nil {
					return err
				}

				narrowed = &id
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				works, next, err := connection.ListEbooks(ctx, narrowed, pageSize, pageToken)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, works); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(works))
				for _, work := range works {
					rows = append(rows, []string{
						work.GetId(),
						work.GetTitle(),
						work.GetAuthor(),
						client.FormatName(work.GetFormat()),
					})
				}

				a.table([]string{"ID", "TITLE", "AUTHOR", "FORMAT"}, rows)

				if next != "" {
					a.print("\nnext page: --page-token %s", next)
				}

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&collection, "collection", "", "show only the works filed under this grouping")
	flags.Int32Var(&pageSize, "page-size", 0, "how many to return; zero asks the node to choose")
	flags.StringVar(&pageToken, "page-token", "", "the page to continue from")

	return command
}

// newEbookUpdateCommand writes the metadata of a work. UC01 is «CRD» because
// the file is not editable; its metadata is, which is RF05.
func newEbookUpdateCommand(a *app) *cobra.Command {
	var title, author, publisher, language, meta string

	command := &cobra.Command{
		Use:   "update <ebook-id>",
		Short: "Change the metadata of a work (RF05)",
		Long: "A field this command is not given is left to whichever device wrote it last:\n" +
			"reconciliation is per field, so the change claims what it names and nothing else.",
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the work")
			if err != nil {
				return err
			}

			changes := client.EbookChanges{
				Title:     claimed(command, "title", &title),
				Author:    claimed(command, "author", &author),
				Publisher: claimed(command, "publisher", &publisher),
				Language:  claimed(command, "language", &language),
			}

			if command.Flags().Changed("extra") {
				changes.Extra = map[string]any{}
				if err := json.Unmarshal([]byte(meta), &changes.Extra); err != nil {
					return fmt.Errorf("--extra is not a JSON object: %w", err)
				}
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.UpdateEbook(ctx, id, changes)
				if err != nil {
					return err
				}

				a.written("ebook", written)

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&title, "title", "", "the title of the work")
	flags.StringVar(&author, "author", "", "who wrote it")
	flags.StringVar(&publisher, "publisher", "", "who published it")
	flags.StringVar(&language, "language", "", "what it is written in")
	flags.StringVar(&meta, "extra", "", "the metadata this contract does not name, as a JSON object")

	return command
}

// newEbookDeleteCommand tombstones a work.
func newEbookDeleteCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <ebook-id>",
		Short: "Remove a work, as a tombstone rather than a deletion",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the work")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.DeleteEbook(ctx, id)
				if err != nil {
					return err
				}

				a.written("deleted", written)

				return nil
			})
		},
	}
}

// newEbookUploadCommand imports the bytes of a work on their own.
//
// It takes no work identifier, and C16 in docs/tcc-corrections.md is why: the
// call stores bytes under their digest, and which works point at them is the
// metadata's business.
func newEbookUploadCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "upload <file>",
		Short: "Store the bytes of a work, addressed by their digest (UC02)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				content, err := connection.UploadContent(ctx, args[0])
				if err != nil {
					return err
				}

				if done, err := a.emit(content); done || err != nil {
					return err
				}

				a.print("stored %d bytes under %s", content.GetSizeBytes(), content.GetContentHash())

				return nil
			})
		},
	}
}

// newEbookDownloadCommand reads the bytes back.
func newEbookDownloadCommand(a *app) *cobra.Command {
	var out string

	command := &cobra.Command{
		Use:   "download <ebook-id>",
		Short: "Fetch the file of a work",
		Long: "A node that replicates this reader without their files answers that it does not\n" +
			"hold them, which the authorization makes legitimate rather than an error.",
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the work")
			if err != nil {
				return err
			}

			if out == "" {
				return fmt.Errorf("no destination: pass --output, since a file is not printable")
			}

			file, err := os.Create(out) //nolint:gosec // the caller names their own destination
			if err != nil {
				return fmt.Errorf("the destination could not be created: %w", err)
			}

			defer func() { _ = file.Close() }()

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				content, err := connection.DownloadContent(ctx, id, file)
				if err != nil {
					return err
				}

				a.print("wrote %d bytes of %s to %s",
					content.GetSizeBytes(), content.GetMediaType(), out)

				return nil
			})
		},
	}

	command.Flags().StringVar(&out, "output", "", "the file to write the bytes to")

	return command
}

// newCollectionCommand groups the shelves the reader defined (UC03).
func newCollectionCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:     "collection",
		Aliases: []string{"col"},
		Short:   "Maintain the groupings over the collection (UC03)",
		Args:    cobra.NoArgs,
		RunE:    func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newCollectionCreateCommand(a),
		newCollectionGetCommand(a),
		newCollectionListCommand(a),
		newCollectionUpdateCommand(a),
		newCollectionDeleteCommand(a),
		newCollectionAddCommand(a),
		newCollectionRemoveCommand(a),
	)

	return command
}

// newCollectionCreateCommand creates a grouping.
func newCollectionCreateCommand(a *app) *cobra.Command {
	var in client.CollectionInput

	command := &cobra.Command{
		Use:   "create",
		Short: "Create a grouping",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.CreateCollection(ctx, &in)
				if err != nil {
					return err
				}

				a.written("collection", written)

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&in.Name, "name", "", "what the grouping is called")
	flags.StringVar(&in.Kind, "kind", "collection", "collection or category")
	flags.StringVar(&in.Description, "description", "", "what it is for")

	return command
}

// newCollectionGetCommand reads one grouping.
func newCollectionGetCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "get <collection-id>",
		Short: "Show one grouping",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the grouping")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				grouping, err := connection.GetCollection(ctx, id)
				if err != nil {
					return err
				}

				if done, err := a.emit(grouping); done || err != nil {
					return err
				}

				a.print("%s", grouping.GetId())
				a.print("name        %s", grouping.GetName())
				a.print("kind        %s", client.CollectionKindName(grouping.GetKind()))
				a.print("description %s", grouping.GetDescription())
				a.print("revision    %s", revisionOf(grouping.GetRevision()))

				return nil
			})
		},
	}
}

// newCollectionListCommand reads the groupings, optionally the ones a work is
// filed under.
func newCollectionListCommand(a *app) *cobra.Command {
	var ebook string

	command := &cobra.Command{
		Use:   "list",
		Short: "List the groupings, optionally the ones one work is filed under",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var narrowed *uuid.UUID

			if ebook != "" {
				id, err := identifier(ebook, "the work")
				if err != nil {
					return err
				}

				narrowed = &id
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				groupings, err := connection.ListCollections(ctx, narrowed)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, groupings); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(groupings))
				for _, grouping := range groupings {
					rows = append(rows, []string{
						grouping.GetId(),
						grouping.GetName(),
						client.CollectionKindName(grouping.GetKind()),
					})
				}

				a.table([]string{"ID", "NAME", "KIND"}, rows)

				return nil
			})
		},
	}

	command.Flags().StringVar(&ebook, "ebook", "", "show only the groupings this work is filed under")

	return command
}

// newCollectionUpdateCommand writes the fields of a grouping.
func newCollectionUpdateCommand(a *app) *cobra.Command {
	var name, kind, description string

	command := &cobra.Command{
		Use:   "update <collection-id>",
		Short: "Change a grouping",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the grouping")
			if err != nil {
				return err
			}

			changes := client.CollectionChanges{
				Name:        claimed(command, "name", &name),
				Kind:        claimed(command, "kind", &kind),
				Description: claimed(command, "description", &description),
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.UpdateCollection(ctx, id, changes)
				if err != nil {
					return err
				}

				a.written("collection", written)

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&name, "name", "", "what the grouping is called")
	flags.StringVar(&kind, "kind", "", "collection or category")
	flags.StringVar(&description, "description", "", "what it is for")

	return command
}

// newCollectionDeleteCommand tombstones a grouping. The works survive it.
func newCollectionDeleteCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <collection-id>",
		Short: "Remove a grouping; the works filed under it survive",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the grouping")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				written, err := connection.DeleteCollection(ctx, id)
				if err != nil {
					return err
				}

				a.written("deleted", written)

				return nil
			})
		},
	}
}

// newCollectionAddCommand files a work under a grouping.
func newCollectionAddCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "add <ebook-id> <collection-id>",
		Short: "File a work under a grouping",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			return a.file(command, args, true)
		},
	}
}

// newCollectionRemoveCommand clears that filing.
func newCollectionRemoveCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <ebook-id> <collection-id>",
		Short: "Take a work out of a grouping",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			return a.file(command, args, false)
		},
	}
}

// file sets or clears the register both commands write, which is idempotent
// either way: the pair is unique (C06) and filing is a register that is set,
// not a row that is appended.
func (a *app) file(command *cobra.Command, args []string, filed bool) error {
	work, err := identifier(args[0], "the work")
	if err != nil {
		return err
	}

	grouping, err := identifier(args[1], "the grouping")
	if err != nil {
		return err
	}

	return a.with(command, func(ctx context.Context, connection *client.Client) error {
		var written client.Written

		if filed {
			written, err = connection.AddToCollection(ctx, work, grouping)
		} else {
			written, err = connection.RemoveFromCollection(ctx, work, grouping)
		}

		if err != nil {
			return err
		}

		a.written("filing", written)

		return nil
	})
}

// revisionOf renders the causal state of a record on one line: what wrote it
// last, when, and how many events each device has contributed.
func revisionOf(revision *quirev1.Revision) string {
	if revision == nil {
		return ""
	}

	rendered := "device " + revision.GetDeviceId() +
		" at " + strconv.FormatInt(revision.GetUpdatedAt().GetUnixMicros(), 10)

	for device, counter := range revision.GetVectorClock().GetEntries() {
		rendered += " " + device + ":" + strconv.FormatUint(counter, 10)
	}

	if revision.GetDeleted() {
		rendered += " deleted"
	}

	return rendered
}
