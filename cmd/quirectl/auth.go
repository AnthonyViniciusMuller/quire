package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/anthonyvsmuller/quire/internal/client"
)

// newRegisterCommand serves UC14: creating the reader on a node is what binds
// them to it.
func newRegisterCommand(a *app) *cobra.Command {
	var in client.Registration

	command := &cobra.Command{
		Use:   "register",
		Short: "Create the reader on this node, which binds them to it (UC14)",
		Long: "The server half of the federated identifier is not a parameter: it is which\n" +
			"node the call was addressed to.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			secret, err := password(in.Password, "password")
			if err != nil {
				return err
			}

			in.Password = secret

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				reader, err := connection.Register(ctx, &in)
				if err != nil {
					return err
				}

				if done, err := a.emit(reader); done || err != nil {
					return err
				}

				a.print("registered %s", reader.GetFederatedId())

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&in.LocalName, "local-name", "", "the first half of the federated identifier")
	flags.StringVar(&in.DisplayName, "display-name", "", "what the reader calls themselves")
	flags.StringVar(&in.Email, "email", "", "the address a password recovery is sent to")
	flags.StringVar(&in.Password, "password", "", "the password ($"+passwordVariable+")")

	return command
}

// newLoginCommand serves the first half of UC07, and is what binds this device.
func newLoginCommand(a *app) *cobra.Command {
	var in client.Credentials

	command := &cobra.Command{
		Use:   "login",
		Short: "Start a session for this device (UC07)",
		Long: "The device is bound the first time and reuses its identifier afterwards, which\n" +
			"is what keeps every change it ever authored counted under one vector clock entry.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			secret, err := password(in.Password, "password")
			if err != nil {
				return err
			}

			in.Password = secret

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				response, err := connection.Login(ctx, &in)
				if err != nil {
					return err
				}

				if done, err := a.emit(response); done || err != nil {
					return err
				}

				a.print("%s", response.GetUser().GetFederatedId())
				a.print("device %s (%s)", response.GetDevice().GetId(), response.GetDevice().GetName())

				return nil
			})
		},
	}

	flags := command.Flags()
	flags.StringVar(&in.LocalName, "local-name", "", "the reader's local name")
	flags.StringVar(&in.Email, "email", "", "the reader's address, instead of the local name")
	flags.StringVar(&in.Password, "password", "", "the password ($"+passwordVariable+")")
	flags.StringVar(&in.DeviceName, "device-name", "quirectl", "what to call this device")
	flags.StringVar(&in.DevicePlatform, "device-platform", "cli", "what this device runs on")

	return command
}

// newLogoutCommand serves the second half of UC07.
func newLogoutCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "End this device's session, and no other device's (RN07)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				if err := connection.Logout(ctx); err != nil {
					return err
				}

				a.print("logged out")

				return nil
			})
		},
	}
}

// newWhoamiCommand reads the reader's own record, which is the only record of a
// reader a node serves to anybody (RN09).
func newWhoamiCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the reader this device is signed in as (UC06)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				reader, err := connection.Whoami(ctx)
				if err != nil {
					return err
				}

				if done, err := a.emit(reader); done || err != nil {
					return err
				}

				a.print("%s", reader.GetFederatedId())
				a.print("display name  %s", reader.GetDisplayName())
				a.print("email         %s", reader.GetEmail())
				a.print("origin        %s", reader.GetOriginServerDomain())

				return nil
			})
		},
	}
}

// newUserCommand groups what UC06 does to a reader's own record.
func newUserCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "user",
		Short: "Maintain the reader's own record (UC06)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(newUserUpdateCommand(a), newUserPasswordCommand(a), newUserDeleteCommand(a))

	return command
}

// newUserUpdateCommand writes the two fields of a reader that are writable.
func newUserUpdateCommand(a *app) *cobra.Command {
	var displayName, email string

	command := &cobra.Command{
		Use:   "update",
		Short: "Change the display name or the address",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				reader, err := connection.UpdateUser(ctx,
					claimed(command, "display-name", &displayName),
					claimed(command, "email", &email))
				if err != nil {
					return err
				}

				if done, err := a.emit(reader); done || err != nil {
					return err
				}

				a.print("updated %s", reader.GetFederatedId())

				return nil
			})
		},
	}

	command.Flags().StringVar(&displayName, "display-name", "", "what the reader calls themselves")
	command.Flags().StringVar(&email, "email", "", "the address a password recovery is sent to")

	return command
}

// newUserPasswordCommand changes the password, which takes the old one as well
// as the new — the half a field mask cannot express.
func newUserPasswordCommand(a *app) *cobra.Command {
	var current, next string

	command := &cobra.Command{
		Use:   "passwd",
		Short: "Change the password, which ends every session",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			given, err := password(current, "current")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				if err := connection.ChangePassword(ctx, given, next); err != nil {
					return err
				}

				a.print("the password was changed, and every session ended with it")

				return nil
			})
		},
	}

	command.Flags().StringVar(&current, "current", "", "the password now ($"+passwordVariable+")")
	command.Flags().StringVar(&next, "new", "", "the password from now on")

	return command
}

// newUserDeleteCommand removes the reader from this node. It is not a
// migration: UC16 is `quirectl migrate`.
func newUserDeleteCommand(a *app) *cobra.Command {
	var secret string

	command := &cobra.Command{
		Use:   "delete",
		Short: "Remove the reader from this node, with everything that cascades",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			given, err := password(secret, "password")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				if err := connection.DeleteUser(ctx, given); err != nil {
					return err
				}

				a.print("the reader was removed from this node")

				return nil
			})
		},
	}

	command.Flags().StringVar(&secret, "password", "", "the password ($"+passwordVariable+")")

	return command
}

// newDeviceCommand groups what a reader does to the devices that write on their
// behalf (RF11, UC10).
func newDeviceCommand(a *app) *cobra.Command {
	command := &cobra.Command{
		Use:   "device",
		Short: "Maintain the devices bound to the reader (RF11)",
		Args:  cobra.NoArgs,
		RunE:  func(command *cobra.Command, _ []string) error { return command.Help() },
	}

	command.AddCommand(
		newDeviceListCommand(a),
		newDeviceRegisterCommand(a),
		newDeviceRenameCommand(a),
		newDeviceRevokeCommand(a),
	)

	return command
}

// newDeviceListCommand lists them, which is what gives a vector clock entry a
// name.
func newDeviceListCommand(a *app) *cobra.Command {
	var includeInactive bool

	command := &cobra.Command{
		Use:   "list",
		Short: "List the reader's devices",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				devices, err := connection.ListDevices(ctx, includeInactive)
				if err != nil {
					return err
				}

				if done, err := emitAll(a, devices); done || err != nil {
					return err
				}

				rows := make([][]string, 0, len(devices))
				for _, device := range devices {
					rows = append(rows, []string{
						device.GetId(),
						device.GetName(),
						device.GetPlatform(),
						activeness(device.GetActive()),
					})
				}

				a.table([]string{"ID", "NAME", "PLATFORM", "STATE"}, rows)

				return nil
			})
		},
	}

	command.Flags().BoolVar(&includeInactive, "include-inactive", false,
		"show the devices that were unbound as well")

	return command
}

// newDeviceRegisterCommand binds a device without logging in with it, which is
// how a reader pairs a second application from an already authenticated one.
func newDeviceRegisterCommand(a *app) *cobra.Command {
	var name, platform string

	command := &cobra.Command{
		Use:   "register",
		Short: "Bind another device from this one (RF11)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				device, err := connection.RegisterDevice(ctx, name, platform)
				if err != nil {
					return err
				}

				if done, err := a.emit(device); done || err != nil {
					return err
				}

				a.print("device %s", device.GetId())

				return nil
			})
		},
	}

	command.Flags().StringVar(&name, "name", "", "what to call the device")
	command.Flags().StringVar(&platform, "platform", "", "what it runs on")

	return command
}

// newDeviceRenameCommand renames one. Nothing else about a bound device is
// editable: its platform is what it is, and its identifier is referenced by
// every clock it appears in.
func newDeviceRenameCommand(a *app) *cobra.Command {
	var name string

	command := &cobra.Command{
		Use:   "rename <device-id>",
		Short: "Rename a device",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the device")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				device, err := connection.RenameDevice(ctx, id, name)
				if err != nil {
					return err
				}

				if done, err := a.emit(device); done || err != nil {
					return err
				}

				a.print("renamed %s to %s", device.GetId(), device.GetName())

				return nil
			})
		},
	}

	command.Flags().StringVar(&name, "name", "", "what to call it from now on")

	return command
}

// newDeviceRevokeCommand unbinds a device. The record survives, because every
// operation it ever authored is still keyed by its identifier.
func newDeviceRevokeCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <device-id>",
		Short: "Unbind a device: it may no longer write, and its sessions end",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			id, err := identifier(args[0], "the device")
			if err != nil {
				return err
			}

			return a.with(command, func(ctx context.Context, connection *client.Client) error {
				if err := connection.RevokeDevice(ctx, id); err != nil {
					return err
				}

				a.print("revoked %s", id)

				return nil
			})
		},
	}
}

// activeness renders the one flag every catalogue in this contract carries.
func activeness(active bool) string {
	if active {
		return "active"
	}

	return "inactive"
}
