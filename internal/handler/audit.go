package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

func parseDateFilter(raw string, endOfDay bool) sql.NullTime {
	if raw == "" {
		return sql.NullTime{}
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return sql.NullTime{}
	}
	if endOfDay {
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 999999999, time.UTC)
	} else {
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
	}
	return sql.NullTime{Time: parsed, Valid: true}
}

func (h *Handler) HandleAuditLogs(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	// Access control - admin only
	if !user.IsAdmin() {
		h.UIError.Respond(w, r, nil, "Forbidden", http.StatusForbidden)
		return
	}

	// Parse Query Params
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	actionType := q.Get("action_type")
	entityType := q.Get("entity_type")
	search := q.Get("search")
	startDate := parseDateFilter(q.Get("start_date"), false)
	endDate := parseDateFilter(q.Get("end_date"), true)

	var userID sql.NullInt64
	if uidStr := q.Get("user_id"); uidStr != "" {
		if uid, err := strconv.Atoi(uidStr); err == nil {
			userID = sql.NullInt64{Int64: int64(uid), Valid: true}
		}
	}

	// Call DB
	params := db.ListAuditLogsParams{
		Limit:      int64(limit),
		Offset:     int64(offset),
		ActionType: sql.NullString{String: actionType, Valid: actionType != ""},
		EntityType: sql.NullString{String: entityType, Valid: entityType != ""},
		Search:     sql.NullString{String: search, Valid: search != ""},
		UserID:     userID,
		StartDate:  startDate,
		EndDate:    endDate,
	}

	logs, err := h.Audit.ListLogs(r.Context(), params)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to list audit logs", http.StatusInternalServerError)
		return
	}

	// Count for pagination
	countParams := db.CountAuditLogsParams{
		ActionType: params.ActionType,
		EntityType: params.EntityType,
		UserID:     params.UserID,
		Search:     params.Search,
		StartDate:  params.StartDate,
		EndDate:    params.EndDate,
	}
	totalCount, err := h.Audit.CountLogs(r.Context(), countParams)
	if err != nil {
		h.Logger.Error("failed to count audit logs", "err", err)
	}

	// Fetch users for filter
	users, err := h.Settings.ListUsers(r.Context())
	if err != nil {
		h.Logger.Error("failed to list users for audit filter", "err", err)
	}

	// Render Template
	if q.Get("scroll") == "true" {
		pages.AuditLogRows(logs, params, page).Render(r.Context(), w)
		return
	}

	pages.AuditLogs(user, logs, users, params, totalCount, page).Render(r.Context(), w)
}
