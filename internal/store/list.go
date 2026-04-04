package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func listEntities[T any](
	ctx context.Context,
	pool *pgxpool.Pool,
	baseQuery string,
	clauses []string,
	args []any,
	cursor *PageCursor,
	pageSize int32,
	idColumn string,
	scan func(pgx.Row) (T, error),
	idFunc func(T) uuid.UUID,
) ([]T, *PageCursor, error) {
	localClauses := append([]string(nil), clauses...)
	localArgs := append([]any(nil), args...)
	limit := NormalizePageSize(pageSize)

	query := strings.Builder{}
	query.WriteString(baseQuery)

	paramIndex := len(localArgs) + 1
	if cursor != nil {
		localClauses = append(localClauses, fmt.Sprintf("%s > $%d", idColumn, paramIndex))
		localArgs = append(localArgs, cursor.AfterID)
		paramIndex++
	}

	if len(localClauses) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(localClauses, " AND "))
	}
	query.WriteString(fmt.Sprintf(" ORDER BY %s ASC LIMIT $%d", idColumn, paramIndex))
	localArgs = append(localArgs, int(limit)+1)

	rows, err := pool.Query(ctx, query.String(), localArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	items := make([]T, 0, limit)
	var (
		nextCursor *PageCursor
		lastID     uuid.UUID
		hasMore    bool
	)
	for rows.Next() {
		if int32(len(items)) == limit {
			hasMore = true
			break
		}
		item, err := scan(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
		lastID = idFunc(item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if hasMore {
		nextCursor = &PageCursor{AfterID: lastID}
	}
	return items, nextCursor, nil
}
