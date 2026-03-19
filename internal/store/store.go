package store

import (
  	"context"
  	"errors"
  	"fmt"
  	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
  	userColumns = `identity_id, oidc_subject, name, nickname, photo_url, created_at, updated_at`
)

type EntityMeta struct {
  	ID        uuid.UUID
  	CreatedAt time.Time
  	UpdatedAt time.Time
}

type User struct {
  	Meta        EntityMeta
  	OIDCSubject string
  	Name        string
  	Nickname    string
  	PhotoURL    string
}

type UserInput struct {
  	OIDCSubject string
  	Name        string
  	Nickname    string
  	PhotoURL    string
}

type UserUpdate struct {
  	Name     *string
  	Nickname *string
  	PhotoURL *string
}

type Store struct {
  	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
  	return &Store{pool: pool}
}

func scanUser(row pgx.Row) (User, error) {
  	var user User
  	if err := row.Scan(
  		&user.Meta.ID,
  		&user.OIDCSubject,
  		&user.Name,
  		&user.Nickname,
  		&user.PhotoURL,
  		&user.Meta.CreatedAt,
  		&user.Meta.UpdatedAt,
  	); err != nil {
  		return User{}, err
  	}
  	return user, nil
}

func (s *Store) ResolveOrCreateUser(ctx context.Context, input UserInput) (User, bool, error) {
  	row := s.pool.QueryRow(ctx,
  		fmt.Sprintf(`SELECT %s FROM users WHERE oidc_subject = $1`, userColumns),
  		input.OIDCSubject,
  	)
  	user, err := scanUser(row)
  	if err == nil {
  		return user, false, nil
  	}
  	if !errors.Is(err, pgx.ErrNoRows) {
  		return User{}, false, err
  	}

  	identityID := uuid.New()
  	row = s.pool.QueryRow(ctx,
  		fmt.Sprintf(`INSERT INTO users (identity_id, oidc_subject, name, nickname, photo_url)
         VALUES ($1, $2, $3, $4, $5)
         RETURNING %s`, userColumns),
  		identityID,
  		input.OIDCSubject,
  		input.Name,
  		input.Nickname,
  		input.PhotoURL,
  	)
  	user, err = scanUser(row)
  	if err != nil {
  		var pgErr *pgconn.PgError
  		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
  			row = s.pool.QueryRow(ctx,
  				fmt.Sprintf(`SELECT %s FROM users WHERE oidc_subject = $1`, userColumns),
  				input.OIDCSubject,
  			)
  			user, err = scanUser(row)
  			if err != nil {
  				if errors.Is(err, pgx.ErrNoRows) {
  					return User{}, false, NotFound("user")
  				}
  				return User{}, false, err
  			}
  			return user, false, nil
  		}
  		return User{}, false, err
  	}

  	// TODO: Call Identity.RegisterIdentity(identityID, "user") here.

  	return user, true, nil
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
  	row := s.pool.QueryRow(ctx,
  		fmt.Sprintf(`SELECT %s FROM users WHERE identity_id = $1`, userColumns),
  		id,
  	)
  	user, err := scanUser(row)
  	if err != nil {
  		if errors.Is(err, pgx.ErrNoRows) {
  			return User{}, NotFound("user")
  		}
  		return User{}, err
  	}
  	return user, nil
}

func (s *Store) GetUserByOIDCSubject(ctx context.Context, oidcSubject string) (User, error) {
  	row := s.pool.QueryRow(ctx,
  		fmt.Sprintf(`SELECT %s FROM users WHERE oidc_subject = $1`, userColumns),
  		oidcSubject,
  	)
  	user, err := scanUser(row)
  	if err != nil {
  		if errors.Is(err, pgx.ErrNoRows) {
  			return User{}, NotFound("user")
  		}
  		return User{}, err
  	}
  	return user, nil
}

func (s *Store) BatchGetUsers(ctx context.Context, identityIDs []uuid.UUID) ([]User, error) {
  	if len(identityIDs) == 0 {
  		return []User{}, nil
  	}

	array := make([]pgtype.UUID, len(identityIDs))
	for i, id := range identityIDs {
		array[i] = pgtype.UUID{Bytes: id, Valid: true}
	}

	rows, err := s.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM users WHERE identity_id = ANY($1) ORDER BY identity_id`, userColumns),
		array,
	)
  	if err != nil {
  		return nil, err
  	}
  	defer rows.Close()

  	users := make([]User, 0, len(identityIDs))
  	for rows.Next() {
  		user, err := scanUser(rows)
  		if err != nil {
  			return nil, err
  		}
  		users = append(users, user)
  	}
  	if err := rows.Err(); err != nil {
  		return nil, err
  	}
  	return users, nil
}

func (s *Store) UpdateUser(ctx context.Context, id uuid.UUID, update UserUpdate) (User, error) {
  	builder := updateBuilder{}
  	if update.Name != nil {
  		builder.add("name", *update.Name)
  	}
  	if update.Nickname != nil {
  		builder.add("nickname", *update.Nickname)
  	}
  	if update.PhotoURL != nil {
  		builder.add("photo_url", *update.PhotoURL)
  	}

  	if builder.empty() {
  		return User{}, fmt.Errorf("user update requires at least one field")
  	}
  	query, args := builder.build("users", userColumns, "identity_id", id)
  	row := s.pool.QueryRow(ctx, query, args...)
  	user, err := scanUser(row)
  	if err != nil {
  		if errors.Is(err, pgx.ErrNoRows) {
  			return User{}, NotFound("user")
  		}
  		return User{}, err
  	}
  	return user, nil
}
