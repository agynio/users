package store

import (
  	"fmt"
  	"strings"

  	"github.com/google/uuid"
)

type updateBuilder struct {
  	setClauses []string
  	args       []any
}

func (b *updateBuilder) add(field string, value any) {
  	placeholder := len(b.args) + 1
  	b.setClauses = append(b.setClauses, fmt.Sprintf("%s = $%d", field, placeholder))
  	b.args = append(b.args, value)
}

func (b *updateBuilder) addNull(field string) {
  	b.setClauses = append(b.setClauses, fmt.Sprintf("%s = NULL", field))
}

func (b *updateBuilder) empty() bool {
  	return len(b.setClauses) == 0
}

func (b *updateBuilder) build(table string, returning string, idColumn string, id uuid.UUID) (string, []any) {
  	return buildUpdateQuery(table, returning, idColumn, b.setClauses, b.args, id)
}

func buildUpdateQuery(table string, returning string, idColumn string, setClauses []string, args []any, id uuid.UUID) (string, []any) {
  	setClauses = append(setClauses, "updated_at = NOW()")
  	args = append(args, id)
  	query := fmt.Sprintf(
  		"UPDATE %s SET %s WHERE %s = $%d RETURNING %s",
  		table,
  		strings.Join(setClauses, ", "),
  		idColumn,
  		len(args),
  		returning,
  	)
  	return query, args
}
