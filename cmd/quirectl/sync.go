package main

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anthonyvsmuller/quire/internal/client"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
)

// newSyncCommand groups the replication mechanism (UC09, UC10, UC11).
func newSyncCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "sync",
		Short: "Exchange changes with the node (UC09, UC10, UC11)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newSyncPushCommand(a),
		newSyncPullCommand(a),
		newSyncWatchCommand(a),
		newSyncStatusCommand(a),
	)

	return command
}

// newSyncPushCommand hands over what this device authored while it could not
// reach the node.
func newSyncPushCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Hand the node the changes authored offline (UC09, UC11)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				report, err := connection.Push(ctx)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, report.Results); done || err != nil {
					return err
				}

				if len(report.Results) == 0 {
					a.print("nothing to push")

					return nil
				}

				rows := make([][]string, 0, len(report.Results))
				for _, result := range report.Results {
					rows = append(rows, []string{
						result.GetOperationId(),
						outcomeOf(result.GetOutcome()),
						result.GetDetail(),
					})
				}

				a.table([]string{"OPERATION", "OUTCOME", "DETAIL"}, rows)
				a.print("\nthe node's head is now %d", report.LastPosition)

				return nil
			})
		},
	}
}

// newSyncPullCommand collects everything the node holds after this device's
// cursor.
func newSyncPullCommand(a *app) *cobra.Command {
	var (
		limit int32
		all   bool
	)

	command := &cobra.Command{
		Use:   "pull",
		Short: "Collect the changes this device has not seen (UC09, RN06)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				for {
					report, err := connection.Pull(ctx, limit)
					if err != nil {
						return err
					}

					if done, err := emitAll(a, report.Operations); err != nil {
						return err
					} else if !done {
						a.operations(report.Operations)
					}

					if !all || !report.HasMore {
						a.print("\nthe cursor is now %d", connection.Cursor())

						return nil
					}
				}
			})
		},
	}

	flags := command.Flags()
	flags.Int32Var(&limit, "limit", 0, "how many to take; zero asks the node to choose")
	flags.BoolVar(&all, "all", false, "keep pulling until the node has nothing left")

	return command
}

// newSyncWatchCommand keeps the stream open, which is what makes a change on
// one device reach another as it happens rather than at the next poll.
func newSyncWatchCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Stay connected: drain the backlog, then follow what happens (UC10, UC11)",
		Long: "It drains what this device missed, pushes what it authored offline, and stays\n" +
			"open until it is interrupted. Every page is acknowledged after it has been\n" +
			"stored, which is what makes the node send the next one.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				return connection.Watch(ctx, client.Watcher{
					Operations: func(operations []*quirev1.Operation) {
						if done, err := emitAll(a, operations); done || err != nil {
							return
						}

						a.operations(operations)
					},
					PushResults: func(results []*quirev1.OperationResult) {
						for _, result := range results {
							a.print("pushed %s: %s %s",
								result.GetOperationId(), outcomeOf(result.GetOutcome()), result.GetDetail())
						}
					},
				})
			})
		},
	}
}

// newSyncStatusCommand shows what this device is holding, which is the one
// command that answers without calling the node.
func newSyncStatusCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this device's identity, cursor and unpushed changes",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(_ context.Context, connection *client.Client) error {
				state := connection.State()

				a.print("reader    %s", state.User.FederatedID)
				a.print("device    %s (%s)", state.Device.ID, state.Device.Name)
				a.print("node      %s", connection.Address())
				a.print("cursor    %d", connection.Cursor())
				a.print("observed  %s", state.ObservedAt.Format("2006-01-02T15:04:05.000000Z07:00"))
				a.print("pending   %d", len(state.Pending))

				if len(state.Pending) == 0 {
					return nil
				}

				rows := make([][]string, 0, len(state.Pending))
				for _, queued := range state.Pending {
					rows = append(rows, []string{
						queued.Entity,
						queued.Kind,
						queued.TargetID.String(),
						strings.Join(fieldsOf(&queued), ","),
					})
				}

				a.print("")
				a.table([]string{"ENTITY", "CHANGE", "TARGET", "FIELDS"}, rows)

				return nil
			})
		},
	}
}

// operations prints a page of the log.
func (a *app) operations(operations []*quirev1.Operation) {
	if len(operations) == 0 {
		a.print("nothing new")

		return
	}

	rows := make([][]string, 0, len(operations))
	for _, op := range operations {
		rows = append(rows, []string{
			strconv.FormatInt(op.GetPosition(), 10),
			entityOf(op.GetTargetEntity()),
			kindOf(op.GetOperation()),
			op.GetTargetId(),
			op.GetDeviceId(),
		})
	}

	a.table([]string{"POSITION", "ENTITY", "CHANGE", "TARGET", "DEVICE"}, rows)
}

// fieldsOf names what a queued change claims, sorted, so that the same change
// reads the same way twice.
func fieldsOf(queued *client.Operation) []string {
	fields := make([]string, 0, len(queued.Delta))
	for field := range queued.Delta {
		fields = append(fields, field)
	}

	slices.Sort(fields)

	return fields
}

// The three enumerations of the sync contract, rendered as a person reads them.
// They are lowercased names of the enumerator rather than a list written here,
// so a value added to the contract prints as itself rather than as a number.
func entityOf(entity quirev1.TargetEntity) string {
	return strings.ToLower(strings.TrimPrefix(entity.String(), "TARGET_ENTITY_"))
}

func kindOf(kind quirev1.OperationKind) string {
	return strings.ToLower(strings.TrimPrefix(kind.String(), "OPERATION_KIND_"))
}

func outcomeOf(outcome quirev1.OperationOutcome) string {
	return strings.ToLower(strings.TrimPrefix(outcome.String(), "OPERATION_OUTCOME_"))
}
