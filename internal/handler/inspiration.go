package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// HandleInspiration renders the main inspiration page
func (h *Handler) HandleInspiration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fetch all templates
	templates, err := h.Inspiration.GetAllTemplates(ctx)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch inspiration templates", http.StatusInternalServerError)
		return
	}

	// Fetch all tags for the filter dropdown
	allTags, err := h.Queries.ListAllTags(ctx)
	if err != nil {
		h.Logger.Error("Failed to fetch tags for inspiration filter", "err", err)
		// The app can still proceed without tags, just won't be able to filter
	}

	// Convert DB tags to strings for the UI
	tagNames := make([]string, len(allTags))
	for i, t := range allTags {
		tagNames[i] = t.Name
	}

	user := auth.GetUserFromRequest(r)

	// Render the page
	pages.Inspiration(templates, tagNames, user).Render(ctx, w)
}

// HandleInspirationGenerate constructs the prompt based on the template and filters
func (h *Handler) HandleInspirationGenerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid Template ID", http.StatusBadRequest)
		return
	}

	// Get tags from query params (supports both ?tags=a,b and repeated ?tags=a&tags=b)
	var tagFilters []string
	for _, raw := range r.URL.Query()["tags"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				tagFilters = append(tagFilters, part)
			}
		}
	}

	prompt, err := h.Inspiration.ConstructPrompt(ctx, id, tagFilters)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate prompt", http.StatusInternalServerError)
		return
	}

	// Return as plain text for easy copying
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, prompt)
}

func (h *Handler) HandleInspirationNew(w http.ResponseWriter, r *http.Request) {
	components.InspirationFormModal(nil).Render(r.Context(), w)
}

func (h *Handler) HandleInspirationCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.UIError.Respond(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	content := r.FormValue("content")

	_, err := h.Inspiration.CreateTemplate(r.Context(), title, content)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create inspiration template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Location", "/inspiration")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HandleInspirationEdit(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid ID", http.StatusBadRequest)
		return
	}

	tmpl, err := h.Inspiration.GetTemplate(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Template not found", http.StatusNotFound)
		return
	}

	components.InspirationFormModal(&tmpl).Render(r.Context(), w)
}

func (h *Handler) HandleInspirationUpdate(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.UIError.Respond(w, r, err, "Invalid form data", http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	content := r.FormValue("content")

	err = h.Inspiration.UpdateTemplate(r.Context(), id, title, content)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update inspiration template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Location", "/inspiration")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HandleInspirationDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid ID", http.StatusBadRequest)
		return
	}

	err = h.Inspiration.DeleteTemplate(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete inspiration template", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
