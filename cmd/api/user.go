package main

import (
	"net/http"

	"github.com/mysecodgit/go_accounting/internal/store"
)

func (app *application) getUsersHandler(w http.ResponseWriter, r *http.Request) {

	users, err := app.store.User.GetAll(r.Context())

	if err != nil {
		switch err {
		case store.ErrNotFound:
			app.notFoundError(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}

		return;

	}

	if err := app.jsonResponse(w, http.StatusOK, users); err != nil {
		app.internalServerError(w, r, err)
	}
}
