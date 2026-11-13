package tgclient

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"tg-cli/internal/rpc"
)

// cursorVersion is stored inside every cursor, so the format can change later
// without a cursor of the old format being misread.
const cursorVersion = 1

type cursorBody struct {
	V    int   `json:"v"`
	Chat int64 `json:"c"`
	ID   int   `json:"i"`
}

// encodeCursor makes the opaque string a client passes back to get the page
// after the message with the given id.
func encodeCursor(chat int64, id int) string {
	data, _ := json.Marshal(cursorBody{V: cursorVersion, Chat: chat, ID: id})
	return base64.RawURLEncoding.EncodeToString(data)
}

// decodeCursor returns the id of the last message of the previous page. The
// cursor of another chat or of an unknown format is rejected.
func decodeCursor(cursor string, chat int64) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, invalidCursor()
	}
	var body cursorBody
	if err := json.Unmarshal(data, &body); err != nil || body.V != cursorVersion || body.ID <= 0 {
		return 0, invalidCursor()
	}
	if body.Chat != chat {
		return 0, rpc.NewError(rpc.CodeInvalidParams, "the cursor belongs to another chat")
	}
	return body.ID, nil
}

func invalidCursor() error {
	return rpc.NewError(rpc.CodeInvalidParams, fmt.Sprint(errors.New("the cursor is not valid")))
}
