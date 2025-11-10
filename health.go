package main

import (
	"database/sql"
	"log"
	"time"
)

func (a *App) startBackgroundServices() {
	log.Println("Starting background services...")

	healthTicker := time.NewTicker(1 * time.Minute)
	defer healthTicker.Stop()

	cleanupTicker := time.NewTicker(6 * time.Hour)
	defer cleanupTicker.Stop()

	go a.runHealthChecks()
	go a.runCleanupJob()

	for {
		select {
		case <-healthTicker.C:
			go a.runHealthChecks()
		case <-cleanupTicker.C:
			go a.runCleanupJob()
		}
	}
}

func (a *App) runCleanupJob() {
	log.Println("Running background tag cleanup...")
	if err := a.partStore.CleanupOrphanedCategories(); err != nil {
		log.Println("Background cleanup failed:", err)
	}
	log.Println("Background tag cleanup complete.")
}

func (a *App) runHealthChecks() {
	log.Println("Running WLED health checks...")

	controllers, err := a.ctrlStore.GetAllControllersForHealthCheck()
	if err != nil {
		log.Println("HealthCheck: Error querying controllers:", err)
		return
	}

	for _, c := range controllers {
		online := a.wled.Ping(c.IPAddress)

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

		if err := a.ctrlStore.UpdateControllerStatus(c.ID, status, lastSeen); err != nil {
			log.Println("HealthCheck: Error updating controller status:", err)
		}
	}
	log.Println("WLED health checks complete.")
}
