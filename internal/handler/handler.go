package handler

import (
	"database/sql"
	"log/slog"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/backup"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/inspiration"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/projects"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// Handler holds shared dependencies for all HTTP handlers and services
type Handler struct {
	Logger      *slog.Logger
	Queries     db.Store
	Session     *scs.SessionManager
	WLED        wled.Service
	Database    *sql.DB
	Backup      backup.Service
	Parts       parts.Service
	Tags        tags.Service
	Inspiration inspiration.Service
	UIError     *uierror.Responder
	Hardware    hardware.Service
	Settings    settings.Service
	Audit       audit.Service
	Dashboard   dashboard.Service
	Stock       stock.Service
	Documents   documents.Service
	Projects     projects.Service
	APITokens    *APITokenHandler
	StatusPoller *hardware.StatusPoller
}

// New creates a new Handler with dependencies
func New(
	logger *slog.Logger,
	queries db.Store,
	session *scs.SessionManager,
	wledService wled.Service,
	database *sql.DB,
	backupService backup.Service,
	partsService parts.Service,
	tagsService tags.Service,
	inspirationService inspiration.Service,
	uiError *uierror.Responder,
	hardwareService hardware.Service,
	settingsService settings.Service,
	auditService audit.Service,
	dashboardService dashboard.Service,
	stockService stock.Service,
	docsService documents.Service,
) *Handler {
	var projectsService projects.Service
	if queries != nil {
		projectsService = projects.NewService(queries, wledService, logger)
	}
	return &Handler{
		Logger:      logger,
		Queries:     queries,
		Session:     session,
		WLED:        wledService,
		Database:    database,
		Backup:      backupService,
		Parts:       partsService,
		Tags:        tagsService,
		Inspiration: inspirationService,
		UIError:     uiError,
		Hardware:    hardwareService,
		Settings:    settingsService,
		Audit:       auditService,
		Dashboard:   dashboardService,
		Stock:       stockService,
		Documents:   docsService,
		Projects:    projectsService,
	}
}

// WithProjects sets the Projects service (for example configured with projects.WithWLEDClient) on the Handler.
func (h *Handler) WithProjects(s projects.Service) *Handler {
	h.Projects = s
	return h
}

// WithAPITokens sets the APITokenHandler on the Handler.
func (h *Handler) WithAPITokens(th *APITokenHandler) *Handler {
	h.APITokens = th
	return h
}

// WithStatusPoller attaches the background controller status poller to the Handler.
func (h *Handler) WithStatusPoller(p *hardware.StatusPoller) *Handler {
	h.StatusPoller = p
	return h
}

func (h *Handler) notifyStatusPollerSettingsChanged() {
	if h != nil && h.StatusPoller != nil {
		h.StatusPoller.NotifySettingsChanged()
	}
}

func (h *Handler) triggerStatusPollerNow() {
	if h != nil && h.StatusPoller != nil {
		h.StatusPoller.TriggerNow()
	}
}
