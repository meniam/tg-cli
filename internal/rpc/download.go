package rpc

type DownloadParams struct {
	Chat   string `json:"chat"`
	IDs    []int  `json:"ids"`
	Output string `json:"output"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type DownloadedFile struct {
	MessageID int    `json:"message_id"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	MIME      string `json:"mime" untrusted:"true"`
	FileName  string `json:"file_name,omitempty" untrusted:"true"`
	SHA256    string `json:"sha256,omitempty"`
}
type DownloadResult struct {
	Batch
	ChatID int64            `json:"chat_id"`
	Files  []DownloadedFile `json:"files"`
	DryRun bool             `json:"dry_run,omitempty"`
}
