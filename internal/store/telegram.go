package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"tg-cli/internal/textnorm"
)

// ErrNotFound means a requested chat, person, or message is absent.
var ErrNotFound = errors.New("not found")

// Chat contains locally stored Telegram chat data. Nil optional fields mean
// the source did not supply them; SearchText and timestamps are store-owned.
type Chat struct {
	ID         int64
	Type       string
	Title      string
	Username   *string
	About      *string
	Members    *int64
	Forum      *bool
	Verified   *bool
	Unread     *int64
	Settings   json.RawMessage
	Raw        json.RawMessage
	SearchText string
	// CreatedAt is the creation date of the chat in Unix seconds.
	CreatedAt *int64
	// Scam, Fake and Restricted are the flags Telegram sets on a chat. The
	// columns are NOT NULL with a default of 0, so false means "no flag", and the
	// latest read wins.
	Scam       bool
	Fake       bool
	Restricted bool
	// RestrictionReason explains a restriction as JSON: platform, reason, text.
	RestrictionReason json.RawMessage
	// FullSyncedAt and FullError belong to the full-data job of the chat.
	FullSyncedAt *int64
	FullError    *string
	FirstSeenAt  int64
	LastSeenAt   int64
}

// Person contains locally stored Telegram person data. Nil optional fields mean
// the source did not supply them; SearchText and timestamps are store-owned.
type Person struct {
	ID          int64
	Username    *string
	FirstName   *string
	DisplayName *string
	LastName    *string
	Phone       *string
	Bio         *string
	IsBot       *bool
	IsContact   *bool
	Raw         json.RawMessage
	SearchText  string
	FirstSeenAt int64
	LastSeenAt  int64
}

// Message contains locally stored Telegram message data. Raw must be valid JSON.
// SearchText, DeletedAt, and timestamps are store-owned.
type Message struct {
	ChatID      int64
	ID          int
	Date        int64
	SenderID    *int64
	SenderName  *string
	Text        string
	Out         *bool
	ReplyTo     *int64
	TopicID     *int64
	MediaType   *string
	EditedAt    *int64
	Pinned      *bool
	Service     *string
	Raw         json.RawMessage
	SearchText  string
	DeletedAt   *int64
	FirstSeenAt int64
	LastSeenAt  int64
}

// MessageVersion preserves a message before a text change.
type MessageVersion struct {
	ChatID    int64
	MessageID int
	Version   int
	Text      string
	EditedAt  *int64
	Raw       json.RawMessage
	SavedAt   int64
}

type rowScanner interface{ Scan(...any) error }

func scanChat(row rowScanner) (Chat, error) {
	var c Chat
	var settings, raw, restriction sql.NullString
	err := row.Scan(&c.ID, &c.Type, &c.Title, &c.Username, &c.About, &c.Members, &c.Forum, &c.Verified, &c.Unread, &settings, &raw, &c.SearchText,
		&c.CreatedAt, &c.Scam, &c.Fake, &c.Restricted, &restriction, &c.FullSyncedAt, &c.FullError, &c.FirstSeenAt, &c.LastSeenAt)
	if settings.Valid {
		c.Settings = json.RawMessage(settings.String)
	}
	if raw.Valid {
		c.Raw = json.RawMessage(raw.String)
	}
	if restriction.Valid {
		c.RestrictionReason = json.RawMessage(restriction.String)
	}
	return c, err
}

func scanPerson(row rowScanner) (Person, error) {
	var p Person
	var raw sql.NullString
	err := row.Scan(&p.ID, &p.Username, &p.FirstName, &p.LastName, &p.Phone, &p.Bio, &p.IsBot, &p.IsContact, &raw, &p.SearchText, &p.FirstSeenAt, &p.LastSeenAt)
	if raw.Valid {
		p.Raw = json.RawMessage(raw.String)
	}
	return p, err
}

func scanMessage(row rowScanner) (Message, error) {
	var m Message
	var raw string
	err := row.Scan(&m.ChatID, &m.ID, &m.Date, &m.SenderID, &m.SenderName, &m.Text, &m.Out, &m.ReplyTo, &m.TopicID, &m.MediaType, &m.EditedAt, &m.Pinned, &m.Service, &raw, &m.SearchText, &m.DeletedAt, &m.FirstSeenAt, &m.LastSeenAt)
	m.Raw = json.RawMessage(raw)
	return m, err
}

const chatColumns = `id,type,title,username,about,members,forum,verified,unread,settings,raw,search_text,created_at,scam,fake,restricted,restriction_reason,full_synced_at,full_error,first_seen_at,last_seen_at`
const personColumns = `id,username,first_name,last_name,phone,bio,is_bot,is_contact,raw,search_text,first_seen_at,last_seen_at`
const messageColumns = `chat_id,id,date,sender_id,sender_name,text,out,reply_to,topic_id,media_type,edited_at,pinned,service,raw,search_text,deleted_at,first_seen_at,last_seen_at`

// GetChat returns a chat or ErrNotFound.
func (db *DB) GetChat(ctx context.Context, id int64) (Chat, error) {
	c, err := scanChat(db.read.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, ErrNotFound
	}
	if err != nil {
		return Chat{}, fmt.Errorf("get chat %d: %w", id, err)
	}
	return c, nil
}

// GetPerson returns a person or ErrNotFound.
func (db *DB) GetPerson(ctx context.Context, id int64) (Person, error) {
	p, err := scanPerson(db.read.QueryRowContext(ctx, `SELECT `+personColumns+` FROM people WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, fmt.Errorf("get person %d: %w", id, err)
	}
	return p, nil
}

// FindChatByUsername finds a chat by its ASCII Telegram username.
func (db *DB) FindChatByUsername(ctx context.Context, username string) (Chat, error) {
	c, err := scanChat(db.read.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats WHERE lower(username)=lower(?) ORDER BY id LIMIT 1`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return Chat{}, ErrNotFound
	}
	if err != nil {
		return Chat{}, fmt.Errorf("find chat by username: %w", err)
	}
	return c, nil
}

// FindPersonByUsername finds a person by their ASCII Telegram username.
func (db *DB) FindPersonByUsername(ctx context.Context, username string) (Person, error) {
	p, err := scanPerson(db.read.QueryRowContext(ctx, `SELECT `+personColumns+` FROM people WHERE lower(username)=lower(?) ORDER BY id LIMIT 1`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, fmt.Errorf("find person by username: %w", err)
	}
	return p, nil
}

// GetMessage returns a message, including a marked deleted message, or ErrNotFound.
func (db *DB) GetMessage(ctx context.Context, chatID int64, id int) (Message, error) {
	m, err := scanMessage(db.read.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE chat_id=? AND id=?`, chatID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message %d/%d: %w", chatID, id, err)
	}
	return m, nil
}

// MessageVersions returns the saved versions of a message, oldest first.
func (db *DB) MessageVersions(ctx context.Context, chatID int64, id int) ([]MessageVersion, error) {
	rows, err := db.read.QueryContext(ctx, `SELECT chat_id,message_id,version,text,edited_at,raw,saved_at FROM message_versions WHERE chat_id=? AND message_id=? ORDER BY version`, chatID, id)
	if err != nil {
		return nil, fmt.Errorf("get message versions %d/%d: %w", chatID, id, err)
	}
	defer rows.Close()
	var versions []MessageVersion
	for rows.Next() {
		var v MessageVersion
		var raw sql.NullString
		if err := rows.Scan(&v.ChatID, &v.MessageID, &v.Version, &v.Text, &v.EditedAt, &raw, &v.SavedAt); err != nil {
			return nil, err
		}
		if raw.Valid {
			v.Raw = json.RawMessage(raw.String)
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func validateOptionalJSON(raw json.RawMessage) error {
	if len(raw) > 0 && !json.Valid(raw) {
		return errors.New("invalid JSON")
	}
	return nil
}
func jsonArg(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}
func textValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func mergeText(old, incoming *string) *string {
	if incoming != nil && *incoming != "" {
		return incoming
	}
	return old
}
func mergeInt(old, incoming *int64) *int64 {
	if incoming != nil {
		return incoming
	}
	return old
}
func mergeBool(old, incoming *bool) *bool {
	if incoming != nil {
		return incoming
	}
	return old
}
func boolInt(p *bool) int {
	if p != nil && *p {
		return 1
	}
	return 0
}
func mergeJSON(old, incoming json.RawMessage) json.RawMessage {
	if len(incoming) > 0 {
		return incoming
	}
	return old
}
func nonEmpty(old, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return old
}

func mergeChat(old, incoming Chat, exists bool, now int64) Chat {
	c := incoming
	if exists {
		c.Type = nonEmpty(old.Type, incoming.Type)
		c.Title = nonEmpty(old.Title, incoming.Title)
		c.Username = mergeText(old.Username, incoming.Username)
		c.About = mergeText(old.About, incoming.About)
		c.Members = mergeInt(old.Members, incoming.Members)
		c.Forum = mergeBool(old.Forum, incoming.Forum)
		c.Verified = mergeBool(old.Verified, incoming.Verified)
		c.Unread = mergeInt(old.Unread, incoming.Unread)
		c.Settings = mergeJSON(old.Settings, incoming.Settings)
		c.CreatedAt = mergeInt(old.CreatedAt, incoming.CreatedAt)
		c.RestrictionReason = mergeJSON(old.RestrictionReason, incoming.RestrictionReason)
		c.FullSyncedAt = mergeInt(old.FullSyncedAt, incoming.FullSyncedAt)
		c.FullError = mergeText(old.FullError, incoming.FullError)
		c.Raw = mergeJSON(old.Raw, incoming.Raw)
		c.FirstSeenAt = old.FirstSeenAt
	} else {
		c.FirstSeenAt = now
	}
	c.LastSeenAt = now
	c.SearchText = textnorm.Normalize(strings.Join([]string{c.Title, textValue(c.Username), textValue(c.About)}, " "))
	return c
}

// UpsertChats merges a batch of chats in one transaction.
func (db *DB) UpsertChats(ctx context.Context, chats []Chat) error {
	for _, c := range chats {
		if err := validateOptionalJSON(c.Settings); err != nil {
			return fmt.Errorf("chat %d settings: %w", c.ID, err)
		}
		if err := validateOptionalJSON(c.Raw); err != nil {
			return fmt.Errorf("chat %d raw: %w", c.ID, err)
		}
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chat upsert: %w", err)
	}
	defer tx.Rollback()
	now := db.now().Unix()
	const insert = `INSERT INTO chats (` + chatColumns + `) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET type=excluded.type,title=excluded.title,username=excluded.username,about=excluded.about,members=excluded.members,forum=excluded.forum,verified=excluded.verified,unread=excluded.unread,settings=excluded.settings,raw=excluded.raw,created_at=excluded.created_at,scam=excluded.scam,fake=excluded.fake,restricted=excluded.restricted,restriction_reason=excluded.restriction_reason,full_synced_at=excluded.full_synced_at,full_error=excluded.full_error,last_seen_at=excluded.last_seen_at`
	for _, incoming := range chats {
		old, err := scanChat(tx.QueryRowContext(ctx, `SELECT `+chatColumns+` FROM chats WHERE id=?`, incoming.ID))
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read chat %d: %w", incoming.ID, err)
		}
		c := mergeChat(old, incoming, exists, now)
		query := insert
		if !exists || c.SearchText != old.SearchText {
			query += `,search_text=excluded.search_text`
		}
		_, err = tx.ExecContext(ctx, query, c.ID, c.Type, c.Title, c.Username, c.About, c.Members, boolInt(c.Forum), boolInt(c.Verified), c.Unread, jsonArg(c.Settings), jsonArg(c.Raw), c.SearchText,
			c.CreatedAt, c.Scam, c.Fake, c.Restricted, jsonArg(c.RestrictionReason), c.FullSyncedAt, c.FullError, c.FirstSeenAt, c.LastSeenAt)
		if err != nil {
			return fmt.Errorf("upsert chat %d: %w", c.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chats: %w", err)
	}
	return nil
}

func phoneDigits(phone *string) string {
	if phone == nil {
		return ""
	}
	var b strings.Builder
	for _, r := range *phone {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func mergePerson(old, incoming Person, exists bool, now int64) Person {
	p := incoming
	if exists {
		p.Username = mergeText(old.Username, incoming.Username)
		p.FirstName = mergeText(old.FirstName, incoming.FirstName)
		p.LastName = mergeText(old.LastName, incoming.LastName)
		p.Phone = mergeText(old.Phone, incoming.Phone)
		p.Bio = mergeText(old.Bio, incoming.Bio)
		p.IsBot = mergeBool(old.IsBot, incoming.IsBot)
		p.IsContact = mergeBool(old.IsContact, incoming.IsContact)
		p.Raw = mergeJSON(old.Raw, incoming.Raw)
		p.FirstSeenAt = old.FirstSeenAt
	} else {
		p.FirstSeenAt = now
	}
	if incoming.DisplayName != nil && *incoming.DisplayName != "" {
		if strings.TrimSpace(textValue(p.FirstName)+" "+textValue(p.LastName)) != *incoming.DisplayName {
			p.FirstName = incoming.DisplayName
			p.LastName = nil
		}
	}
	p.LastSeenAt = now
	p.SearchText = textnorm.Normalize(strings.Join([]string{textValue(p.FirstName), textValue(p.LastName), textValue(p.Username), phoneDigits(p.Phone), textValue(p.Bio)}, " "))
	return p
}

// UpsertPeople merges a batch of people in one transaction.
func (db *DB) UpsertPeople(ctx context.Context, people []Person) error {
	for _, p := range people {
		if err := validateOptionalJSON(p.Raw); err != nil {
			return fmt.Errorf("person %d raw: %w", p.ID, err)
		}
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin people upsert: %w", err)
	}
	defer tx.Rollback()
	if err := upsertPeopleTx(ctx, tx, people, db.now().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit people: %w", err)
	}
	return nil
}

func upsertPeopleTx(ctx context.Context, tx *sql.Tx, people []Person, now int64) error {
	const insert = `INSERT INTO people (` + personColumns + `) VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username,first_name=excluded.first_name,last_name=excluded.last_name,phone=excluded.phone,bio=excluded.bio,is_bot=excluded.is_bot,is_contact=excluded.is_contact,raw=excluded.raw,last_seen_at=excluded.last_seen_at`
	for _, incoming := range people {
		old, err := scanPerson(tx.QueryRowContext(ctx, `SELECT `+personColumns+` FROM people WHERE id=?`, incoming.ID))
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read person %d: %w", incoming.ID, err)
		}
		p := mergePerson(old, incoming, exists, now)
		query := insert
		if !exists || p.SearchText != old.SearchText {
			query += `,search_text=excluded.search_text`
		}
		_, err = tx.ExecContext(ctx, query, p.ID, p.Username, p.FirstName, p.LastName, p.Phone, p.Bio, boolInt(p.IsBot), boolInt(p.IsContact), jsonArg(p.Raw), p.SearchText, p.FirstSeenAt, p.LastSeenAt)
		if err != nil {
			return fmt.Errorf("upsert person %d: %w", p.ID, err)
		}
	}
	return nil
}

func mergeMessage(old, incoming Message, exists bool, now int64) Message {
	m := incoming
	if exists {
		m.SenderID = mergeInt(old.SenderID, incoming.SenderID)
		m.SenderName = mergeText(old.SenderName, incoming.SenderName)
		m.Out = mergeBool(old.Out, incoming.Out)
		m.ReplyTo = mergeInt(old.ReplyTo, incoming.ReplyTo)
		m.TopicID = mergeInt(old.TopicID, incoming.TopicID)
		m.MediaType = mergeText(old.MediaType, incoming.MediaType)
		m.EditedAt = mergeInt(old.EditedAt, incoming.EditedAt)
		m.Pinned = mergeBool(old.Pinned, incoming.Pinned)
		m.Service = mergeText(old.Service, incoming.Service)
		m.FirstSeenAt = old.FirstSeenAt
	} else {
		m.FirstSeenAt = now
	}
	m.LastSeenAt = now
	m.DeletedAt = nil
	m.SearchText = textnorm.Normalize(m.Text)
	return m
}

// UpsertMessages stores a batch, preserving old text when it changes.
func (db *DB) UpsertMessages(ctx context.Context, messages []Message) error {
	last, err := messageLastIndexes(messages, false)
	if err != nil {
		return err
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin message upsert: %w", err)
	}
	defer tx.Rollback()
	if err := upsertMessagesTx(ctx, tx, messages, last, db.now().Unix()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit messages: %w", err)
	}
	return nil
}

func messageLastIndexes(messages []Message, strict bool) (map[[2]int64]int, error) {
	last := make(map[[2]int64]int, len(messages))
	for i, m := range messages {
		last[[2]int64{m.ChatID, int64(m.ID)}] = i
	}
	for i, m := range messages {
		if (strict || last[[2]int64{m.ChatID, int64(m.ID)}] == i) && !json.Valid(m.Raw) {
			return nil, fmt.Errorf("message %d/%d raw: invalid JSON", m.ChatID, m.ID)
		}
	}
	return last, nil
}

func upsertMessagesTx(ctx context.Context, tx *sql.Tx, messages []Message, last map[[2]int64]int, now int64) error {
	const insertNew = `INSERT INTO messages (` + messageColumns + `) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(chat_id,id) DO NOTHING`
	newStmt, err := tx.PrepareContext(ctx, insertNew)
	if err != nil {
		return fmt.Errorf("prepare message insert: %w", err)
	}
	defer newStmt.Close()
	const insert = `INSERT INTO messages (` + messageColumns + `) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(chat_id,id) DO UPDATE SET date=excluded.date,sender_id=excluded.sender_id,sender_name=excluded.sender_name,text=excluded.text,out=excluded.out,reply_to=excluded.reply_to,topic_id=excluded.topic_id,media_type=excluded.media_type,edited_at=excluded.edited_at,pinned=excluded.pinned,service=excluded.service,raw=excluded.raw,deleted_at=NULL,last_seen_at=excluded.last_seen_at`
	for i, incoming := range messages {
		if last[[2]int64{incoming.ChatID, int64(incoming.ID)}] != i {
			continue
		}
		fresh := mergeMessage(Message{}, incoming, false, now)
		result, err := newStmt.ExecContext(ctx, fresh.ChatID, fresh.ID, fresh.Date, fresh.SenderID, fresh.SenderName, fresh.Text, boolInt(fresh.Out), fresh.ReplyTo, fresh.TopicID, fresh.MediaType, fresh.EditedAt, boolInt(fresh.Pinned), fresh.Service, string(fresh.Raw), fresh.SearchText, nil, fresh.FirstSeenAt, fresh.LastSeenAt)
		if err != nil {
			return fmt.Errorf("insert message %d/%d: %w", incoming.ChatID, incoming.ID, err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check message insert %d/%d: %w", incoming.ChatID, incoming.ID, err)
		}
		if inserted == 1 {
			continue
		}
		old, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE chat_id=? AND id=?`, incoming.ChatID, incoming.ID))
		if err != nil {
			return fmt.Errorf("read message %d/%d: %w", incoming.ChatID, incoming.ID, err)
		}
		if old.Text != incoming.Text {
			var version int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM message_versions WHERE chat_id=? AND message_id=?`, incoming.ChatID, incoming.ID).Scan(&version); err != nil {
				return fmt.Errorf("read message version %d/%d: %w", incoming.ChatID, incoming.ID, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO message_versions(chat_id,message_id,version,text,edited_at,raw,saved_at) VALUES(?,?,?,?,?,?,?)`, incoming.ChatID, incoming.ID, version, old.Text, old.EditedAt, jsonArg(old.Raw), now); err != nil {
				return fmt.Errorf("save message version %d/%d: %w", incoming.ChatID, incoming.ID, err)
			}
		}
		m := mergeMessage(old, incoming, true, now)
		query := insert
		if m.SearchText != old.SearchText {
			query += `,search_text=excluded.search_text`
		}
		_, err = tx.ExecContext(ctx, query, m.ChatID, m.ID, m.Date, m.SenderID, m.SenderName, m.Text, boolInt(m.Out), m.ReplyTo, m.TopicID, m.MediaType, m.EditedAt, boolInt(m.Pinned), m.Service, string(m.Raw), m.SearchText, nil, m.FirstSeenAt, m.LastSeenAt)
		if err != nil {
			return fmt.Errorf("upsert message %d/%d: %w", m.ChatID, m.ID, err)
		}
	}
	if err := newStmt.Close(); err != nil {
		return fmt.Errorf("close message insert: %w", err)
	}
	return nil
}

// SetMessagePinned updates only a saved message's pin state. It never creates a row.
func (db *DB) SetMessagePinned(ctx context.Context, chatID int64, id int, pinned bool) (bool, error) {
	result, err := db.write.ExecContext(ctx, `UPDATE messages SET pinned=? WHERE chat_id=? AND id=?`, boolInt(&pinned), chatID, id)
	if err != nil {
		return false, fmt.Errorf("set message %d/%d pinned: %w", chatID, id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check message %d/%d pin update: %w", chatID, id, err)
	}
	return n != 0, nil
}

// MarkMessagesDeleted marks known messages once and returns the changed count.
func (db *DB) MarkMessagesDeleted(ctx context.Context, chatID int64, ids []int) (int, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin delete markers: %w", err)
	}
	defer tx.Rollback()
	now := db.now().Unix()
	var changed int
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE messages SET deleted_at=? WHERE chat_id=? AND id=? AND deleted_at IS NULL`, now, chatID, id)
		if err != nil {
			return 0, fmt.Errorf("mark message %d/%d deleted: %w", chatID, id, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		changed += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit delete markers: %w", err)
	}
	return changed, nil
}

// ChatsOfMessages finds non-channel chats containing any of the account-wide message IDs.
func (db *DB) ChatsOfMessages(ctx context.Context, ids []int) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.read.QueryContext(ctx, `SELECT DISTINCT chat_id FROM messages WHERE id IN (`+placeholders(len(ids))+`) AND chat_id > -1000000000000`, args...)
	if err != nil {
		return nil, fmt.Errorf("find message chats: %w", err)
	}
	defer rows.Close()
	var chats []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message chat: %w", err)
		}
		chats = append(chats, id)
	}
	return chats, rows.Err()
}
