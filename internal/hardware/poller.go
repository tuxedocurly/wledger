package hardware

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

const (
	defaultPingIntervalSeconds = 600
	defaultMinInterval         = 10 * time.Second
	maxPingInterval            = 24 * time.Hour
)

// PollerOption configures optional StatusPoller behavior.
type PollerOption func(*StatusPoller)

// WithMinInterval overrides the minimum allowed polling interval (primarily for tests).
func WithMinInterval(d time.Duration) PollerOption {
	return func(p *StatusPoller) {
		if d > 0 {
			p.minInterval = d
		}
	}
}

// WithIntervalUnit overrides the unit used when converting stored seconds into durations (primarily for tests).
func WithIntervalUnit(d time.Duration) PollerOption {
	return func(p *StatusPoller) {
		if d > 0 {
			p.intervalUnit = d
		}
	}
}

// WithOnCycle registers a hook called after each completed ping cycle (primarily for tests).
func WithOnCycle(fn func()) PollerOption {
	return func(p *StatusPoller) {
		p.onCycle = fn
	}
}

// StatusPoller periodically checks all configured WLED controllers so online/offline state
// stays accurate across the dashboard and hardware pages without client-side polling.
type StatusPoller struct {
	store        db.Store
	hw           Service
	logger       *slog.Logger
	minInterval  time.Duration
	intervalUnit time.Duration
	onCycle      func()

	reloadCh  chan struct{}
	triggerCh chan struct{}
}

// NewStatusPoller constructs a background controller status poller.
func NewStatusPoller(store db.Store, hw Service, logger *slog.Logger, opts ...PollerOption) *StatusPoller {
	p := &StatusPoller{
		store:        store,
		hw:           hw,
		logger:       logger,
		minInterval:  defaultMinInterval,
		intervalUnit: time.Second,
		reloadCh:     make(chan struct{}, 1),
		triggerCh:    make(chan struct{}, 1),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// NotifySettingsChanged wakes the poller to re-read ping settings and reschedule.
func (p *StatusPoller) NotifySettingsChanged() {
	if p == nil {
		return
	}
	select {
	case p.reloadCh <- struct{}{}:
	default:
	}
}

// TriggerNow runs an immediate ping cycle (coalescing rapid duplicate requests).
func (p *StatusPoller) TriggerNow() {
	if p == nil {
		return
	}
	select {
	case p.triggerCh <- struct{}{}:
	default:
	}
}

// Run runs the periodic ping loop until ctx is canceled.
func (p *StatusPoller) Run(ctx context.Context) {
	enabled, interval := p.loadSchedule(ctx)
	if enabled {
		p.pingCycle(ctx)
	}

	var timer *time.Timer
	var timerC <-chan time.Time
	resetTimer := func(on bool, d time.Duration) {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
			timerC = nil
		}
		if on {
			timer = time.NewTimer(d)
			timerC = timer.C
		}
	}

	resetTimer(enabled, interval)
	defer resetTimer(false, 0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timerC:
			timer = nil
			timerC = nil
			enabled, interval = p.loadSchedule(ctx)
			if enabled {
				p.pingCycle(ctx)
			}
			resetTimer(enabled, interval)
		case <-p.triggerCh:
			p.pingCycle(ctx)
			enabled, interval = p.loadSchedule(ctx)
			resetTimer(enabled, interval)
		case <-p.reloadCh:
			wasEnabled := enabled
			enabled, interval = p.loadSchedule(ctx)
			if !wasEnabled && enabled {
				p.pingCycle(ctx)
			}
			resetTimer(enabled, interval)
		}
	}
}

func (p *StatusPoller) loadSchedule(ctx context.Context) (bool, time.Duration) {
	sett, err := p.store.GetSettings(ctx)
	if err != nil {
		return true, p.clampInterval(time.Duration(defaultPingIntervalSeconds) * p.intervalUnit)
	}
	enabled := !sett.EnableControllerPing.Valid || sett.EnableControllerPing.Bool
	secs := sett.ControllerPingIntervalSeconds.Int64
	if !sett.ControllerPingIntervalSeconds.Valid || secs <= 0 {
		secs = defaultPingIntervalSeconds
	}
	return enabled, p.clampInterval(time.Duration(secs) * p.intervalUnit)
}

func (p *StatusPoller) clampInterval(d time.Duration) time.Duration {
	if d < p.minInterval {
		return p.minInterval
	}
	if d > maxPingInterval {
		return maxPingInterval
	}
	return d
}

func (p *StatusPoller) pingCycle(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if err := p.hw.PingAllControllers(ctx); err != nil && !errors.Is(err, context.Canceled) {
		if p.logger != nil {
			p.logger.Debug("periodic controller ping finished with errors", "err", err)
		}
	}
	if p.onCycle != nil {
		p.onCycle()
	}
}
