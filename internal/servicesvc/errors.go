// Package servicesvc provides shared helpers for connect service implementations.
package servicesvc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	artifactapp "github.com/abcdlsj/sumi/internal/artifact/application"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	grantapp "github.com/abcdlsj/sumi/internal/grant/application"
	organizationapp "github.com/abcdlsj/sumi/internal/organization/application"
	placementapp "github.com/abcdlsj/sumi/internal/placement/application"
	workapp "github.com/abcdlsj/sumi/internal/work/application"
)

// ErrInvalidArgument and ErrInternal are sentinel helpers for connect errors.
var (
	ErrInvalArg = func(msg string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(msg)) }
	ErrInternal = connect.NewError(connect.CodeInternal, errors.New("service unavailable"))
	ErrUnauth   = connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
)

// InvalArg is a convenience for invalidArgument.
func InvalArg(msg string) error { return ErrInvalArg(msg) }

// ServiceErr maps domain errors to connect errors.
func ServiceErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("request deadline exceeded"))
	case errors.Is(err, authorityapp.ErrRuntimeUnauthenticated):
		return ErrUnauth
	case errors.Is(err, authoritydomain.ErrPermissionDenied),
		errors.Is(err, executionapp.ErrInboxAccessLost):
		return connect.NewError(connect.CodePermissionDenied, errors.New("action denied"))
	case errors.Is(err, agentapp.ErrNotFound),
		errors.Is(err, agentapp.ErrRuntimeSpecMissing),
		errors.Is(err, artifactapp.ErrNotFound),
		errors.Is(err, artifactapp.ErrVersionNotFound),
		errors.Is(err, artifactapp.ErrGrantNotFound),
		errors.Is(err, executionapp.ErrInboxItemNotFound),
		errors.Is(err, executionapp.ErrHeldDraftNotFound),
		errors.Is(err, grantapp.ErrNotFound),
		errors.Is(err, organizationapp.ErrHumanNotFound),
		errors.Is(err, placementapp.ErrNotFound),
		errors.Is(err, placementapp.ErrAgentNotFound),
		errors.Is(err, workapp.ErrNotFound),
		errors.Is(err, workapp.ErrApprovalNotFound),
		isErrKind(err, executionapp.ErrRunNotFound),
		isErrKind(err, collaborationapp.ErrSpaceNotFound),
		isErrKind(err, collaborationapp.ErrMessageNotFound),
		isErrKind(err, collaborationapp.ErrThreadNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	case errors.Is(err, agentapp.ErrRequestConflict),
		errors.Is(err, artifactapp.ErrRequestConflict),
		errors.Is(err, executionapp.ErrInboxRequestConflict),
		errors.Is(err, grantapp.ErrIssueConflict),
		errors.Is(err, grantapp.ErrRevokeConflict),
		errors.Is(err, organizationapp.ErrHumanRequestConflict),
		errors.Is(err, organizationapp.ErrHumanStatusConflict),
		errors.Is(err, placementapp.ErrRequestConflict),
		errors.Is(err, workapp.ErrRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("request conflicts with committed request"))
	case errors.Is(err, artifactapp.ErrCursorUnavailable),
		errors.Is(err, workapp.ErrCursorUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("cursor unavailable"))
	case errors.Is(err, artifactapp.ErrInvalid),
		errors.Is(err, collaborationapp.ErrInvalidMessageLimit),
		errors.Is(err, collaborationapp.ErrInvalidMessageTarget),
		errors.Is(err, collaborationapp.ErrInvalidMessageBody),
		errors.Is(err, collaborationapp.ErrInvalidMention),
		errors.Is(err, executionapp.ErrInvalidInboxLimit),
		errors.Is(err, executionapp.ErrInvalidDraftResolution),
		errors.Is(err, grantapp.ErrInvalid),
		errors.Is(err, grantapp.ErrParentInvalid),
		errors.Is(err, grantapp.ErrExpansion),
		errors.Is(err, grantapp.ErrLastOwner),
		errors.Is(err, placementapp.ErrStale),
		errors.Is(err, placementapp.ErrConflict),
		errors.Is(err, placementapp.ErrCredentialBindingInvalid),
		errors.Is(err, workapp.ErrInvalid):
		return ErrInvalArg("input is invalid")
	case errors.Is(err, artifactapp.ErrIntegrity):
		return connect.NewError(connect.CodeDataLoss, errors.New("content integrity failure"))
	case errors.Is(err, artifactapp.ErrBlobUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("content unavailable"))
	case isErrKind(err, workapp.ErrTransitionInvalid),
		isErrKind(err, workapp.ErrTerminal),
		isErrKind(err, workapp.ErrAcceptanceIncomplete),
		isErrKind(err, workapp.ErrApprovalConflict),
		isErrKind(err, workapp.ErrAssignmentConflict),
		isErrKind(err, workapp.ErrPlacementInvalid):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("work state conflict"))
	case isErrKind(err, executionapp.ErrRunNotFound),
		isErrKind(err, executionapp.ErrRunNotRunning),
		isErrKind(err, executionapp.ErrRunLeaseStale),
		isErrKind(err, executionapp.ErrRunLeaseExpired),
		errors.Is(err, executionapp.ErrInboxItemNotUnread),
		errors.Is(err, executionapp.ErrInboxItemNotClaimed),
		errors.Is(err, executionapp.ErrInboxItemHasHeldDraft),
		errors.Is(err, executionapp.ErrInboxBasisMismatch),
		errors.Is(err, executionapp.ErrHeldDraftNotHeld),
		errors.Is(err, collaborationapp.ErrSpaceArchived):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("operation precondition failed"))
	default:
		return ErrInternal
	}
}

// isErrKind checks if err's kind matches target via simple equality (avoids interface overhead).
func isErrKind(err, target error) bool {
	return errors.Is(err, target)
}
