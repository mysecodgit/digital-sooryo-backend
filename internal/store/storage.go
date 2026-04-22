package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("resource not found")
	QueryTimeOutDuration = time.Second * 5
	ErrConflict          = errors.New("resource already exists")

	// QR claim flow
	ErrQrCodeNotFound                = errors.New("qr code not found")
	ErrQrCodeAlreadyClaimed          = errors.New("this qr code has already been claimed")
	ErrQrCodeInactive                = errors.New("this qr code is not active")
	ErrQrCodeExpired                 = errors.New("this qr code has expired")
	ErrQrCodeAlreadyAssigned         = errors.New("this qr code is already assigned to another wedding")
)

type Storage struct {
	User interface {
		GetAll(context.Context) ([]User, error)
		GetByEmail(context.Context, string) (User, error)
	}
	Wedding interface {
		GetByUserID(context.Context, int64) ([]Wedding, error)
		GetByID(context.Context, int64) (Wedding, error)
		Create(context.Context, *Wedding) error
		Update(context.Context, Wedding, int64) error
	}
	Qrcode interface {
		GenerateBatchQrCodes(context.Context, int, float64) ([]Qrcode, error)
		GetAll(context.Context) ([]Qrcode, error)
		GetByIDs(ctx context.Context, ids []int64) ([]Qrcode, error)
		GetBySerialRange(ctx context.Context, fromSerial, toSerial string, onlyUnassigned bool) ([]Qrcode, error)
		Activate(ctx context.Context, ids []int64, activeFrom, activeTo time.Time) error
		AssignToWedding(ctx context.Context, ids []int64, weddingID int64) error
		UnassignFromWedding(ctx context.Context, ids []int64, ownerUserID int64) error
		GetPublicByToken(ctx context.Context, token string) (PublicQrCode, error)
		ClaimQrCode(context.Context, string, string) (*ClaimResult, error)
	}
}

func NewStorage(db *sql.DB) Storage {
	return Storage{
		User:    &UserStore{db},
		Wedding: &WeddingStore{db},
		Qrcode:  &QrcodeStore{db},
	}
}

func withTx(db *sql.DB, ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}
