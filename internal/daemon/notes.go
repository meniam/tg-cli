package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/textnorm"
)

func storeError(err error) error {
	switch {
	case errors.Is(err, store.ErrInvalidNote), errors.Is(err, store.ErrEmptyQuery), errors.Is(err, store.ErrInvalidSearchOptions):
		return rpc.NewError(rpc.CodeInvalidParams, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return rpc.NewError(rpc.CodeNotFound, "not found")
	default:
		return err
	}
}

func rpcNote(note store.Note) rpc.Note {
	result := rpc.Note{
		ID: note.ID, Kind: note.Kind, ChatID: note.ChatID, TargetID: note.TargetID,
		Body: note.Body, Author: note.Author, Tags: note.Tags,
		CreatedAt: time.Unix(note.CreatedAt, 0).UTC().Format(time.RFC3339),
		UpdatedAt: time.Unix(note.UpdatedAt, 0).UTC().Format(time.RFC3339),
	}
	if note.Model != nil {
		result.Model = *note.Model
	}
	return result
}

// resolveStoredReference returns known numeric or local username IDs. An unknown
// username is returned as found=false unless allowTelegram permits a fallback.
func (d *daemon) resolveStoredReference(ctx context.Context, value string, person, allowTelegram bool) (id int64, found bool, err error) {
	ref, err := chatref.Parse(value)
	if err != nil {
		return 0, false, rpc.NewError(rpc.CodeInvalidParams, err.Error())
	}
	if ref.Kind == chatref.Phone || ref.Kind == chatref.Invite {
		return 0, false, rpc.NewError(rpc.CodeInvalidParams, "phone references and invite links are not supported for notes")
	}
	if ref.Kind == chatref.ID {
		if person && ref.ID <= 0 {
			return 0, false, rpc.NewError(rpc.CodeInvalidParams, "a person needs a positive user id")
		}
		return ref.ID, true, nil
	}
	if person {
		p, err := d.store.FindPersonByUsername(ctx, ref.Username)
		if err == nil {
			if p.ID <= 0 {
				return 0, false, rpc.NewError(rpc.CodeInvalidParams, "a person needs a positive user id")
			}
			return p.ID, true, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return 0, false, err
		}
	} else {
		c, err := d.store.FindChatByUsername(ctx, ref.Username)
		if err == nil {
			return c.ID, true, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return 0, false, err
		}
	}
	if !allowTelegram {
		return 0, false, nil
	}
	if err := d.requireAccount(ctx); err != nil {
		return 0, false, err
	}
	resolved, err := d.opts.Telegram.Resolve(ctx, ref)
	if err != nil {
		return 0, false, err
	}
	if person && (resolved.ID <= 0 || (resolved.Type != "user" && resolved.Type != "bot")) {
		return 0, false, rpc.NewError(rpc.CodeInvalidParams, "a person needs a positive user id")
	}
	if resolved.ID == 0 {
		return 0, false, rpc.NewError(rpc.CodeNotFound, "not found")
	}
	return resolved.ID, true, nil
}

func (d *daemon) noteAdd(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.NoteAddParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	note := store.Note{Kind: p.Kind, Body: p.Body, Author: p.Author, Tags: p.Tags}
	if note.Author == "" {
		note.Author = "owner"
	}
	if p.Model != "" {
		note.Model = &p.Model
	}
	switch p.Kind {
	case "chat":
		if p.Chat == "" || p.Person != "" || p.MessageID != 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "chat note needs chat only")
		}
		id, _, err := d.resolveStoredReference(ctx, p.Chat, false, true)
		if err != nil {
			return nil, err
		}
		note.ChatID = &id
	case "person":
		if p.Person == "" || p.Chat != "" || p.MessageID != 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "person note needs person only")
		}
		id, _, err := d.resolveStoredReference(ctx, p.Person, true, true)
		if err != nil {
			return nil, err
		}
		note.TargetID = &id
	case "message":
		if p.Chat == "" || p.Person != "" || p.MessageID <= 0 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "message note needs chat and positive message_id")
		}
		id, _, err := d.resolveStoredReference(ctx, p.Chat, false, true)
		if err != nil {
			return nil, err
		}
		note.ChatID, note.TargetID = &id, &p.MessageID
	default:
		return nil, rpc.NewError(rpc.CodeInvalidParams, "kind must be chat, person, or message")
	}
	stored, err := d.store.AddNote(ctx, note)
	if err != nil {
		return nil, storeError(err)
	}
	return rpcNote(stored), nil
}

func (d *daemon) noteList(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.NoteListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Kind != "" && p.Kind != "chat" && p.Kind != "person" && p.Kind != "message" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "kind must be chat, person, or message")
	}
	if p.Author != "" && p.Author != "owner" && p.Author != "ai" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "author must be owner or ai")
	}
	if p.Person != "" && p.MessageID != 0 || p.MessageID < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "person and message_id cannot be combined")
	}
	filter := store.NoteFilter{Kind: p.Kind, Author: p.Author, Tag: p.Tag, Limit: p.Limit, BeforeID: p.BeforeID}
	if p.Person != "" {
		if filter.Kind != "" && filter.Kind != "person" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "person filter requires person kind")
		}
		filter.Kind = "person"
	}
	if p.MessageID > 0 {
		if filter.Kind != "" && filter.Kind != "message" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "message_id filter requires message kind")
		}
		filter.Kind = "message"
	}
	if p.Chat != "" {
		id, found, err := d.resolveStoredReference(ctx, p.Chat, false, false)
		if err != nil {
			return nil, err
		}
		if !found {
			return rpc.NoteListResult{Notes: []rpc.Note{}}, nil
		}
		filter.ChatID = &id
	}
	if p.Person != "" {
		id, found, err := d.resolveStoredReference(ctx, p.Person, true, false)
		if err != nil {
			return nil, err
		}
		if !found {
			return rpc.NoteListResult{Notes: []rpc.Note{}}, nil
		}
		filter.TargetID = &id
	}
	if p.MessageID > 0 {
		filter.TargetID = &p.MessageID
	}
	notes, err := d.store.ListNotes(ctx, filter)
	if err != nil {
		return nil, storeError(err)
	}
	result := rpc.NoteListResult{Notes: make([]rpc.Note, 0, len(notes))}
	for _, note := range notes {
		result.Notes = append(result.Notes, rpcNote(note))
	}
	return result, nil
}

func (d *daemon) noteDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.NoteDeleteParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "id must be positive")
	}
	if err := d.store.DeleteNote(ctx, p.ID); err != nil {
		return nil, storeError(err)
	}
	return rpc.NoteDeleteResult{Deleted: 1}, nil
}

func (d *daemon) noteDeleteByAuthor(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.NoteDeleteByAuthorParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	count, err := d.store.DeleteNotesByAuthor(ctx, p.Author)
	if err != nil {
		return nil, storeError(err)
	}
	return rpc.NoteDeleteResult{Deleted: count}, nil
}

func (d *daemon) search(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.SearchParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if textnorm.Query(p.Query, p.Prefix) == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, store.ErrEmptyQuery.Error())
	}
	if p.Author != "" && p.Author != "owner" && p.Author != "ai" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown author")
	}
	for _, scope := range p.Scopes {
		if scope != "chats" && scope != "people" && scope != "messages" && scope != "notes" {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown scope")
		}
	}
	options := store.SearchOptions{Query: p.Query, Scopes: p.Scopes, Author: p.Author, Limit: p.Limit, Prefix: p.Prefix, IncludeDeleted: p.IncludeDeleted}
	if p.Chat != "" {
		id, found, err := d.resolveStoredReference(ctx, p.Chat, false, false)
		if err != nil {
			return nil, err
		}
		if !found {
			return rpc.SearchResult{Hits: []rpc.SearchHit{}}, nil
		}
		options.ChatID = &id
	}
	hits, err := d.store.Search(ctx, options)
	if err != nil {
		return nil, storeError(err)
	}
	result := rpc.SearchResult{Hits: make([]rpc.SearchHit, 0, len(hits))}
	for _, hit := range hits {
		out := rpc.SearchHit{Scope: hit.Scope, ID: hit.ID, ChatID: hit.ChatID, Title: hit.Title, Snippet: hit.Snippet, Deleted: hit.Deleted, Author: hit.Author, NoteKind: hit.NoteKind, Rank: hit.Rank}
		if hit.Model != nil {
			out.Model = *hit.Model
		}
		if hit.Scope == "messages" {
			out.Date = time.Unix(hit.Date, 0).UTC().Format(time.RFC3339)
		}
		result.Hits = append(result.Hits, out)
	}
	return result, nil
}
