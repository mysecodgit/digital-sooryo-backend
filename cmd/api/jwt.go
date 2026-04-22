package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mysecodgit/go_accounting/internal/store"
)

type ctxKey int

const authenticatedUserIDKey ctxKey = 1

func contextWithAuthenticatedUserID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, authenticatedUserIDKey, id)
}

func authenticatedUserIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(authenticatedUserIDKey).(int64)
	return v, ok
}

func (app *application) issueJWT(userID int64, email, role string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", userID),
		"email": email,
		"role":  role,
		"iat":   now.Unix(),
		"exp":   now.Add(7 * 24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(app.config.jwtSecret))
}

func (app *application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "Bearer") {
			app.unauthorizedErrorResponse(w, r, fmt.Errorf("missing bearer token"))
			return
		}

		raw := strings.TrimSpace(parts[1])
		if raw == "" {
			app.unauthorizedErrorResponse(w, r, fmt.Errorf("empty token"))
			return
		}

		tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(app.config.jwtSecret), nil
		})
		if err != nil || !tok.Valid {
			app.unauthorizedErrorResponse(w, r, err)
			return
		}

		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			app.unauthorizedErrorResponse(w, r, fmt.Errorf("invalid claims"))
			return
		}

		sub, _ := claims["sub"].(string)
		uid, err := strconv.ParseInt(sub, 10, 64)
		if err != nil || uid < 1 {
			app.unauthorizedErrorResponse(w, r, fmt.Errorf("invalid subject"))
			return
		}

		ctx := contextWithAuthenticatedUserID(r.Context(), uid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (app *application) requireURLUserIDMatchesJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlUID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
		if err != nil {
			app.badRequestError(w, r, err)
			return
		}

		jwtUID, ok := authenticatedUserIDFromContext(r.Context())
		if !ok || jwtUID != urlUID {
			if err := writeJSONError(w, http.StatusForbidden, "you cannot access another user"); err != nil {
				app.logger.Error(err)
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (app *application) requireWeddingOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		weddingID, err := strconv.ParseInt(chi.URLParam(r, "weddingID"), 10, 64)
		if err != nil {
			app.badRequestError(w, r, err)
			return
		}

		jwtUID, ok := authenticatedUserIDFromContext(r.Context())
		if !ok {
			app.unauthorizedErrorResponse(w, r, fmt.Errorf("not authenticated"))
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

		if wed.UserID != jwtUID {
			if err := writeJSONError(w, http.StatusForbidden, "you do not own this wedding"); err != nil {
				app.logger.Error(err)
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}
