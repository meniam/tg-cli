package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tg-cli/internal/redact"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

const saveTimeout = 10 * time.Second

// save keeps storage failures and request cancellation out of read responses.
func (d *daemon) save(ctx context.Context, op string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	if err := fn(ctx); err != nil {
		d.log.Warn("local storage failed", "op", op, "err", redact.Text(err.Error()))
	}
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func chatPerson(chat rpc.Chat) (store.Person, bool) {
	if chat.ID <= 0 || (chat.Type != "user" && chat.Type != "bot") {
		return store.Person{}, false
	}
	isBot := chat.Type == "bot"
	return store.Person{ID: chat.ID, Username: optionalText(chat.Username), DisplayName: optionalText(chat.Title), IsBot: &isBot}, true
}

func (d *daemon) saveChats(ctx context.Context, op string, chats []rpc.Chat, includeUnread bool) {
	if len(chats) == 0 {
		return
	}
	stored := make([]store.Chat, 0, len(chats))
	people := make([]store.Person, 0, len(chats))
	for _, chat := range chats {
		item := store.Chat{ID: chat.ID, Type: chat.Type, Title: chat.Title, Username: optionalText(chat.Username)}
		if includeUnread {
			unread := int64(chat.Unread)
			item.Unread = &unread
		}
		stored = append(stored, item)
		if person, ok := chatPerson(chat); ok {
			people = append(people, person)
		}
	}
	d.save(ctx, op, func(ctx context.Context) error {
		if err := d.store.UpsertChats(ctx, stored); err != nil {
			return err
		}
		if len(people) > 0 {
			return d.store.UpsertPeople(ctx, people)
		}
		return nil
	})
}

func (d *daemon) savePerson(ctx context.Context, op string, person store.Person) {
	d.save(ctx, op, func(ctx context.Context) error {
		return d.store.UpsertPeople(ctx, []store.Person{person})
	})
}

func (d *daemon) saveMembers(ctx context.Context, members []rpc.Member) {
	if len(members) == 0 {
		return
	}
	people := make([]store.Person, 0, len(members))
	for _, member := range members {
		if member.ID <= 0 {
			continue
		}
		isBot := member.IsBot
		people = append(people, store.Person{
			ID: member.ID, Username: optionalText(member.Username),
			DisplayName: optionalText(member.Name), IsBot: &isBot,
		})
	}
	if len(people) == 0 {
		return
	}
	d.save(ctx, rpc.MethodChatMembers, func(ctx context.Context) error {
		return d.store.UpsertPeople(ctx, people)
	})
}

func (d *daemon) saveChatInfo(ctx context.Context, info rpc.ChatInfo) {
	item := store.Chat{
		ID: info.ID, Type: info.Type, Title: info.Title, Username: optionalText(info.Username),
		About: optionalText(info.About), Members: positiveIntPointer(info.Members), // 0 means unknown
		Forum: boolPointer(info.Forum), Verified: boolPointer(info.Verified), Settings: info.Settings,
	}
	d.save(ctx, rpc.MethodChatInfo, func(ctx context.Context) error {
		return d.store.UpsertChats(ctx, []store.Chat{item})
	})
}

func int64Pointer(value int64) *int64 { return &value }
func boolPointer(value bool) *bool    { return &value }

func (d *daemon) saveMessages(ctx context.Context, op string, messages []rpc.Message) {
	if len(messages) == 0 {
		return
	}
	d.save(ctx, op, func(ctx context.Context) error {
		return d.persistMessages(ctx, op, messages)
	})
}

func (d *daemon) persistMessages(ctx context.Context, op string, messages []rpc.Message) error {
	stored, people, skipped, err := convertStoredMessages(messages, false)
	if err != nil {
		return err
	}
	if skipped > 0 {
		d.log.Warn("messages skipped with invalid dates", "op", op, "count", skipped)
	}
	if len(stored) > 0 {
		if err := d.store.UpsertMessages(ctx, stored); err != nil {
			return err
		}
	}
	if len(people) > 0 {
		return d.store.UpsertPeople(ctx, people)
	}
	return nil
}

// convertStoredMessages keeps the ordinary best-effort read path unchanged;
// strict history sync must fail rather than advance past an invalid message.
func convertStoredMessages(messages []rpc.Message, strict bool) ([]store.Message, []store.Person, int, error) {
	stored := make([]store.Message, 0, len(messages))
	people := make([]store.Person, 0)
	personIndex := make(map[int64]int)
	skipped := 0
	for _, message := range messages {
		date, err := time.Parse(time.RFC3339, message.Date)
		if err != nil {
			if strict {
				return nil, nil, 0, fmt.Errorf("invalid message date for %d/%d: %w", message.ChatID, message.ID, err)
			}
			skipped++
			continue
		}
		raw, err := json.Marshal(message)
		if err != nil {
			return nil, nil, 0, err
		}
		item := store.Message{
			ChatID: message.ChatID, ID: message.ID, Date: date.Unix(),
			Text: message.Text, Out: boolPointer(message.Out), Pinned: boolPointer(message.Pinned),
			ReplyTo: positiveIntPointer(message.ReplyTo), TopicID: positiveIntPointer(message.TopicID),
			MediaType: optionalText(message.MediaType), Service: optionalText(message.Service), Raw: raw,
		}
		if message.Sender != nil {
			if message.Sender.ID != 0 {
				item.SenderID = int64Pointer(message.Sender.ID)
			}
			item.SenderName = optionalText(message.Sender.Name)
			if message.Sender.ID > 0 {
				if index, exists := personIndex[message.Sender.ID]; exists {
					if message.Sender.Username != "" {
						people[index].Username = optionalText(message.Sender.Username)
					}
					if message.Sender.Name != "" {
						people[index].DisplayName = optionalText(message.Sender.Name)
					}
				} else {
					personIndex[message.Sender.ID] = len(people)
					people = append(people, store.Person{
						ID: message.Sender.ID, Username: optionalText(message.Sender.Username),
						DisplayName: optionalText(message.Sender.Name),
					})
				}
			}
		}
		stored = append(stored, item)
	}
	return stored, people, skipped, nil
}

func positiveIntPointer(value int) *int64 {
	if value <= 0 {
		return nil
	}
	return int64Pointer(int64(value))
}
