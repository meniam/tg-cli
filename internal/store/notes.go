package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"tg-cli/internal/textnorm"
)

// ErrInvalidNote identifies invalid note input.
var ErrInvalidNote = errors.New("invalid note")

// Note is user-owned data attached to a chat, person, or message.
type Note struct {
	ID        int64
	Kind      string
	ChatID    *int64
	TargetID  *int64
	Body      string
	Author    string
	Model     *string
	Tags      []string
	CreatedAt int64
	UpdatedAt int64
}

// NoteFilter selects notes in descending ID order.
type NoteFilter struct {
	Kind     string
	ChatID   *int64
	TargetID *int64
	Author   string
	Tag      string
	Limit    int
	BeforeID int64
}

func validNoteKind(kind string) bool     { return kind == "chat" || kind == "person" || kind == "message" }
func validNoteAuthor(author string) bool { return author == "owner" || author == "ai" }

func validateNote(note Note) (Note, error) {
	if !validNoteKind(note.Kind) {
		return Note{}, fmt.Errorf("%w: kind must be chat, person, or message", ErrInvalidNote)
	}
	switch note.Kind {
	case "chat":
		if note.ChatID == nil || note.TargetID != nil {
			return Note{}, fmt.Errorf("%w: chat note needs chat_id only", ErrInvalidNote)
		}
	case "person":
		if note.ChatID != nil || note.TargetID == nil {
			return Note{}, fmt.Errorf("%w: person note needs target_id only", ErrInvalidNote)
		}
	case "message":
		if note.ChatID == nil || note.TargetID == nil {
			return Note{}, fmt.Errorf("%w: message note needs chat_id and target_id", ErrInvalidNote)
		}
	}
	note.Body = strings.TrimSpace(note.Body)
	if note.Body == "" || len(note.Body) > 16*1024 {
		return Note{}, fmt.Errorf("%w: body must contain 1 to 16384 bytes", ErrInvalidNote)
	}
	if !validNoteAuthor(note.Author) {
		return Note{}, fmt.Errorf("%w: author must be owner or ai", ErrInvalidNote)
	}
	if note.Model != nil && note.Author != "ai" {
		return Note{}, fmt.Errorf("%w: model requires ai author", ErrInvalidNote)
	}
	if len(note.Tags) > 20 {
		return Note{}, fmt.Errorf("%w: too many tags", ErrInvalidNote)
	}
	seen := make(map[string]struct{}, len(note.Tags))
	tags := make([]string, 0, len(note.Tags))
	for _, input := range note.Tags {
		tag := strings.TrimSpace(input)
		if n := utf8.RuneCountInString(tag); n < 1 || n > 64 {
			return Note{}, fmt.Errorf("%w: tag must contain 1 to 64 characters", ErrInvalidNote)
		}
		tag = textnorm.Normalize(tag)
		if _, exists := seen[tag]; !exists {
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	note.Tags = tags
	return note, nil
}

// AddNote validates and stores a note and its tags in one transaction.
func (db *DB) AddNote(ctx context.Context, input Note) (Note, error) {
	note, err := validateNote(input)
	if err != nil {
		return Note{}, err
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, fmt.Errorf("begin note insert: %w", err)
	}
	defer tx.Rollback()
	now := db.now().Unix()
	searchText := textnorm.Normalize(note.Body + " " + strings.Join(note.Tags, " "))
	result, err := tx.ExecContext(ctx, `INSERT INTO notes(kind,chat_id,target_id,body,author,model,search_text,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, note.Kind, note.ChatID, note.TargetID, note.Body, note.Author, note.Model, searchText, now, now)
	if err != nil {
		return Note{}, fmt.Errorf("insert note: %w", err)
	}
	note.ID, err = result.LastInsertId()
	if err != nil {
		return Note{}, fmt.Errorf("get note id: %w", err)
	}
	for _, tag := range note.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO note_tags(note_id,tag) VALUES(?,?)`, note.ID, tag); err != nil {
			return Note{}, fmt.Errorf("insert note tag: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Note{}, fmt.Errorf("commit note: %w", err)
	}
	note.CreatedAt, note.UpdatedAt = now, now
	return note, nil
}

func scanNote(row rowScanner) (Note, error) {
	var note Note
	err := row.Scan(&note.ID, &note.Kind, &note.ChatID, &note.TargetID, &note.Body, &note.Author, &note.Model, &note.CreatedAt, &note.UpdatedAt)
	return note, err
}

const noteColumns = `id,kind,chat_id,target_id,body,author,model,created_at,updated_at`

func loadNoteTags(ctx context.Context, db *sql.DB, notes []Note) error {
	if len(notes) == 0 {
		return nil
	}
	args := make([]any, len(notes))
	indices := make(map[int64]int, len(notes))
	for i := range notes {
		args[i] = notes[i].ID
		indices[notes[i].ID] = i
	}
	query := `SELECT note_id,tag FROM note_tags WHERE note_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(notes)), ",") + `) ORDER BY note_id,tag`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return err
		}
		notes[indices[id]].Tags = append(notes[indices[id]].Tags, tag)
	}
	return rows.Err()
}

// GetNote returns a note with tags or ErrNotFound.
func (db *DB) GetNote(ctx context.Context, id int64) (Note, error) {
	note, err := scanNote(db.read.QueryRowContext(ctx, `SELECT `+noteColumns+` FROM notes WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, fmt.Errorf("get note %d: %w", id, err)
	}
	items := []Note{note}
	if err := loadNoteTags(ctx, db.read, items); err != nil {
		return Note{}, fmt.Errorf("get note %d tags: %w", id, err)
	}
	return items[0], nil
}

// ListNotes returns matching notes newest first, with a default limit of 50.
func (db *DB) ListNotes(ctx context.Context, filter NoteFilter) ([]Note, error) {
	if filter.Kind != "" && !validNoteKind(filter.Kind) {
		return nil, fmt.Errorf("%w: unknown kind", ErrInvalidNote)
	}
	if filter.Author != "" && !validNoteAuthor(filter.Author) {
		return nil, fmt.Errorf("%w: unknown author", ErrInvalidNote)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}
	var query strings.Builder
	query.WriteString(`SELECT ` + noteColumns + ` FROM notes WHERE 1=1`)
	var args []any
	if filter.Kind != "" {
		query.WriteString(` AND kind=?`)
		args = append(args, filter.Kind)
	}
	if filter.ChatID != nil {
		query.WriteString(` AND chat_id=?`)
		args = append(args, *filter.ChatID)
	}
	if filter.TargetID != nil {
		query.WriteString(` AND target_id=?`)
		args = append(args, *filter.TargetID)
	}
	if filter.Author != "" {
		query.WriteString(` AND author=?`)
		args = append(args, filter.Author)
	}
	if filter.Tag != "" {
		query.WriteString(` AND EXISTS (SELECT 1 FROM note_tags WHERE note_id=notes.id AND tag=?)`)
		args = append(args, textnorm.Normalize(filter.Tag))
	}
	if filter.BeforeID > 0 {
		query.WriteString(` AND id<?`)
		args = append(args, filter.BeforeID)
	}
	query.WriteString(` ORDER BY id DESC LIMIT ?`)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	var notes []Note
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan note: %w", err)
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := loadNoteTags(ctx, db.read, notes); err != nil {
		return nil, fmt.Errorf("load note tags: %w", err)
	}
	return notes, nil
}

// DeleteNote removes one note and its tags, returning ErrNotFound when absent.
func (db *DB) DeleteNote(ctx context.Context, id int64) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin note deletion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete note %d: %w", id, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit note deletion: %w", err)
	}
	return nil
}

// DeleteNotesByAuthor removes all notes from one author and returns their count.
func (db *DB) DeleteNotesByAuthor(ctx context.Context, author string) (int, error) {
	if !validNoteAuthor(author) {
		return 0, fmt.Errorf("%w: unknown author", ErrInvalidNote)
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin author note deletion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE author=?`, author)
	if err != nil {
		return 0, fmt.Errorf("delete notes by author: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit author note deletion: %w", err)
	}
	return int(count), nil
}
