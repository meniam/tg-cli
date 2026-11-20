package rpc

// Reasons the vector leg of a semantic search did not take part. The text of
// the query and of the messages is never part of a reason.
const (
	// VectorSkippedDisabled: [semantic_search] is off.
	VectorSkippedDisabled = "disabled"
	// VectorSkippedUnavailable: Ollama did not answer in time.
	VectorSkippedUnavailable = "unavailable"
	// VectorSkippedModelMissing: Ollama has no such model.
	VectorSkippedModelMissing = "model_missing"
	// VectorSkippedIndexEmpty: no fragment is indexed with the current model.
	VectorSkippedIndexEmpty = "index_empty"
	// VectorSkippedError: any other failure of the vector leg.
	VectorSkippedError = "error"
)

// Sources of a fragment: the legs that found it.
const (
	SourceExact  = "exact"
	SourceStem   = "stem"
	SourceVector = "vector"
)

// SemanticSearchParams selects a hybrid search over conversation fragments.
type SemanticSearchParams struct {
	Query  string `json:"query"`
	Chat   string `json:"chat,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Prefix bool   `json:"prefix,omitempty"`
	// MinSimilarity overrides [semantic_search] min_similarity for this query: a
	// cosine from 0 to 1, 0 turns the threshold off, nil takes the setting.
	MinSimilarity *float64 `json:"min_similarity,omitempty"`
}

// SemanticFragment is a stretch of one conversation found by exact words,
// stemmed words, vector similarity, or several of them.
type SemanticFragment struct {
	ChatID int64 `json:"chat_id"`
	// TopicID is the forum topic of the fragment, 0 if the chat has none.
	TopicID int64 `json:"topic_id"`
	// FirstID and LastID bound the message ids of the fragment.
	FirstID int64 `json:"first_id"`
	LastID  int64 `json:"last_id"`
	// StartsAt and EndsAt are RFC 3339 UTC.
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
	// Score is the Reciprocal Rank Fusion score; higher is better.
	Score float64 `json:"score"`
	// Sources lists the legs that found the fragment: exact, stem, vector.
	Sources []string `json:"sources"`
	// Indexed is false for a fragment made of one message the vector index does
	// not cover (a short fragment), true for a stored fragment.
	Indexed bool `json:"indexed"`
	// Text is the live messages of the fragment, one per line, at most 1500
	// characters.
	Text string `json:"text" untrusted:"true"`
	// Link is the t.me link of the first message, built locally; null if the chat
	// has none, and LinkUnavailable then holds private_chat or basic_group, or
	// nothing if the chat is unknown.
	Link *string `json:"link"`
	// MatchLink is the link of the message the word legs ranked best; null if
	// only the vector leg found the fragment.
	MatchLink       *string `json:"match_link"`
	LinkUnavailable string  `json:"link_unavailable,omitempty"`
	// ChatTitle is the title of the chat, empty if the database does not know
	// the chat; ChatUsername is null when the chat has no username. Both come
	// from the chats table and are text of Telegram.
	ChatTitle    string  `json:"chat_title" untrusted:"true"`
	ChatUsername *string `json:"chat_username" untrusted:"true"`
}

// SemanticSearchResult is the answer of search.semantic.
type SemanticSearchResult struct {
	Fragments []SemanticFragment `json:"fragments"`
	// VectorSkipped names why the vector leg did not take part; the fragments
	// then come from the word legs alone. Absent when the vector leg ran.
	VectorSkipped string `json:"vector_skipped,omitempty"`
	// VectorDropped is the number of vector hits left out because their cosine
	// similarity was below the threshold. 0 when nothing was dropped or the
	// vector leg did not run.
	VectorDropped int `json:"vector_dropped"`
}
