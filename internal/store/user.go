package store

import (
	"context"
	"database/sql"
	"errors"
)

type User struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	PasswordHash string `json:"-"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type UserStore struct {
	db *sql.DB
}

func (s *UserStore) GetAll(ctx context.Context) ([]User, error) {
	query := `
		SELECT id, name, email, role, created_at
		FROM users
		ORDER BY id ASC
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.CreatedAt)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	return users, nil
}

// GetByEmail loads one user including password hash (for login only).
func (s *UserStore) GetByEmail(ctx context.Context, email string) (User, error) {
	query := `
		SELECT id, name, email, role, password
		FROM users
		WHERE email = ?
		LIMIT 1
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	var u User
	err := s.db.QueryRowContext(ctx, query, email).Scan(
		&u.ID,
		&u.Name,
		&u.Email,
		&u.Role,
		&u.PasswordHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}

	return u, nil
}
