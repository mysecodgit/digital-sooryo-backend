package main

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/mysecodgit/go_accounting/internal/store"
)

func (app *application) getWeddingsHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}

	weddings, err := app.store.Wedding.GetByUserID(r.Context(), userID)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}

	if err := app.jsonResponse(w, http.StatusOK, weddings); err != nil {
		app.internalServerError(w, r, err)
	}
}

type publicWeddingResponse struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	AmountPerQr float64 `json:"amount_per_qr"`
}

func (app *application) getPublicWeddingHandler(w http.ResponseWriter, r *http.Request) {
	weddingID, err := strconv.ParseInt(chi.URLParam(r, "weddingID"), 10, 64)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}

	wed, err := app.store.Wedding.GetByID(r.Context(), weddingID)
	if err != nil {
		if err == store.ErrNotFound {
			app.notFoundMessage(w, r, "wedding not found")
			return
		}
		app.internalServerError(w, r, err)
		return
	}

	out := publicWeddingResponse{
		ID:          wed.ID,
		Name:        wed.Name,
		AmountPerQr: wed.AmountPerQr,
	}

	if err := app.jsonResponse(w, http.StatusOK, out); err != nil {
		app.internalServerError(w, r, err)
	}
}

type CreateWeddingRequest struct {
	Name        string  `json:"name" validate:"required,max=160"`
	HostName    string  `json:"host_name" validate:"required,max=160"`
	EventDate   string  `json:"event_date" validate:"required"`
	Location    string  `json:"location" validate:"required,max=200"`
	AmountPerQr float64 `json:"amount_per_qr" validate:"required,gte=0"`
	TotalQr     int     `json:"total_qr" validate:"gte=0"`
}

func (app *application) createWeddingHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := authenticatedUserIDFromContext(r.Context())
	if !ok {
		app.unauthorizedErrorResponse(w, r, nil)
		return
	}

	var req CreateWeddingRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	wedding := &store.Wedding{
		UserID:      ownerID,
		Name:        req.Name,
		HostName:    req.HostName,
		EventDate:   req.EventDate,
		Location:    req.Location,
		AmountPerQr: req.AmountPerQr,
		TotalQr:     req.TotalQr,
	}

	if err := app.store.Wedding.Create(r.Context(), wedding); err != nil {
		app.internalServerError(w, r, err)
		return
	}

	if err := app.jsonResponse(w, http.StatusCreated, wedding); err != nil {
		app.internalServerError(w, r, err)
	}
}

type UpdateWeddingRequest struct {
	ID          int64   `json:"id" validate:"required,gt=0"`
	Name        string  `json:"name" validate:"required,max=160"`
	HostName    string  `json:"host_name" validate:"required,max=160"`
	EventDate   string  `json:"event_date" validate:"required"`
	Location    string  `json:"location" validate:"required,max=200"`
	AmountPerQr float64 `json:"amount_per_qr" validate:"required,gte=0"`
	TotalQr     int     `json:"total_qr" validate:"gte=0"`
}

func (app *application) updateWeddingHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := authenticatedUserIDFromContext(r.Context())
	if !ok {
		app.unauthorizedErrorResponse(w, r, nil)
		return
	}

	var req UpdateWeddingRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	wedding := store.Wedding{
		ID:          req.ID,
		Name:        req.Name,
		HostName:    req.HostName,
		EventDate:   req.EventDate,
		Location:    req.Location,
		AmountPerQr: req.AmountPerQr,
		TotalQr:     req.TotalQr,
	}

	if err := app.store.Wedding.Update(r.Context(), wedding, ownerID); err != nil {
		if err == store.ErrNotFound {
			app.notFoundMessage(w, r, "wedding not found")
			return
		}
		app.internalServerError(w, r, err)
		return
	}

	if err := app.jsonResponse(w, http.StatusOK, wedding); err != nil {
		app.internalServerError(w, r, err)
	}
}
