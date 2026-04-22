package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

type Qrcode struct {
	ID           int64    `json:"id"`
	SerialNumber string   `json:"serial_number"`
	WeddingID    *int64   `json:"wedding_id"`
	Token        string   `json:"token"`
	Amount       float64  `json:"amount"`
	ActiveFrom   *string  `json:"active_from"`
	ActiveTo     *string  `json:"active_to"`
	ActivatedAt  *string  `json:"activated_at"`
	IsClaimed    bool     `json:"is_claimed"`
	ClaimedPhone *string  `json:"claimed_phone"`
	ClaimedAt    *string  `json:"claimed_at"`
	CreatedAt    string   `json:"created_at"`
}

type QrcodeStore struct {
	db *sql.DB
}

type PublicQrCode struct {
	SerialNumber string   `json:"serial_number"`
	Amount       float64  `json:"amount"`
	Status       string   `json:"status"` // inactive | active | expired | claimed
	ActiveFrom   *string  `json:"active_from"`
	ActiveTo     *string  `json:"active_to"`
	ClaimedPhone *string  `json:"claimed_phone"`
	ClaimedAt    *string  `json:"claimed_at"`
}

// ClaimResult is returned after a successful claim.
type ClaimResult struct {
	ClaimID      int64   `json:"claim_id"`
	SerialNumber string  `json:"serial_number"`
	Amount       float64 `json:"amount"`
	Phone        string  `json:"phone"`
}

// ClaimQrCode records a claim for the given QR token and phone number.
func (s *QrcodeStore) ClaimQrCode(ctx context.Context, phone, token string) (*ClaimResult, error) {
	phone = strings.TrimSpace(phone)
	token = strings.TrimSpace(token)
	if phone == "" || token == "" {
		return nil, ErrQrCodeNotFound
	}

	var out ClaimResult

	err := withTx(s.db, ctx, func(tx *sql.Tx) error {
		var qrID int64
		var serial string
		var isClaimed bool
		var amount float64
		var activeFrom sql.NullTime
		var activeTo sql.NullTime

		err := tx.QueryRowContext(ctx, `
			SELECT id, serial_number, amount, active_from, active_to, is_claimed
			FROM qr_codes
			WHERE token = ?
			LIMIT 1
		`, token).Scan(&qrID, &serial, &amount, &activeFrom, &activeTo, &isClaimed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrQrCodeNotFound
		}
		if err != nil {
			return err
		}
		if isClaimed {
			return ErrQrCodeAlreadyClaimed
		}

		// A QR code is claimable only when it is within an active time window.
		now := time.Now()
		if !activeFrom.Valid || !activeTo.Valid {
			return ErrQrCodeInactive
		}
		if now.Before(activeFrom.Time) {
			return ErrQrCodeInactive
		}
		if now.After(activeTo.Time) {
			return ErrQrCodeExpired
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO claims (qr_id, phone, amount)
			VALUES (?, ?, ?)
		`, qrID, phone, amount)
		if err != nil {
			var me *mysql.MySQLError
			if errors.As(err, &me) && me.Number == 1062 {
				msg := strings.ToLower(me.Message)
				if strings.Contains(msg, "uq_claim_qr") {
					return ErrQrCodeAlreadyClaimed
				}
				return ErrConflict
			}
			return err
		}

		claimID, err := res.LastInsertId()
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE qr_codes
			SET is_claimed = 1, claimed_at = NOW(), claimed_phone = ?
			WHERE id = ?
		`, phone, qrID)
		if err != nil {
			return err
		}

		out = ClaimResult{
			ClaimID:      claimID,
			SerialNumber: serial,
			Amount:       amount,
			Phone:        phone,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

func serialFromUUID() string {
	// Short human-friendly serial; uniqueness is enforced by a DB UNIQUE constraint.
	return strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", "")[:12])
}

// GenerateBatchQrCodes creates inactive QR codes that can later be activated.
func (s *QrcodeStore) GenerateBatchQrCodes(ctx context.Context, count int, amount float64) ([]Qrcode, error) {
	query := `
		INSERT INTO qr_codes (serial_number, token, amount, active_from, active_to, activated_at, is_claimed, claimed_at, created_at)
		VALUES (?, ?, ?, NULL, NULL, NULL, 0, NULL, NOW())
	`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	out := make([]Qrcode, 0, count)
	err := withTx(s.db, ctx, func(tx *sql.Tx) error {
		for i := 0; i < count; i++ {
			token := uuid.New().String()
			// Insert a temporary unique serial, then replace with a sequential one based on the row ID.
			tmpSerial := "TMP" + strings.ToUpper(strings.ReplaceAll(uuid.New().String(), "-", "")[:16])
			res, err := tx.ExecContext(ctx, query, tmpSerial, token, amount)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}

			// Sequential serial number like 0001, 0002, ...
			_, err = tx.ExecContext(ctx, `UPDATE qr_codes SET serial_number = LPAD(id, 4, '0') WHERE id = ?`, id)
			if err != nil {
				return err
			}

			var serial string
			err = tx.QueryRowContext(ctx, `SELECT serial_number FROM qr_codes WHERE id = ?`, id).Scan(&serial)
			if err != nil {
				return err
			}

			out = append(out, Qrcode{
				ID:           id,
				SerialNumber: serial,
				Token:        token,
				Amount:       amount,
				ActiveFrom:   nil,
				ActiveTo:     nil,
				ActivatedAt:  nil,
				IsClaimed:    false,
				ClaimedPhone: nil,
				ClaimedAt:    nil,
				CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Activate sets an active time window for selected QR codes.
func (s *QrcodeStore) Activate(ctx context.Context, ids []int64, activeFrom, activeTo time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	if !activeTo.After(activeFrom) {
		return errors.New("active_to must be after active_from")
	}

	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, 2+len(ids))
	args = append(args, activeFrom, activeTo)
	for range ids {
		placeholders = append(placeholders, "?")
	}
	for _, id := range ids {
		args = append(args, id)
	}

	query := `
		UPDATE qr_codes
		SET active_from = ?, active_to = ?, activated_at = NOW()
		WHERE is_claimed = 0 AND id IN (` + strings.Join(placeholders, ",") + `)
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// AssignToWedding assigns selected QR codes to a wedding.
func (s *QrcodeStore) AssignToWedding(ctx context.Context, ids []int64, weddingID int64) error {
	if len(ids) == 0 {
		return nil
	}
	if weddingID < 1 {
		return errors.New("invalid wedding id")
	}

	placeholders := make([]string, 0, len(ids))
	idArgs := make([]any, 0, len(ids))
	for range ids {
		placeholders = append(placeholders, "?")
	}
	for _, id := range ids {
		idArgs = append(idArgs, id)
	}

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	// Reject if any selected code is claimed.
	var claimedCount int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM qr_codes
		WHERE is_claimed = 1 AND id IN (`+strings.Join(placeholders, ",")+`)
	`, idArgs...).Scan(&claimedCount)
	if err != nil {
		return err
	}
	if claimedCount > 0 {
		return ErrQrCodeAlreadyClaimed
	}

	// Reject if any selected code is assigned to a different wedding.
	conflictArgs := make([]any, 0, 1+len(idArgs))
	conflictArgs = append(conflictArgs, weddingID)
	conflictArgs = append(conflictArgs, idArgs...)
	var assignedElsewhere int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM qr_codes
		WHERE wedding_id IS NOT NULL AND wedding_id <> ? AND id IN (`+strings.Join(placeholders, ",")+`)
	`, conflictArgs...).Scan(&assignedElsewhere)
	if err != nil {
		return err
	}
	if assignedElsewhere > 0 {
		return ErrQrCodeAlreadyAssigned
	}

	// Assign only unassigned rows (and not claimed). Re-assigning to same wedding is a no-op.
	assignArgs := make([]any, 0, 1+len(idArgs))
	assignArgs = append(assignArgs, weddingID)
	assignArgs = append(assignArgs, idArgs...)
	_, err = s.db.ExecContext(ctx, `
		UPDATE qr_codes
		SET wedding_id = ?
		WHERE is_claimed = 0 AND wedding_id IS NULL AND id IN (`+strings.Join(placeholders, ",")+`)
	`, assignArgs...)
	return err
}

// UnassignFromWedding removes wedding assignment from selected QR codes.
// It refuses to operate on claimed codes, and only allows unassigning codes whose wedding is owned by ownerUserID.
func (s *QrcodeStore) UnassignFromWedding(ctx context.Context, ids []int64, ownerUserID int64) error {
	if len(ids) == 0 {
		return nil
	}
	if ownerUserID < 1 {
		return errors.New("invalid owner user id")
	}

	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for range ids {
		placeholders = append(placeholders, "?")
	}
	for _, id := range ids {
		args = append(args, id)
	}

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	// Reject if any selected code is claimed.
	var claimedCount int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM qr_codes
		WHERE is_claimed = 1 AND id IN (`+strings.Join(placeholders, ",")+`)
	`, args...).Scan(&claimedCount)
	if err != nil {
		return err
	}
	if claimedCount > 0 {
		return ErrQrCodeAlreadyClaimed
	}

	// Reject if any selected code is assigned to a wedding not owned by this user.
	ownerArgs := make([]any, 0, 1+len(args))
	ownerArgs = append(ownerArgs, ownerUserID)
	ownerArgs = append(ownerArgs, args...)
	var notOwned int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM qr_codes qc
		JOIN weddings w ON w.id = qc.wedding_id
		WHERE w.user_id <> ? AND qc.id IN (`+strings.Join(placeholders, ",")+`)
	`, ownerArgs...).Scan(&notOwned)
	if err != nil {
		return err
	}
	if notOwned > 0 {
		return errors.New("cannot unassign qr codes from a wedding you do not own")
	}

	// Unassign (only affects rows that are currently assigned).
	_, err = s.db.ExecContext(ctx, `
		UPDATE qr_codes
		SET wedding_id = NULL
		WHERE is_claimed = 0 AND id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	return err
}

func (s *QrcodeStore) GetByIDs(ctx context.Context, ids []int64) ([]Qrcode, error) {
	if len(ids) == 0 {
		return []Qrcode{}, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for range ids {
		placeholders = append(placeholders, "?")
	}
	for _, id := range ids {
		args = append(args, id)
	}

	query := `
		SELECT id, serial_number, wedding_id, token, amount, active_from, active_to, activated_at, is_claimed, claimed_phone, claimed_at, created_at
		FROM qr_codes
		WHERE id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY serial_number ASC
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qrCodes []Qrcode
	for rows.Next() {
		var qrcode Qrcode
		var weddingID sql.NullInt64
		var activeFrom sql.NullTime
		var activeTo sql.NullTime
		var activatedAt sql.NullTime
		var claimedPhone sql.NullString
		var claimedAt sql.NullTime
		err := rows.Scan(
			&qrcode.ID,
			&qrcode.SerialNumber,
			&weddingID,
			&qrcode.Token,
			&qrcode.Amount,
			&activeFrom,
			&activeTo,
			&activatedAt,
			&qrcode.IsClaimed,
			&claimedPhone,
			&claimedAt,
			&qrcode.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if weddingID.Valid {
			v := weddingID.Int64
			qrcode.WeddingID = &v
		}
		if activeFrom.Valid {
			s := activeFrom.Time.Format("2006-01-02 15:04:05")
			qrcode.ActiveFrom = &s
		}
		if activeTo.Valid {
			s := activeTo.Time.Format("2006-01-02 15:04:05")
			qrcode.ActiveTo = &s
		}
		if activatedAt.Valid {
			s := activatedAt.Time.Format("2006-01-02 15:04:05")
			qrcode.ActivatedAt = &s
		}
		if claimedAt.Valid {
			s := claimedAt.Time.Format("2006-01-02 15:04:05")
			qrcode.ClaimedAt = &s
		}
		if claimedPhone.Valid {
			s := strings.TrimSpace(claimedPhone.String)
			if s != "" {
				qrcode.ClaimedPhone = &s
			}
		}
		qrCodes = append(qrCodes, qrcode)
	}

	return qrCodes, nil
}

// GetBySerialRange returns QR codes by serial range (inclusive). Serial numbers are compared
// lexicographically; for zero-padded serials like 0001..9999 this matches numeric order.
func (s *QrcodeStore) GetBySerialRange(ctx context.Context, fromSerial, toSerial string, onlyUnassigned bool) ([]Qrcode, error) {
	fromSerial = strings.TrimSpace(fromSerial)
	toSerial = strings.TrimSpace(toSerial)
	if fromSerial == "" || toSerial == "" {
		return nil, errors.New("from and to serial are required")
	}
	if fromSerial > toSerial {
		return nil, errors.New("from serial must be <= to serial")
	}

	query := `
		SELECT id, serial_number, wedding_id, token, amount, active_from, active_to, activated_at, is_claimed, claimed_phone, claimed_at, created_at
		FROM qr_codes
		WHERE serial_number >= ? AND serial_number <= ?
	`
	args := []any{fromSerial, toSerial}
	if onlyUnassigned {
		query += ` AND wedding_id IS NULL`
	}
	query += ` ORDER BY serial_number ASC`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qrCodes []Qrcode
	for rows.Next() {
		var qrcode Qrcode
		var weddingID sql.NullInt64
		var activeFrom sql.NullTime
		var activeTo sql.NullTime
		var activatedAt sql.NullTime
		var claimedPhone sql.NullString
		var claimedAt sql.NullTime

		err := rows.Scan(
			&qrcode.ID,
			&qrcode.SerialNumber,
			&weddingID,
			&qrcode.Token,
			&qrcode.Amount,
			&activeFrom,
			&activeTo,
			&activatedAt,
			&qrcode.IsClaimed,
			&claimedPhone,
			&claimedAt,
			&qrcode.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if weddingID.Valid {
			v := weddingID.Int64
			qrcode.WeddingID = &v
		}
		if activeFrom.Valid {
			s := activeFrom.Time.Format("2006-01-02 15:04:05")
			qrcode.ActiveFrom = &s
		}
		if activeTo.Valid {
			s := activeTo.Time.Format("2006-01-02 15:04:05")
			qrcode.ActiveTo = &s
		}
		if activatedAt.Valid {
			s := activatedAt.Time.Format("2006-01-02 15:04:05")
			qrcode.ActivatedAt = &s
		}
		if claimedAt.Valid {
			s := claimedAt.Time.Format("2006-01-02 15:04:05")
			qrcode.ClaimedAt = &s
		}
		if claimedPhone.Valid {
			s := strings.TrimSpace(claimedPhone.String)
			if s != "" {
				qrcode.ClaimedPhone = &s
			}
		}

		qrCodes = append(qrCodes, qrcode)
	}

	return qrCodes, nil
}

// GetPublicByToken returns public info used by the claim page.
func (s *QrcodeStore) GetPublicByToken(ctx context.Context, token string) (PublicQrCode, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return PublicQrCode{}, ErrQrCodeNotFound
	}

	query := `
		SELECT serial_number, amount, active_from, active_to, is_claimed, claimed_phone, claimed_at
		FROM qr_codes
		WHERE token = ?
		LIMIT 1
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	var serial string
	var amount float64
	var activeFrom sql.NullTime
	var activeTo sql.NullTime
	var claimed bool
	var claimedPhone sql.NullString
	var claimedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, token).Scan(&serial, &amount, &activeFrom, &activeTo, &claimed, &claimedPhone, &claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicQrCode{}, ErrQrCodeNotFound
	}
	if err != nil {
		return PublicQrCode{}, err
	}

	status := "inactive"
	now := time.Now()
	if claimed {
		status = "claimed"
	} else if activeFrom.Valid && activeTo.Valid {
		if now.After(activeTo.Time) {
			status = "expired"
		} else if !now.Before(activeFrom.Time) && !now.After(activeTo.Time) {
			status = "active"
		} else {
			status = "inactive"
		}
	}

	var af, at, ca *string
	if activeFrom.Valid {
		s := activeFrom.Time.Format("2006-01-02 15:04:05")
		af = &s
	}
	if activeTo.Valid {
		s := activeTo.Time.Format("2006-01-02 15:04:05")
		at = &s
	}
	if claimedAt.Valid {
		s := claimedAt.Time.Format("2006-01-02 15:04:05")
		ca = &s
	}
	var cp *string
	if claimedPhone.Valid {
		s := claimedPhone.String
		if strings.TrimSpace(s) != "" {
			cp = &s
		}
	}

	return PublicQrCode{
		SerialNumber: serial,
		Amount:       amount,
		Status:       status,
		ActiveFrom:   af,
		ActiveTo:     at,
		ClaimedPhone: cp,
		ClaimedAt:    ca,
	}, nil
}

// GetAll returns all QR codes (most recent first).
func (s *QrcodeStore) GetAll(ctx context.Context) ([]Qrcode, error) {
	query := `
		SELECT id, serial_number, wedding_id, token, amount, active_from, active_to, activated_at, is_claimed, claimed_phone, claimed_at, created_at
		FROM qr_codes
		ORDER BY id DESC
	`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qrCodes []Qrcode
	for rows.Next() {
		var qrcode Qrcode
		var weddingID sql.NullInt64
		var activeFrom sql.NullTime
		var activeTo sql.NullTime
		var activatedAt sql.NullTime
		var claimedPhone sql.NullString
		var claimedAt sql.NullTime
		err := rows.Scan(
			&qrcode.ID,
			&qrcode.SerialNumber,
			&weddingID,
			&qrcode.Token,
			&qrcode.Amount,
			&activeFrom,
			&activeTo,
			&activatedAt,
			&qrcode.IsClaimed,
			&claimedPhone,
			&claimedAt,
			&qrcode.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if weddingID.Valid {
			v := weddingID.Int64
			qrcode.WeddingID = &v
		}
		if activeFrom.Valid {
			s := activeFrom.Time.Format("2006-01-02 15:04:05")
			qrcode.ActiveFrom = &s
		}
		if activeTo.Valid {
			s := activeTo.Time.Format("2006-01-02 15:04:05")
			qrcode.ActiveTo = &s
		}
		if activatedAt.Valid {
			s := activatedAt.Time.Format("2006-01-02 15:04:05")
			qrcode.ActivatedAt = &s
		}
		if claimedAt.Valid {
			s := claimedAt.Time.Format("2006-01-02 15:04:05")
			qrcode.ClaimedAt = &s
		}
		if claimedPhone.Valid {
			s := strings.TrimSpace(claimedPhone.String)
			if s != "" {
				qrcode.ClaimedPhone = &s
			}
		}
		qrCodes = append(qrCodes, qrcode)
	}
	return qrCodes, nil
}
