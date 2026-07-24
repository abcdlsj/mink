package remote

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/internal/provider"
	"github.com/abcdlsj/sumi/internal/tool"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type sendMessageArguments struct {
	Body              string   `json:"body"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
}

type workIDArguments struct {
	WorkID string `json:"work_id"`
}

type workCreateArguments struct {
	ParentWorkID       string   `json:"parent_work_id"`
	Goal               string   `json:"goal"`
	Constraints        []string `json:"constraints"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

type workAssignArguments struct {
	WorkID  string `json:"work_id"`
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
}

type criterionResultArguments struct {
	CriterionID string `json:"criterion_id"`
	Verdict     string `json:"verdict"`
	Evidence    string `json:"evidence"`
}

type workTransitionArguments struct {
	WorkID           string                     `json:"work_id"`
	ToState          string                     `json:"to_state"`
	Reason           string                     `json:"reason"`
	Result           string                     `json:"result"`
	CriterionResults []criterionResultArguments `json:"criterion_results"`
}

type workApprovalArguments struct {
	WorkID   string `json:"work_id"`
	Question string `json:"question"`
}

type knowledgeArguments struct {
	Query string `json:"query"`
	Limit uint32 `json:"limit"`
}

type artifactReferenceArguments struct {
	ArtifactID string `json:"artifact_id"`
	Version    uint64 `json:"version"`
}

type artifactListArguments struct {
	OwningWorkID string `json:"owning_work_id"`
	Limit        uint32 `json:"limit"`
}

type artifactPublishArguments struct {
	ArtifactID    string `json:"artifact_id"`
	OwningWorkID  string `json:"owning_work_id"`
	Name          string `json:"name"`
	MediaType     string `json:"media_type"`
	Summary       string `json:"summary"`
	ContentBase64 string `json:"content_base64"`
}

func validateSendMessage(raw json.RawMessage) error {
	var arguments sendMessageArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if textInvalid(arguments.Body, maximumMessageRunes, false) || len(arguments.MentionedAgentIDs) > 100 {
		return errors.New("message arguments are invalid")
	}
	seen := make(map[string]struct{}, len(arguments.MentionedAgentIDs))
	for _, agentID := range arguments.MentionedAgentIDs {
		if !canonicalID(agentID) {
			return errors.New("mentioned Agent ID is invalid")
		}
		if _, duplicate := seen[agentID]; duplicate {
			return errors.New("mentioned Agent ID is duplicated")
		}
		seen[agentID] = struct{}{}
	}
	return nil
}

func validateWorkID(raw json.RawMessage) error {
	var arguments workIDArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) {
		return errors.New("work ID is invalid")
	}
	return nil
}

func validateWorkCreate(raw json.RawMessage) error {
	var arguments workCreateArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if (arguments.ParentWorkID != "" && !canonicalID(arguments.ParentWorkID)) ||
		textInvalid(arguments.Goal, 20_000, false) || len(arguments.Constraints) > 100 ||
		len(arguments.AcceptanceCriteria) == 0 || len(arguments.AcceptanceCriteria) > 100 {
		return errors.New("work create arguments are invalid")
	}
	for _, value := range append(append([]string(nil), arguments.Constraints...), arguments.AcceptanceCriteria...) {
		if textInvalid(value, 20_000, false) {
			return errors.New("work text is invalid")
		}
	}
	return nil
}

func validateWorkAssign(raw json.RawMessage) error {
	var arguments workAssignArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) || !canonicalID(arguments.AgentID) || assignmentRole(arguments.Role) == workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_UNSPECIFIED {
		return errors.New("work assignment arguments are invalid")
	}
	return nil
}

func validateWorkTransition(raw json.RawMessage) error {
	var arguments workTransitionArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) || workState(arguments.ToState) == workv1.WorkState_WORK_STATE_UNSPECIFIED ||
		textInvalid(arguments.Reason, 20_000, true) || textInvalid(arguments.Result, maximumWorkRunes, true) || len(arguments.CriterionResults) > 100 {
		return errors.New("work transition arguments are invalid")
	}
	for _, result := range arguments.CriterionResults {
		if !canonicalID(result.CriterionID) || criterionVerdict(result.Verdict) == workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_UNSPECIFIED || textInvalid(result.Evidence, 20_000, true) {
			return errors.New("work criterion result is invalid")
		}
	}
	return nil
}

func validateWorkApproval(raw json.RawMessage) error {
	var arguments workApprovalArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) || textInvalid(arguments.Question, 20_000, false) {
		return errors.New("work approval arguments are invalid")
	}
	return nil
}

func validateKnowledgeSearch(raw json.RawMessage) error {
	var arguments knowledgeArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if textInvalid(arguments.Query, maximumSearchRunes, false) || arguments.Limit == 0 || arguments.Limit > 20 {
		return errors.New("knowledge search arguments are invalid")
	}
	return nil
}

func validateArtifactReference(raw json.RawMessage) error {
	var arguments artifactReferenceArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.ArtifactID) {
		return errors.New("artifact reference is invalid")
	}
	return nil
}

func validateArtifactList(raw json.RawMessage) error {
	var arguments artifactListArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if (arguments.OwningWorkID != "" && !canonicalID(arguments.OwningWorkID)) || arguments.Limit == 0 || arguments.Limit > 20 {
		return errors.New("artifact list arguments are invalid")
	}
	return nil
}

func validateArtifactPublish(raw json.RawMessage) error {
	var arguments artifactPublishArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if (arguments.ArtifactID != "" && !canonicalID(arguments.ArtifactID)) || !canonicalID(arguments.OwningWorkID) ||
		textInvalid(arguments.Name, 255, false) || textInvalid(arguments.MediaType, 255, false) ||
		textInvalid(arguments.Summary, 20_000, true) || arguments.ContentBase64 == "" || len(arguments.ContentBase64) > 699_052 {
		return errors.New("artifact publish arguments are invalid")
	}
	content, err := base64.StdEncoding.DecodeString(arguments.ContentBase64)
	if err != nil || len(content) == 0 || len(content) > maximumArtifactBytes {
		return errors.New("artifact content is invalid")
	}
	return nil
}

func decode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("tool arguments contain trailing data")
	}
	return nil
}

func textInvalid(value string, maximum int, optional bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return true
	}
	return !optional && strings.TrimSpace(value) == ""
}

func canonicalID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func toolRequestID(run tool.RunContext, call provider.ToolCall) string {
	payload := fmt.Sprintf("sumi.tool.v1\x00%s\x00%d\x00%d\x00%s\x00%s", run.RunID, run.Attempt, run.Fence, call.Name, call.ID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(payload)).String()
}

func marshal(message proto.Message) (json.RawMessage, error) {
	payload, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	return payload, nil
}

func idSchema(field string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"%s":{"type":"string","format":"uuid"}},"required":["%s"],"additionalProperties":false}`, field, field))
}

func artifactReferenceSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"artifact_id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":0}},"required":["artifact_id","version"],"additionalProperties":false}`)
}

func workState(value string) workv1.WorkState {
	switch value {
	case "open":
		return workv1.WorkState_WORK_STATE_OPEN
	case "blocked":
		return workv1.WorkState_WORK_STATE_BLOCKED
	case "completed":
		return workv1.WorkState_WORK_STATE_COMPLETED
	case "failed":
		return workv1.WorkState_WORK_STATE_FAILED
	case "cancelled":
		return workv1.WorkState_WORK_STATE_CANCELLED
	default:
		return workv1.WorkState_WORK_STATE_UNSPECIFIED
	}
}

func criterionVerdict(value string) workv1.WorkCriterionVerdict {
	switch value {
	case "passed":
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED
	case "failed":
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_FAILED
	default:
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_UNSPECIFIED
	}
}

func assignmentRole(value string) workv1.WorkAssignmentRole {
	switch value {
	case "coordinator":
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR
	case "contributor":
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_CONTRIBUTOR
	default:
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_UNSPECIFIED
	}
}

var _ tool.Authorizer = (*client)(nil)
