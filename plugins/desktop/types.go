package desktop

import (
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/msg"
)

type SessionItem struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	TitleFixed   bool      `json:"title_fixed,omitempty"`
	PersonaID    string    `json:"persona_id,omitempty"`
	PersonaName  string    `json:"persona_name,omitempty"`
	Runtime      string    `json:"runtime"`
	Model        string    `json:"model"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	EventCount   int       `json:"event_count"`
	Running      bool      `json:"running"`
	Pinned       bool      `json:"pinned"`
}

type SessionDetail struct {
	Item     SessionItem   `json:"item"`
	Messages []MessageView `json:"messages"`
	Summary  string        `json:"summary,omitempty"`
}

type MessageView struct {
	ID              string             `json:"id"`
	Role            string             `json:"role"`
	AuthorID        string             `json:"author_id,omitempty"`
	AuthorName      string             `json:"author_name,omitempty"`
	Content         string             `json:"content,omitempty"`
	Reasoning       string             `json:"reasoning,omitempty"`
	Status          string             `json:"status,omitempty"`
	Error           string             `json:"error,omitempty"`
	Time            time.Time          `json:"time"`
	Events          []EventBlock       `json:"events,omitempty"`
	Usage           *TokenUsage        `json:"usage,omitempty"`
	ThreadID        string             `json:"thread_id,omitempty"`
	ThreadSummary   string             `json:"thread_summary,omitempty"`
	ThreadInfo      *ThreadSummary     `json:"thread_info,omitempty"`
	IsThreadReply   bool               `json:"is_thread_reply,omitempty"`
	TaskAccessory   *TaskAccessoryInfo `json:"task_accessory,omitempty"`
	AutoReplyReason string             `json:"auto_reply_reason,omitempty"`
	Mentions        []string           `json:"mentions,omitempty"`
	RuntimeMeta     map[string]string  `json:"runtime_meta,omitempty"`
	Attachments     []msg.Attachment   `json:"attachments,omitempty"`
}

type TaskAccessoryInfo struct {
	TaskID        string `json:"task_id"`
	WorkerID      string `json:"worker_id"`
	WorkerDisplay string `json:"worker_display,omitempty"`
	Status        string `json:"status"`
	ShortOutcome  string `json:"short_outcome,omitempty"`
	Terminal      bool   `json:"terminal"`
}

type EventBlock struct {
	Kind         string         `json:"kind"`
	ToolName     string         `json:"tool_name,omitempty"`
	Args         string         `json:"args,omitempty"`
	Output       string         `json:"output,omitempty"`
	Status       string         `json:"status,omitempty"`
	Err          string         `json:"err,omitempty"`
	DurationMs   int64          `json:"duration_ms,omitempty"`
	Time         time.Time      `json:"time"`
	AgentID      string         `json:"agent_id,omitempty"`
	AgentDisplay string         `json:"agent_display,omitempty"`
	Task         string         `json:"task,omitempty"`
	TaskID       string         `json:"task_id,omitempty"`
	Reply        string         `json:"reply,omitempty"`
	Steps        []DelegateStep `json:"steps,omitempty"`
}

type DelegateStep struct {
	Tool   string    `json:"tool"`
	Status string    `json:"status"`
	Output string    `json:"output,omitempty"`
	Err    string    `json:"err,omitempty"`
	Time   time.Time `json:"time"`
}

type TokenUsage struct {
	Input   int     `json:"input"`
	Output  int     `json:"output"`
	Total   int     `json:"total"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	Model   string  `json:"model,omitempty"`
	Source  string  `json:"source,omitempty"`
}

type PersonaItem struct {
	ID            string   `json:"id"`
	Display       string   `json:"display"`
	Runtime       string   `json:"runtime"`
	Model         string   `json:"model,omitempty"`
	Description   string   `json:"description"`
	Tools         []string `json:"tools,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	TaskPolicy    string   `json:"task_policy,omitempty"`
	ShowInSidebar bool     `json:"show_in_sidebar"`
}

type ModelItem struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	MaxTokens     int    `json:"max_tokens"`
	ContextWindow int    `json:"context_window"`
	Ready         bool   `json:"ready"`
}

type ToolItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type CommandItem struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Usage   string `json:"usage,omitempty"`
}

type WorkspaceState struct {
	Workspace string `json:"workspace"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Runtime   string `json:"runtime"`
	Ready     bool   `json:"ready"`
	DataDir   string `json:"data_dir"`
}

type SendRequest struct {
	SessionID       string           `json:"session_id"`
	PersonaID       string           `json:"persona_id,omitempty"`
	Input           string           `json:"input"`
	ParentMessageID string           `json:"parent_message_id,omitempty"`
	Attachments     []msg.Attachment `json:"attachments,omitempty"`
}

type RetryMessageRequest struct {
	SpaceID   string `json:"space_id"`
	MessageID string `json:"message_id"`
}

type DeleteMemoryRequest struct {
	PersonaID string `json:"persona_id,omitempty"`
	Source    string `json:"source,omitempty"`
	SpaceID   string `json:"space_id,omitempty"`
	Scope     string `json:"scope"`
	ID        string `json:"id"`
}

type DeleteMemoryResult struct {
	OK       bool               `json:"ok"`
	Output   string             `json:"output"`
	Overview app.MemoryOverview `json:"overview"`
}

type GetMemoryRequest struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
}

type UpdateMemoryRequest struct {
	PersonaID  string `json:"persona_id,omitempty"`
	Source     string `json:"source,omitempty"`
	SpaceID    string `json:"space_id,omitempty"`
	Scope      string `json:"scope"`
	ID         string `json:"id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Summary    string `json:"summary,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type UpdateMemoryResult struct {
	OK       bool                `json:"ok"`
	Output   string              `json:"output"`
	Memory   app.MemoryDocDetail `json:"memory"`
	Overview app.MemoryOverview  `json:"overview"`
}

type DeleteConversationRequest struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
}

type DeleteConversationResult struct {
	OK              bool `json:"ok"`
	DeletedSpace    bool `json:"deleted_space,omitempty"`
	DeletedSessions int  `json:"deleted_sessions,omitempty"`
	DeletedTasks    int  `json:"deleted_tasks,omitempty"`
}

type ContextInspectRequest struct {
	SpaceID         string `json:"space_id,omitempty"`
	Source          string `json:"source,omitempty"`
	SessionSource   string `json:"session_source,omitempty"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Profile         string `json:"profile,omitempty"`
}

type ContextInspectView struct {
	Profile         string                  `json:"profile"`
	Source          string                  `json:"source,omitempty"`
	SessionSource   string                  `json:"session_source,omitempty"`
	SessionID       string                  `json:"session_id,omitempty"`
	SpaceID         string                  `json:"space_id,omitempty"`
	ParentMessageID string                  `json:"parent_message_id,omitempty"`
	AgentID         string                  `json:"agent_id,omitempty"`
	TokenLimit      int                     `json:"token_limit,omitempty"`
	RawMessageCount int                     `json:"raw_message_count"`
	EligibleCount   int                     `json:"eligible_count"`
	SelectedCount   int                     `json:"selected_count"`
	SummarizedCount int                     `json:"summarized_count"`
	FilteredCounts  []ContextFilteredCount  `json:"filtered_counts,omitempty"`
	Summary         string                  `json:"summary,omitempty"`
	SessionSummary  string                  `json:"session_summary,omitempty"`
	Messages        []ContextInspectMessage `json:"messages"`
	Notes           []string                `json:"notes,omitempty"`
}

type ContextInspectMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	AuthorID  string    `json:"author_id,omitempty"`
	Content   string    `json:"content,omitempty"`
	Tokens    int       `json:"tokens,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ContextFilteredCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type ContextResetRequest struct {
	SpaceID         string `json:"space_id,omitempty"`
	Source          string `json:"source,omitempty"`
	SessionSource   string `json:"session_source,omitempty"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Action          string `json:"action"`
}

type ContextResetResult struct {
	OK                     bool   `json:"ok"`
	Action                 string `json:"action"`
	Source                 string `json:"source,omitempty"`
	SessionSource          string `json:"session_source,omitempty"`
	PreviousSessionID      string `json:"previous_session_id,omitempty"`
	SessionID              string `json:"session_id,omitempty"`
	ClearedSummary         bool   `json:"cleared_summary,omitempty"`
	RemovedSummaryMessages int    `json:"removed_summary_messages,omitempty"`
	Note                   string `json:"note,omitempty"`
}

type BusEvent struct {
	Type            string    `json:"type"`
	Source          string    `json:"source,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	MessageID       string    `json:"message_id,omitempty"`
	ToolCallID      string    `json:"tool_call_id,omitempty"`
	Tool            string    `json:"tool,omitempty"`
	Input           string    `json:"input,omitempty"`
	Output          string    `json:"output,omitempty"`
	Text            string    `json:"text,omitempty"`
	Err             string    `json:"err,omitempty"`
	Time            time.Time `json:"time"`
	SpaceID         string    `json:"space_id,omitempty"`
	ParentMessageID string    `json:"parent_message_id,omitempty"`
	AgentID         string    `json:"agent_id,omitempty"`
	StreamID        string    `json:"stream_id,omitempty"`
}

type ChannelItem struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Topic       string            `json:"topic,omitempty"`
	Agents      []string          `json:"agents"`
	AgentModes  map[string]string `json:"agent_modes,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
	UnreadCount int               `json:"unread_count"`
	HasRunning  bool              `json:"has_running"`
}

type ThreadItem struct {
	ID         string    `json:"id"`
	ChannelID  string    `json:"channel_id"`
	Title      string    `json:"title"`
	UpdatedAt  time.Time `json:"updated_at"`
	EventCount int       `json:"event_count"`
	HasRunning bool      `json:"has_running"`
}

type DirectChatItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind,omitempty"`
	PersonaID   string    `json:"persona_id,omitempty"`
	PersonaName string    `json:"persona_name,omitempty"`
	Title       string    `json:"title"`
	TitleFixed  bool      `json:"title_fixed,omitempty"`
	Agents      []string  `json:"agents"`
	UpdatedAt   time.Time `json:"updated_at"`
	UnreadCount int       `json:"unread_count"`
	HasRunning  bool      `json:"has_running"`
}

type AgentDMItem struct {
	ID           string    `json:"id"`
	PersonaID    string    `json:"persona_id"`
	PersonaName  string    `json:"persona_name,omitempty"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
}

type RecentItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentItem struct {
	ID      string `json:"id"`
	Display string `json:"display"`
	Role    string `json:"role,omitempty"`
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
	Status  string `json:"status"`
}

type ParticipantsView struct {
	Agents            []AgentItem `json:"agents"`
	RunningAgent      string      `json:"running_agent,omitempty"`
	ActiveRuns        []AgentRun  `json:"active_runs,omitempty"`
	RecentRuns        []AgentRun  `json:"recent_runs,omitempty"`
	ArchivedRunsCount int         `json:"archived_runs_count,omitempty"`
}

type AgentRun struct {
	ID                 string    `json:"id"`
	AgentID            string    `json:"agent_id"`
	Title              string    `json:"title"`
	Status             string    `json:"status"`
	Lifecycle          string    `json:"lifecycle"`
	CreatedBy          string    `json:"created_by,omitempty"`
	AssignedBy         string    `json:"assigned_by,omitempty"`
	SpaceID            string    `json:"space_id,omitempty"`
	ThreadID           string    `json:"thread_id,omitempty"`
	TriggerMessageID   string    `json:"trigger_message_id,omitempty"`
	ParentMessageID    string    `json:"parent_message_id,omitempty"`
	ExpectedOutcome    string    `json:"expected_outcome,omitempty"`
	AcceptanceCriteria string    `json:"acceptance_criteria,omitempty"`
	Time               time.Time `json:"time"`
	DurationMs         int64     `json:"duration_ms,omitempty"`
}

type TaskStateView struct {
	Goal       string   `json:"goal,omitempty"`
	Todo       []string `json:"todo,omitempty"`
	Checkpoint string   `json:"checkpoint,omitempty"`
	Artifacts  []string `json:"artifacts,omitempty"`
	Blockers   []string `json:"blockers,omitempty"`
	RelatedIDs []string `json:"related_ids,omitempty"`
}

type CapabilityView struct {
	Skills                 []SkillView          `json:"skills"`
	Tasks                  []TaskStateCard      `json:"tasks"`
	ArchivedTaskStateCount int                  `json:"archived_task_state_count,omitempty"`
	ActionProposals        []ActionProposalCard `json:"action_proposals"`
}

type SkillView struct {
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	When          string         `json:"when,omitempty"`
	Risk          string         `json:"risk,omitempty"`
	Env           []string       `json:"env,omitempty"`
	EnvNeeds      []SkillEnvNeed `json:"env_needs,omitempty"`
	Entrypoints   []string       `json:"entrypoints,omitempty"`
	Examples      []string       `json:"examples,omitempty"`
	Path          string         `json:"path,omitempty"`
	Configured    bool           `json:"configured"`
	MissingEnv    []string       `json:"missing_env,omitempty"`
	LastAction    string         `json:"last_action,omitempty"`
	LastListed    *time.Time     `json:"last_listed,omitempty"`
	LastDescribed *time.Time     `json:"last_described,omitempty"`
	LastUsed      *time.Time     `json:"last_used,omitempty"`
	Body          string         `json:"body,omitempty"`
}

type SkillEnvNeed struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint,omitempty"`
}

type TaskStateCard struct {
	ID                 string        `json:"id"`
	Title              string        `json:"title"`
	Status             string        `json:"status"`
	Lifecycle          string        `json:"lifecycle"`
	CreatedBy          string        `json:"created_by,omitempty"`
	WorkerID           string        `json:"worker_id,omitempty"`
	AssigneeID         string        `json:"assignee_id,omitempty"`
	Assignee           string        `json:"assignee,omitempty"`
	AssignedBy         string        `json:"assigned_by,omitempty"`
	SpaceID            string        `json:"space_id,omitempty"`
	Source             string        `json:"source,omitempty"`
	SourceMessageID    string        `json:"source_message,omitempty"`
	SourceThreadID     string        `json:"source_thread_id,omitempty"`
	SourceThread       string        `json:"source_thread,omitempty"`
	TriggerMessageID   string        `json:"trigger_message_id,omitempty"`
	ParentMessageID    string        `json:"parent_message_id,omitempty"`
	UpdatedAt          time.Time     `json:"updated_at"`
	ExpectedOutcome    string        `json:"expected_outcome,omitempty"`
	AcceptanceCriteria string        `json:"acceptance_criteria,omitempty"`
	Outcome            string        `json:"outcome,omitempty"`
	State              TaskStateView `json:"state,omitempty"`
	LatestRun          string        `json:"latest_run,omitempty"`
	RunStatus          string        `json:"run_status,omitempty"`
	RunStarted         time.Time     `json:"run_started,omitempty"`
}

type ActionProposalCard struct {
	Time      time.Time `json:"time"`
	Source    string    `json:"source,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Result    string    `json:"result,omitempty"`
	Intent    string    `json:"intent,omitempty"`
	Target    string    `json:"target,omitempty"`
	Risk      string    `json:"risk,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	Rollback  string    `json:"rollback,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}
