package store

import "strings"

// ChatFilter constrains chat-owned rows before LIMIT is applied. A nil filter
// leaves existing queries unchanged. Hide closes every chat-owned row.
type ChatFilter struct {
	Hide  bool
	Allow []int64
	Block []int64
}

func (f ChatFilter) condition(column string, nullable bool) (string, []any) {
	if f.Hide {
		if nullable {
			return " AND " + column + " IS NULL", nil
		}
		return " AND 1=0", nil
	}
	var query strings.Builder
	var args []any
	if len(f.Allow) > 0 {
		query.WriteString(" AND ")
		if nullable {
			query.WriteString("(")
			query.WriteString(column)
			query.WriteString(" IS NULL OR ")
		}
		query.WriteString(column + " IN (" + placeholders(len(f.Allow)) + ")")
		if nullable {
			query.WriteString(")")
		}
		for _, id := range f.Allow {
			args = append(args, id)
		}
	}
	if len(f.Block) > 0 {
		query.WriteString(" AND ")
		if nullable {
			query.WriteString("(")
			query.WriteString(column)
			query.WriteString(" IS NULL OR ")
		}
		query.WriteString(column + " NOT IN (" + placeholders(len(f.Block)) + ")")
		if nullable {
			query.WriteString(")")
		}
		for _, id := range f.Block {
			args = append(args, id)
		}
	}
	return query.String(), args
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
