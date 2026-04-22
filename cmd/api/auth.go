package main

import (
	"net/http"

	"github.com/mysecodgit/go_accounting/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email,max=190"`
	Password string `json:"password" validate:"required,min=6,max=200"`
}

type loginUserResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type loginResponse struct {
	Token string            `json:"token"`
	User  loginUserResponse `json:"user"`
}

func (app *application) loginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	u, err := app.store.User.GetByEmail(r.Context(), req.Email)
	if err != nil {
		if err == store.ErrNotFound {
			app.unauthorizedErrorResponse(w, r, err)
			return
		}
		app.internalServerError(w, r, err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		app.unauthorizedErrorResponse(w, r, err)
		return
	}

	token, err := app.issueJWT(u.ID, u.Email, u.Role)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}

	out := loginResponse{
		Token: token,
		User: loginUserResponse{
			ID:    u.ID,
			Name:  u.Name,
			Email: u.Email,
			Role:  u.Role,
		},
	}

	if err := app.jsonResponse(w, http.StatusOK, out); err != nil {
		app.internalServerError(w, r, err)
	}
}
