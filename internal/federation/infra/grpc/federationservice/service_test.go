package federationservice_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	addserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/addserver"
	authorizereplicausecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/authorizereplica"
	discoverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/discover"
	getserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/getserver"
	listauthorizationsusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listauthorizations"
	listserversusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/listservers"
	refreshserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/refreshserver"
	removeserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/removeserver"
	revokereplicausecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/revokereplica"
	updateserverusecase "github.com/anthonyvsmuller/quire/internal/federation/application/usecase/updateserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/addknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/authorizereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/discoverserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/getknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/listknownservers"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/listreplicaauthorizations"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/refreshknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/removeknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/revokereplica"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/controller/updateknownserver"
	"github.com/anthonyvsmuller/quire/internal/federation/infra/grpc/federationservice"
	quirev1 "github.com/anthonyvsmuller/quire/internal/gen/quire/v1"
	"github.com/anthonyvsmuller/quire/internal/identity/application/apptest"
	"github.com/anthonyvsmuller/quire/internal/identity/infra/grpc/authn"
)

// errReached is what a recorder reports instead of doing the work: the call
// got this far, and the test is about how far that is.
var errReached = errors.New("the use case was reached")

// recorder stands in for a use case and writes down that it ran.
type recorder[In, Out any] struct {
	name  string
	calls *[]string
}

func (r recorder[In, Out]) Execute(_ context.Context, _ In) (Out, error) {
	var zero Out

	*r.calls = append(*r.calls, r.name)

	return zero, errReached
}

// TestEveryCallReachesItsController is what the embedded Unimplemented struct
// costs, paid back.
//
// buf.gen.yaml keeps that embedding on purpose, so a method left out of this
// service compiles and answers Unimplemented rather than failing to build.
// This calls all ten and refuses that answer — and, because each stand-in has
// a name, it also refuses a forwarding method wired to the wrong controller,
// which is the mistake a file of ten near-identical methods invites.
func TestEveryCallReachesItsController(t *testing.T) {
	t.Parallel()

	var calls []string

	service := federationservice.New(&federationservice.Controllers{
		DiscoverServer: discoverserver.New(
			recorder[discoverusecase.Input, discoverusecase.Output]{name: "DiscoverServer", calls: &calls}),
		AddKnownServer: addknownserver.New(
			recorder[addserverusecase.Input, addserverusecase.Output]{name: "AddKnownServer", calls: &calls}),
		GetKnownServer: getknownserver.New(
			recorder[getserverusecase.Input, getserverusecase.Output]{name: "GetKnownServer", calls: &calls}),
		ListKnownServers: listknownservers.New(
			recorder[listserversusecase.Input, listserversusecase.Output]{
				name: "ListKnownServers", calls: &calls,
			}),
		UpdateKnownServer: updateknownserver.New(
			recorder[updateserverusecase.Input, updateserverusecase.Output]{
				name: "UpdateKnownServer", calls: &calls,
			}),
		RefreshKnownServer: refreshknownserver.New(
			recorder[refreshserverusecase.Input, refreshserverusecase.Output]{
				name: "RefreshKnownServer", calls: &calls,
			}),
		RemoveKnownServer: removeknownserver.New(
			recorder[removeserverusecase.Input, removeserverusecase.Output]{
				name: "RemoveKnownServer", calls: &calls,
			}),
		AuthorizeReplica: authorizereplica.New(
			recorder[authorizereplicausecase.Input, authorizereplicausecase.Output]{
				name: "AuthorizeReplica", calls: &calls,
			}),
		RevokeReplica: revokereplica.New(
			recorder[revokereplicausecase.Input, revokereplicausecase.Output]{
				name: "RevokeReplica", calls: &calls,
			}),
		ListReplicaAuthorizations: listreplicaauthorizations.New(
			recorder[listauthorizationsusecase.Input, listauthorizationsusecase.Output]{
				name: "ListReplicaAuthorizations", calls: &calls,
			}),
	})

	ctx := authenticated(t)
	serverID := uuid.New().String()

	tests := []struct {
		name string
		call func() error
	}{
		{"DiscoverServer", func() error {
			_, err := service.DiscoverServer(ctx, &quirev1.DiscoverServerRequest{Domain: "quire-b.example"})

			return err
		}},
		{"AddKnownServer", func() error {
			_, err := service.AddKnownServer(ctx, &quirev1.AddKnownServerRequest{Domain: "quire-b.example"})

			return err
		}},
		{"GetKnownServer", func() error {
			_, err := service.GetKnownServer(ctx, &quirev1.GetKnownServerRequest{ServerId: serverID})

			return err
		}},
		{"ListKnownServers", func() error {
			_, err := service.ListKnownServers(ctx, &quirev1.ListKnownServersRequest{})

			return err
		}},
		{"UpdateKnownServer", func() error {
			_, err := service.UpdateKnownServer(ctx, &quirev1.UpdateKnownServerRequest{
				ServerId:   serverID,
				Server:     &quirev1.Server{Active: false},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"active"}},
			})

			return err
		}},
		{"RefreshKnownServer", func() error {
			_, err := service.RefreshKnownServer(ctx, &quirev1.RefreshKnownServerRequest{ServerId: serverID})

			return err
		}},
		{"RemoveKnownServer", func() error {
			_, err := service.RemoveKnownServer(ctx, &quirev1.RemoveKnownServerRequest{ServerId: serverID})

			return err
		}},
		{"AuthorizeReplica", func() error {
			_, err := service.AuthorizeReplica(ctx, &quirev1.AuthorizeReplicaRequest{ServerId: serverID})

			return err
		}},
		{"RevokeReplica", func() error {
			_, err := service.RevokeReplica(ctx, &quirev1.RevokeReplicaRequest{ServerId: serverID})

			return err
		}},
		{"ListReplicaAuthorizations", func() error {
			_, err := service.ListReplicaAuthorizations(ctx, &quirev1.ListReplicaAuthorizationsRequest{})

			return err
		}},
	}

	for _, test := range tests {
		calls = calls[:0]

		err := test.call()

		if status.Code(err) == codes.Unimplemented {
			t.Errorf("%s answers Unimplemented, so the service does not serve it", test.name)

			continue
		}

		if !errors.Is(err, errReached) {
			t.Errorf("%s did not reach a use case: %v", test.name, err)

			continue
		}

		if len(calls) != 1 || calls[0] != test.name {
			t.Errorf("%s reached %v, want its own controller", test.name, calls)
		}
	}
}

// TestMigrateHomeServerIsNotServedYet names the one method that is meant to
// answer Unimplemented, so that it stays a decision rather than an omission.
//
// UC16 belongs to phase 9. It creates a reader, adopts their devices with the
// identifiers those devices already hold (C11) and issues a session, none of
// which this slice can do — and until it can, the honest reply is that the
// method is not implemented here.
func TestMigrateHomeServerIsNotServedYet(t *testing.T) {
	t.Parallel()

	service := federationservice.New(&federationservice.Controllers{})

	_, err := service.MigrateHomeServer(t.Context(), &quirev1.MigrateHomeServerRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("MigrateHomeServer = %v, want Unimplemented until phase 9 serves it", err)
	}
}

// authenticated is a context carrying an identity, built by running the real
// interceptor rather than by reaching into it.
//
// The interceptor belongs to the identity slice, which is the only part of the
// node that can verify a token, and every slice's controllers read the
// identity it stamps. This one does the same thing the node does.
func authenticated(t *testing.T) context.Context {
	t.Helper()

	auth := apptest.NewAuthService()
	clock := apptest.NewClock(time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC))

	token, _, err := auth.IssueAccess(uuid.New(), uuid.New(), clock.Now())
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	incoming := metadata.NewIncomingContext(t.Context(), metadata.Pairs("authorization", "Bearer "+token))

	var served context.Context

	_, err = authn.New(auth, clock, nil).Unary()(incoming, nil,
		&grpc.UnaryServerInfo{FullMethod: quirev1.FederationService_ListKnownServers_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			served = ctx

			return nil, nil //nolint:nilnil // the handler only captures the context.
		})
	if err != nil {
		t.Fatalf("building an authenticated context: %v", err)
	}

	return served
}
