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
	userColumns     = `identity_id, oidc_subject, name, email, nickname, username, photo_url, created_at, updated_at`
	apiTokenColumns = `id, identity_id, name, token_hash, token_prefix, expires_at, created_at, last_used_at`
	deviceColumns   = `id, identity_id, name, openziti_identity_id, enrollment_jwt, status, created_at`
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
	Email       string
	Nickname    string
	Username    string
	PhotoURL    string
}

type UserInput struct {
	OIDCSubject string
	Name        string
	Email       string
	Nickname    string
	Username    string
	PhotoURL    string
}

type UserUpdate struct {
	Name     *string
	Email    *string
	Nickname *string
	Username *string
	PhotoURL *string
}

type UserListResult struct {
	Users      []User
	NextCursor *PageCursor
}

type UserDirectoryEntry struct {
	IdentityID uuid.UUID
	Username   string
	Name       string
	PhotoURL   string
}

type APIToken struct {
	ID          uuid.UUID
	IdentityID  uuid.UUID
	Name        string
	TokenHash   string
	TokenPrefix string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
	LastUsedAt  *time.Time
}

type DeviceStatus string

const (
	DeviceStatusPending  DeviceStatus = "pending"
	DeviceStatusEnrolled DeviceStatus = "enrolled"
)

type CreateAPITokenInput struct {
	IdentityID  uuid.UUID
	Name        string
	TokenHash   string
	TokenPrefix string
	ExpiresAt   *time.Time
}

type Device struct {
	ID                 uuid.UUID
	IdentityID         uuid.UUID
	Name               string
	OpenZitiIdentityID string
	EnrollmentJWT      *string
	Status             DeviceStatus
	CreatedAt          time.Time
}

type CreateDeviceInput struct {
	IdentityID         uuid.UUID
	Name               string
	OpenZitiIdentityID string
	EnrollmentJWT      string
}

type DeviceListResult struct {
	Devices    []Device
	NextCursor *PageCursor
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func scanUser(row pgx.Row) (User, error) {
	var user User
	var username pgtype.Text
	if err := row.Scan(
		&user.Meta.ID,
		&user.OIDCSubject,
		&user.Name,
		&user.Email,
		&user.Nickname,
		&username,
		&user.PhotoURL,
		&user.Meta.CreatedAt,
		&user.Meta.UpdatedAt,
	); err != nil {
		return User{}, err
	}
	if username.Valid {
		user.Username = username.String
	}
	return user, nil
}

func scanUserDirectoryEntry(row pgx.Row) (UserDirectoryEntry, error) {
	var entry UserDirectoryEntry
	if err := row.Scan(
		&entry.IdentityID,
		&entry.Username,
		&entry.Name,
		&entry.PhotoURL,
	); err != nil {
		return UserDirectoryEntry{}, err
	}
	return entry, nil
}

func scanAPIToken(row pgx.Row) (APIToken, error) {
	var token APIToken
	var expiresAt pgtype.Timestamptz
	var lastUsedAt pgtype.Timestamptz
	if err := row.Scan(
		&token.ID,
		&token.IdentityID,
		&token.Name,
		&token.TokenHash,
		&token.TokenPrefix,
		&expiresAt,
		&token.CreatedAt,
		&lastUsedAt,
	); err != nil {
		return APIToken{}, err
	}
	if expiresAt.Valid {
		value := expiresAt.Time
		token.ExpiresAt = &value
	}
	if lastUsedAt.Valid {
		value := lastUsedAt.Time
		token.LastUsedAt = &value
	}
	return token, nil
}

func scanDevice(row pgx.Row) (Device, error) {
	var device Device
	var enrollmentJWT pgtype.Text
	var status string
	if err := row.Scan(
		&device.ID,
		&device.IdentityID,
		&device.Name,
		&device.OpenZitiIdentityID,
		&enrollmentJWT,
		&status,
		&device.CreatedAt,
	); err != nil {
		return Device{}, err
	}
	if enrollmentJWT.Valid {
		value := enrollmentJWT.String
		device.EnrollmentJWT = &value
	}
	deviceStatus := DeviceStatus(status)
	switch deviceStatus {
	case DeviceStatusPending, DeviceStatusEnrolled:
		device.Status = deviceStatus
	default:
		return Device{}, fmt.Errorf("unsupported device status %q", status)
	}
	return device, nil
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
		fmt.Sprintf(`INSERT INTO users (identity_id, oidc_subject, name, email, nickname, username, photo_url)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING %s`, userColumns),
		identityID,
		input.OIDCSubject,
		input.Name,
		input.Email,
		input.Nickname,
		input.Username,
		input.PhotoURL,
	)
	user, err = scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "users_username_idx":
				return User{}, false, AlreadyExists("username")
			case "users_oidc_subject_idx":
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
			default:
				return User{}, false, AlreadyExists("user")
			}
		}
		return User{}, false, err
	}

	// TODO: Call Identity.RegisterIdentity(identityID, "user") here.

	return user, true, nil
}

func (s *Store) CreateUser(ctx context.Context, input UserInput) (User, error) {
	identityID := uuid.New()
	row := s.pool.QueryRow(ctx,
		fmt.Sprintf(`INSERT INTO users (identity_id, oidc_subject, name, email, nickname, username, photo_url)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING %s`, userColumns),
		identityID,
		input.OIDCSubject,
		input.Name,
		input.Email,
		input.Nickname,
		input.Username,
		input.PhotoURL,
	)
	user, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "users_username_idx":
				return User{}, AlreadyExists("username")
			case "users_oidc_subject_idx":
				return User{}, AlreadyExists("user")
			default:
				return User{}, AlreadyExists("user")
			}
		}
		return User{}, err
	}
	return user, nil
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
	if update.Email != nil {
		builder.add("email", *update.Email)
	}
	if update.Nickname != nil {
		builder.add("nickname", *update.Nickname)
	}
	if update.Username != nil {
		builder.add("username", *update.Username)
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_username_idx" {
				return User{}, AlreadyExists("username")
			}
			return User{}, AlreadyExists("user")
		}
		return User{}, err
	}
	return user, nil
}

func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM users WHERE identity_id = $1`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return NotFound("user")
	}
	return nil
}

func (s *Store) ListUsers(ctx context.Context, pageSize int32, cursor *PageCursor) (UserListResult, error) {
	users, nextCursor, err := listEntities(ctx, s.pool,
		fmt.Sprintf("SELECT %s FROM users", userColumns),
		"identity_id",
		nil,
		nil,
		cursor,
		pageSize,
		scanUser,
		func(user User) uuid.UUID { return user.Meta.ID },
	)
	if err != nil {
		return UserListResult{}, err
	}
	return UserListResult{Users: users, NextCursor: nextCursor}, nil
}

func (s *Store) SearchUsers(ctx context.Context, prefix string, limit int32) ([]UserDirectoryEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT identity_id, username, name, photo_url FROM users
        WHERE username IS NOT NULL AND username <> '' AND username LIKE $1 || '%'
        ORDER BY (username = $1) DESC, username ASC
        LIMIT $2`,
		prefix,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []UserDirectoryEntry{}
	for rows.Next() {
		entry, err := scanUserDirectoryEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) CreateAPIToken(ctx context.Context, input CreateAPITokenInput) (APIToken, error) {
	row := s.pool.QueryRow(ctx,
		fmt.Sprintf(`INSERT INTO user_api_tokens (identity_id, name, token_hash, token_prefix, expires_at)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING %s`, apiTokenColumns),
		input.IdentityID,
		input.Name,
		input.TokenHash,
		input.TokenPrefix,
		input.ExpiresAt,
	)
	apiToken, err := scanAPIToken(row)
	if err != nil {
		return APIToken{}, err
	}
	return apiToken, nil
}

func (s *Store) ListAPITokens(ctx context.Context, identityID uuid.UUID) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM user_api_tokens WHERE identity_id = $1 ORDER BY created_at DESC`, apiTokenColumns),
		identityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := []APIToken{}
	for rows.Next() {
		token, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tokens, nil
}

func (s *Store) RevokeAPIToken(ctx context.Context, identityID uuid.UUID, tokenID uuid.UUID) error {
	result, err := s.pool.Exec(ctx,
		`DELETE FROM user_api_tokens WHERE id = $1 AND identity_id = $2`,
		tokenID,
		identityID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return NotFound("api token")
	}
	return nil
}

func (s *Store) ResolveAPIToken(ctx context.Context, tokenHash string) (APIToken, error) {
	// Skip last_used_at updates for expired tokens; expiration is enforced in the server layer.
	row := s.pool.QueryRow(ctx,
		fmt.Sprintf(`UPDATE user_api_tokens SET last_used_at = NOW()
        WHERE token_hash = $1 AND (expires_at IS NULL OR expires_at > NOW())
        RETURNING %s`, apiTokenColumns),
		tokenHash,
	)
	apiToken, err := scanAPIToken(row)
	if err == nil {
		return apiToken, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return APIToken{}, err
	}

	row = s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM user_api_tokens WHERE token_hash = $1`, apiTokenColumns),
		tokenHash,
	)
	apiToken, err = scanAPIToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIToken{}, NotFound("api token")
		}
		return APIToken{}, err
	}

	return apiToken, nil
}

func (s *Store) CreateDevice(ctx context.Context, input CreateDeviceInput) (Device, error) {
	row := s.pool.QueryRow(ctx,
		fmt.Sprintf(`INSERT INTO user_devices (identity_id, name, openziti_identity_id, enrollment_jwt, status)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING %s`, deviceColumns),
		input.IdentityID,
		input.Name,
		input.OpenZitiIdentityID,
		input.EnrollmentJWT,
		DeviceStatusPending,
	)
	device, err := scanDevice(row)
	if err != nil {
		return Device{}, err
	}
	return device, nil
}

func (s *Store) ListDevices(ctx context.Context, identityID uuid.UUID, pageSize int32, cursor *PageCursor) (DeviceListResult, error) {
	devices, nextCursor, err := listEntities(ctx, s.pool,
		fmt.Sprintf("SELECT %s FROM user_devices", deviceColumns),
		"id",
		[]string{"identity_id = $1"},
		[]any{identityID},
		cursor,
		pageSize,
		scanDevice,
		func(device Device) uuid.UUID { return device.ID },
	)
	if err != nil {
		return DeviceListResult{}, err
	}
	return DeviceListResult{Devices: devices, NextCursor: nextCursor}, nil
}

func (s *Store) DeleteDevice(ctx context.Context, identityID uuid.UUID, deviceID uuid.UUID) (Device, error) {
	row := s.pool.QueryRow(ctx,
		fmt.Sprintf(`DELETE FROM user_devices WHERE id = $1 AND identity_id = $2 RETURNING %s`, deviceColumns),
		deviceID,
		identityID,
	)
	device, err := scanDevice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Device{}, NotFound("device")
		}
		return Device{}, err
	}
	return device, nil
}
