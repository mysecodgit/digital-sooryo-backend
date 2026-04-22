package main

import (
	"net/http"

	"github.com/mysecodgit/go_accounting/internal/store"
)

type ClaimQrRequest struct {
	Token string `json:"token" validate:"required,max=64"`
	Phone string `json:"phone" validate:"required,min=7,max=40"`
}

func (app *application) claimQrCodeHandler(w http.ResponseWriter, r *http.Request) {
	var req ClaimQrRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	result, err := app.store.Qrcode.ClaimQrCode(r.Context(), req.Phone, req.Token)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		case store.ErrQrCodeAlreadyClaimed, store.ErrQrCodeInactive, store.ErrQrCodeExpired:
			app.conflictResponse(w, r, err)
		case store.ErrConflict:
			app.conflictResponse(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	if err := app.jsonResponse(w, http.StatusCreated, result); err != nil {
		app.internalServerError(w, r, err)
	}
}
