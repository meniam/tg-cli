package cli

import (
	"context"
	"io"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (f *fakeTelegram) Download(context.Context, chatref.Ref, []int, func(rpc.DownloadedFile, func(io.Writer) error) error) (int64, []rpc.ItemError, error) {
	return 0, nil, rpc.NewError(rpc.CodeTelegram, "download fixture is not configured")
}
