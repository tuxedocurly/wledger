package backup

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

const (
	ManifestVersionV1 = "1.0"
	ManifestVersionV2 = "2.0"
)

// Manifest represents the JSON payload stored as restore_data.json inside a backup archive.
type Manifest struct {
	Version              string                   `json:"version"`
	ExportedAt           time.Time                `json:"exported_at"`
	Settings             db.Setting               `json:"settings"`
	Users                []db.User                `json:"users"`
	Controllers          []db.Controller          `json:"controllers"`
	Containers           []db.Container           `json:"containers"`
	Walls                []db.Wall                `json:"walls"`
	WallCards            []db.WallCard            `json:"wall_cards"`
	Bins                 []db.Bin                 `json:"bins"`
	Parts                []db.Part                `json:"parts"`
	PartAssignments      []db.PartAssignment      `json:"part_assignments"`
	PartLinks            []db.PartLink            `json:"part_links"`
	PartDocs             []db.PartDoc             `json:"part_docs"`
	PartAiPrompts        []db.PartAiPrompt        `json:"part_ai_prompts"`
	Tags                 []db.Tag                 `json:"tags"`
	PartTags             []db.PartTag             `json:"part_tags"`
	AuditLogs            []db.AuditLog            `json:"audit_logs"`
	InspirationTemplates []db.InspirationTemplate `json:"inspiration_templates"`
	SystemFlags          []db.SystemFlag          `json:"system_flags"`
	Projects             []db.Project             `json:"projects"`
	ProjectBomItems      []db.ProjectBomItem      `json:"project_bom_items"`
}

// nullInt64Compat decodes both v1.0 integer controller_id values and v2.0 sql.NullInt64 objects.
type nullInt64Compat struct {
	sql.NullInt64
}

func (n *nullInt64Compat) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		n.NullInt64 = sql.NullInt64{}
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return json.Unmarshal(data, &n.NullInt64)
	}
	v, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid nullable int64 %q: %w", trimmed, err)
	}
	n.NullInt64 = sql.NullInt64{Int64: v, Valid: true}
	return nil
}

type containerCompat struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	ControllerID  nullInt64Compat `json:"controller_id"`
	SegmentID     int64           `json:"segment_id"`
	ConfigJson    sql.NullString  `json:"config_json"`
	CreatedAt     sql.NullTime    `json:"created_at"`
	UpdatedAt     sql.NullTime    `json:"updated_at"`
	PositionIndex int64           `json:"position_index"`
}

type auditLogCompat struct {
	ID         int64           `json:"id"`
	UserID     sql.NullInt64   `json:"user_id"`
	ActionType string          `json:"action_type"`
	EntityType string          `json:"entity_type"`
	EntityID   int64           `json:"entity_id"`
	Details    sql.NullString  `json:"details"`
	OldValue   json.RawMessage `json:"old_value"`
	NewValue   json.RawMessage `json:"new_value"`
	CreatedAt  sql.NullTime    `json:"created_at"`
}

func rawJSONToBytes(raw json.RawMessage) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	cp := make([]byte, len(trimmed))
	copy(cp, trimmed)
	return cp
}

// UnmarshalJSON decodes both v1.0 and v2.0 backup manifests while preserving raw audit JSON payloads.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	type manifestAlias Manifest
	var raw struct {
		*manifestAlias
		Containers []containerCompat `json:"containers"`
		AuditLogs  []auditLogCompat  `json:"audit_logs"`
	}
	raw.manifestAlias = (*manifestAlias)(m)

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.Containers != nil {
		m.Containers = make([]db.Container, len(raw.Containers))
		for i, c := range raw.Containers {
			m.Containers[i] = db.Container{
				ID:            c.ID,
				Name:          c.Name,
				ControllerID:  c.ControllerID.NullInt64,
				SegmentID:     c.SegmentID,
				ConfigJson:    c.ConfigJson,
				CreatedAt:     c.CreatedAt,
				UpdatedAt:     c.UpdatedAt,
				PositionIndex: c.PositionIndex,
			}
		}
	}

	if raw.AuditLogs != nil {
		m.AuditLogs = make([]db.AuditLog, len(raw.AuditLogs))
		for i, l := range raw.AuditLogs {
			m.AuditLogs[i] = db.AuditLog{
				ID:         l.ID,
				UserID:     l.UserID,
				ActionType: l.ActionType,
				EntityType: l.EntityType,
				EntityID:   l.EntityID,
				Details:    l.Details,
				OldValue:   rawJSONToBytes(l.OldValue),
				NewValue:   rawJSONToBytes(l.NewValue),
				CreatedAt:  l.CreatedAt,
			}
		}
	}

	return nil
}
