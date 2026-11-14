package rpc

// SearchMessagesParams searches one Telegram chat, independent of local search.
type SearchMessagesParams struct {
	Chat   string `json:"chat"`
	Query  string `json:"query"`
	Media  string `json:"media,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type PublicSearchParams struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type PublicHit struct {
	ChatID  int64   `json:"chat_id"`
	Chat    *Chat   `json:"chat,omitempty"`
	Message Message `json:"message"`
}

type PublicSearchResult struct {
	Hits       []PublicHit `json:"hits"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type ContextParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	Before int    `json:"before,omitempty"`
	After  int    `json:"after,omitempty"`
	Local  bool   `json:"local,omitempty"`
}

type CommentsParams struct {
	Chat   string `json:"chat"`
	ID     int    `json:"id"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type LinkParams struct {
	Chat  string `json:"chat"`
	ID    int    `json:"id"`
	Topic int    `json:"topic,omitempty"`
}

type LinkResult struct {
	Link string `json:"link"`
}

type MarkReadParams struct {
	Chat   string `json:"chat"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type MarkReadResult struct {
	ChatID int64 `json:"chat_id"`
	DryRun bool  `json:"dry_run,omitempty"`
}
