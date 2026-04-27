package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jung-kurt/gofpdf"
	"github.com/mysecodgit/go_accounting/internal/store"
	"github.com/skip2/go-qrcode"
)

type GenerateQrCodesRequest struct {
	Count  int     `json:"count" validate:"required,gt=0,lte=500"`
	Amount float64 `json:"amount" validate:"required,gte=0"`
}

func encodeShortToken(uuidToken string) (string, bool) {
	u, err := uuid.Parse(strings.TrimSpace(uuidToken))
	if err != nil {
		return "", false
	}
	// 16 bytes => 22 chars base64url (no padding)
	return base64.RawURLEncoding.EncodeToString(u[:]), true
}

func tokenPrefix8(uuidToken string) string {
	uuidToken = strings.TrimSpace(uuidToken)
	if uuidToken == "" {
		return ""
	}
	// UUID tokens are like "aaaaaaaa-bbbb-...." – prefix before the first dash is 8 hex chars.
	if i := strings.IndexByte(uuidToken, '-'); i > 0 {
		if i >= 8 {
			return uuidToken[:8]
		}
		return uuidToken[:i]
	}
	if len(uuidToken) > 8 {
		return uuidToken[:8]
	}
	return uuidToken
}

func normalizeAbsoluteBaseURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return strings.TrimRight(s, "/")
	}
	// Default to http for localhost, https otherwise.
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "localhost") || strings.HasPrefix(low, "127.") || strings.HasPrefix(low, "0.0.0.0") {
		return "http://" + strings.TrimRight(s, "/")
	}
	return "https://" + strings.TrimRight(s, "/")
}

func (app *application) generateQrCodesHandler(w http.ResponseWriter, r *http.Request) {
	var req GenerateQrCodesRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}
	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	qrCodes, err := app.store.Qrcode.GenerateBatchQrCodes(r.Context(), req.Count, req.Amount)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}

	if err := app.jsonResponse(w, http.StatusCreated, qrCodes); err != nil {
		app.internalServerError(w, r, err)
	}
}

func (app *application) listQrCodesHandler(w http.ResponseWriter, r *http.Request) {
	qrCodes, err := app.store.Qrcode.GetAll(r.Context())
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	if err := app.jsonResponse(w, http.StatusOK, qrCodes); err != nil {
		app.internalServerError(w, r, err)
	}
}

func (app *application) listClaimsHandler(w http.ResponseWriter, r *http.Request) {
	phone := strings.TrimSpace(r.URL.Query().Get("phone"))
	serial := strings.TrimSpace(r.URL.Query().Get("serial"))
	page, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	pageSize, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page_size")))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}

	items, total, err := app.store.Qrcode.ListClaims(r.Context(), phone, serial, page, pageSize)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	totalPages := (total + pageSize - 1) / pageSize
	resp := map[string]any{
		"data": items,
		"meta": map[string]any{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
		},
	}
	if err := app.jsonResponse(w, http.StatusOK, resp); err != nil {
		app.internalServerError(w, r, err)
	}
}

type ActivateQrCodesRequest struct {
	IDs        []int64 `json:"ids" validate:"required,min=1"`
	ActiveFrom string  `json:"active_from" validate:"required"`
	ActiveTo   string  `json:"active_to" validate:"required"`
}

func parseTimeAny(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, strconv.ErrSyntax
	}
	// Accept RFC3339 (recommended) or "2006-01-02 15:04:05".
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}

func (app *application) activateQrCodesHandler(w http.ResponseWriter, r *http.Request) {
	var req ActivateQrCodesRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}
	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	activeFrom, err := parseTimeAny(req.ActiveFrom)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}
	activeTo, err := parseTimeAny(req.ActiveTo)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := app.store.Qrcode.Activate(r.Context(), req.IDs, activeFrom, activeTo); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := app.jsonResponse(w, http.StatusOK, map[string]any{"updated": len(req.IDs)}); err != nil {
		app.internalServerError(w, r, err)
	}
}

func (app *application) listQrCodesRangeHandler(w http.ResponseWriter, r *http.Request) {
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	onlyUnassigned := strings.TrimSpace(r.URL.Query().Get("unassigned")) == "1"

	qrCodes, err := app.store.Qrcode.GetBySerialRange(r.Context(), from, to, onlyUnassigned)
	if err != nil {
		app.badRequestError(w, r, err)
		return
	}
	if err := app.jsonResponse(w, http.StatusOK, qrCodes); err != nil {
		app.internalServerError(w, r, err)
	}
}

type AssignQrCodesRequest struct {
	IDs       []int64 `json:"ids" validate:"required,min=1"`
	WeddingID int64   `json:"wedding_id" validate:"required,gt=0"`
}

func (app *application) assignQrCodesHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := authenticatedUserIDFromContext(r.Context())
	if !ok {
		app.unauthorizedErrorResponse(w, r, nil)
		return
	}

	var req AssignQrCodesRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}
	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	// Ensure wedding belongs to this user.
	wed, err := app.store.Wedding.GetByID(r.Context(), req.WeddingID)
	if err != nil {
		if err == store.ErrNotFound {
			app.notFoundMessage(w, r, "wedding not found")
			return
		}
		app.internalServerError(w, r, err)
		return
	}
	if wed.UserID != ownerID {
		_ = writeJSONError(w, http.StatusForbidden, "you do not own this wedding")
		return
	}

	if err := app.store.Qrcode.AssignToWedding(r.Context(), req.IDs, req.WeddingID); err != nil {
		switch err {
		case store.ErrQrCodeAlreadyClaimed, store.ErrQrCodeAlreadyAssigned:
			app.conflictResponse(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	if err := app.jsonResponse(w, http.StatusOK, map[string]any{"updated": len(req.IDs)}); err != nil {
		app.internalServerError(w, r, err)
	}
}

type UnassignQrCodesRequest struct {
	IDs []int64 `json:"ids" validate:"required,min=1"`
}

func (app *application) unassignQrCodesHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := authenticatedUserIDFromContext(r.Context())
	if !ok {
		app.unauthorizedErrorResponse(w, r, nil)
		return
	}

	var req UnassignQrCodesRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}
	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	if err := app.store.Qrcode.UnassignFromWedding(r.Context(), req.IDs, ownerID); err != nil {
		switch err {
		case store.ErrQrCodeAlreadyClaimed:
			app.conflictResponse(w, r, err)
		default:
			// includes forbidden-like error messages for non-owned weddings
			app.badRequestError(w, r, err)
		}
		return
	}

	if err := app.jsonResponse(w, http.StatusOK, map[string]any{"updated": len(req.IDs)}); err != nil {
		app.internalServerError(w, r, err)
	}
}

func (app *application) getPublicQrHandler(w http.ResponseWriter, r *http.Request) {
	token, err := app.store.Qrcode.ResolveToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		case store.ErrConflict:
			app.conflictResponse(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}
		return
	}
	info, err := app.store.Qrcode.GetPublicByToken(r.Context(), token)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	if err := app.jsonResponse(w, http.StatusOK, info); err != nil {
		app.internalServerError(w, r, err)
	}
}

func (app *application) redirectQrToClaimHandler(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(chi.URLParam(r, "token"))
	token, err := app.store.Qrcode.ResolveToken(r.Context(), raw)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		case store.ErrConflict:
			app.conflictResponse(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}
		return
	}
	_, err = app.store.Qrcode.GetPublicByToken(r.Context(), token)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	// Always redirect to claim page using collision-free short_code.
	outToken := raw
	if sc, err := app.store.Qrcode.GetShortCodeByToken(r.Context(), token); err == nil && strings.TrimSpace(sc) != "" {
		outToken = sc
	}

	base := normalizeAbsoluteBaseURL(app.config.frontendURL)
	if base == "" {
		base = "http://localhost:5174"
	}
	dst := base + "/claim?t=" + url.QueryEscape(outToken)

	// Redirect + HTML fallback (some scanner webviews don't follow 302 reliably).
	w.Header().Set("Location", dst)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta http-equiv="refresh" content="0; url=` + dst + `" />
    <title>Redirecting…</title>
  </head>
  <body>
    <script>window.location.replace(` + "`" + dst + "`" + `);</script>
    <p>Redirecting… <a href="` + dst + `">Continue</a></p>
  </body>
</html>`))
}

func (app *application) getPublicQrImageHandler(w http.ResponseWriter, r *http.Request) {
	// Resolve any incoming token to the canonical UUID, then fetch the QR record again
	// to obtain its collision-free short_code for the QR payload.
	token, err := app.store.Qrcode.ResolveToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		case store.ErrConflict:
			app.conflictResponse(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}
		return
	}
	_, err = app.store.Qrcode.GetPublicByToken(r.Context(), token)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	// Shorter payload => fewer QR modules ("dots") => easier scanning.
	apiBase := normalizeAbsoluteBaseURL(app.config.apiURL)
	if apiBase == "" {
		apiBase = "http://localhost:5075"
	}
	short, err := app.store.Qrcode.GetShortCodeByToken(r.Context(), token)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	claimURL := apiBase + "/c/" + url.PathEscape(strings.TrimSpace(short))

	// Allow requesting a larger QR code (useful for printing).
	// `s`/`size` is the PNG pixel size, clamped to a safe range.
	size := 900
	if qs := strings.TrimSpace(r.URL.Query().Get("s")); qs != "" {
		if n, err := strconv.Atoi(qs); err == nil {
			size = n
		}
	} else if qs := strings.TrimSpace(r.URL.Query().Get("size")); qs != "" {
		if n, err := strconv.Atoi(qs); err == nil {
			size = n
		}
	}
	if size < 120 {
		size = 120
	}
	if size > 1024 {
		size = 1024
	}

	// Low error correction reduces density. Disable built-in quiet-zone so we don't get a big white border.
	qr, err := qrcode.New(claimURL, qrcode.Low)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	qr.DisableBorder = true
	png, err := qr.PNG(size)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300") // 5 min
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

func qrSVGPayload(data string, level qrcode.RecoveryLevel, quietZoneModules int) (string, error) {
	qr, err := qrcode.New(data, level)
	if err != nil {
		return "", err
	}
	bm := qr.Bitmap()
	n := len(bm)
	if n == 0 {
		return "", fmt.Errorf("empty qr bitmap")
	}

	// Quiet zone is provided by the card's white padding; keep SVG tight.
	qz := quietZoneModules
	size := n + qz*2

	var b strings.Builder
	b.Grow(size * size)
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `)
	b.WriteString(strconv.Itoa(size))
	b.WriteString(` `)
	b.WriteString(strconv.Itoa(size))
	b.WriteString(`" shape-rendering="crispEdges" preserveAspectRatio="xMidYMid meet">`)
	// Transparent background; the print card provides the white QR panel.
	b.WriteString(`<g fill="#000">`)

	for y := 0; y < n; y++ {
		row := bm[y]
		// Pack consecutive black modules into wider rects for smaller SVG.
		for x := 0; x < n; {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < n && row[x] {
				x++
			}
			w := x - start
			b.WriteString(`<rect x="`)
			b.WriteString(strconv.Itoa(start + qz))
			b.WriteString(`" y="`)
			b.WriteString(strconv.Itoa(y + qz))
			b.WriteString(`" width="`)
			b.WriteString(strconv.Itoa(w))
			b.WriteString(`" height="1"/>`)
		}
	}

	b.WriteString(`</g></svg>`)
	return b.String(), nil
}

func (app *application) getPublicQrImageSVGHandler(w http.ResponseWriter, r *http.Request) {
	token, err := app.store.Qrcode.ResolveToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		case store.ErrConflict:
			app.conflictResponse(w, r, err)
		default:
			app.internalServerError(w, r, err)
		}
		return
	}
	_, err = app.store.Qrcode.GetPublicByToken(r.Context(), token)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	apiBase := normalizeAbsoluteBaseURL(app.config.apiURL)
	if apiBase == "" {
		apiBase = "http://localhost:5075"
	}
	short, err := app.store.Qrcode.GetShortCodeByToken(r.Context(), token)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	claimURL := apiBase + "/c/" + url.PathEscape(strings.TrimSpace(short))

	svg, err := qrSVGPayload(claimURL, qrcode.Low, 0)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(svg))
}

type ExportQrCodesPDFRequest struct {
	IDs []int64 `json:"ids" validate:"required,min=1"`
}

func (app *application) exportQrCodesPDFHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := authenticatedUserIDFromContext(r.Context())
	if !ok {
		app.unauthorizedErrorResponse(w, r, nil)
		return
	}

	var req ExportQrCodesPDFRequest
	if err := readJSON(w, r, &req); err != nil {
		app.badRequestError(w, r, err)
		return
	}
	if err := Validate.Struct(req); err != nil {
		app.badRequestError(w, r, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), store.QueryTimeOutDuration)
	defer cancel()

	selected, err := app.store.Qrcode.GetByIDs(ctx, req.IDs)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	if len(selected) == 0 {
		app.notFoundMessage(w, r, "no qr codes found for export")
		return
	}

	// Map wedding names from user's accessible weddings list.
	weddings, err := app.store.Wedding.GetByUserID(ctx, ownerID)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}
	wedName := map[int64]string{}
	for _, ww := range weddings {
		wedName[ww.ID] = ww.Name
	}

	// Verify ownership for assigned codes.
	for _, qr := range selected {
		if qr.WeddingID != nil {
			if _, ok := wedName[*qr.WeddingID]; !ok {
				_ = writeJSONError(w, http.StatusForbidden, "one or more selected qr codes belong to another user's wedding")
				return
			}
		}
	}

	formatAmount := func(a float64) string {
		return strconv.FormatFloat(a, 'f', 2, 64)
	}
	shortToken := func(t string) string {
		if len(t) <= 18 {
			return t
		}
		return t[:8] + "…" + t[len(t)-8:]
	}

	// Build PDF (units: mm).
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(false, 10)
	pdf.AddPage()

	// Card size (Mastercard): 85.60mm x 53.98mm
	cardW, cardH := 85.60, 53.98
	marginX, marginY := 10.0, 10.0
	gapX, gapY := 6.0, 6.0
	cols := 2
	x0, y0 := marginX, marginY

	// Fonts
	pdf.SetFont("Helvetica", "", 9)

	drawCard := func(x, y float64, qr store.Qrcode) error {
		pdf.SetDrawColor(30, 30, 30)
		pdf.SetLineWidth(0.3)
		pdf.RoundedRect(x, y, cardW, cardH, 3.5, "D", "1234")
		pdf.SetFillColor(180, 180, 180)
		// Header: serial + amount
		pdf.SetXY(x+4, y+4)
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(40, 6, qr.SerialNumber, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(13, 110, 253)
		pdf.SetXY(x+cardW-4-25, y+4)
		pdf.CellFormat(25, 6, formatAmount(qr.Amount), "", 0, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)

		// Wedding + claimed
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetXY(x+4, y+12)
		wName := "Unassigned"
		if qr.WeddingID != nil {
			if n, ok := wedName[*qr.WeddingID]; ok && strings.TrimSpace(n) != "" {
				wName = n
			} else {
				wName = "Wedding #" + strconv.FormatInt(*qr.WeddingID, 10)
			}
		}
		status := "Unclaimed"
		if qr.IsClaimed {
			status = "Claimed"
		}
		pdf.MultiCell(cardW-4-28-4, 4, "Wedding: "+wName+"\nStatus: "+status, "", "L", false)

		// QR image (generate now)
		base := strings.TrimRight(strings.TrimSpace(app.config.frontendURL), "/")
		if base == "" {
			base = "http://localhost:5174"
		}
		apiBase := normalizeAbsoluteBaseURL(app.config.apiURL)
		if apiBase == "" {
			apiBase = "http://localhost:5075"
		}
		uuidToken := strings.TrimSpace(qr.Token)
		short, err := app.store.Qrcode.GetShortCodeByToken(ctx, uuidToken)
		if err != nil {
			return err
		}
		claimURL := apiBase + "/c/" + url.PathEscape(strings.TrimSpace(short))
		qrImg, err := qrcode.New(claimURL, qrcode.Low)
		if err != nil {
			return err
		}
		qrImg.DisableBorder = true
		png, err := qrImg.PNG(900)
		if err != nil {
			return err
		}
		imgName := "qr-" + qr.Token
		opt := gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}
		pdf.RegisterImageOptionsReader(imgName, opt, bytes.NewReader(png))
		pdf.ImageOptions(imgName, x+cardW-4-26, y+4, 26, 26, false, opt, 0, "")

		// Footer: token shortened
		pdf.SetFont("Helvetica", "", 6)
		pdf.SetTextColor(90, 90, 90)
		pdf.SetXY(x+4, y+cardH-8)
		pdf.CellFormat(cardW-8, 4, "Token: "+shortToken(qr.Token), "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		return nil
	}

	for i, qr := range selected {
		idx := i % (cols * 4) // 2 cols * 4 rows per A4 with our margins/gaps approx
		if i > 0 && idx == 0 {
			pdf.AddPage()
		}
		col := idx % cols
		row := idx / cols
		x := x0 + float64(col)*(cardW+gapX)
		y := y0 + float64(row)*(cardH+gapY)
		if err := drawCard(x, y, qr); err != nil {
			app.internalServerError(w, r, err)
			return
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		app.internalServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="qr-cards.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
