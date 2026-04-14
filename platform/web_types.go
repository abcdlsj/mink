package platform

type WebNavItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}

type WebIndexItem struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Label   string `json:"label"`
	Meta    string `json:"meta,omitempty"`
	Active  bool   `json:"active"`
}

type WebIndexGroup struct {
	Title string         `json:"title"`
	Items []WebIndexItem `json:"items"`
}

type WebMessage struct {
	Role        string          `json:"role"`
	Sender      string          `json:"sender"`
	Descriptor  string          `json:"descriptor,omitempty"`
	Time        string          `json:"time,omitempty"`
	Content     string          `json:"content,omitempty"`
	Reasoning   string          `json:"reasoning,omitempty"`
	ToolCalls   []WebToolCall   `json:"toolCalls,omitempty"`
	ToolResults []WebToolResult `json:"toolResults,omitempty"`
}

type WebToolCall struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

type WebToolResult struct {
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

type WebCard struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Meta     string `json:"meta,omitempty"`
}

type WebContextBlock struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type WebState struct {
	Workspace           string            `json:"workspace"`
	Section             string            `json:"section"`
	Nav                 []WebNavItem      `json:"nav"`
	IndexTitle          string            `json:"indexTitle"`
	IndexGroups         []WebIndexGroup   `json:"indexGroups"`
	IndexAction         string            `json:"indexAction,omitempty"`
	IndexActionLabel    string            `json:"indexActionLabel,omitempty"`
	HeaderTitle         string            `json:"headerTitle"`
	HeaderSubtitle      string            `json:"headerSubtitle,omitempty"`
	HeaderMeta          []string          `json:"headerMeta,omitempty"`
	Messages            []WebMessage      `json:"messages,omitempty"`
	Cards               []WebCard         `json:"cards,omitempty"`
	ContextTitle        string            `json:"contextTitle,omitempty"`
	ContextBlocks       []WebContextBlock `json:"contextBlocks,omitempty"`
	ComposerLabel       string            `json:"composerLabel"`
	ComposerPlaceholder string            `json:"composerPlaceholder"`
	ComposerDisabled    bool              `json:"composerDisabled"`
	EmptyHint           string            `json:"emptyHint,omitempty"`
}

type WebCallbacks struct {
	State       func() (WebState, error)
	Select      func(section, id string) error
	SendMessage func(text string) error
	NewSession  func() error
	Action      func(name string) error
}
