package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /hardware
func (h *Handler) HandleHardwareList(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	controllers, err := h.Hardware.ListControllers(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch hardware list", http.StatusInternalServerError)
		return
	}

	containers, err := h.Hardware.ListAllContainersWithController(r.Context())
	if err != nil {
		containers = []db.GetAllContainersWithControllerRow{}
	}

	pages.Hardware(user, controllers, containers).Render(r.Context(), w)
}

// POST /hardware
func (h *Handler) HandleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	ip := normalizeControllerAddressInput(r.FormValue("ip_address"))

	if name == "" || ip == "" {
		h.UIError.Respond(w, r, nil, "Controller name and IP address are required", http.StatusBadRequest)
		return
	}

	port, err := parsePortInput(r.FormValue("port"), 80)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid port: must be between 1 and 65535", http.StatusBadRequest)
		return
	}

	_, err = h.Hardware.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: port, Valid: true},
	})
	if err != nil {
		if errors.Is(err, hardware.ErrInvalidEndpoint) {
			h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to create controller", http.StatusInternalServerError)
		return
	}

	h.triggerStatusPollerNow()

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/update
func (h *Handler) HandleControllerUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		h.UIError.Respond(w, r, err, "Invalid controller ID", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	ip := normalizeControllerAddressInput(r.FormValue("ip_address"))
	if name == "" || ip == "" {
		h.UIError.Respond(w, r, nil, "Controller name and IP address are required", http.StatusBadRequest)
		return
	}

	port, err := parsePortInput(r.FormValue("port"), 80)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid HTTP port: must be between 1 and 65535", http.StatusBadRequest)
		return
	}

	udpPort, err := parsePortInput(r.FormValue("udp_port"), 21324)
	if err != nil {
		h.UIError.Respond(w, r, err, "Invalid UDP port: must be between 1 and 65535", http.StatusBadRequest)
		return
	}

	idlePresetID := int64(0)
	if presetStr := strings.TrimSpace(r.FormValue("idle_preset_id")); presetStr != "" {
		parsedPreset, pErr := strconv.ParseInt(presetStr, 10, 64)
		if pErr != nil || parsedPreset < 0 {
			h.UIError.Respond(w, r, pErr, "Invalid idle preset ID", http.StatusBadRequest)
			return
		}
		idlePresetID = parsedPreset
	}

	useUDP := parseBoolFormValue(r.FormValue("use_udp"))
	restoreState := parseBoolFormValue(r.FormValue("restore_state"))

	err = h.Hardware.UpdateController(r.Context(), db.UpdateControllerParams{
		ID:           id,
		Name:         name,
		IpAddress:    ip,
		Port:         sql.NullInt64{Int64: port, Valid: true},
		UseUdp:       sql.NullBool{Bool: useUDP, Valid: true},
		UdpPort:      sql.NullInt64{Int64: udpPort, Valid: true},
		RestoreState: sql.NullBool{Bool: restoreState, Valid: true},
		IdlePresetID: sql.NullInt64{Int64: idlePresetID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, hardware.ErrInvalidEndpoint) {
			h.UIError.Respond(w, r, err, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to update controller", http.StatusInternalServerError)
		return
	}

	h.triggerStatusPollerNow()

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/delete
func (h *Handler) HandleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	err := h.Hardware.DeleteController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete hardware", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/discover
func (h *Handler) HandleHardwareDiscover(w http.ResponseWriter, r *http.Request) {
	res, err := h.Hardware.DiscoverControllers(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to run mDNS WLED discovery", http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	items := make([]pages.DiscoveryResultItem, 0, len(res.Devices))
	for _, m := range res.Devices {
		items = append(items, pages.DiscoveryResultItem{
			Name:           m.Device.Name,
			IP:             m.Device.IP,
			Port:           m.Device.Port,
			MAC:            m.Device.MAC,
			LedCount:       m.Device.LedCount,
			Status:         m.Status,
			ControllerName: m.ControllerName,
			PreviousIP:     m.PreviousIP,
		})
	}
	_ = pages.DiscoverResults(items).Render(r.Context(), w)
}

// GET /hardware/{id}/status
func (h *Handler) HandleHardwareStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		h.UIError.Respond(w, r, err, "Invalid controller ID", http.StatusBadRequest)
		return
	}

	online, err := h.Hardware.UpdateStatus(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to update controller status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ControllerStatusControl(id, online).Render(r.Context(), w); err != nil && h.Logger != nil {
		h.Logger.Error("failed to render controller status control", "controller_id", id, "err", err)
	}
}

// GET /hardware/{id}/grid
func (h *Handler) HandleHardwareGrid(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	c, err := h.Hardware.GetController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}

	bins, err := h.Hardware.GetBinsByController(r.Context(), int64(id))
	if err != nil {
		bins = []db.Bin{}
	}

	containers, err := h.Hardware.GetContainers(r.Context(), int64(id))
	if err != nil {
		containers = []db.Container{}
	}

	pages.HardwareGrid(user, c, containers, bins).Render(r.Context(), w)
}

// POST /hardware/{id}/grid
func (h *Handler) HandleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	controllerID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	ctx := r.Context()

	gridDataJSON := r.FormValue("grid_data")
	configJSON := r.FormValue("config_data")

	_, err := h.Hardware.SaveGrid(ctx, int64(controllerID), gridDataJSON, configJSON)
	if err != nil {
		if errors.Is(err, hardware.ErrContainerOwnership) {
			h.UIError.Respond(w, r, err, err.Error(), http.StatusForbidden)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to save grid layout", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/containers
func (h *Handler) HandleContainerCreateStandalone(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.UIError.Respond(w, r, nil, "Container name is required", http.StatusBadRequest)
		return
	}

	rows, _ := strconv.Atoi(r.FormValue("rows"))
	cols, _ := strconv.Atoi(r.FormValue("cols"))

	id, err := h.Hardware.CreateStandaloneContainer(r.Context(), name, rows, cols)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create standalone container", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/hardware/containers/%d/configure", id), http.StatusSeeOther)
}

// GET /hardware/containers/{id}/configure
func (h *Handler) HandleContainerConfigure(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		h.UIError.Respond(w, r, err, "Invalid container ID", http.StatusBadRequest)
		return
	}

	c, err := h.Hardware.GetContainer(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Container not found", http.StatusNotFound)
		return
	}

	if c.ControllerID.Valid {
		http.Redirect(w, r, fmt.Sprintf("/hardware/%d/grid", c.ControllerID.Int64), http.StatusSeeOther)
		return
	}

	bins, err := h.Hardware.GetBinsByContainer(r.Context(), id)
	if err != nil {
		bins = []db.Bin{}
	}

	pages.StandaloneContainerGrid(user, c, bins).Render(r.Context(), w)
}

// POST /hardware/containers/{id}/configure
func (h *Handler) HandleContainerConfigureSave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		h.UIError.Respond(w, r, err, "Invalid container ID", http.StatusBadRequest)
		return
	}

	gridDataJSON := r.FormValue("grid_data")
	configJSON := r.FormValue("config_data")

	_, err = h.Hardware.SaveStandaloneGrid(r.Context(), id, gridDataJSON, configJSON)
	if err != nil {
		if errors.Is(err, hardware.ErrContainerOwnership) {
			h.UIError.Respond(w, r, err, err.Error(), http.StatusForbidden)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			h.UIError.Respond(w, r, err, "Container not found", http.StatusNotFound)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to save standalone container layout", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/containers/{id}/delete
func (h *Handler) HandleContainerDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		h.UIError.Respond(w, r, err, "Invalid container ID", http.StatusBadRequest)
		return
	}

	if err := h.Hardware.DeleteContainer(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.UIError.Respond(w, r, err, "Container not found", http.StatusNotFound)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to delete container", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/containers/{id}/controller
func (h *Handler) HandleContainerBindController(w http.ResponseWriter, r *http.Request) {
	containerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || containerID <= 0 {
		h.UIError.Respond(w, r, err, "Invalid container ID", http.StatusBadRequest)
		return
	}

	ctrlStr := strings.TrimSpace(r.FormValue("controller_id"))
	target := sql.NullInt64{Valid: false}
	if ctrlStr != "" && ctrlStr != "0" {
		cid, parseErr := strconv.ParseInt(ctrlStr, 10, 64)
		if parseErr != nil || cid <= 0 {
			h.UIError.Respond(w, r, parseErr, "Invalid controller ID", http.StatusBadRequest)
			return
		}
		target = sql.NullInt64{Int64: cid, Valid: true}
	}

	if err := h.Hardware.UpdateContainerController(r.Context(), containerID, target); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.UIError.Respond(w, r, err, "Container or controller not found", http.StatusNotFound)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to update container controller binding", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (h *Handler) HandleGlobalOff(w http.ResponseWriter, r *http.Request) {
	err := h.WLED.GlobalOff(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to trigger global off", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// POST /hardware/{id}/locate
func (h *Handler) HandleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.Atoi(chi.URLParam(r, "id"))
	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	err := h.WLED.LocateBin(r.Context(), int64(cid), int64(binID))
	if err != nil {
		h.UIError.Respond(w, r, err, "Locate failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func parsePortInput(raw string, defaultPort int64) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultPort, nil
	}
	p, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("invalid port %q", raw)
	}
	return p, nil
}

func parseBoolFormValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "on", "1", "yes":
		return true
	default:
		return false
	}
}

// normalizeControllerAddressInput strips an accidental http:// scheme, root trailing slash,
// and surrounding whitespace (including WLED's Web UI layout "http:// \nwled-xxxxxx\n .local")
// while leaving non-HTTP schemes, paths, or arbitrary interior whitespace untouched for strict validation.
func normalizeControllerAddressInput(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= len("http://") && strings.EqualFold(s[:len("http://")], "http://") {
		s = strings.TrimSpace(s[len("http://"):])
	}
	if strings.HasSuffix(s, "/") && strings.Count(s, "/") == 1 {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	if len(s) > len(".local") && strings.EqualFold(s[len(s)-len(".local"):], ".local") {
		suffix := s[len(s)-len(".local"):]
		prefix := strings.TrimSpace(s[:len(s)-len(".local")])
		if prefix != "" && !strings.ContainsAny(prefix, " \t\r\n\v\f") {
			s = prefix + suffix
		}
	}
	return s
}
