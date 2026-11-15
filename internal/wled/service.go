package wled

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
)

type Service interface {
	LocatePart(ctx context.Context, partID int64) error
	LocateBin(ctx context.Context, controllerID, binID int64) error
	GlobalOff(ctx context.Context) error
	Ping(ctx context.Context, ip string) (bool, error)
}

type controllerState struct {
	opMu          sync.Mutex
	gen           uint64
	active        bool
	timer         *time.Timer
	snapshot      *WLEDSnapshot
	knownSegments []int
	ledCount      int
}

type service struct {
	store  db.Store
	client *Client
	logger *slog.Logger

	timersMu        sync.Mutex
	controllers     map[int64]*controllerState
	timeoutOverride time.Duration // optional test hook for fast timeout verification
}

func NewService(store db.Store, client *Client, logger *slog.Logger) Service {
	return &service{
		store:       store,
		client:      client,
		logger:      logger,
		controllers: make(map[int64]*controllerState),
	}
}

func (s *service) getControllerState(ctrlID int64) *controllerState {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	st, ok := s.controllers[ctrlID]
	if !ok {
		st = &controllerState{}
		s.controllers[ctrlID] = st
	}
	return st
}

func (s *service) Ping(ctx context.Context, ip string) (bool, error) {
	return s.client.Ping(ctx, ip)
}

func (s *service) GlobalOff(ctx context.Context) error {
	controllers, err := s.store.GetControllers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch controllers: %w", err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, c := range controllers {
		wg.Add(1)
		go func(ctrl db.Controller) {
			defer wg.Done()

			reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()

			if offErr := s.turnOffController(reqCtx, ctrl); offErr != nil {
				s.logger.Error("failed to clear controller", "name", ctrl.Name, "ip", ctrl.IpAddress, "err", offErr)
				mu.Lock()
				errs = append(errs, fmt.Errorf("controller %s (%s): %w", ctrl.Name, ctrl.IpAddress, offErr))
				mu.Unlock()
			}
		}(c)
	}

	wg.Wait()
	return errors.Join(errs...)
}

func (s *service) turnOffController(ctx context.Context, ctrl db.Controller) error {
	st := s.getControllerState(ctrl.ID)
	st.opMu.Lock()
	defer st.opMu.Unlock()

	s.timersMu.Lock()
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.gen++
	st.active = false
	snap := st.snapshot
	knownSegs := append([]int(nil), st.knownSegments...)
	ledCount := st.ledCount
	st.snapshot = nil
	s.timersMu.Unlock()

	containers, err := s.store.GetContainersByController(ctx, sql.NullInt64{Int64: ctrl.ID, Valid: true})
	if err == nil {
		knownSegs = mergeKnownSegments(knownSegs, containers, snap)
	} else {
		knownSegs = mergeKnownSegments(knownSegs, nil, snap)
	}
	ledCount = maxInt(ledCount, int(ctrl.LedCount))
	if snap != nil {
		ledCount = maxInt(ledCount, snap.LedCount)
	}

	return s.finishLocate(ctx, ctrl, knownSegs, ledCount, snap)
}

func (s *service) LocatePart(ctx context.Context, partID int64) error {
	part, err := s.store.GetPart(ctx, partID)
	if err != nil {
		return fmt.Errorf("part not found: %w", err)
	}

	assignments, err := s.store.GetPartAssignments(ctx, partID)
	if err != nil {
		return fmt.Errorf("failed to fetch part locations: %w", err)
	}

	settings, _ := s.store.GetSettings(ctx)

	type ctrlBatch struct {
		assignments []db.GetPartAssignmentsRow
	}
	batches := make(map[int64]*ctrlBatch)
	var ctrlIDs []int64

	for _, a := range assignments {
		// Skip empty/depleted stock, unmounted standalone bins, or bins without LEDs.
		if a.Quantity <= 0 {
			continue
		}
		if !a.BinID.Valid || !a.LedIndex.Valid || !a.ControllerID.Valid || !a.ControllerIp.Valid || a.ControllerIp.String == "" {
			continue
		}
		b, ok := batches[a.ControllerID.Int64]
		if !ok {
			b = &ctrlBatch{}
			batches[a.ControllerID.Int64] = b
			ctrlIDs = append(ctrlIDs, a.ControllerID.Int64)
		}
		b.assignments = append(b.assignments, a)
	}

	if len(ctrlIDs) == 0 {
		s.logger.Info("no valid assignments found to locate", "part_id", partID)
		return nil
	}

	sort.Slice(ctrlIDs, func(i, j int) bool { return ctrlIDs[i] < ctrlIDs[j] })

	var errs []error
	foundAny := false

	for _, ctrlID := range ctrlIDs {
		ctrl, err := s.store.GetController(ctx, ctrlID)
		if err != nil {
			errs = append(errs, fmt.Errorf("controller %d not found: %w", ctrlID, err))
			continue
		}

		containers, err := s.store.GetContainersByController(ctx, sql.NullInt64{Int64: ctrlID, Valid: true})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to fetch containers for controller %d: %w", ctrlID, err))
			continue
		}

		bySeg := make(map[int][]LEDRange)
		for _, a := range batches[ctrlID].assignments {
			bin := db.Bin{
				ID:          a.BinID.Int64,
				ContainerID: a.ContainerID.Int64,
				LedIndex:    a.LedIndex,
				Width:       a.Width,
			}

			segID, globalIdx, mapErr := mapper.CalculateGlobalIndex(containers, bin)
			if mapErr != nil {
				s.logger.Error("mapping failed for part locate", "bin_id", bin.ID, "err", mapErr)
				continue
			}

			width := int(a.Width.Int64)
			if width < 1 {
				width = 1
			}

			hexColor := resolveLocateHexColor(settings)
			if settings.LocateColorByStock.Bool {
				hexColor = resolveStockHealthHexColor(settings, part, a.Quantity)
			}
			rgb, rgbErr := HexToRGBArray(hexColor)
			if rgbErr != nil {
				rgb = [3]byte{0, 0, 255}
			}

			bySeg[int(segID)] = append(bySeg[int(segID)], LEDRange{
				Start: int(globalIdx),
				Count: width,
				RGB:   rgb,
			})
			foundAny = true
		}

		if len(bySeg) == 0 {
			continue
		}

		if locErr := s.executeLocate(ctx, ctrl, containers, bySeg, settings); locErr != nil {
			errs = append(errs, fmt.Errorf("controller %s (%s): %w", ctrl.Name, ctrl.IpAddress, locErr))
		}
	}

	if !foundAny && len(errs) == 0 {
		s.logger.Info("no mapped assignments found to locate", "part_id", partID)
	}

	return errors.Join(errs...)
}

func (s *service) LocateBin(ctx context.Context, controllerID, binID int64) error {
	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("controller not found: %w", err)
	}

	bin, err := s.store.GetBin(ctx, binID)
	if err != nil {
		return fmt.Errorf("bin not found: %w", err)
	}

	containers, err := s.store.GetContainersByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
	if err != nil {
		return fmt.Errorf("failed to fetch containers for controller: %w", err)
	}

	segID, globalIdx, err := mapper.CalculateGlobalIndex(containers, bin)
	if err != nil {
		return fmt.Errorf("mapping failed: %w", err)
	}

	settings, _ := s.store.GetSettings(ctx)
	hexColor := resolveLocateHexColor(settings)
	if settings.LocateColorByStock.Bool {
		if binHex, ok := s.resolveBinStockColor(ctx, binID, settings); ok {
			hexColor = binHex
		}
	}

	rgb, rgbErr := HexToRGBArray(hexColor)
	if rgbErr != nil {
		rgb = [3]byte{0, 0, 255}
	}

	width := int(bin.Width.Int64)
	if width < 1 {
		width = 1
	}

	bySeg := map[int][]LEDRange{
		int(segID): {
			{Start: int(globalIdx), Count: width, RGB: rgb},
		},
	}

	return s.executeLocate(ctx, controller, containers, bySeg, settings)
}

func (s *service) resolveBinStockColor(ctx context.Context, binID int64, settings db.Setting) (string, bool) {
	parts, err := s.store.ListParts(ctx, db.ListPartsParams{
		BinID:  sql.NullInt64{Int64: binID, Valid: true},
		Limit:  50,
		Offset: 0,
	})
	if err != nil || len(parts) == 0 {
		return "", false
	}

	assignments, err := s.store.GetAllPartAssignments(ctx)
	if err != nil {
		return "", false
	}
	qtyByPart := make(map[int64]int64)
	for _, a := range assignments {
		if a.BinID.Valid && a.BinID.Int64 == binID && a.Quantity > 0 {
			qtyByPart[a.PartID] += a.Quantity
		}
	}

	worstRank := 0 // 0=none, 1=ok, 2=low, 3=critical
	for _, p := range parts {
		qty, ok := qtyByPart[p.ID]
		if !ok || qty <= 0 {
			continue
		}
		minThresh := p.MinStockThreshold.Int64
		reorder := p.ReorderLevel.Int64
		rank := 1
		if qty <= minThresh {
			rank = 3
		} else if qty <= reorder {
			rank = 2
		}
		if rank > worstRank {
			worstRank = rank
		}
	}

	switch worstRank {
	case 3:
		return fallbackHex(settings.ColorStockCritical, "#EF4444"), true
	case 2:
		return fallbackHex(settings.ColorStockLow, "#F59E0B"), true
	case 1:
		return fallbackHex(settings.ColorStockOk, "#10B981"), true
	default:
		return "", false
	}
}

func (s *service) executeLocate(ctx context.Context, ctrl db.Controller, containers []db.Container, bySeg map[int][]LEDRange, settings db.Setting) error {
	st := s.getControllerState(ctrl.ID)
	st.opMu.Lock()
	defer st.opMu.Unlock()

	s.timersMu.Lock()
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.gen++
	existingSnap := st.snapshot
	prevSegs := append([]int(nil), st.knownSegments...)
	prevLedCount := st.ledCount
	s.timersMu.Unlock()

	snap := existingSnap
	if !ctrl.UseUdp.Bool && ctrl.RestoreState.Bool && snap == nil {
		fetchedSnap, snapErr := s.client.GetSnapshotWithPort(ctx, ctrl.IpAddress, ctrl.Port)
		if snapErr != nil {
			s.logger.Debug("unable to snapshot WLED state before locate", "controller", ctrl.Name, "err", snapErr)
		} else {
			snap = fetchedSnap
		}
	}

	knownSegs := mergeKnownSegments(prevSegs, containers, snap)
	for segID := range bySeg {
		knownSegs = appendUniqueInt(knownSegs, segID)
	}
	sort.Ints(knownSegs)

	ledCount := maxInt(prevLedCount, int(ctrl.LedCount))
	if snap != nil {
		ledCount = maxInt(ledCount, snap.LedCount)
	}
	for _, ranges := range bySeg {
		for _, r := range ranges {
			ledCount = maxInt(ledCount, r.Start+r.Count)
		}
	}

	timeoutDur, hasTimeout := s.locateTimeoutDuration(settings)

	var sendErr error
	if ctrl.UseUdp.Bool {
		udpTimeout := byte(DefaultUDPHoldTimeout)
		if hasTimeout {
			secs := int64(timeoutDur / time.Second)
			if secs < 1 {
				secs = 1
			} else if secs > 254 {
				secs = 254
			}
			udpTimeout = byte(secs)
		}
		udpRanges := flattenUDPRanges(containers, bySeg)
		udpPort := ctrl.UdpPort.Int64
		if !ctrl.UdpPort.Valid || udpPort <= 0 {
			udpPort = DefaultUDPPort
		}
		sendErr = SendDNRGBRanges(ctx, ctrl.IpAddress, udpPort, udpTimeout, ledCount, udpRanges)
	} else {
		sendErr = s.client.LightRangesWithPort(ctx, ctrl.IpAddress, ctrl.Port, ledCount, knownSegs, bySeg)
	}

	if sendErr != nil {
		return sendErr
	}

	// HTTP locate proved the controller is reachable; mark it online immediately.
	if !ctrl.UseUdp.Bool && !ctrl.IsOnline.Bool {
		if err := s.store.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{
			IsOnline: sql.NullBool{Bool: true, Valid: true},
			ID:       ctrl.ID,
		}); err != nil && s.logger != nil {
			s.logger.Debug("failed to mark controller online after locate", "controller", ctrl.Name, "err", err)
		}
	}

	s.timersMu.Lock()
	st.gen++
	myGen := st.gen
	st.active = true
	st.snapshot = snap
	st.knownSegments = knownSegs
	st.ledCount = ledCount

	if hasTimeout {
		ctrlCopy := ctrl
		st.timer = time.AfterFunc(timeoutDur, func() {
			s.handleTimeout(ctrlCopy, myGen)
		})
	}
	s.timersMu.Unlock()

	return nil
}

func (s *service) handleTimeout(ctrl db.Controller, myGen uint64) {
	st := s.getControllerState(ctrl.ID)
	st.opMu.Lock()
	defer st.opMu.Unlock()

	s.timersMu.Lock()
	if !st.active || st.gen != myGen {
		s.timersMu.Unlock()
		return
	}
	st.active = false
	st.timer = nil
	snap := st.snapshot
	knownSegs := append([]int(nil), st.knownSegments...)
	ledCount := st.ledCount
	st.snapshot = nil
	s.timersMu.Unlock()

	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.finishLocate(bgCtx, ctrl, knownSegs, ledCount, snap); err != nil {
		s.logger.Error("failed to expire locate state", "controller", ctrl.Name, "ip", ctrl.IpAddress, "err", err)
	}
}

func (s *service) finishLocate(ctx context.Context, ctrl db.Controller, knownSegments []int, ledCount int, snap *WLEDSnapshot) error {
	if ctrl.UseUdp.Bool {
		udpPort := ctrl.UdpPort.Int64
		if !ctrl.UdpPort.Valid || udpPort <= 0 {
			udpPort = DefaultUDPPort
		}
		return SendDNRGBRanges(ctx, ctrl.IpAddress, udpPort, 1, ledCount, nil)
	}

	if ctrl.IdlePresetID.Valid && ctrl.IdlePresetID.Int64 > 0 {
		return s.client.SetStateWithPort(ctx, ctrl.IpAddress, ctrl.Port, map[string]any{
			"ps": ctrl.IdlePresetID.Int64,
		})
	}

	if ctrl.RestoreState.Bool && snap != nil {
		return s.client.RestoreStateWithPort(ctx, ctrl.IpAddress, ctrl.Port, snap)
	}

	return s.client.ClearSegmentsWithPort(ctx, ctrl.IpAddress, ctrl.Port, ledCount, knownSegments)
}

func (s *service) locateTimeoutDuration(settings db.Setting) (time.Duration, bool) {
	if !settings.EnableLocateTimeout.Bool {
		return 0, false
	}
	if s.timeoutOverride > 0 {
		return s.timeoutOverride, true
	}
	if settings.LocateTimeoutSeconds.Int64 <= 0 {
		return 0, false
	}
	return time.Duration(settings.LocateTimeoutSeconds.Int64) * time.Second, true
}

func flattenUDPRanges(containers []db.Container, bySeg map[int][]LEDRange) []LEDRange {
	if len(bySeg) == 1 {
		for _, ranges := range bySeg {
			return ranges
		}
	}
	// Flatten across sorted segment IDs for UDP DNRGB linear frame indexing if multiple segments exist.
	var out []LEDRange
	for _, ranges := range bySeg {
		out = append(out, ranges...)
	}
	_ = containers
	return out
}

func resolveLocateHexColor(settings db.Setting) string {
	return fallbackHex(settings.ColorLocate, "#0000FF")
}

func resolveStockHealthHexColor(settings db.Setting, part db.Part, qty int64) string {
	minThresh := int64(0)
	if part.MinStockThreshold.Valid {
		minThresh = part.MinStockThreshold.Int64
	}
	reorder := int64(0)
	if part.ReorderLevel.Valid {
		reorder = part.ReorderLevel.Int64
	}
	if qty <= minThresh {
		return fallbackHex(settings.ColorStockCritical, "#EF4444")
	}
	if qty <= reorder {
		return fallbackHex(settings.ColorStockLow, "#F59E0B")
	}
	return fallbackHex(settings.ColorStockOk, "#10B981")
}

func fallbackHex(ns sql.NullString, def string) string {
	if ns.Valid && ns.String != "" {
		return ns.String
	}
	return def
}

func mergeKnownSegments(base []int, containers []db.Container, snap *WLEDSnapshot) []int {
	set := make(map[int]bool)
	for _, id := range base {
		set[id] = true
	}
	for _, c := range containers {
		set[int(c.SegmentID)] = true
	}
	if snap != nil {
		for _, id := range snap.SegmentIDs {
			set[id] = true
		}
	}
	set[0] = true
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func appendUniqueInt(slice []int, val int) []int {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
