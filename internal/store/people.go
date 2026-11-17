package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tg-cli/internal/textnorm"
)

// ReplaceContacts reconciles visible contacts in one transaction. The caller
// must serialize this operation with contact mutations.
func (db *DB) ReplaceContacts(ctx context.Context, people []Person, filter ChatFilter) error {
	for _, p := range people {
		if err := validateOptionalJSON(p.Raw); err != nil {
			return fmt.Errorf("invalid person data: %w", err)
		}
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	condition, args := filter.condition("id", false)
	if _, err = tx.ExecContext(ctx, `UPDATE people SET is_contact=0 WHERE is_contact=1`+condition, args...); err != nil {
		return err
	}
	contacts := make([]Person, 0, len(people))
	for _, person := range people {
		if personVisible(person.ID, filter) {
			contacts = append(contacts, person)
		}
	}
	for i := range contacts {
		value := true
		contacts[i].IsContact = &value
	}
	if err = upsertPeopleTx(ctx, tx, contacts, db.now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func personVisible(id int64, filter ChatFilter) bool {
	if filter.Hide {
		return false
	}
	allowed := len(filter.Allow) == 0
	for _, value := range filter.Allow {
		if value == id {
			allowed = true
			break
		}
	}
	for _, value := range filter.Block {
		if value == id {
			return false
		}
	}
	return allowed
}

// SetContact changes the contact flag of an existing person.
func (db *DB) SetContact(ctx context.Context, id int64, contact bool) error {
	result, err := db.write.ExecContext(ctx, `UPDATE people SET is_contact=? WHERE id=?`, contact, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListContacts returns a sorted page and whether another page exists.
func (db *DB) ListContacts(ctx context.Context, limit, offset int, filter ChatFilter) ([]Person, bool, error) {
	if limit < 1 || limit > 1000 || offset < 0 {
		return nil, false, ErrInvalidSearchOptions
	}
	condition, args := filter.condition("id", false)
	rows, err := db.read.QueryContext(ctx, `SELECT `+personColumns+` FROM people WHERE is_contact=1`+condition, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	people := []Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, false, err
		}
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	sort.Slice(people, func(i, j int) bool {
		a, b := people[i], people[j]
		af, bf := strings.ToLower(textValue(a.FirstName)), strings.ToLower(textValue(b.FirstName))
		if af != bf {
			return af < bf
		}
		al, bl := strings.ToLower(textValue(a.LastName)), strings.ToLower(textValue(b.LastName))
		if al != bl {
			return al < bl
		}
		return a.ID < b.ID
	})
	if offset >= len(people) {
		return []Person{}, false, nil
	}
	end := offset + limit
	more := end < len(people)
	if end > len(people) {
		end = len(people)
	}
	return people[offset:end], more, nil
}

// SearchContacts searches visible contacts using the same normalized FTS query as Search.
func (db *DB) SearchContacts(ctx context.Context, query string, limit int, filter ChatFilter) ([]Person, error) {
	match := textnorm.Query(query, false)
	if match == "" {
		return nil, ErrEmptyQuery
	}
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidSearchOptions
	}
	condition, args := filter.condition("p.id", false)
	args = append([]any{match}, args...)
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, `SELECT p.id,p.username,p.first_name,p.last_name,p.phone,p.bio,p.is_bot,p.is_contact,p.deleted,p.raw,p.search_text,p.first_seen_at,p.last_seen_at FROM people_fts JOIN people p ON p.rowid=people_fts.rowid WHERE people_fts MATCH ? AND p.is_contact=1`+condition+` ORDER BY bm25(people_fts),p.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := []Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	return people, rows.Err()
}
