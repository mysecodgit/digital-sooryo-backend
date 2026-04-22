package store

import (
	"context"
	"database/sql"
	"errors"
)

type Wedding struct {
	ID          int64   `json:"id"`
	UserID      int64   `json:"user_id"`
	Name        string  `json:"name"`
	HostName    string  `json:"host_name"`
	EventDate   string  `json:"event_date"`
	Location    string  `json:"location"`
	AmountPerQr float64 `json:"amount_per_qr"`
	TotalQr     int     `json:"total_qr"`
}

type WeddingStore struct {
	db *sql.DB
}

func (s *WeddingStore) GetByUserID(ctx context.Context, userID int64) ([]Wedding, error) {
	query := `
		SELECT id, user_id, name, host_name, event_date, location, amount_per_qr, total_qr
		FROM weddings
		WHERE user_id = ?
		ORDER BY event_date DESC, id DESC
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var weddings []Wedding
	for rows.Next() {
		var w Wedding
		err := rows.Scan(
			&w.ID,
			&w.UserID,
			&w.Name,
			&w.HostName,
			&w.EventDate,
			&w.Location,
			&w.AmountPerQr,
			&w.TotalQr,
		)
		if err != nil {
			return nil, err
		}
		weddings = append(weddings, w)
	}

	return weddings, nil
}

func (s *WeddingStore) GetByID(ctx context.Context, id int64) (Wedding, error) {
	query := `
		SELECT id, user_id, name, host_name, event_date, location, amount_per_qr, total_qr
		FROM weddings
		WHERE id = ?
		LIMIT 1
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	var w Wedding
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&w.ID,
		&w.UserID,
		&w.Name,
		&w.HostName,
		&w.EventDate,
		&w.Location,
		&w.AmountPerQr,
		&w.TotalQr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Wedding{}, ErrNotFound
	}
	if err != nil {
		return Wedding{}, err
	}

	return w, nil
}

func (s *WeddingStore) Create(ctx context.Context, w *Wedding) error {
	query := `
		INSERT INTO weddings (user_id, name, host_name, event_date, location, amount_per_qr, total_qr)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	res, err := s.db.ExecContext(ctx, query, w.UserID, w.Name, w.HostName, w.EventDate, w.Location, w.AmountPerQr, w.TotalQr)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	w.ID = id
	return nil
}

func (s *WeddingStore) Update(ctx context.Context, w Wedding, ownerUserID int64) error {
	query := `
		UPDATE weddings
		SET name = ?, host_name = ?, event_date = ?, location = ?, amount_per_qr = ?, total_qr = ?
		WHERE id = ? AND user_id = ?
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	res, err := s.db.ExecContext(ctx, query, w.Name, w.HostName, w.EventDate, w.Location, w.AmountPerQr, w.TotalQr, w.ID, ownerUserID)
	if err != nil {
		return err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}

	return nil
}
