package store

import (
	"context"
	"database/sql"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

type Qrcode struct {
	ID           int64   `json:"id"`
	SerialNumber string  `json:"serial_number"`
	ShortCode    string  `json:"short_code"`
	WeddingID    *int64  `json:"wedding_id"`
	Token        string  `json:"token"`
	Amount       float64 `json:"amount"`
	ActiveFrom   *string `json:"active_from"`
	ActiveTo     *string `json:"active_to"`
	ActivatedAt  *string `json:"activated_at"`
	IsClaimed    bool    `json:"is_claimed"`
	ClaimedPhone *string `json:"claimed_phone"`
	ClaimedAt    *string `json:"claimed_at"`
	CreatedAt    string  `json:"created_at"`
}

type QrcodeStore struct {
	db *sql.DB
}

const shortCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomShortCode(n int) (string, error) {
	if n < 1 {
		n = 1
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = shortCodeAlphabet[int(b[i])%len(shortCodeAlphabet)]
	}
	return string(out), nil
}

func (s *QrcodeStore) ensureRandomShortCode6(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrQrCodeNotFound
	}

	// Read current code.
	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	var current sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT short_code FROM qr_codes WHERE token = ? LIMIT 1`, token).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrQrCodeNotFound
	}
	if err != nil {
		return "", err
	}
	cur := strings.TrimSpace(current.String)
	if len(cur) == 6 {
		return cur, nil
	}

	// Upgrade (or fill) to a random 6-char code, retry on unique collisions.
	for tries := 0; tries < 300; tries++ {
		sc, err := randomShortCode(6)
		if err != nil {
			return "", err
		}
		_, err = s.db.ExecContext(ctx, `UPDATE qr_codes SET short_code = ? WHERE token = ?`, sc, token)
		if err == nil {
			return sc, nil
		}
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			continue
		}
		return "", err
	}

	return "", ErrConflict
}

func (s *QrcodeStore) GetShortCodeByToken(ctx context.Context, token string) (string, error) {
	return s.ensureRandomShortCode6(ctx, token)
}

func isHexPrefix(s string) bool {
	if len(s) < 8 || len(s) > 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'f') ||
			(c >= 'A' && c <= 'F')
		if !ok {
			return false
		}
	}
	return true
}

// ResolveToken accepts:
// - full UUID token
// - base64url short token (22 chars)
// - short_code (base36 id, collision-free, UNIQUE)
// - (legacy) hex prefix (8-12 chars) like "d4c2614b"
// and resolves it to the stored UUID token.
func (s *QrcodeStore) ResolveToken(ctx context.Context, tokenOrShort string) (string, error) {
	x := strings.TrimSpace(tokenOrShort)
	if x == "" {
		return "", ErrQrCodeNotFound
	}

	// Full UUID?
	if _, err := uuid.Parse(x); err == nil {
		return x, nil
	}

	// Base64url short token?
	if b, err := base64.RawURLEncoding.DecodeString(x); err == nil && len(b) == 16 {
		if u, err := uuid.FromBytes(b); err == nil {
			return u.String(), nil
		}
	}

	// Collision-free short_code (base36 of id).
	// Prefer exact match, it's indexed and guaranteed unique.
	{
		ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
		defer cancel()

		var t string
		err := s.db.QueryRowContext(ctx, `SELECT token FROM qr_codes WHERE short_code = ? LIMIT 1`, x).Scan(&t)
		if err == nil && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t), nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	// Hex prefix?
	if !isHexPrefix(x) {
		return x, nil
	}

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `SELECT token FROM qr_codes WHERE token LIKE ? LIMIT 2`, x+"%")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	out := make([]string, 0, 2)
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return "", err
		}
		out = append(out, strings.TrimSpace(t))
	}
	if len(out) == 0 {
		return "", ErrQrCodeNotFound
	}
	if len(out) > 1 {
		// Prefix collision.
		return "", ErrConflict
	}
	if out[0] == "" {
		return "", ErrQrCodeNotFound
	}
	return out[0], nil
}

type PublicQrCode struct {
	SerialNumber string  `json:"serial_number"`
	Amount       float64 `json:"amount"`
	Status       string  `json:"status"` // inactive | active | expired | claimed
	ActiveFrom   *string `json:"active_from"`
	ActiveTo     *string `json:"active_to"`
	ClaimedPhone *string `json:"claimed_phone"`
	ClaimedAt    *string `json:"claimed_at"`
}

// ClaimResult is returned after a successful claim.
type ClaimResult struct {
	ClaimID      int64   `json:"claim_id"`
	SerialNumber string  `json:"serial_number"`
	Amount       float64 `json:"amount"`
	Phone        string  `json:"phone"`
}

// ClaimRow is returned by the claims listing endpoint.
type ClaimRow struct {
	ClaimID      int64  `json:"claim_id"`
	Phone        string `json:"phone"`
	SerialNumber string `json:"serial_number"`
	ClaimedAt    string `json:"claimed_at"`
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

// ListClaims returns claims with optional search and pagination.
// It reads from the `claims` table and joins `qr_codes` for the serial number.
func (s *QrcodeStore) ListClaims(ctx context.Context, phoneQuery, serialQuery string, page, pageSize int) ([]ClaimRow, int, error) {
	phoneQuery = strings.TrimSpace(phoneQuery)
	serialQuery = strings.TrimSpace(serialQuery)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	if pageSize > 200 {
		pageSize = 200
	}

	where := ` WHERE 1=1 `
	args := make([]any, 0, 6)
	if phoneQuery != "" {
		where += ` AND c.phone LIKE ? `
		args = append(args, "%"+phoneQuery+"%")
	}
	if serialQuery != "" {
		where += ` AND qc.serial_number LIKE ? `
		args = append(args, "%"+serialQuery+"%")
	}

	ctx, cancel := context.WithTimeout(ctx, QueryTimeOutDuration)
	defer cancel()

	// Total count
	var total int
	countQuery := `
		SELECT COUNT(*)
		FROM claims c
		JOIN qr_codes qc ON qc.id = c.qr_id
	` + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	listQuery := `
		SELECT c.id, c.phone, qc.serial_number, c.claimed_at
		FROM claims c
		JOIN qr_codes qc ON qc.id = c.qr_id
	` + where + `
		ORDER BY c.claimed_at DESC, c.id DESC
		LIMIT ? OFFSET ?
	`
	listArgs := make([]any, 0, len(args)+2)
	listArgs = append(listArgs, args...)
	listArgs = append(listArgs, pageSize, offset)

	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]ClaimRow, 0, pageSize)
	for rows.Next() {
		var r ClaimRow
		var claimedAt sql.NullTime
		if err := rows.Scan(&r.ClaimID, &r.Phone, &r.SerialNumber, &claimedAt); err != nil {
			return nil, 0, err
		}
		if claimedAt.Valid {
			r.ClaimedAt = claimedAt.Time.Format("2006-01-02 15:04:05")
		} else {
			r.ClaimedAt = ""
		}
		out = append(out, r)
	}
	return out, total, nil
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

			// Random 6-char short code (unique in DB, retry on collision).
			for tries := 0; tries < 300; tries++ {
				sc, err := randomShortCode(6)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `UPDATE qr_codes SET short_code = ? WHERE id = ?`, sc, id)
				if err == nil {
					break
				}
				var me *mysql.MySQLError
				if errors.As(err, &me) && me.Number == 1062 {
					continue
				}
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
		SELECT id, serial_number, short_code, wedding_id, token, amount, active_from, active_to, activated_at, is_claimed, claimed_phone, claimed_at, created_at
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
			&qrcode.ShortCode,
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
		SELECT id, serial_number, short_code, wedding_id, token, amount, active_from, active_to, activated_at, is_claimed, claimed_phone, claimed_at, created_at
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
			&qrcode.ShortCode,
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
		SELECT id, serial_number, short_code, wedding_id, token, amount, active_from, active_to, activated_at, is_claimed, claimed_phone, claimed_at, created_at
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
			&qrcode.ShortCode,
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
