package rpc

import (
	"time"
)

const MethodMessageExportMonth = "message.export_month"
const MethodMessageExportVerify = "message.export_verify"

type ExportMonthParams struct {
	Chats          []string `json:"chats"`
	Month          string   `json:"month"`
	Output         string   `json:"output"`
	IncludeDeleted bool     `json:"include_deleted"`
}
type ExportVerifyParams struct {
	Dir string `json:"dir"`
}
type ExportSync struct {
	Complete          bool `json:"complete"`
	OldestID          *int `json:"oldest_id"`
	NewestID          *int `json:"newest_id"`
	MonthStartCovered bool `json:"month_start_covered"`
}
type ManifestFile struct {
	Name     string     `json:"name"`
	ChatID   int64      `json:"chat_id"`
	Messages int        `json:"messages"`
	Bytes    int64      `json:"bytes"`
	SHA256   string     `json:"sha256"`
	FirstID  int        `json:"first_id"`
	LastID   int        `json:"last_id"`
	Sync     ExportSync `json:"sync"`
}
type Manifest struct {
	Format         string         `json:"format"`
	Version        int            `json:"version"`
	AccountID      int64          `json:"account_id"`
	Month          string         `json:"month"`
	Since          string         `json:"since"`
	Until          string         `json:"until"`
	IncludeDeleted bool           `json:"include_deleted"`
	CreatedAt      string         `json:"created_at"`
	ToolVersion    string         `json:"tool_version"`
	Files          []ManifestFile `json:"files"`
}
type ExportMonthFile struct {
	Name              string `json:"name"`
	ChatID            int64  `json:"chat_id"`
	Messages          int    `json:"messages"`
	Bytes             int64  `json:"bytes"`
	SHA256            string `json:"sha256"`
	MonthStartCovered bool   `json:"month_start_covered"`
}
type ExportMonthResult struct {
	Dir            string            `json:"dir"`
	Month          string            `json:"month"`
	Existing       bool              `json:"existing"`
	ManifestSHA256 string            `json:"manifest_sha256"`
	Files          []ExportMonthFile `json:"files"`
}
type ExportVerifyFile struct {
	Name           string `json:"name"`
	OK             bool   `json:"ok"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualSHA256   string `json:"actual_sha256"`
	Bytes          int64  `json:"bytes"`
}
type ExportVerifyResult struct {
	Dir     string             `json:"dir"`
	Month   string             `json:"month"`
	OK      bool               `json:"ok"`
	Files   []ExportVerifyFile `json:"files"`
	Missing []string           `json:"missing"`
	Extra   []string           `json:"extra"`
}

func ParseExportMonth(month string, now time.Time) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01", month)
	if err != nil || start.Format("2006-01") != month || !start.AddDate(0, 1, 0).After(start) || start.AddDate(0, 1, 0).After(now.UTC()) {
		return time.Time{}, time.Time{}, NewError(CodeInvalidParams, "month must be a completed UTC month in YYYY-MM format")
	}
	return start, start.AddDate(0, 1, 0), nil
}
