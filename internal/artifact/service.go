package artifact

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"connectrpc.com/connect"
	artifactv1 "github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1/artifactv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/internal/artifactblob"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type artifactStore interface {
	Publish(context.Context, store.PublishArtifactParams) (store.PublishArtifactResult, error)
	Get(context.Context, store.GetArtifactParams) (store.ArtifactView, error)
	List(context.Context, store.ListArtifactsParams) (store.ListArtifactsResult, error)
	Grant(context.Context, store.GrantArtifactParams) (store.ArtifactGrant, error)
	RevokeGrant(context.Context, store.RevokeArtifactGrantParams) (store.ArtifactGrant, error)
	Fetch(context.Context, store.FetchArtifactParams) (store.FetchArtifactResult, error)
}

type Service struct {
	store          artifactStore
	authentication authenticationResolver
	now            func() time.Time
}

var _ artifactv1connect.ArtifactServiceHandler = (*Service)(nil)

func New(artifacts artifactStore, authentication authenticator, browserOrigin string) *Service {
	return &Service{
		store: artifacts,
		authentication: authenticationResolver{
			authenticator: authentication,
			origin:        browserOrigin,
		},
		now: time.Now,
	}
}

func (s *Service) PublishArtifact(ctx context.Context, stream *connect.ClientStream[artifactv1.PublishArtifactRequest]) (*connect.Response[artifactv1.PublishArtifactResponse], error) {
	now := s.now()
	authentication, err := s.authentication.resolve(ctx, stream.RequestHeader(), true, now)
	if err != nil {
		return nil, err
	}
	if !stream.Receive() {
		if stream.Err() != nil {
			return nil, internalError()
		}
		return nil, invalidArgument("artifact metadata is required")
	}
	metadata := stream.Msg().GetMetadata()
	if metadata == nil {
		return nil, invalidArgument("artifact metadata must be the first frame")
	}
	content := &publishReader{stream: stream}
	params, err := publishParams(authentication, metadata, content, now)
	if err != nil {
		return nil, err
	}
	result, err := s.store.Publish(ctx, params)
	if content.serviceError != nil {
		return nil, content.serviceError
	}
	if err := serviceError(err); err != nil {
		return nil, err
	}
	artifact, err := artifactMessage(result.Artifact, false)
	if err != nil {
		return nil, err
	}
	version, err := publishedVersionMessage(result.Version)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.PublishArtifactResponse{
		Artifact: artifact, Version: version, CommittedAt: timestamppb.New(result.CommittedAt),
	}), nil
}

func (s *Service) GetArtifact(ctx context.Context, request *connect.Request[artifactv1.GetArtifactRequest]) (*connect.Response[artifactv1.GetArtifactResponse], error) {
	authentication, artifactID, version, now, err := s.readParams(ctx, request.Header(), request.Msg.GetArtifactId(), request.Msg.GetVersion())
	if err != nil {
		return nil, err
	}
	view, err := s.store.Get(ctx, store.GetArtifactParams{
		Authentication: authentication, ArtifactID: artifactID, Version: version, Now: now,
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := artifactViewMessage(view)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.GetArtifactResponse{View: message}), nil
}

func (s *Service) ListArtifacts(ctx context.Context, request *connect.Request[artifactv1.ListArtifactsRequest]) (*connect.Response[artifactv1.ListArtifactsResponse], error) {
	now := s.now()
	authentication, err := s.authentication.resolve(ctx, request.Header(), false, now)
	if err != nil {
		return nil, err
	}
	workID := ""
	if request.Msg.GetOwningWorkId() != "" {
		workID, err = connectapi.CanonicalID(request.Msg.GetOwningWorkId(), "owning work id")
		if err != nil {
			return nil, err
		}
	}
	afterID := ""
	if request.Msg.GetAfterArtifactId() != "" {
		afterID, err = connectapi.CanonicalID(request.Msg.GetAfterArtifactId(), "artifact cursor")
		if err != nil {
			return nil, err
		}
	}
	limit := request.Msg.GetLimit()
	if limit == 0 {
		limit = 50
	}
	if limit > 200 {
		return nil, invalidArgument("artifact list limit must be at most 200")
	}
	result, err := s.store.List(ctx, store.ListArtifactsParams{
		Authentication: authentication, OwningWorkID: workID, AfterArtifactID: afterID, Limit: limit, Now: now,
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	response := &artifactv1.ListArtifactsResponse{
		Views: make([]*artifactv1.ArtifactView, 0, len(result.Views)), NextArtifactId: result.NextArtifactID,
	}
	for _, view := range result.Views {
		message, err := artifactViewMessage(view)
		if err != nil {
			return nil, err
		}
		response.Views = append(response.Views, message)
	}
	return connect.NewResponse(response), nil
}

func (s *Service) GrantArtifact(ctx context.Context, request *connect.Request[artifactv1.GrantArtifactRequest]) (*connect.Response[artifactv1.GrantArtifactResponse], error) {
	now := s.now()
	authentication, err := s.authentication.resolve(ctx, request.Header(), true, now)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	artifactID, err := connectapi.CanonicalID(request.Msg.GetArtifactId(), "artifact id")
	if err != nil {
		return nil, err
	}
	targetKind, targetValue, capability, err := artifactGrantParams(request.Msg.GetTarget(), request.Msg.GetCapability())
	if err != nil {
		return nil, err
	}
	targetID, err := connectapi.CanonicalID(targetValue, "artifact grant target id")
	if err != nil {
		return nil, err
	}
	grant, err := s.store.Grant(ctx, store.GrantArtifactParams{
		RequestID: requestID, Authentication: authentication, ArtifactID: artifactID,
		TargetKind: targetKind, TargetID: targetID, Capability: capability, Now: now,
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := artifactGrantMessage(grant)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.GrantArtifactResponse{Grant: message}), nil
}

func (s *Service) RevokeArtifactGrant(ctx context.Context, request *connect.Request[artifactv1.RevokeArtifactGrantRequest]) (*connect.Response[artifactv1.RevokeArtifactGrantResponse], error) {
	now := s.now()
	authentication, err := s.authentication.resolve(ctx, request.Header(), true, now)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	grantID, err := connectapi.CanonicalID(request.Msg.GetGrantId(), "artifact grant id")
	if err != nil {
		return nil, err
	}
	grant, err := s.store.RevokeGrant(ctx, store.RevokeArtifactGrantParams{
		RequestID: requestID, Authentication: authentication, GrantID: grantID, Now: now,
	})
	if err := serviceError(err); err != nil {
		return nil, err
	}
	message, err := artifactGrantMessage(grant)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.RevokeArtifactGrantResponse{Grant: message}), nil
}

func (s *Service) FetchArtifact(ctx context.Context, request *connect.Request[artifactv1.FetchArtifactRequest], stream *connect.ServerStream[artifactv1.FetchArtifactResponse]) error {
	authentication, artifactID, version, now, err := s.readParams(ctx, request.Header(), request.Msg.GetArtifactId(), request.Msg.GetVersion())
	if err != nil {
		return err
	}
	result, err := s.store.Fetch(ctx, store.FetchArtifactParams{
		Authentication: authentication, ArtifactID: artifactID, Version: version, Now: now,
	})
	if err := serviceError(err); err != nil {
		return err
	}
	defer result.Content.Close()
	artifact, err := artifactMessage(result.Artifact, result.Artifact.OwningWorkID == "")
	if err != nil {
		return err
	}
	versionMessage, err := versionViewMessage(result.Version)
	if err != nil {
		return err
	}
	if err := stream.Send(&artifactv1.FetchArtifactResponse{Payload: &artifactv1.FetchArtifactResponse_Metadata{
		Metadata: &artifactv1.FetchArtifactMetadata{View: &artifactv1.ArtifactView{Artifact: artifact, Version: versionMessage}},
	}}); err != nil {
		return err
	}
	buffer := make([]byte, artifactblob.MaxChunkSize)
	for {
		read, readErr := result.Content.Read(buffer)
		if read > 0 {
			chunk := append([]byte(nil), buffer[:read]...)
			if err := stream.Send(&artifactv1.FetchArtifactResponse{Payload: &artifactv1.FetchArtifactResponse_Chunk{Chunk: chunk}}); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return internalError()
		}
		if read == 0 {
			return internalError()
		}
	}
}

func (s *Service) readParams(ctx context.Context, header http.Header, artifactIDValue string, versionValue uint64) (store.ArtifactAuthentication, string, uint64, time.Time, error) {
	now := s.now()
	authentication, err := s.authentication.resolve(ctx, header, false, now)
	if err != nil {
		return store.ArtifactAuthentication{}, "", 0, time.Time{}, err
	}
	artifactID, err := connectapi.CanonicalID(artifactIDValue, "artifact id")
	if err != nil {
		return store.ArtifactAuthentication{}, "", 0, time.Time{}, err
	}
	version, err := artifactVersionParam(versionValue)
	if err != nil {
		return store.ArtifactAuthentication{}, "", 0, time.Time{}, err
	}
	return authentication, artifactID, version, now, nil
}

type publishReader struct {
	stream       *connect.ClientStream[artifactv1.PublishArtifactRequest]
	pending      []byte
	total        int64
	serviceError error
}

func (r *publishReader) Read(buffer []byte) (int, error) {
	if len(r.pending) > 0 {
		read := copy(buffer, r.pending)
		r.pending = r.pending[read:]
		return read, nil
	}
	if !r.stream.Receive() {
		if r.stream.Err() != nil {
			r.serviceError = internalError()
			return 0, errors.New("receive artifact content")
		}
		return 0, io.EOF
	}
	payload, ok := r.stream.Msg().GetPayload().(*artifactv1.PublishArtifactRequest_Chunk)
	if !ok || len(payload.Chunk) == 0 {
		r.serviceError = invalidArgument("artifact content frames must be non-empty chunks")
		return 0, errors.New("invalid artifact content frame")
	}
	if len(payload.Chunk) > artifactblob.MaxChunkSize {
		r.serviceError = invalidArgument("artifact content chunk exceeds 64 KiB")
		return 0, errors.New("artifact content chunk exceeds limit")
	}
	if r.total+int64(len(payload.Chunk)) > artifactblob.MaxBlobSize {
		r.serviceError = connect.NewError(connect.CodeResourceExhausted, errors.New("artifact content exceeds 64 MiB"))
		return 0, errors.New("artifact content exceeds limit")
	}
	r.total += int64(len(payload.Chunk))
	r.pending = payload.Chunk
	return r.Read(buffer)
}

func publishParams(authentication store.ArtifactAuthentication, metadata *artifactv1.PublishArtifactMetadata, content io.Reader, now time.Time) (store.PublishArtifactParams, error) {
	requestID, err := connectapi.CanonicalID(metadata.GetRequestId(), "request id")
	if err != nil {
		return store.PublishArtifactParams{}, err
	}
	artifactID := ""
	if metadata.GetArtifactId() != "" {
		artifactID, err = connectapi.CanonicalID(metadata.GetArtifactId(), "artifact id")
		if err != nil {
			return store.PublishArtifactParams{}, err
		}
	}
	workID, err := connectapi.CanonicalID(metadata.GetOwningWorkId(), "owning work id")
	if err != nil {
		return store.PublishArtifactParams{}, err
	}
	if metadata.GetDeclaredSize() < 0 || metadata.GetDeclaredSize() > artifactblob.MaxBlobSize {
		return store.PublishArtifactParams{}, invalidArgument("declared artifact size is invalid")
	}
	if len(metadata.GetDeclaredDigest()) != sha256.Size {
		return store.PublishArtifactParams{}, invalidArgument("declared artifact digest must be 32 bytes")
	}
	var expectedDigest [sha256.Size]byte
	copy(expectedDigest[:], metadata.GetDeclaredDigest())
	expectedSize := metadata.GetDeclaredSize()
	params := store.PublishArtifactParams{
		RequestID: requestID, Authentication: authentication, ArtifactID: artifactID,
		OwningWorkID: workID, Name: metadata.GetName(), MediaType: metadata.GetMediaType(),
		Summary: metadata.GetSummary(), ExpectedDigest: &expectedDigest, ExpectedSize: &expectedSize,
		Content: content, Now: now,
	}
	if metadata.GetExecution() != nil {
		execution := metadata.GetExecution()
		deliveryID, err := connectapi.CanonicalID(execution.GetDeliveryId(), "delivery id")
		if err != nil {
			return store.PublishArtifactParams{}, err
		}
		runID, err := connectapi.CanonicalID(execution.GetRunId(), "run id")
		if err != nil {
			return store.PublishArtifactParams{}, err
		}
		launchID, err := connectapi.CanonicalID(execution.GetLaunchId(), "launch id")
		if err != nil {
			return store.PublishArtifactParams{}, err
		}
		if execution.GetFence() == 0 || execution.GetFence() > math.MaxInt64 {
			return store.PublishArtifactParams{}, invalidArgument("artifact execution fence is invalid")
		}
		params.Execution = &store.ArtifactExecutionInput{
			DeliveryID: deliveryID, RunID: runID, LaunchID: launchID, Fence: execution.GetFence(),
		}
	}
	params.Sources = make([]store.ArtifactSourceInput, 0, len(metadata.GetSources()))
	for _, source := range metadata.GetSources() {
		if source == nil {
			return store.PublishArtifactParams{}, invalidArgument("artifact source is invalid")
		}
		switch value := source.GetSource().(type) {
		case *artifactv1.ArtifactSourceInput_MessageId:
			messageID, err := connectapi.CanonicalID(value.MessageId, "artifact source message id")
			if err != nil {
				return store.PublishArtifactParams{}, err
			}
			params.Sources = append(params.Sources, store.ArtifactSourceInput{Kind: store.ArtifactSourceMessage, MessageID: messageID})
		case *artifactv1.ArtifactSourceInput_ArtifactVersion:
			if value.ArtifactVersion == nil {
				return store.PublishArtifactParams{}, invalidArgument("artifact version source is required")
			}
			artifactID, err := connectapi.CanonicalID(value.ArtifactVersion.GetArtifactId(), "source artifact id")
			if err != nil {
				return store.PublishArtifactParams{}, err
			}
			version, err := requiredArtifactVersionParam(value.ArtifactVersion.GetVersion())
			if err != nil {
				return store.PublishArtifactParams{}, err
			}
			params.Sources = append(params.Sources, store.ArtifactSourceInput{
				Kind: store.ArtifactSourceVersion, ArtifactID: artifactID, ArtifactVersion: version,
			})
		default:
			return store.PublishArtifactParams{}, invalidArgument("artifact source is invalid")
		}
	}
	return params, nil
}

func artifactGrantParams(target *artifactv1.ArtifactGrantTarget, capability artifactv1.ArtifactCapability) (string, string, string, error) {
	targetKind := ""
	targetID := ""
	if target == nil {
		return "", "", "", invalidArgument("artifact grant target is required")
	}
	switch value := target.GetTarget().(type) {
	case *artifactv1.ArtifactGrantTarget_AgentId:
		targetKind = store.ArtifactGrantTargetAgent
		targetID = value.AgentId
	case *artifactv1.ArtifactGrantTarget_SpaceId:
		targetKind = store.ArtifactGrantTargetSpace
		targetID = value.SpaceId
	case *artifactv1.ArtifactGrantTarget_WorkId:
		targetKind = store.ArtifactGrantTargetWork
		targetID = value.WorkId
	default:
		return "", "", "", invalidArgument("artifact grant target is invalid")
	}
	capabilityName := ""
	switch capability {
	case artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ:
		capabilityName = store.ArtifactGrantRead
	case artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_MANAGE:
		capabilityName = store.ArtifactGrantManage
	default:
		return "", "", "", invalidArgument("artifact capability is invalid")
	}
	return targetKind, targetID, capabilityName, nil
}

func artifactVersionParam(value uint64) (uint64, error) {
	if value > math.MaxInt64 {
		return 0, invalidArgument("artifact version is too large")
	}
	return value, nil
}

func requiredArtifactVersionParam(value uint64) (uint64, error) {
	if value == 0 {
		return 0, invalidArgument("artifact version is required")
	}
	return artifactVersionParam(value)
}

func artifactViewMessage(value store.ArtifactView) (*artifactv1.ArtifactView, error) {
	artifact, err := artifactMessage(value.Artifact, value.OwningWorkRestricted)
	if err != nil {
		return nil, err
	}
	version, err := versionViewMessage(value.Version)
	if err != nil {
		return nil, err
	}
	return &artifactv1.ArtifactView{Artifact: artifact, Version: version}, nil
}

func artifactMessage(value store.Artifact, owningWorkRestricted bool) (*artifactv1.Artifact, error) {
	creator, err := principalMessage(value.Creator)
	if err != nil {
		return nil, err
	}
	return &artifactv1.Artifact{
		Id: value.ID, OrganizationId: value.OrganizationID, OwningWorkId: value.OwningWorkID,
		OwningWorkRestricted: owningWorkRestricted, Name: value.Name, MediaType: value.MediaType,
		Creator: creator, CreatedAt: timestamppb.New(value.CreatedAt),
	}, nil
}

func versionViewMessage(value store.ArtifactVersionView) (*artifactv1.ArtifactVersion, error) {
	author, err := principalMessage(value.Author)
	if err != nil {
		return nil, err
	}
	message := &artifactv1.ArtifactVersion{
		ArtifactId: value.ArtifactID, OrganizationId: value.OrganizationID, Version: value.Version,
		Digest: append([]byte(nil), value.Digest[:]...), Size: value.Size, Summary: value.Summary,
		Author: author, CreatedAt: timestamppb.New(value.CreatedAt),
	}
	message.IntegrityState, err = integrityStateMessage(value.IntegrityState)
	if err != nil {
		return nil, err
	}
	if value.Execution != nil {
		message.Execution = &artifactv1.ArtifactExecution{
			Restricted: value.Execution.Restricted, DeliveryId: value.Execution.DeliveryID,
			RunId: value.Execution.RunID, LaunchId: value.Execution.LaunchID, AgentId: value.Execution.AgentID,
			ComputerId: value.Execution.ComputerID, PlacementGeneration: value.Execution.PlacementGeneration,
			Fence: value.Execution.Fence,
		}
	}
	message.Sources = make([]*artifactv1.ArtifactSource, 0, len(value.Sources))
	for _, source := range value.Sources {
		message.Sources = append(message.Sources, sourceViewMessage(source))
	}
	return message, nil
}

func publishedVersionMessage(value store.ArtifactVersion) (*artifactv1.ArtifactVersion, error) {
	author, err := principalMessage(value.Author)
	if err != nil {
		return nil, err
	}
	message := &artifactv1.ArtifactVersion{
		ArtifactId: value.ArtifactID, OrganizationId: value.OrganizationID, Version: value.Version,
		Digest: append([]byte(nil), value.Digest[:]...), Size: value.Size, Summary: value.Summary,
		Author: author, CreatedAt: timestamppb.New(value.CreatedAt),
	}
	message.IntegrityState, err = integrityStateMessage(value.IntegrityState)
	if err != nil {
		return nil, err
	}
	if value.Execution != nil {
		message.Execution = &artifactv1.ArtifactExecution{
			DeliveryId: value.Execution.DeliveryID, RunId: value.Execution.RunID, LaunchId: value.Execution.LaunchID,
			AgentId: value.Execution.AgentID, ComputerId: value.Execution.ComputerID,
			PlacementGeneration: value.Execution.PlacementGeneration, Fence: value.Execution.Fence,
		}
	}
	message.Sources = make([]*artifactv1.ArtifactSource, 0, len(value.Sources))
	for _, source := range value.Sources {
		message.Sources = append(message.Sources, sourceMessage(source))
	}
	return message, nil
}

func sourceViewMessage(value store.ArtifactSourceView) *artifactv1.ArtifactSource {
	message := &artifactv1.ArtifactSource{Restricted: value.Restricted}
	if value.Restricted {
		return message
	}
	if value.Kind == store.ArtifactSourceMessage {
		message.Source = &artifactv1.ArtifactSource_MessageId{MessageId: value.MessageID}
	} else if value.Kind == store.ArtifactSourceVersion {
		message.Source = &artifactv1.ArtifactSource_ArtifactVersion{ArtifactVersion: &artifactv1.ArtifactVersionRef{
			ArtifactId: value.ArtifactID, Version: value.ArtifactVersion,
		}}
	}
	return message
}

func sourceMessage(value store.ArtifactSource) *artifactv1.ArtifactSource {
	message := &artifactv1.ArtifactSource{}
	if value.Kind == store.ArtifactSourceMessage {
		message.Source = &artifactv1.ArtifactSource_MessageId{MessageId: value.MessageID}
	} else if value.Kind == store.ArtifactSourceVersion {
		message.Source = &artifactv1.ArtifactSource_ArtifactVersion{ArtifactVersion: &artifactv1.ArtifactVersionRef{
			ArtifactId: value.ArtifactID, Version: value.ArtifactVersion,
		}}
	}
	return message
}

func artifactGrantMessage(value store.ArtifactGrant) (*artifactv1.ArtifactGrant, error) {
	grantedBy, err := principalMessage(value.GrantedBy)
	if err != nil {
		return nil, err
	}
	message := &artifactv1.ArtifactGrant{
		Id: value.ID, ArtifactId: value.ArtifactID, OrganizationId: value.OrganizationID,
		Target: &artifactv1.ArtifactGrantTarget{}, GrantedBy: grantedBy, GrantedAt: timestamppb.New(value.GrantedAt),
	}
	switch value.TargetKind {
	case store.ArtifactGrantTargetAgent:
		message.Target.Target = &artifactv1.ArtifactGrantTarget_AgentId{AgentId: value.TargetID}
	case store.ArtifactGrantTargetSpace:
		message.Target.Target = &artifactv1.ArtifactGrantTarget_SpaceId{SpaceId: value.TargetID}
	case store.ArtifactGrantTargetWork:
		message.Target.Target = &artifactv1.ArtifactGrantTarget_WorkId{WorkId: value.TargetID}
	default:
		return nil, internalError()
	}
	switch value.Capability {
	case store.ArtifactGrantRead:
		message.Capability = artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ
	case store.ArtifactGrantManage:
		message.Capability = artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_MANAGE
	default:
		return nil, internalError()
	}
	if value.RevokedAt != nil {
		if value.RevokedBy == nil {
			return nil, internalError()
		}
		message.RevokedBy, err = principalMessage(*value.RevokedBy)
		if err != nil {
			return nil, err
		}
		message.RevokedAt = timestamppb.New(*value.RevokedAt)
	} else if value.RevokedBy != nil {
		return nil, internalError()
	}
	return message, nil
}

func principalMessage(value store.Principal) (*grantv1.Principal, error) {
	kind := grantv1.PrincipalKind_PRINCIPAL_KIND_UNSPECIFIED
	switch value.Kind {
	case "human":
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN
	case "agent":
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT
	case "system":
		kind = grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM
	default:
		return nil, internalError()
	}
	return &grantv1.Principal{Kind: kind, Id: value.ID}, nil
}

func integrityStateMessage(value string) (artifactv1.ArtifactIntegrityState, error) {
	switch value {
	case "ready":
		return artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_READY, nil
	case "missing":
		return artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_MISSING, nil
	case "corrupt":
		return artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_CORRUPT, nil
	default:
		return artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_UNSPECIFIED, internalError()
	}
}

func invalidArgument(message string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(message))
}

func serviceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New("artifact request canceled"))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("artifact request deadline exceeded"))
	case errors.Is(err, store.ErrAgentRuntimeUnauthenticated):
		return unauthenticated()
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("artifact action denied"))
	case errors.Is(err, store.ErrArtifactNotFound), errors.Is(err, store.ErrArtifactVersionNotFound), errors.Is(err, store.ErrArtifactGrantNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("artifact fact not found"))
	case errors.Is(err, store.ErrArtifactRequestConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("artifact request conflicts with committed request"))
	case errors.Is(err, store.ErrArtifactCursorUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("artifact cursor is unavailable"))
	case errors.Is(err, store.ErrArtifactInvalid):
		return invalidArgument("artifact input is invalid")
	case errors.Is(err, store.ErrArtifactIntegrity):
		return connect.NewError(connect.CodeDataLoss, errors.New("artifact content integrity failure"))
	case errors.Is(err, store.ErrArtifactBlobUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("artifact content unavailable"))
	default:
		return internalError()
	}
}
