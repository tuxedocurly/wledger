package mapper_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
)

func TestCalculateGlobalIndex(t *testing.T) {
	tests := []struct {
		name        string
		containers  []db.Container // Should be sorted by order
		targetBin   db.Bin
		wantSegment int64
		wantIndex   int64
		wantErr     bool
	}{
		{
			name: "Single Container (8x8)",
			containers: []db.Container{
				{ID: 1, Name: "C1", SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true}},
			},
			targetBin:   db.Bin{ContainerID: 1, LedIndex: sql.NullInt64{Int64: 10, Valid: true}},
			wantSegment: 0,
			wantIndex:   10,
		},
		{
			name: "Two Containers Same Segment (Absolute Index 69)",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true}}, // 64
				{ID: 2, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"linear","total":16}`, Valid: true}},      // 16
			},
			targetBin:   db.Bin{ContainerID: 2, LedIndex: sql.NullInt64{Int64: 69, Valid: true}},
			wantSegment: 0,
			wantIndex:   69,
		},
		{
			name: "Two Containers Different Segments",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true}},
				{ID: 2, SegmentID: 1, ConfigJson: sql.NullString{String: `{"type":"linear","total":16}`, Valid: true}},
			},
			targetBin:   db.Bin{ContainerID: 2, LedIndex: sql.NullInt64{Int64: 5, Valid: true}},
			wantSegment: 1,
			wantIndex:   5,
		},
		{
			name: "Compound Container (Absolute Index 32)",
			containers: []db.Container{
				// Compound: 2 sections of 4x4 = 16 + 16 = 32
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"compound","sections":[{"rows":4,"cols":4},{"rows":4,"cols":4}]}`, Valid: true}},
				{ID: 2, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}},
			},
			targetBin:   db.Bin{ContainerID: 2, LedIndex: sql.NullInt64{Int64: 32, Valid: true}},
			wantSegment: 0,
			wantIndex:   32,
		},
		{
			name: "Container Not Found",
			containers: []db.Container{
				{ID: 1, SegmentID: 0},
			},
			targetBin: db.Bin{ContainerID: 99, LedIndex: sql.NullInt64{Int64: 0, Valid: true}},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg, idx, err := mapper.CalculateGlobalIndex(tt.containers, tt.targetBin)
			if (err != nil) != tt.wantErr {
				t.Errorf("CalculateGlobalIndex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if seg != tt.wantSegment {
					t.Errorf("CalculateGlobalIndex() segment = %v, want %v", seg, tt.wantSegment)
				}
				if idx != tt.wantIndex {
					t.Errorf("CalculateGlobalIndex() index = %v, want %v", idx, tt.wantIndex)
				}
			}
		})
	}
}

func TestResolveBinRanges(t *testing.T) {
	tests := []struct {
		name       string
		containers []db.Container
		targetBin  db.Bin
		want       []mapper.MappedRange
		wantErr    bool
	}{
		{
			name: "Standard Grid Bin Delegates With Width",
			containers: []db.Container{
				{ID: 1, SegmentID: 2, ConfigJson: sql.NullString{String: `{"type":"grid","rows":4,"cols":4}`, Valid: true}},
			},
			targetBin: db.Bin{
				ContainerID: 1,
				LedIndex:    sql.NullInt64{Int64: 12, Valid: true},
				Width:       sql.NullInt64{Int64: 3, Valid: true},
			},
			want: []mapper.MappedRange{
				{SegmentID: 2, StartIndex: 12, Width: 3},
			},
		},
		{
			name: "XY Default Starts (X=0, Y=Cols)",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6}`, Valid: true}},
			},
			targetBin: db.Bin{
				ContainerID: 1,
				GridX:       sql.NullInt64{Int64: 2, Valid: true},
				GridY:       sql.NullInt64{Int64: 3, Valid: true},
			},
			want: []mapper.MappedRange{
				{SegmentID: 0, StartIndex: 2, Width: 1},
				{SegmentID: 0, StartIndex: 9, Width: 1},
			},
		},
		{
			name: "XY Custom Starts With Explicit Y=0 And X=Rows",
			containers: []db.Container{
				{ID: 5, SegmentID: 1, ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6,"x_start_led":4,"y_start_led":0}`, Valid: true}},
			},
			targetBin: db.Bin{
				ContainerID: 5,
				GridX:       sql.NullInt64{Int64: 5, Valid: true},
				GridY:       sql.NullInt64{Int64: 1, Valid: true},
			},
			want: []mapper.MappedRange{
				{SegmentID: 1, StartIndex: 9, Width: 1},
				{SegmentID: 1, StartIndex: 1, Width: 1},
			},
		},
		{
			name: "XY Reversed Both Axes With Offsets",
			containers: []db.Container{
				{ID: 3, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6,"x_start_led":20,"x_reverse":true,"y_start_led":50,"y_reverse":true}`, Valid: true}},
			},
			targetBin: db.Bin{
				ContainerID: 3,
				GridX:       sql.NullInt64{Int64: 1, Valid: true}, // cols-1-1 = 4 => 24
				GridY:       sql.NullInt64{Int64: 0, Valid: true}, // rows-1-0 = 3 => 53
			},
			want: []mapper.MappedRange{
				{SegmentID: 0, StartIndex: 24, Width: 1},
				{SegmentID: 0, StartIndex: 53, Width: 1},
			},
		},
		{
			name: "XY Missing Coordinates Returns Error",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6}`, Valid: true}},
			},
			targetBin: db.Bin{
				ContainerID: 1,
				GridX:       sql.NullInt64{Int64: 1, Valid: true},
			},
			wantErr: true,
		},
		{
			name: "XY Out Of Bounds Returns Error",
			containers: []db.Container{
				{ID: 1, SegmentID: 0, ConfigJson: sql.NullString{String: `{"type":"xy","rows":4,"cols":6}`, Valid: true}},
			},
			targetBin: db.Bin{
				ContainerID: 1,
				GridX:       sql.NullInt64{Int64: 6, Valid: true},
				GridY:       sql.NullInt64{Int64: 1, Valid: true},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mapper.ResolveBinRanges(tt.containers, tt.targetBin)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveBinRanges() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolveBinRanges() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestXYHelpers(t *testing.T) {
	c := db.Container{
		ID:         1,
		SegmentID:  0,
		ConfigJson: sql.NullString{String: `{"type":"xy","rows":5,"cols":8}`, Valid: true},
	}
	length, err := mapper.GetContainerLength(c)
	if err != nil {
		t.Fatalf("GetContainerLength() error = %v", err)
	}
	if length != 13 {
		t.Errorf("GetContainerLength() = %d, want 13", length)
	}

	xStart, yStart := 10, 30
	extent := mapper.ContainerMaxLEDExtent(mapper.ContainerConfig{
		Type:      "xy",
		Rows:      4,
		Cols:      6,
		XStartLed: &xStart,
		YStartLed: &yStart,
	})
	if extent != 34 {
		t.Errorf("ContainerMaxLEDExtent() = %d, want 34", extent)
	}

	if !mapper.IsAssignmentLocatable(true, false, true, true, true, sql.NullString{String: "192.168.1.5", Valid: true}, c.ConfigJson) {
		t.Errorf("expected XY assignment with NULL led_index and valid coords to be locatable")
	}
	if mapper.IsAssignmentLocatable(true, false, true, false, true, sql.NullString{String: "192.168.1.5", Valid: true}, c.ConfigJson) {
		t.Errorf("expected XY assignment missing grid_y not to be locatable")
	}
	if mapper.IsAssignmentLocatable(true, false, true, true, true, sql.NullString{String: "192.168.1.5", Valid: true}, sql.NullString{String: `{"type":"grid","rows":5,"cols":8}`, Valid: true}) {
		t.Errorf("expected grid assignment with NULL led_index not to be locatable")
	}
	if !mapper.IsAssignmentLocatable(true, true, false, false, true, sql.NullString{String: "192.168.1.5", Valid: true}, sql.NullString{}) {
		t.Errorf("expected standard assignment with led_index to remain locatable")
	}
}
