package rpc

const (
	MethodWebFolders = "web.folders"
	MethodWebHistory = "web.history"
	MethodWebContext = "web.context"
)

const (
	MethodWebChat            = "web.chat"
	MethodWebAccess          = "web.access"
	MethodWebChatSettings    = "web.chat_settings"
	MethodWebSetChatSettings = "web.set_chat_settings"
	MethodWebThumbEnabled    = "web.thumb_enabled"
	MethodMediaThumb         = "media.thumb"
)

type WebAccessResult struct {
	Closed    bool     `json:"closed"`
	Allow     int      `json:"allow"`
	Block     int      `json:"block"`
	Write     int      `json:"write"`
	Admin     int      `json:"admin"`
	Dialogs   bool     `json:"dialogs"`
	Sessions  bool     `json:"sessions"`
	Create    bool     `json:"create"`
	Profile   bool     `json:"profile"`
	Files     []string `json:"files"`
	Downloads []string `json:"downloads"`
}

type WebChatSettings struct {
	Chat        string `json:"chat"`
	HistoryMode string `json:"history_mode,omitempty"`
}

type WebEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url,omitempty" untrusted:"true"`
}

type WebMessage struct {
	Message
	Entities  []WebEntity     `json:"entities,omitempty"`
	Forwarded bool            `json:"forwarded,omitempty"`
	Reactions []ReactionCount `json:"reactions,omitempty"`
	MediaName string          `json:"media_name,omitempty" untrusted:"true"`
	MediaSize int64           `json:"media_size,omitempty"`
	Link      *string         `json:"link,omitempty"`
}

type WebMessages struct {
	Messages   []WebMessage `json:"messages"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type WebTopic struct {
	ID    int    `json:"id"`
	Title string `json:"title" untrusted:"true"`
}

type WebChat struct {
	Title    string     `json:"title" untrusted:"true"`
	Username *string    `json:"username,omitempty" untrusted:"true"`
	About    *string    `json:"about,omitempty" untrusted:"true"`
	Members  *int64     `json:"member_count,omitempty"`
	Topics   []WebTopic `json:"topics,omitempty"`
	Pinned   []Message  `json:"pinned,omitempty"`
}

type WebThumbnailParams struct {
	Chat    string `json:"chat"`
	ID      int    `json:"id"`
	Preview bool   `json:"preview,omitempty"`
}

type WebThumbnail struct {
	Bytes []byte `json:"bytes"`
}
