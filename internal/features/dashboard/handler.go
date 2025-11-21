package dashboard

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
)

// Store defines the database methods this module needs.
// It aggregates methods for Dashboard, Locate, and Inspiration (GetParts).
// TODO: Consider simplifying GetPartLocationsForLocate and GetPartLocationsForStop into a
// single method with a flag, parameter or something
type Store interface {
	GetDashboardBinData() ([]models.DashboardBinData, error)
	GetPartLocationsForLocate(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetPartLocationsForStop(partID int) ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
	GetAllBinLocationsForStopAll() ([]struct {
		IP       string
		SegID    int
		LEDIndex int
	}, error)
}

// WLEDClient defines the hardware communication methods
type WLEDClient interface {
	SendCommand(ipAddress string, state models.WLEDState) error
}

type Handler struct {
	store     Store
	wled      WLEDClient
	templates core.TemplateExecutor
}

func New(s Store, w WLEDClient, t core.TemplateExecutor) *Handler {
	return &Handler{store: s, wled: w, templates: t}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/dashboard", h.handleShowDashboard)
	r.Post("/api/v1/stock-status", h.handleShowStockStatus)
	r.Post("/api/v1/stop-all", h.handleStopAll)

	// Locate routes
	r.Post("/locate/part/{id}", h.handleLocatePart)
	r.Post("/locate/stop/{id}", h.handleStopLocate)
	r.Get("/locate/button/{id}", h.handleGetLocateButton)
}

// Handlers

func (h *Handler) handleShowDashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title": "Stock Dashboard",
	}
	err := h.templates.ExecuteTemplate(w, "dashboard.html", data)
	if err != nil {
		core.ServerError(w, r, err)
	}
}

func (h *Handler) handleShowStockStatus(w http.ResponseWriter, r *http.Request) {
	level := r.FormValue("level")

	// Clear all LEDs
	allBinsForStop, err := h.store.GetAllBinLocationsForStopAll()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	type ledPayload map[string]map[int][]interface{}
	stopPayloads := make(ledPayload)
	colorBlack := "000000"

	for _, bin := range allBinsForStop {
		if stopPayloads[bin.IP] == nil {
			stopPayloads[bin.IP] = make(map[int][]interface{})
		}
		stopPayloads[bin.IP][bin.SegID] = append(
			stopPayloads[bin.IP][bin.SegID],
			bin.LEDIndex, colorBlack,
		)
	}

	for ip, segments := range stopPayloads {
		wledSegments := []models.WLEDSegment{}
		for segID, iPayload := range segments {
			wledSegments = append(wledSegments, models.WLEDSegment{
				ID: segID,
				On: true,
				I:  iPayload,
			})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := h.wled.SendCommand(ip, state); err != nil {
			// Log only
			fmt.Printf("Dashboard (Clear): Failed to send WLED 'off' command to %s: %v\n", ip, err)
		}
	}

	// Get all bin data
	allBins, err := h.store.GetDashboardBinData()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	colorRed := "FF0000"
	colorYellow := "FFFF00"
	colorGreen := "00FF00"

	payloads := make(ledPayload)
	binsLit := 0

	for _, bin := range allBins {
		var color string
		if bin.BinQuantity <= bin.MinStock {
			color = colorRed
		} else if bin.BinQuantity <= bin.ReorderPoint {
			color = colorYellow
		} else {
			color = colorGreen
		}

		if level == "critical" && color != colorRed {
			continue
		}
		if level == "attention" && color == colorGreen {
			continue
		}

		if payloads[bin.BinIP] == nil {
			payloads[bin.BinIP] = make(map[int][]interface{})
		}
		payloads[bin.BinIP][bin.BinSegmentID] = append(
			payloads[bin.BinIP][bin.BinSegmentID],
			bin.BinLEDIndex, color,
		)
		binsLit++
	}

	for ip, segments := range payloads {
		wledSegments := []models.WLEDSegment{}
		for segID, iPayload := range segments {
			wledSegments = append(wledSegments, models.WLEDSegment{
				ID: segID,
				On: true,
				I:  iPayload,
			})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := h.wled.SendCommand(ip, state); err != nil {
			fmt.Printf("Dashboard: Failed to send WLED command to %s: %v\n", ip, err)
		}
	}

	fmt.Fprintf(w, "<strong>Success!</strong> Lit %d bins.", binsLit)
}

func (h *Handler) handleStopAll(w http.ResponseWriter, r *http.Request) {
	locations, err := h.store.GetAllBinLocationsForStopAll()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	type ledMap map[string]map[int][]interface{}
	ledsByController := make(ledMap)

	for _, loc := range locations {
		if ledsByController[loc.IP] == nil {
			ledsByController[loc.IP] = make(map[int][]interface{})
		}
		ledsByController[loc.IP][loc.SegID] = append(ledsByController[loc.IP][loc.SegID], loc.LEDIndex, "000000")
	}

	for ip, segments := range ledsByController {
		wledSegments := []models.WLEDSegment{}
		for segID, iPayload := range segments {
			wledSegments = append(wledSegments, models.WLEDSegment{
				ID: segID,
				On: true,
				I:  iPayload,
			})
		}
		state := models.WLEDState{Segments: wledSegments}
		if err := h.wled.SendCommand(ip, state); err != nil {
			fmt.Printf("StopAll: Failed to send WLED 'off' command to %s: %v\n", ip, err)
		}
	}

	w.Header().Set("HX-Trigger", "resetLocateButtons")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleLocatePart(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	locations, err := h.store.GetPartLocationsForLocate(partID)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	type ledMap map[string]map[int][]int
	ledsByController := make(ledMap)

	for _, loc := range locations {
		if ledsByController[loc.IP] == nil {
			ledsByController[loc.IP] = make(map[int][]int)
		}
		ledsByController[loc.IP][loc.SegID] = append(ledsByController[loc.IP][loc.SegID], loc.LEDIndex)
	}

	color := "FF0000" // Red
	for ip, segments := range ledsByController {
		wledSegments := []models.WLEDSegment{}
		for segID, ledIndices := range segments {
			iPayload := []interface{}{}
			for _, ledIndex := range ledIndices {
				iPayload = append(iPayload, ledIndex, color)
			}
			wledSegments = append(wledSegments, models.WLEDSegment{
				ID: segID,
				On: true,
				I:  iPayload,
			})
		}

		state := models.WLEDState{Segments: wledSegments}
		if err := h.wled.SendCommand(ip, state); err != nil {
			fmt.Printf("Failed to send WLED command to %s: %v\n", ip, err)
			// Send back 'Start' button on failure
			part := models.Part{ID: partID}
			h.templates.ExecuteTemplate(w, "_locate-start-button.html", part)
			return
		}
	}

	part := models.Part{ID: partID}
	h.templates.ExecuteTemplate(w, "_locate-stop-button.html", part)
}

func (h *Handler) handleStopLocate(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))

	locations, err := h.store.GetPartLocationsForStop(partID)
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	type ledMap map[string]map[int][]int
	ledsByController := make(ledMap)

	for _, loc := range locations {
		if ledsByController[loc.IP] == nil {
			ledsByController[loc.IP] = make(map[int][]int)
		}
		ledsByController[loc.IP][loc.SegID] = append(ledsByController[loc.IP][loc.SegID], loc.LEDIndex)
	}

	color := "000000" // Black
	for ip, segments := range ledsByController {
		wledSegments := []models.WLEDSegment{}
		for segID, ledIndices := range segments {
			iPayload := []interface{}{}
			for _, ledIndex := range ledIndices {
				iPayload = append(iPayload, ledIndex, color)
			}
			wledSegments = append(wledSegments, models.WLEDSegment{
				ID: segID,
				On: true,
				I:  iPayload,
			})
		}

		state := models.WLEDState{Segments: wledSegments}
		if err := h.wled.SendCommand(ip, state); err != nil {
			fmt.Printf("Failed to send WLED 'off' command to %s: %v\n", ip, err)
		}
	}

	part := models.Part{ID: partID}
	h.templates.ExecuteTemplate(w, "_locate-start-button.html", part)
}

func (h *Handler) handleGetLocateButton(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	part := models.Part{ID: id}
	h.templates.ExecuteTemplate(w, "_locate-start-button.html", part)
}
