package servicesvc

import (
	"time"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	artifactv1 "github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1"
	auditv1 "github.com/abcdlsj/sumi/gen/go/sumi/audit/v1"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	agentapp "github.com/abcdlsj/sumi/internal/agent/application"
	auditapp "github.com/abcdlsj/sumi/internal/audit/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	workapp "github.com/abcdlsj/sumi/internal/work/application"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── Principal ────────────────────────────────────────────────

var principalProto = map[authoritydomain.PrincipalKind]grantv1.PrincipalKind{
	authoritydomain.PrincipalSystem: grantv1.PrincipalKind_PRINCIPAL_KIND_SYSTEM,
	authoritydomain.PrincipalHuman:  grantv1.PrincipalKind_PRINCIPAL_KIND_HUMAN,
	authoritydomain.PrincipalAgent:  grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
}

// ToPrincipal converts a domain principal to a proto principal.
func ToPrincipal(p authoritydomain.Principal) (*grantv1.Principal, error) {
	kind, ok := principalProto[p.Kind]
	if !ok {
		return nil, ErrInternal
	}
	return &grantv1.Principal{Kind: kind, Id: p.ID}, nil
}

// ── Agent Engine ─────────────────────────────────────────────

var (
	engineProto = map[agentv1.EngineKind]agentapp.EngineKind{
		agentv1.EngineKind_ENGINE_KIND_BUILTIN:        agentapp.EngineBuiltin,
		agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER:  agentapp.EngineCodexAdapter,
		agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER: agentapp.EngineClaudeAdapter,
	}
	engineDomain = map[agentapp.EngineKind]agentv1.EngineKind{
		agentapp.EngineBuiltin:       agentv1.EngineKind_ENGINE_KIND_BUILTIN,
		agentapp.EngineCodexAdapter:  agentv1.EngineKind_ENGINE_KIND_CODEX_ADAPTER,
		agentapp.EngineClaudeAdapter: agentv1.EngineKind_ENGINE_KIND_CLAUDE_ADAPTER,
	}
)

func EngineFromProto(v agentv1.EngineKind) (agentapp.EngineKind, bool) {
	e, ok := engineProto[v]
	return e, ok
}

func EngineToProto(v agentapp.EngineKind) agentv1.EngineKind {
	e, ok := engineDomain[v]
	if !ok {
		return agentv1.EngineKind_ENGINE_KIND_UNSPECIFIED
	}
	return e
}

// ── Provider Protocol ────────────────────────────────────────

var (
	provProto = map[agentv1.ProviderProtocol]agentapp.ProviderProtocol{
		agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED:              "",
		agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES:         agentapp.ProviderOpenAIResponses,
		agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES:       agentapp.ProviderAnthropicMessages,
	}
	provDomain = map[agentapp.ProviderProtocol]agentv1.ProviderProtocol{
		"":                                  agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED,
		agentapp.ProviderOpenAIResponses:    agentv1.ProviderProtocol_PROVIDER_PROTOCOL_OPENAI_RESPONSES,
		agentapp.ProviderAnthropicMessages:  agentv1.ProviderProtocol_PROVIDER_PROTOCOL_ANTHROPIC_MESSAGES,
	}
)

func ProvFromProto(v agentv1.ProviderProtocol) (agentapp.ProviderProtocol, bool) {
	p, ok := provProto[v]
	return p, ok
}

func ProvToProto(v agentapp.ProviderProtocol) agentv1.ProviderProtocol {
	p, ok := provDomain[v]
	if !ok {
		return agentv1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED
	}
	return p
}

// ── Work State ───────────────────────────────────────────────

var (
	stateProto = map[workv1.WorkState]string{
		workv1.WorkState_WORK_STATE_OPEN:             workapp.StateOpen,
		workv1.WorkState_WORK_STATE_BLOCKED:          workapp.StateBlocked,
		workv1.WorkState_WORK_STATE_COMPLETED:        workapp.StateCompleted,
		workv1.WorkState_WORK_STATE_FAILED:           workapp.StateFailed,
		workv1.WorkState_WORK_STATE_CANCELLED:        workapp.StateCancelled,
	}
	stateDomain = map[string]workv1.WorkState{
		"":                              workv1.WorkState_WORK_STATE_UNSPECIFIED,
		workapp.StateOpen:               workv1.WorkState_WORK_STATE_OPEN,
		workapp.StateBlocked:            workv1.WorkState_WORK_STATE_BLOCKED,
		workapp.StateWaitingApproval:    workv1.WorkState_WORK_STATE_WAITING_APPROVAL,
		workapp.StateCompleted:          workv1.WorkState_WORK_STATE_COMPLETED,
		workapp.StateFailed:             workv1.WorkState_WORK_STATE_FAILED,
		workapp.StateCancelled:          workv1.WorkState_WORK_STATE_CANCELLED,
	}
)

func StateFromProto(v workv1.WorkState) (string, error) {
	s, ok := stateProto[v]
	if !ok {
		return "", ErrInvalArg("work state is invalid")
	}
	return s, nil
}

func StateToProto(v string) (workv1.WorkState, error) {
	s, ok := stateDomain[v]
	if !ok {
		return workv1.WorkState_WORK_STATE_UNSPECIFIED, ErrInternal
	}
	return s, nil
}

// ── Work Assignment Role ─────────────────────────────────────

var (
	roleProto = map[workv1.WorkAssignmentRole]string{
		workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR: workapp.AssignmentCoordinator,
		workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_CONTRIBUTOR: workapp.AssignmentContributor,
	}
	roleDomain = map[string]workv1.WorkAssignmentRole{
		workapp.AssignmentCoordinator: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR,
		workapp.AssignmentContributor: workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_CONTRIBUTOR,
	}
)

func RoleFromProto(v workv1.WorkAssignmentRole) (string, error) {
	r, ok := roleProto[v]
	if !ok {
		return "", ErrInvalArg("assignment role is invalid")
	}
	return r, nil
}

func RoleToProto(v string) (workv1.WorkAssignmentRole, error) {
	r, ok := roleDomain[v]
	if !ok {
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_UNSPECIFIED, ErrInternal
	}
	return r, nil
}

// ── Approval Decision ────────────────────────────────────────

var decisionProto = map[workv1.WorkApprovalDecision]string{
	workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_APPROVED: "approved",
	workv1.WorkApprovalDecision_WORK_APPROVAL_DECISION_REJECTED: "rejected",
}

func DecisionFromProto(v workv1.WorkApprovalDecision) (string, error) {
	d, ok := decisionProto[v]
	if !ok {
		return "", ErrInvalArg("approval decision is invalid")
	}
	return d, nil
}

// ── Approval Status ──────────────────────────────────────────

var statusDomain = map[string]workv1.WorkApprovalStatus{
	"pending":   workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_PENDING,
	"approved":  workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_APPROVED,
	"rejected":  workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_REJECTED,
	"cancelled": workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_CANCELLED,
}

func StatusToProto(v string) (workv1.WorkApprovalStatus, error) {
	s, ok := statusDomain[v]
	if !ok {
		return workv1.WorkApprovalStatus_WORK_APPROVAL_STATUS_UNSPECIFIED, ErrInternal
	}
	return s, nil
}

// ── Criterion Verdict ────────────────────────────────────────

var (
	verdictProto = map[workv1.WorkCriterionVerdict]string{
		workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED: "passed",
		workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_FAILED: "failed",
	}
	verdictDomain = map[string]workv1.WorkCriterionVerdict{
		"passed": workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED,
		"failed": workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_FAILED,
	}
)

func VerdictFromProto(v workv1.WorkCriterionVerdict) (string, error) {
	vd, ok := verdictProto[v]
	if !ok {
		return "", ErrInvalArg("criterion verdict is invalid")
	}
	return vd, nil
}

func VerdictToProto(v string) (workv1.WorkCriterionVerdict, error) {
	vd, ok := verdictDomain[v]
	if !ok {
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_UNSPECIFIED, ErrInternal
	}
	return vd, nil
}

// ── Audit Context ────────────────────────────────────────────

var auditCtxMap = map[string]auditv1.AuditContextKind{
	"space":    auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_SPACE,
	"thread":   auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_THREAD,
	"computer": auditv1.AuditContextKind_AUDIT_CONTEXT_KIND_COMPUTER,
}

func AuditCtxToProto(v string) auditv1.AuditContextKind {
	return auditCtxMap[v]
}

// ── Audit Action ─────────────────────────────────────────────

var auditActionMap = map[string]auditv1.AuditAction{
	auditapp.ActionOrganizationBootstrap: auditv1.AuditAction_AUDIT_ACTION_ORGANIZATION_BOOTSTRAP,
	auditapp.ActionHumanCreate:           auditv1.AuditAction_AUDIT_ACTION_HUMAN_CREATE,
	auditapp.ActionHumanStatusSet:        auditv1.AuditAction_AUDIT_ACTION_HUMAN_STATUS_SET,
	auditapp.ActionGrantIssue:            auditv1.AuditAction_AUDIT_ACTION_GRANT_ISSUE,
	auditapp.ActionGrantRevoke:           auditv1.AuditAction_AUDIT_ACTION_GRANT_REVOKE,
	auditapp.ActionAgentCreate:           auditv1.AuditAction_AUDIT_ACTION_AGENT_CREATE,
	auditapp.ActionAgentPlace:            auditv1.AuditAction_AUDIT_ACTION_AGENT_PLACE,
	auditapp.ActionSpaceCreate:           auditv1.AuditAction_AUDIT_ACTION_SPACE_CREATE,
	auditapp.ActionSpaceMemberAdd:        auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_ADD,
	auditapp.ActionSpaceMemberRemove:     auditv1.AuditAction_AUDIT_ACTION_SPACE_MEMBER_REMOVE,
	auditapp.ActionSpaceArchive:          auditv1.AuditAction_AUDIT_ACTION_SPACE_ARCHIVE,
	auditapp.ActionSpaceUnarchive:        auditv1.AuditAction_AUDIT_ACTION_SPACE_UNARCHIVE,
	auditapp.ActionThreadCreate:          auditv1.AuditAction_AUDIT_ACTION_THREAD_CREATE,
	auditapp.ActionMessageSend:           auditv1.AuditAction_AUDIT_ACTION_MESSAGE_SEND,
	auditapp.ActionComputerPairPrepare:   auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR_PREPARE,
	auditapp.ActionComputerPair:          auditv1.AuditAction_AUDIT_ACTION_COMPUTER_PAIR,
}

func AuditActionToProto(v string) auditv1.AuditAction {
	return auditActionMap[v]
}

// ── Audit Target ─────────────────────────────────────────────

var auditTargetMap = map[string]auditv1.AuditTargetKind{
	"organization":     auditv1.AuditTargetKind_AUDIT_TARGET_KIND_ORGANIZATION,
	"human":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_HUMAN,
	"agent":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_AGENT,
	"grant":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_GRANT,
	"space":            auditv1.AuditTargetKind_AUDIT_TARGET_KIND_SPACE,
	"thread":           auditv1.AuditTargetKind_AUDIT_TARGET_KIND_THREAD,
	"message":          auditv1.AuditTargetKind_AUDIT_TARGET_KIND_MESSAGE,
	"computer_pairing": auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER_PAIRING,
	"computer":         auditv1.AuditTargetKind_AUDIT_TARGET_KIND_COMPUTER,
}

func AuditTargetToProto(v string) auditv1.AuditTargetKind {
	return auditTargetMap[v]
}

// ── Audit Outcome ────────────────────────────────────────────

func AuditOutcomeToProto(v string) auditv1.AuditOutcome {
	switch v {
	case "committed":
		return auditv1.AuditOutcome_AUDIT_OUTCOME_COMMITTED
	case "denied":
		return auditv1.AuditOutcome_AUDIT_OUTCOME_DENIED
	default:
		return auditv1.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED
	}
}

// ── Artifact Integrity State ─────────────────────────────────

var integMap = map[string]artifactv1.ArtifactIntegrityState{
	"ready":   artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_READY,
	"missing": artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_MISSING,
	"corrupt": artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_CORRUPT,
}

func IntegToProto(v string) (artifactv1.ArtifactIntegrityState, error) {
	s, ok := integMap[v]
	if !ok {
		return artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_UNSPECIFIED, ErrInternal
	}
	return s, nil
}

// ── Artifact Grant Target ────────────────────────────────────

const (
	GrantTargetAgent = "agent"
	GrantTargetSpace = "space"
	GrantTargetWork  = "work"
	GrantRead        = "read"
	GrantManage      = "manage"
)

// ── Timestamp ────────────────────────────────────────────────

func Ts(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}

func TsOpt(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// ── Message Target ───────────────────────────────────────────

func MsgTargetToProto(t collaborationapp.MessageTarget) (*spacev1.MessageTarget, error) {
	switch t.Kind {
	case collaborationdomain.TargetSpace:
		return &spacev1.MessageTarget{Target: &spacev1.MessageTarget_SpaceId{SpaceId: t.ID}}, nil
	case collaborationdomain.TargetThread:
		return &spacev1.MessageTarget{Target: &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: t.ID}}, nil
	default:
		return nil, ErrInternal
	}
}
