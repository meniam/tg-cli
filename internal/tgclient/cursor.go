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

type topicsCursor struct {
	V     int   `json:"v"`
	Chat  int64 `json:"chat"`
	Date  int   `json:"date"`
	ID    int   `json:"id"`
	Topic int   `json:"topic"`
}

type commonChatsCursor struct {
	V     int   `json:"v"`
	User  int64 `json:"user"`
	MaxID int64 `json:"max_id"`
}

func encodeOpaqueCursor(value any) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeOpaqueCursor(value string, target any) error {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(data, target) != nil {
		return invalidCursor()
	}
	return nil
}

func encodeTopicsCursor(value topicsCursor) string {
	value.V = cursorVersion
	return encodeOpaqueCursor(value)
}

func decodeTopicsCursor(value string, chat int64) (topicsCursor, error) {
	if value == "" {
		return topicsCursor{}, nil
	}
	var cursor topicsCursor
	if err := decodeOpaqueCursor(value, &cursor); err != nil || cursor.V != cursorVersion || cursor.Date <= 0 || cursor.ID <= 0 || cursor.Topic <= 0 {
		return topicsCursor{}, invalidCursor()
	}
	if cursor.Chat != chat {
		return topicsCursor{}, rpc.NewError(rpc.CodeInvalidParams, "the cursor belongs to another chat")
	}
	return cursor, nil
}

func encodeCommonChatsCursor(value commonChatsCursor) string {
	value.V = cursorVersion
	return encodeOpaqueCursor(value)
}

func decodeCommonChatsCursor(value string, user int64) (commonChatsCursor, error) {
	if value == "" {
		return commonChatsCursor{}, nil
	}
	var cursor commonChatsCursor
	if err := decodeOpaqueCursor(value, &cursor); err != nil || cursor.V != cursorVersion || cursor.MaxID <= 0 {
		return commonChatsCursor{}, invalidCursor()
	}
	if cursor.User != user {
		return commonChatsCursor{}, rpc.NewError(rpc.CodeInvalidParams, "the cursor belongs to another user")
	}
	return cursor, nil
}

// encodeCursor makes the opaque string a client passes back to get the page
// after the message with the given id.
func encodeCursor(chat int64, id int) string {
	return encodeOpaqueCursor(cursorBody{V: cursorVersion, Chat: chat, ID: id})
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
