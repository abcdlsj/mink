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
	artifactapp "github.com/abcdlsj/sumi/internal/artifact/application"
	artifactblob "github.com/abcdlsj/sumi/internal/artifact/blob"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type artifactStore interface {
	Publish(context.Context, artifactapp.PublishCommand) (artifactapp.PublishResult, error)
	Get(context.Context, artifactapp.GetQuery) (artifactapp.View, error)
	List(context.Context, artifactapp.ListQuery) (artifactapp.ListResult, error)
	Grant(context.Context, artifactapp.GrantCommand) (artifactapp.Grant, error)
	RevokeGrant(context.Context, artifactapp.RevokeGrantCommand) (artifactapp.Grant, error)
	Fetch(context.Context, artifactapp.FetchQuery) (artifactapp.FetchResult, error)
}

type Service struct {
	store          artifactStore
	authentication authenticationResolver
	now            func() time.Time
}

var _ artifactv1connect.ArtifactServiceHandler = (*Service)(nil)

func New(artifacts artifactStore, authenticator authenticator, browserOrigin string) *Service {
	return &Service{
		store: artifacts,
		authentication: authenticationResolver{
			authenticator: authenticator,
			origin:        browserOrigin,
		},
		now: time.Now,
	}
}

// ── Handlers ─────────────────────────────────────────────────

func (s *Service) PublishArtifact(ctx context.Context, stream *connect.ClientStream[artifactv1.PublishArtifactRequest]) (*connect.Response[artifactv1.PublishArtifactResponse], error) {
	now := s.now()
	auth, err := s.authentication.resolve(ctx, stream.RequestHeader(), true, now)
	if err != nil {
		return nil, err
	}
	if !stream.Receive() {
		if stream.Err() != nil {
			return nil, servicesvc.ErrInternal
		}
		return nil, servicesvc.InvalArg("artifact metadata is required")
	}
	meta := stream.Msg().GetMetadata()
	if meta == nil {
		return nil, servicesvc.InvalArg("artifact metadata must be the first frame")
	}
	content := &publishReader{stream: stream}
	params, err := buildPublishParams(auth, meta, content, now)
	if err != nil {
		return nil, err
	}
	result, err := s.store.Publish(ctx, params)
	if content.serviceError != nil {
		return nil, content.serviceError
	}
	if err := servicesvc.ServiceErr(err); err != nil {
		return nil, err
	}
	artifact, err := artifactToProto(result.Artifact, false)
	if err != nil {
		return nil, err
	}
	version, err := versionToProto(result.Version)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.PublishArtifactResponse{
		Artifact: artifact, Version: version, CommittedAt: timestamppb.New(result.CommittedAt),
	}), nil
}

func (s *Service) GetArtifact(ctx context.Context, req *connect.Request[artifactv1.GetArtifactRequest]) (*connect.Response[artifactv1.GetArtifactResponse], error) {
	auth, artifactID, version, now, err := s.readParams(ctx, req.Header(), req.Msg.GetArtifactId(), req.Msg.GetVersion())
	if err != nil {
		return nil, err
	}
	view, err := s.store.Get(ctx, artifactapp.GetQuery{
		Authentication: auth, ArtifactID: artifactID, Version: version, Now: now,
	})
	if err := servicesvc.ServiceErr(err); err != nil {
		return nil, err
	}
	msg, err := viewToProto(view)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.GetArtifactResponse{View: msg}), nil
}

func (s *Service) ListArtifacts(ctx context.Context, req *connect.Request[artifactv1.ListArtifactsRequest]) (*connect.Response[artifactv1.ListArtifactsResponse], error) {
	now := s.now()
	auth, err := s.authentication.resolve(ctx, req.Header(), false, now)
	if err != nil {
		return nil, err
	}
	workID := ""
	if req.Msg.GetOwningWorkId() != "" {
		workID, err = connectid.CanonicalID(req.Msg.GetOwningWorkId(), "owning work id")
		if err != nil {
			return nil, err
		}
	}
	afterID := ""
	if req.Msg.GetAfterArtifactId() != "" {
		afterID, err = connectid.CanonicalID(req.Msg.GetAfterArtifactId(), "artifact cursor")
		if err != nil {
			return nil, err
		}
	}
	limit := req.Msg.GetLimit()
	if limit == 0 {
		limit = 50
	}
	if limit > 200 {
		return nil, servicesvc.InvalArg("artifact list limit must be at most 200")
	}
	result, err := s.store.List(ctx, artifactapp.ListQuery{
		Authentication: auth, OwningWorkID: workID, AfterArtifactID: afterID, Limit: limit, Now: now,
	})
	if err := servicesvc.ServiceErr(err); err != nil {
		return nil, err
	}
	resp := &artifactv1.ListArtifactsResponse{
		Views: make([]*artifactv1.ArtifactView, 0, len(result.Views)),
		NextArtifactId: result.NextArtifactID,
	}
	for _, v := range result.Views {
		msg, err := viewToProto(v)
		if err != nil {
			return nil, err
		}
		resp.Views = append(resp.Views, msg)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GrantArtifact(ctx context.Context, req *connect.Request[artifactv1.GrantArtifactRequest]) (*connect.Response[artifactv1.GrantArtifactResponse], error) {
	now := s.now()
	auth, err := s.authentication.resolve(ctx, req.Header(), true, now)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	artifactID, err := connectid.CanonicalID(req.Msg.GetArtifactId(), "artifact id")
	if err != nil {
		return nil, err
	}
	targetKind, targetValue, capability, err := grantTargetParams(req.Msg.GetTarget(), req.Msg.GetCapability())
	if err != nil {
		return nil, err
	}
	targetID, err := connectid.CanonicalID(targetValue, "artifact grant target id")
	if err != nil {
		return nil, err
	}
	grant, err := s.store.Grant(ctx, artifactapp.GrantCommand{
		RequestID: requestID, Authentication: auth, ArtifactID: artifactID,
		TargetKind: targetKind, TargetID: targetID, Capability: capability, Now: now,
	})
	if err := servicesvc.ServiceErr(err); err != nil {
		return nil, err
	}
	msg, err := grantToProto(grant)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.GrantArtifactResponse{Grant: msg}), nil
}

func (s *Service) RevokeArtifactGrant(ctx context.Context, req *connect.Request[artifactv1.RevokeArtifactGrantRequest]) (*connect.Response[artifactv1.RevokeArtifactGrantResponse], error) {
	now := s.now()
	auth, err := s.authentication.resolve(ctx, req.Header(), true, now)
	if err != nil {
		return nil, err
	}
	requestID, err := connectid.CanonicalID(req.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	grantID, err := connectid.CanonicalID(req.Msg.GetGrantId(), "artifact grant id")
	if err != nil {
		return nil, err
	}
	grant, err := s.store.RevokeGrant(ctx, artifactapp.RevokeGrantCommand{
		RequestID: requestID, Authentication: auth, GrantID: grantID, Now: now,
	})
	if err := servicesvc.ServiceErr(err); err != nil {
		return nil, err
	}
	msg, err := grantToProto(grant)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&artifactv1.RevokeArtifactGrantResponse{Grant: msg}), nil
}

func (s *Service) FetchArtifact(ctx context.Context, req *connect.Request[artifactv1.FetchArtifactRequest], stream *connect.ServerStream[artifactv1.FetchArtifactResponse]) error {
	auth, artifactID, version, now, err := s.readParams(ctx, req.Header(), req.Msg.GetArtifactId(), req.Msg.GetVersion())
	if err != nil {
		return err
	}
	result, err := s.store.Fetch(ctx, artifactapp.FetchQuery{
		Authentication: auth, ArtifactID: artifactID, Version: version, Now: now,
	})
	if err := servicesvc.ServiceErr(err); err != nil {
		return err
	}
	defer result.Content.Close()

	artifact, err := artifactToProto(result.Artifact, result.Artifact.OwningWorkID == "")
	if err != nil {
		return err
	}
	versionMsg, err := versionViewToProto(result.Version)
	if err != nil {
		return err
	}
	if err := stream.Send(&artifactv1.FetchArtifactResponse{Payload: &artifactv1.FetchArtifactResponse_Metadata{
		Metadata: &artifactv1.FetchArtifactMetadata{
			View: &artifactv1.ArtifactView{Artifact: artifact, Version: versionMsg},
		},
	}}); err != nil {
		return err
	}
	buf := make([]byte, artifactblob.MaxChunkSize)
	for {
		n, readErr := result.Content.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if err := stream.Send(&artifactv1.FetchArtifactResponse{
				Payload: &artifactv1.FetchArtifactResponse_Chunk{Chunk: chunk},
			}); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil || n == 0 {
			return servicesvc.ErrInternal
		}
	}
}

func (s *Service) readParams(ctx context.Context, header http.Header, artifactIDValue string, versionValue uint64) (artifactapp.Authentication, string, uint64, time.Time, error) {
	now := s.now()
	auth, err := s.authentication.resolve(ctx, header, false, now)
	if err != nil {
		return artifactapp.Authentication{}, "", 0, time.Time{}, err
	}
	artifactID, err := connectid.CanonicalID(artifactIDValue, "artifact id")
	if err != nil {
		return artifactapp.Authentication{}, "", 0, time.Time{}, err
	}
	v, err := parseVersion(versionValue)
	if err != nil {
		return artifactapp.Authentication{}, "", 0, time.Time{}, err
	}
	return auth, artifactID, v, now, nil
}

// ── Streaming reader ─────────────────────────────────────────

type publishReader struct {
	stream       *connect.ClientStream[artifactv1.PublishArtifactRequest]
	pending      []byte
	total        int64
	serviceError error
}

func (r *publishReader) Read(buf []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(buf, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if !r.stream.Receive() {
		if r.stream.Err() != nil {
			r.serviceError = servicesvc.ErrInternal
			return 0, errors.New("receive artifact content")
		}
		return 0, io.EOF
	}
	payload, ok := r.stream.Msg().GetPayload().(*artifactv1.PublishArtifactRequest_Chunk)
	if !ok || len(payload.Chunk) == 0 {
		r.serviceError = servicesvc.InvalArg("artifact content frames must be non-empty chunks")
		return 0, errors.New("invalid artifact content frame")
	}
	if len(payload.Chunk) > artifactblob.MaxChunkSize {
		r.serviceError = servicesvc.InvalArg("artifact content chunk exceeds 64 KiB")
		return 0, errors.New("artifact content chunk exceeds limit")
	}
	if r.total+int64(len(payload.Chunk)) > artifactblob.MaxBlobSize {
		r.serviceError = connect.NewError(connect.CodeResourceExhausted, errors.New("artifact content exceeds 64 MiB"))
		return 0, errors.New("artifact content exceeds limit")
	}
	r.total += int64(len(payload.Chunk))
	r.pending = payload.Chunk
	return r.Read(buf)
}

// ── Params ───────────────────────────────────────────────────

func buildPublishParams(auth artifactapp.Authentication, meta *artifactv1.PublishArtifactMetadata, content io.Reader, now time.Time) (artifactapp.PublishCommand, error) {
	requestID, err := connectid.CanonicalID(meta.GetRequestId(), "request id")
	if err != nil {
		return artifactapp.PublishCommand{}, err
	}
	artifactID := ""
	if meta.GetArtifactId() != "" {
		artifactID, err = connectid.CanonicalID(meta.GetArtifactId(), "artifact id")
		if err != nil {
			return artifactapp.PublishCommand{}, err
		}
	}
	workID, err := connectid.CanonicalID(meta.GetOwningWorkId(), "owning work id")
	if err != nil {
		return artifactapp.PublishCommand{}, err
	}
	if meta.GetDeclaredSize() < 0 || meta.GetDeclaredSize() > artifactblob.MaxBlobSize {
		return artifactapp.PublishCommand{}, servicesvc.InvalArg("declared artifact size is invalid")
	}
	digest := meta.GetDeclaredDigest()
	if len(digest) != sha256.Size {
		return artifactapp.PublishCommand{}, servicesvc.InvalArg("declared artifact digest must be 32 bytes")
	}
	var expectedDigest [sha256.Size]byte
	copy(expectedDigest[:], digest)
	expectedSize := meta.GetDeclaredSize()

	params := artifactapp.PublishCommand{
		RequestID: requestID, Authentication: auth, ArtifactID: artifactID,
		OwningWorkID: workID, Name: meta.GetName(), MediaType: meta.GetMediaType(),
		Summary: meta.GetSummary(), ExpectedDigest: &expectedDigest, ExpectedSize: &expectedSize,
		Content: content, Now: now,
	}
	if meta.GetExecution() != nil {
		exec := meta.GetExecution()
		runID, err := connectid.CanonicalID(exec.GetRunId(), "run id")
		if err != nil {
			return artifactapp.PublishCommand{}, err
		}
		if exec.GetAttempt() == 0 || exec.GetAttempt() > math.MaxInt64 {
			return artifactapp.PublishCommand{}, servicesvc.InvalArg("artifact execution attempt is invalid")
		}
		if exec.GetFence() == 0 || exec.GetFence() > math.MaxInt64 {
			return artifactapp.PublishCommand{}, servicesvc.InvalArg("artifact execution fence is invalid")
		}
		params.Execution = &artifactapp.ExecutionInput{
			RunID: runID, Attempt: exec.GetAttempt(), Fence: exec.GetFence(),
		}
	}
	params.Sources = make([]artifactapp.SourceInput, 0, len(meta.GetSources()))
	for _, src := range meta.GetSources() {
		if src == nil {
			return artifactapp.PublishCommand{}, servicesvc.InvalArg("artifact source is invalid")
		}
		switch v := src.GetSource().(type) {
		case *artifactv1.ArtifactSourceInput_MessageId:
			msgID, err := connectid.CanonicalID(v.MessageId, "artifact source message id")
			if err != nil {
				return artifactapp.PublishCommand{}, err
			}
			params.Sources = append(params.Sources, artifactapp.SourceInput{Kind: artifactapp.SourceMessage, MessageID: msgID})
		case *artifactv1.ArtifactSourceInput_ArtifactVersion:
			if v.ArtifactVersion == nil {
				return artifactapp.PublishCommand{}, servicesvc.InvalArg("artifact version source is required")
			}
			aid, err := connectid.CanonicalID(v.ArtifactVersion.GetArtifactId(), "source artifact id")
			if err != nil {
				return artifactapp.PublishCommand{}, err
			}
			ver, err := requireVersion(v.ArtifactVersion.GetVersion())
			if err != nil {
				return artifactapp.PublishCommand{}, err
			}
			params.Sources = append(params.Sources, artifactapp.SourceInput{
				Kind: artifactapp.SourceVersion, ArtifactID: aid, ArtifactVersion: ver,
			})
		default:
			return artifactapp.PublishCommand{}, servicesvc.InvalArg("artifact source is invalid")
		}
	}
	return params, nil
}

func grantTargetParams(target *artifactv1.ArtifactGrantTarget, capability artifactv1.ArtifactCapability) (string, string, string, error) {
	if target == nil {
		return "", "", "", servicesvc.InvalArg("artifact grant target is required")
	}
	var targetKind, targetID string
	switch v := target.GetTarget().(type) {
	case *artifactv1.ArtifactGrantTarget_AgentId:
		targetKind, targetID = servicesvc.GrantTargetAgent, v.AgentId
	case *artifactv1.ArtifactGrantTarget_SpaceId:
		targetKind, targetID = servicesvc.GrantTargetSpace, v.SpaceId
	case *artifactv1.ArtifactGrantTarget_WorkId:
		targetKind, targetID = servicesvc.GrantTargetWork, v.WorkId
	default:
		return "", "", "", servicesvc.InvalArg("artifact grant target is invalid")
	}
	var capName string
	switch capability {
	case artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ:
		capName = servicesvc.GrantRead
	case artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_MANAGE:
		capName = servicesvc.GrantManage
	default:
		return "", "", "", servicesvc.InvalArg("artifact capability is invalid")
	}
	return targetKind, targetID, capName, nil
}

func parseVersion(v uint64) (uint64, error) {
	if v > math.MaxInt64 {
		return 0, servicesvc.InvalArg("artifact version is too large")
	}
	return v, nil
}

func requireVersion(v uint64) (uint64, error) {
	if v == 0 {
		return 0, servicesvc.InvalArg("artifact version is required")
	}
	return parseVersion(v)
}

// ── Proto converters ─────────────────────────────────────────

func viewToProto(v artifactapp.View) (*artifactv1.ArtifactView, error) {
	artifact, err := artifactToProto(v.Artifact, v.OwningWorkRestricted)
	if err != nil {
		return nil, err
	}
	version, err := versionViewToProto(v.Version)
	if err != nil {
		return nil, err
	}
	return &artifactv1.ArtifactView{Artifact: artifact, Version: version}, nil
}

func artifactToProto(a artifactapp.Artifact, restricted bool) (*artifactv1.Artifact, error) {
	creator, err := servicesvc.ToPrincipal(a.Creator)
	if err != nil {
		return nil, err
	}
	return &artifactv1.Artifact{
		Id: a.ID, OrganizationId: a.OrganizationID, OwningWorkId: a.OwningWorkID,
		OwningWorkRestricted: restricted, Name: a.Name, MediaType: a.MediaType,
		Creator: creator, CreatedAt: servicesvc.Ts(a.CreatedAt),
	}, nil
}

func versionViewToProto(v artifactapp.VersionView) (*artifactv1.ArtifactVersion, error) {
	author, err := servicesvc.ToPrincipal(v.Author)
	if err != nil {
		return nil, err
	}
	state, err := servicesvc.IntegToProto(v.IntegrityState)
	if err != nil {
		return nil, err
	}
	msg := &artifactv1.ArtifactVersion{
		ArtifactId: v.ArtifactID, OrganizationId: v.OrganizationID, Version: v.Version,
		Digest: append([]byte(nil), v.Digest[:]...), Size: v.Size, Summary: v.Summary,
		Author: author, IntegrityState: state,
		CreatedAt: servicesvc.Ts(v.CreatedAt),
	}
	if v.Execution != nil {
		msg.Execution = &artifactv1.ArtifactExecution{
			Restricted: v.Execution.Restricted, RunId: v.Execution.RunID,
			Attempt: v.Execution.Attempt, AgentId: v.Execution.AgentID,
			ComputerId: v.Execution.ComputerID, PlacementDesiredRevision: v.Execution.PlacementDesiredRevision,
			Fence: v.Execution.Fence,
		}
	}
	msg.Sources = make([]*artifactv1.ArtifactSource, 0, len(v.Sources))
	for _, src := range v.Sources {
		msg.Sources = append(msg.Sources, sourceViewToProto(src))
	}
	return msg, nil
}

func versionToProto(v artifactapp.Version) (*artifactv1.ArtifactVersion, error) {
	author, err := servicesvc.ToPrincipal(v.Author)
	if err != nil {
		return nil, err
	}
	state, err := servicesvc.IntegToProto(v.IntegrityState)
	if err != nil {
		return nil, err
	}
	msg := &artifactv1.ArtifactVersion{
		ArtifactId: v.ArtifactID, OrganizationId: v.OrganizationID, Version: v.Version,
		Digest: append([]byte(nil), v.Digest[:]...), Size: v.Size, Summary: v.Summary,
		Author: author, IntegrityState: state,
		CreatedAt: servicesvc.Ts(v.CreatedAt),
	}
	if v.Execution != nil {
		msg.Execution = &artifactv1.ArtifactExecution{
			RunId: v.Execution.RunID, Attempt: v.Execution.Attempt,
			AgentId: v.Execution.AgentID, ComputerId: v.Execution.ComputerID,
			PlacementDesiredRevision: v.Execution.PlacementDesiredRevision, Fence: v.Execution.Fence,
		}
	}
	msg.Sources = make([]*artifactv1.ArtifactSource, 0, len(v.Sources))
	for _, src := range v.Sources {
		msg.Sources = append(msg.Sources, sourceToProto(src))
	}
	return msg, nil
}

func sourceViewToProto(v artifactapp.SourceView) *artifactv1.ArtifactSource {
	msg := &artifactv1.ArtifactSource{Restricted: v.Restricted}
	if !v.Restricted {
		switch v.Kind {
		case artifactapp.SourceMessage:
			msg.Source = &artifactv1.ArtifactSource_MessageId{MessageId: v.MessageID}
		case artifactapp.SourceVersion:
			msg.Source = &artifactv1.ArtifactSource_ArtifactVersion{ArtifactVersion: &artifactv1.ArtifactVersionRef{
				ArtifactId: v.ArtifactID, Version: v.ArtifactVersion,
			}}
		}
	}
	return msg
}

func sourceToProto(v artifactapp.Source) *artifactv1.ArtifactSource {
	msg := &artifactv1.ArtifactSource{}
	switch v.Kind {
	case artifactapp.SourceMessage:
		msg.Source = &artifactv1.ArtifactSource_MessageId{MessageId: v.MessageID}
	case artifactapp.SourceVersion:
		msg.Source = &artifactv1.ArtifactSource_ArtifactVersion{ArtifactVersion: &artifactv1.ArtifactVersionRef{
			ArtifactId: v.ArtifactID, Version: v.ArtifactVersion,
		}}
	}
	return msg
}

func grantToProto(g artifactapp.Grant) (*artifactv1.ArtifactGrant, error) {
	grantedBy, err := servicesvc.ToPrincipal(g.GrantedBy)
	if err != nil {
		return nil, err
	}
	msg := &artifactv1.ArtifactGrant{
		Id: g.ID, ArtifactId: g.ArtifactID, OrganizationId: g.OrganizationID,
		Target: &artifactv1.ArtifactGrantTarget{}, GrantedBy: grantedBy,
		GrantedAt: servicesvc.Ts(g.GrantedAt),
	}
	switch g.TargetKind {
	case servicesvc.GrantTargetAgent:
		msg.Target.Target = &artifactv1.ArtifactGrantTarget_AgentId{AgentId: g.TargetID}
	case servicesvc.GrantTargetSpace:
		msg.Target.Target = &artifactv1.ArtifactGrantTarget_SpaceId{SpaceId: g.TargetID}
	case servicesvc.GrantTargetWork:
		msg.Target.Target = &artifactv1.ArtifactGrantTarget_WorkId{WorkId: g.TargetID}
	default:
		return nil, servicesvc.ErrInternal
	}
	switch g.Capability {
	case servicesvc.GrantRead:
		msg.Capability = artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ
	case servicesvc.GrantManage:
		msg.Capability = artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_MANAGE
	default:
		return nil, servicesvc.ErrInternal
	}
	if g.RevokedAt != nil {
		if g.RevokedBy == nil {
			return nil, servicesvc.ErrInternal
		}
		msg.RevokedBy, err = servicesvc.ToPrincipal(*g.RevokedBy)
		if err != nil {
			return nil, err
		}
		msg.RevokedAt = servicesvc.Ts(*g.RevokedAt)
	} else if g.RevokedBy != nil {
		return nil, servicesvc.ErrInternal
	}
	return msg, nil
}

var _ artifactv1connect.ArtifactServiceHandler = (*Service)(nil)
