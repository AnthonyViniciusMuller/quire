package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anthonyvsmuller/quire/internal/client"
)

// newServerCommand groups the reader's catalogue of nodes (UC12, UC13).
func newServerCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "server",
		Short: "Maintain the nodes the reader knows (UC12, UC13)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newServerDiscoverCommand(a),
		newServerAddCommand(a),
		newServerGetCommand(a),
		newServerListCommand(a),
		newServerActiveCommand(a, true),
		newServerActiveCommand(a, false),
		newServerRefreshCommand(a),
		newServerRemoveCommand(a),
	)

	return command
}

// newServerDiscoverCommand resolves a domain and stores nothing (UC13).
func newServerDiscoverCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "discover <domain>",
		Short: "Ask a domain what it exposes, over /.well-known (UC13, RF14)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				descriptor, err := connection.Discover(ctx, args[0])
				if err != nil {
					return err
				}

				if done, err := a.emit(descriptor); done || err != nil {
					return err
				}

				a.print("domain       %s", descriptor.GetDomain())
				a.print("base url     %s", descriptor.GetBaseUrl())
				a.print("grpc         %s", descriptor.GetGrpc())
				a.print("jwks         %s", descriptor.GetJwksUri())
				a.print("fingerprint  %s", descriptor.GetCertificateFingerprint())

				return nil
			})
		},
	}
}

// newServerAddCommand discovers a domain and records what it found, pinning the
// fingerprint node-to-node mTLS is checked against (UC12, RNF08).
func newServerAddCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "add <domain>",
		Short: "Record a node and pin the certificate it presents (UC12)",
		Long: "Only the domain is given. The rest of the record is what the node says about\n" +
			"itself: a reader who could type the fingerprint by hand could pin the wrong one.",
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				server, err := connection.AddKnownServer(ctx, args[0])
				if err != nil {
					return err
				}

				if done, err := a.emit(server); done || err != nil {
					return err
				}

				a.print("server %s", server.GetId())
				a.print("grpc   %s", server.GetDescriptor_().GetGrpc())
				a.print("pin    %s", server.GetDescriptor_().GetCertificateFingerprint())

				return nil
			})
		},
	}
}

// newServerGetCommand reads one node from the catalogue.
func newServerGetCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "get <server-id>",
		Short: "Show one node from the catalogue",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the node")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				server, err := connection.GetKnownServer(ctx, id)
				if err != nil {
					return err
				}

				if done, err := a.emit(server); done || err != nil {
					return err
				}

				a.print("%s", server.GetId())
				a.print("domain       %s", server.GetDescriptor_().GetDomain())
				a.print("grpc         %s", server.GetDescriptor_().GetGrpc())
				a.print("fingerprint  %s", server.GetDescriptor_().GetCertificateFingerprint())
				a.print("state        %s", activeness(server.GetActive()))
				a.print("local        %t", server.GetIsLocal())

				return nil
			})
		},
	}
}

// newServerListCommand reads the catalogue.
func newServerListCommand(a *app) *cobra.Command {
	var includeInactive bool

	command := &cobra.Command{
		Use:   "list",
		Short: "List the nodes the reader knows",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				servers, err := connection.ListKnownServers(ctx, includeInactive)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, servers); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(servers))
				for _, server := range servers {
					rows = append(rows, []string{
						server.GetId(),
						server.GetDescriptor_().GetDomain(),
						server.GetDescriptor_().GetGrpc(),
						activeness(server.GetActive()),
						local(server.GetIsLocal()),
					})
				}

				a.table([]string{"ID", "DOMAIN", "GRPC", "STATE", "ROLE"}, rows)

				return nil
			})
		},
	}

	command.Flags().BoolVar(&includeInactive, "include-inactive", false,
		"show the nodes the reader deactivated as well")

	return command
}

// newServerActiveCommand decides whether a node takes part in replication.
// Clearing it stops the traffic without losing what discovery already learned.
func newServerActiveCommand(a *app, active bool) *cobra.Command {
	verb := "deactivate"
	if active {
		verb = "activate"
	}

	return &cobra.Command{
		Use:   verb + " <server-id>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a node without forgetting what discovery learned",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the node")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				server, err := connection.SetKnownServerActive(ctx, id, active)
				if err != nil {
					return err
				}

				if done, err := a.emit(server); done || err != nil {
					return err
				}

				a.print("%s is %s", server.GetDescriptor_().GetDomain(), activeness(server.GetActive()))

				return nil
			})
		},
	}
}

// newServerRefreshCommand re-runs discovery against a node already recorded.
//
// It is the only way a pinned fingerprint changes, and a change is reported
// rather than applied silently: a rotation and an interception look identical
// from here, and the reader is the only party who can tell them apart.
func newServerRefreshCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh <server-id>",
		Short: "Discover a recorded node again, and report a changed fingerprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the node")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				server, changed, err := connection.RefreshKnownServer(ctx, id)
				if err != nil {
					return err
				}

				if done, err := a.emit(server); done || err != nil {
					return err
				}

				a.print("%s", server.GetDescriptor_().GetDomain())
				a.print("fingerprint  %s", server.GetDescriptor_().GetCertificateFingerprint())

				if changed {
					a.print("\nthe certificate is not the one that was pinned. " +
						"A rotation and an interception look the same from here.")
				}

				return nil
			})
		},
	}
}

// newServerRemoveCommand forgets a node.
func newServerRemoveCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <server-id>",
		Short: "Forget a node, unless it still holds a copy (RN03)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the node")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				if err := connection.RemoveKnownServer(ctx, id); err != nil {
					return err
				}

				a.print("forgot %s", id)

				return nil
			})
		},
	}
}

// newReplicaCommand groups which nodes may hold a copy of the reader's data
// (UC15, RF16).
func newReplicaCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "replica",
		Short: "Decide which nodes may hold a copy of the reader's data (UC15)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newReplicaAuthorizeCommand(a),
		newReplicaRevokeCommand(a),
		newReplicaListCommand(a),
	)

	return command
}

// newReplicaAuthorizeCommand allows a known node to replicate this reader.
func newReplicaAuthorizeCommand(a *app) *cobra.Command {
	var files bool

	command := &cobra.Command{
		Use:   "authorize <server-id>",
		Short: "Let a node hold a copy, with or without the files (UC15, RF16)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the node")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				authorization, err := connection.AuthorizeReplica(ctx, id, files)
				if err != nil {
					return err
				}

				if done, err := a.emit(authorization); done || err != nil {
					return err
				}

				a.print("%s may replicate this reader%s",
					authorization.GetServerDomain(), withFiles(authorization.GetReplicatesFiles()))

				return nil
			})
		},
	}

	command.Flags().BoolVar(&files, "files", false,
		"replicate the e-book files as well as the metadata")

	return command
}

// newReplicaRevokeCommand withdraws that permission. The authorization is
// deactivated and kept, because the record that it once existed is what
// explains why a peer still holds data.
func newReplicaRevokeCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <server-id>",
		Short: "Stop a node replicating this reader (UC15, RN03)",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the node")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				if err := connection.RevokeReplica(ctx, id); err != nil {
					return err
				}

				a.print("revoked; it holds what it already had, and receives nothing more")

				return nil
			})
		},
	}
}

// newReplicaListCommand reads which nodes hold a copy, and which used to.
func newReplicaListCommand(a *app) *cobra.Command {
	var includeInactive bool

	command := &cobra.Command{
		Use:   "list",
		Short: "List which nodes hold a copy, and which used to",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				authorizations, err := connection.ListReplicaAuthorizations(ctx, includeInactive)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, authorizations); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(authorizations))
				for _, authorization := range authorizations {
					rows = append(rows, []string{
						authorization.GetServerId(),
						authorization.GetServerDomain(),
						files(authorization.GetReplicatesFiles()),
						activeness(authorization.GetActive()),
					})
				}

				a.table([]string{"SERVER", "DOMAIN", "FILES", "STATE"}, rows)

				return nil
			})
		},
	}

	command.Flags().BoolVar(&includeInactive, "include-inactive", false,
		"show the authorizations that were revoked as well")

	return command
}

// newMigrateCommand moves the reader to the node this client is talking to
// (UC16, RF17).
func newMigrateCommand(a *app) *cobra.Command {
	var (
		in      client.Migration
		devices []string
	)

	command := &cobra.Command{
		Use:   "migrate",
		Short: "Move the reader to this node, keeping their devices (UC16, RF17)",
		Long: "It is addressed to the node being moved to, by a device that already holds the\n" +
			"reader's collection — which is what lets it proceed without the previous node's\n" +
			"cooperation or availability.\n\n" +
			"This device is adopted with the identifier it already has, and so is every\n" +
			"device named with --device. Omitting one is not a small mistake: every vector\n" +
			"clock is keyed by a device id, so a device left out arrives later as a log this\n" +
			"node cannot insert.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			secret, err := password(in.Password, "password")
			if err != nil {
				return err
			}

			in.Password = secret

			in.Devices, err = adopted(devices)
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				response, err := connection.Migrate(ctx, &in)
				if err != nil {
					return err
				}

				if done, err := a.emit(response); done || err != nil {
					return err
				}

				a.print("%s", response.GetUser().GetFederatedId())

				rows := make([][]string, 0, len(response.GetDevices()))
				for _, device := range response.GetDevices() {
					rows = append(rows, []string{device.GetId(), device.GetName(), device.GetPlatform()})
				}

				a.table([]string{"DEVICE", "NAME", "PLATFORM"}, rows)
				a.print("\nthe devices now push their local logs: quirectl sync push")

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&in.LocalName, "local-name", "", "the local name to take here, if it is free")
	flags.StringVar(&in.DisplayName, "display-name", "", "what the reader calls themselves")
	flags.StringVar(&in.Email, "email", "", "the address a password recovery is sent to")
	flags.StringVar(&in.Password, "password", "", "the password on this node ($"+passwordVariable+")")
	flags.StringVar(&in.PreviousFederatedID, "previous", "",
		"the identifier being left, recorded as provenance and never as identity")
	flags.StringArrayVar(&devices, "device", nil,
		"another device to adopt, as <id>,<name>,<platform>; repeatable")

	return command
}

// adopted reads the devices named on the command line.
//
// The identifier is what matters and the rest is what a reader sees: a device
// adopted under a new identifier would break every clock it appears in, which
// is why the form insists on the one it already has.
func adopted(named []string) ([]client.Device, error) {
	devices := make([]client.Device, 0, len(named))

	for _, value := range named {
		parts := strings.SplitN(value, ",", 3)

		id, err := identifier(parts[0], "the device")
		if err != nil {
			return nil, err
		}

		device := client.Device{ID: id}

		if len(parts) > 1 {
			device.Name = parts[1]
		}

		if len(parts) > 2 {
			device.Platform = parts[2]
		}

		if device.Name == "" {
			return nil, fmt.Errorf("the device %s was named without a name", id)
		}

		devices = append(devices, device)
	}

	return devices, nil
}

// local renders which row of the catalogue is the node answering the call.
func local(is bool) string {
	if is {
		return "local"
	}

	return "peer"
}

// files renders whether an authorization covers the e-book files.
func files(replicates bool) string {
	if replicates {
		return "files"
	}

	return "metadata"
}

// withFiles is the same fact in the middle of a sentence.
func withFiles(replicates bool) string {
	if replicates {
		return ", files included"
	}

	return ", metadata only"
}
