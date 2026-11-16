package tgclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gotd/td/telegram/message/entity"
	messagehtml "github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/markdown"
	"github.com/gotd/td/telegram/message/rich"
	"github.com/gotd/td/tg"
	"golang.org/x/net/html"

	"tg-cli/internal/rpc"
)

// CheckTextLocal checks rendered text and entities without resolving users remotely.
func (c *Client) CheckTextLocal(p rpc.SendParams) error {
	_, err := prepareWriteText(p.Text, p.Parse, p.Entities, func(id int64) (tg.InputUserClass, error) {
		return &tg.InputUser{UserID: id}, nil
	})
	return err
}

// CheckText validates formatting without sending a message to Telegram.
func (c *Client) CheckText(ctx context.Context, p rpc.SendParams) (rpc.TextCheck, error) {
	prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, c.mentionResolver(ctx))
	if err != nil {
		return rpc.TextCheck{}, err
	}
	mode := p.Parse
	if p.Entities != nil {
		mode = "entities"
	}
	if mode == "" {
		mode = "plain"
	}
	return rpc.TextCheck{Mode: mode, PlainLength: utf8.RuneCountInString(prepared.text), Entities: len(prepared.entities), Rich: prepared.rich != nil}, nil
}

type userResolver func(int64) (tg.InputUserClass, error)

type preparedText struct {
	text     string
	entities []tg.MessageEntityClass
	rich     tg.InputRichMessageClass
}

func invalidFormat(message string) error {
	return rpc.NewError(rpc.CodeInvalidParams, message)
}

func prepareWriteText(source, parse string, explicit []rpc.Entity, resolve userResolver) (preparedText, error) {
	if parse != "" && explicit != nil {
		return preparedText{}, invalidFormat("parse and entities cannot be used together")
	}
	if parse == "rich-html" || parse == "rich-markdown" {
		if strings.TrimSpace(source) == "" {
			return preparedText{}, invalidFormat("message text must not be empty")
		}
		// Telegram does not publish a separate RichMessage limit yet.
		if utf8.RuneCountInString(source) > maxMessageRunes {
			return preparedText{}, invalidFormat("rich message source must be at most 4096 characters")
		}
		if parse == "rich-html" {
			return preparedText{rich: rich.HTML(source)}, nil
		}
		return preparedText{rich: rich.Markdown(source)}, nil
	}
	var result preparedText
	var mentionErr error
	strictResolver := func(id int64) (tg.InputUserClass, error) {
		user, err := resolveUser(resolve, id)
		if err != nil {
			mentionErr = err
		}
		return user, err
	}
	switch parse {
	case "":
		result.text = source
	case "html":
		if err := validateHTMLLinks(source, resolve); err != nil {
			return preparedText{}, err
		}
		var b entity.Builder
		if err := messagehtml.HTML(strings.NewReader(source), &b, messagehtml.Options{UserResolver: strictResolver}); err != nil {
			if mentionErr != nil {
				return preparedText{}, mentionErr
			}
			return preparedText{}, invalidFormat("invalid HTML formatting")
		}
		result.text, result.entities = b.Complete()
	case "markdown":
		var b entity.Builder
		if err := markdown.Markdown(strings.NewReader(source), &b, markdown.Options{UserResolver: strictResolver}); err != nil {
			if mentionErr != nil {
				return preparedText{}, mentionErr
			}
			return preparedText{}, invalidFormat("invalid Markdown formatting or user mention")
		}
		result.text, result.entities = b.Complete()
	default:
		return preparedText{}, invalidFormat("unknown parse mode")
	}
	if err := validateWriteText(result.text); err != nil {
		return preparedText{}, err
	}
	if explicit != nil {
		entities, err := makeEntities(result.text, explicit, resolve)
		if err != nil {
			return preparedText{}, err
		}
		result.entities = entities
	}
	return result, nil
}

// gotd's HTML parser discards malformed links and resolver errors. Check them
// before it runs, so a requested link cannot silently become plain text.
func validateHTMLLinks(source string, resolve userResolver) error {
	t := html.NewTokenizer(strings.NewReader(source))
	var stack []string
	position := 0
	for {
		token := t.Next()
		position += len(t.Raw())
		switch token {
		case html.ErrorToken:
			if err := t.Err(); err != nil && err != io.EOF {
				return invalidFormat(fmt.Sprintf("invalid HTML formatting at byte %d", position))
			}
			if len(stack) > 0 {
				return invalidFormat(fmt.Sprintf("unclosed HTML tag at byte %d", position))
			}
			return nil
		case html.EndTagToken:
			name, _ := t.TagName()
			if !formattingTag(string(name)) {
				continue
			}
			if len(stack) == 0 || stack[len(stack)-1] != string(name) {
				return invalidFormat(fmt.Sprintf("unmatched HTML tag at byte %d", position))
			}
			stack = stack[:len(stack)-1]
		case html.StartTagToken:
			name, attrs := t.TagName()
			if formattingTag(string(name)) {
				stack = append(stack, string(name))
			}
			if string(name) != "a" {
				continue
			}
			if !attrs {
				return invalidFormat(fmt.Sprintf("HTML link needs href at byte %d", position))
			}
			foundHref := false
			for {
				key, value, more := t.TagAttr()
				if string(key) == "href" {
					foundHref = true
					if err := validateLink(string(value), resolve); err != nil {
						if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							return err
						}
						var timeout net.Error
						if errors.As(err, &timeout) && timeout.Timeout() {
							return err
						}
						var rpcErr *rpc.Error
						if errors.As(err, &rpcErr) && (rpcErr.Message == "unknown user id for this session" || rpcErr.Code != rpc.CodeInvalidParams) {
							return rpcErr
						}
						return invalidFormat(fmt.Sprintf("invalid HTML link at byte %d: %v", position, err))
					}
				}
				if !more {
					break
				}
			}
			if !foundHref {
				return invalidFormat(fmt.Sprintf("HTML link needs href at byte %d", position))
			}
		}
	}
}

func formattingTag(name string) bool {
	switch name {
	case "b", "strong", "i", "em", "u", "ins", "s", "strike", "del", "a", "code", "pre", "span", "tg-spoiler", "tg-emoji", "blockquote", "tg-time":
		return true
	}
	return false
}

func validateLink(raw string, resolve userResolver) error {
	if strings.ContainsAny(raw, " \t\r\n") {
		return invalidFormat("invalid link URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return invalidFormat("invalid link URL")
	}
	if u.Scheme == "tg" && u.Host == "user" {
		id, err := strconv.ParseInt(u.Query().Get("id"), 10, 64)
		if err != nil || id <= 0 {
			return invalidFormat("invalid user id in link")
		}
		_, err = resolveUser(resolve, id)
		return err
	}
	if u.Scheme == "" && raw != "" {
		// gotd treats a bare host as an http URL.
		if strings.ContainsAny(raw, "<>\"") {
			return invalidFormat("invalid link URL")
		}
		return nil
	}
	if (u.Scheme == "mailto" || u.Scheme == "tel") && u.Opaque != "" {
		return nil
	}
	if u.Host == "" {
		return invalidFormat("invalid link URL")
	}
	return nil
}

func resolveUser(resolve userResolver, id int64) (tg.InputUserClass, error) {
	if resolve == nil || id <= 0 {
		return nil, invalidFormat("unknown user id for this session")
	}
	user, err := resolve(id)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, err
		}
		var rpcErr *rpc.Error
		if errors.As(err, &rpcErr) && rpcErr.Code != rpc.CodeNotFound && rpcErr.Code != rpc.CodeInvalidParams {
			return nil, err
		}
		return nil, invalidFormat("unknown user id for this session")
	}
	if user == nil {
		return nil, invalidFormat("unknown user id for this session")
	}
	return user, nil
}

func makeEntities(text string, input []rpc.Entity, resolve userResolver) ([]tg.MessageEntityClass, error) {
	boundaries := map[int]bool{0: true}
	units := 0
	for _, r := range text {
		if r > 0xffff {
			units += 2
		} else {
			units++
		}
		boundaries[units] = true
	}
	result := make([]tg.MessageEntityClass, 0, len(input))
	for i, item := range input {
		end := item.Offset + item.Length
		if item.Offset < 0 || item.Length <= 0 || end < item.Offset || end > units || !boundaries[item.Offset] || !boundaries[end] {
			return nil, invalidFormat(fmt.Sprintf("entity %d has invalid UTF-16 range", i))
		}
		f, err := entityFormatter(item, resolve)
		if err != nil {
			return nil, err
		}
		result = append(result, f(item.Offset, item.Length))
	}
	return result, nil
}

func entityFormatter(item rpc.Entity, resolve userResolver) (entity.Formatter, error) {
	switch item.Type {
	case "bold":
		return entity.Bold(), nil
	case "italic":
		return entity.Italic(), nil
	case "underline":
		return entity.Underline(), nil
	case "strikethrough":
		return entity.Strike(), nil
	case "spoiler":
		return entity.Spoiler(), nil
	case "code":
		return entity.Code(), nil
	case "pre":
		return entity.Pre(item.Language), nil
	case "blockquote":
		return entity.Blockquote(item.Collapsed), nil
	case "url":
		return entity.URL(), nil
	case "email":
		return entity.Email(), nil
	case "phone":
		return entity.Phone(), nil
	case "mention":
		return entity.Mention(), nil
	case "hashtag":
		return entity.Hashtag(), nil
	case "cashtag":
		return entity.Cashtag(), nil
	case "bot_command":
		return entity.BotCommand(), nil
	case "text_url":
		if item.URL == "" {
			return nil, invalidFormat("text_url requires url")
		}
		if err := validateLink(item.URL, resolve); err != nil {
			return nil, err
		}
		return entity.TextURL(item.URL), nil
	case "mention_name":
		user, err := resolveUser(resolve, item.UserID)
		if err != nil {
			return nil, err
		}
		return entity.MentionName(user), nil
	case "custom_emoji":
		if item.CustomEmojiID <= 0 {
			return nil, invalidFormat("custom_emoji requires custom_emoji_id")
		}
		return entity.CustomEmoji(item.CustomEmojiID), nil
	case "date_time":
		f, err := entity.FormattedDateFormat(item.Format, item.DateUnix)
		if err != nil {
			return nil, invalidFormat("invalid date_time format")
		}
		return f, nil
	default:
		return nil, invalidFormat("unknown entity type")
	}
}
