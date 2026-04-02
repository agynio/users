package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultListPageSize int32 = 50
	MaxListPageSize     int32 = 100
)

type PageCursor struct {
	AfterID uuid.UUID
}

func NormalizePageSize(size int32) int32 {
	if size <= 0 {
		return DefaultListPageSize
	}
	if size > MaxListPageSize {
		return MaxListPageSize
	}
	return size
}

func EncodePageToken(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id.String()))
}

func DecodePageToken(token string) (uuid.UUID, error) {
	if token == "" {
		return uuid.UUID{}, errors.New("empty token")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("decode token: %w", err)
	}
	value, err := uuid.Parse(string(decoded))
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("parse token: %w", err)
	}
	return value, nil
}

func listEntities[T any](
	ctx context.Context,
	pool *pgxpool.Pool,
	baseQuery string,
	cursorColumn string,
	clauses []string,
	args []any,
	cursor *PageCursor,
	pageSize int32,
	scan func(pgx.Row) (T, error),
	idFunc func(T) uuid.UUID,
) ([]T, *PageCursor, error) {
	limit := NormalizePageSize(pageSize)

	query := strings.Builder{}
	query.WriteString(baseQuery)

	paramIndex := len(args) + 1
	if cursor != nil {
		clauses = append(clauses, fmt.Sprintf("%s > $%d", cursorColumn, paramIndex))
		args = append(args, cursor.AfterID)
		paramIndex++
	}

	if len(clauses) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(clauses, " AND "))
	}
	query.WriteString(fmt.Sprintf(" ORDER BY %s ASC LIMIT $%d", cursorColumn, paramIndex))
	args = append(args, int(limit)+1)

	rows, err := pool.Query(ctx, query.String(), args...)
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
