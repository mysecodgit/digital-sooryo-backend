package main

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jung-kurt/gofpdf"
	"github.com/mysecodgit/go_accounting/internal/store"
	"github.com/skip2/go-qrcode"
)

type GenerateQrCodesRequest struct {
	Count  int     `json:"count" validate:"required,gt=0,lte=500"`
	Amount float64 `json:"amount" validate:"required,gte=0"`
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
	token := chi.URLParam(r, "token")
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

func (app *application) getPublicQrImageHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	_, err := app.store.Qrcode.GetPublicByToken(r.Context(), token)
	if err != nil {
		switch err {
		case store.ErrQrCodeNotFound:
			app.notFoundMessage(w, r, store.ErrQrCodeNotFound.Error())
		default:
			app.internalServerError(w, r, err)
		}
		return
	}

	base := strings.TrimRight(strings.TrimSpace(app.config.frontendURL), "/")
	if base == "" {
		base = "http://localhost:5174"
	}

	claimURL := base + "/claim?t=" + url.QueryEscape(strings.TrimSpace(token))

	png, err := qrcode.Encode(claimURL, qrcode.Medium, 240)
	if err != nil {
		app.internalServerError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300") // 5 min
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
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
		claimURL := base + "/claim?t=" + url.QueryEscape(strings.TrimSpace(qr.Token))
		png, err := qrcode.Encode(claimURL, qrcode.Medium, 240)
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
