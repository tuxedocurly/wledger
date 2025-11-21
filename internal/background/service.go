package background

import (
	"database/sql"
	"log"
	"time"

	"wledger/internal/models"
)

// Store defines the methods this service needs from the database
type Store interface {
	GetAllControllersForHealthCheck() ([]models.WLEDController, error)
	UpdateControllerStatus(id int, status string, lastSeen sql.NullTime) error
	CleanupOrphanedCategories() error
}

// WLEDClient defines the hardware communication methods
type WLEDClient interface {
	Ping(ipAddress string) bool
}

type Service struct {
	store Store
	wled  WLEDClient
}

func New(s Store, w WLEDClient) *Service {
	return &Service{store: s, wled: w}
}

func (s *Service) Start() {
	log.Println("Starting background services...")

	healthTicker := time.NewTicker(1 * time.Minute)
	defer healthTicker.Stop()

	cleanupTicker := time.NewTicker(6 * time.Hour)
	defer cleanupTicker.Stop()

	go s.runHealthChecks()
	go s.runCleanupJob()

	for {
		select {
		case <-healthTicker.C:
			go s.runHealthChecks()
		case <-cleanupTicker.C:
			go s.runCleanupJob()
		}
	}
}

func (s *Service) runCleanupJob() {
	log.Println("Running background tag cleanup...")
	// FIX: Use s.store, not s.PartStore/DashStore
	if err := s.store.CleanupOrphanedCategories(); err != nil {
		log.Println("Background cleanup failed:", err)
	}
	log.Println("Background tag cleanup complete.")
}

func (s *Service) runHealthChecks() {
	log.Println("Running WLED health checks...")

	// FIX: Use s.store, not s.CtrlStore
	controllers, err := s.store.GetAllControllersForHealthCheck()
	if err != nil {
		log.Println("HealthCheck: Error querying controllers:", err)
		return
	}

	for _, c := range controllers {
		// FIX: Use s.wled, not s.Wled
		online := s.wled.Ping(c.IPAddress)

		var status string
		var lastSeen sql.NullTime
		if online {
			status = "online"
			lastSeen.Time = time.Now()
			lastSeen.Valid = true
		} else {
			status = "offline"
			lastSeen.Valid = false
		}

		// FIX: Use s.store, not s.CtrlStore
		if err := s.store.UpdateControllerStatus(c.ID, status, lastSeen); err != nil {
			log.Println("HealthCheck: Error updating controller status:", err)
		}
	}
	log.Println("WLED health checks complete.")
}
