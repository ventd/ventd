package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/nvidia"
)

// Panic button (Session C 2e) — "pin every fan to its configured
// MaxPWM NOW, don't wait for the curve to decide". Intended for the
// thermal event an operator can see but the sensors can't yet: a GPU
// spike a hwmon tick away from being read, a known-hot workload about
// to start, or just a nervous operator who wants headroom.
//
// Design notes on why this does NOT mutate the live config:
//
//   - Setting Control.ManualPWM in the live config would persist to
//     disk on the next Apply — a stale browser tab could un-panic a
//     rig on restart, and an operator who apply'd during panic would
//     leave the config pinned to MaxPWM forever.
//   - Instead: the handler performs ONE direct MaxPWM write per fan at
//     panic start, sets a server-side flag, and the controller tick
//     loop yields the same way it does for active calibration. No
//     config mutation, no disk write, no race between panic and the
//     controllers.
//
// Timer ownership lives on this struct. Cancel and expire paths go
// through the same restore function so the flag flip, the SSE
// wake-up, and the timer cleanup are atomic from the UI's
// perspective.

// panicState is the in-memory bookkeeping for a single active panic.
// Zero value is a ready-to-use mutex; callers don't need to call any
// constructor. timer is nil when no panic is active.
type panicState struct {
	mu        sync.Mutex
	active    bool
	startedAt time.Time
	endAt     time.Time // zero => until cancelled
	timer     *time.Timer
}

// IsPanicked satisfies the controller.PanicChecker interface that the
// controller tick consults to yield during an active panic. The
// pwmPath argument is ignored — panic pins every configured fan, not
// a specific channel, so the check is a flat "is the daemon in panic
// mode?" per-controller lookup.
func (s *Server) IsPanicked(pwmPath string) bool {
	s.panic.mu.Lock()
	defer s.panic.mu.Unlock()
	return s.panic.active
}

// panicPayload mirrors /api/panic/state. The zero value serialises as
// {"active":false,"remaining_s":0,"started_at":null,"end_at":null} so
// the UI has a single branchless path for rendering the header.
type panicPayload struct {
	Active     bool   `json:"active"`
	RemainingS int    `json:"remaining_s"`
	StartedAt  string `json:"started_at,omitempty"`
	EndAt      string `json:"end_at,omitempty"`
}

// panicSnapshot returns the current UI-shaped payload. Callers MUST
// NOT hold s.panic.mu when invoking — the method locks internally.
func (s *Server) panicSnapshot() panicPayload {
	s.panic.mu.Lock()
	defer s.panic.mu.Unlock()
	p := panicPayload{Active: s.panic.active}
	if !s.panic.active {
		return p
	}
	p.StartedAt = s.panic.startedAt.UTC().Format(time.RFC3339)
	if !s.panic.endAt.IsZero() {
		p.EndAt = s.panic.endAt.UTC().Format(time.RFC3339)
		remaining := time.Until(s.panic.endAt)
		if remaining < 0 {
			remaining = 0
		}
		p.RemainingS = int(remaining.Round(time.Second).Seconds())
	}
	return p
}

// handlePanic POST /api/panic starts (or replaces) an active panic.
// Body: {"duration_s": N} where N=0 means "until cancelled". The
// handler:
//
//  1. Cancels any existing panic timer (so a second Panic click during
//     an active panic resets the countdown instead of stacking).
//  2. Writes MaxPWM to every configured fan. Errors are logged but do
//     not abort the panic — partial success is better than bailing
//     halfway and leaving the operator without a safety valve.
//  3. Sets the flag so controller ticks yield.
//  4. Arms the timer (if duration > 0) to call restorePanic when it
//     fires.
func (s *Server) handlePanic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		DurationS int `json:"duration_s"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.DurationS < 0 {
		http.Error(w, "duration_s must be non-negative", http.StatusBadRequest)
		return
	}

	live := s.cfg.Load()

	s.panic.mu.Lock()
	if s.panic.timer != nil {
		s.panic.timer.Stop()
		s.panic.timer = nil
	}
	now := time.Now()
	s.panic.active = true
	s.panic.startedAt = now
	if req.DurationS > 0 {
		s.panic.endAt = now.Add(time.Duration(req.DurationS) * time.Second)
		s.panic.timer = time.AfterFunc(time.Duration(req.DurationS)*time.Second, func() {
			s.restorePanic("timer")
		})
	} else {
		s.panic.endAt = time.Time{}
	}
	s.panic.mu.Unlock()

	s.writeMaxPWMToAllFans(live)

	s.writeJSON(r, w, s.panicSnapshot())
}

// handlePanicState GET /api/panic/state returns the current panic
// snapshot. Polling-safe: no mutation, no side effects.
func (s *Server) handlePanicState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeJSON(r, w, s.panicSnapshot())
}

// handlePanicCancel POST /api/panic/cancel ends an active panic
// immediately. Idempotent — cancelling when there is no active panic
// is a no-op 200 so a stale tab's cancel button doesn't surface an
// error.
func (s *Server) handlePanicCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.restorePanic("api")
	s.writeJSON(r, w, s.panicSnapshot())
}

// restorePanic is the single restore entry point, shared between the
// timer callback and the cancel handler. Clears the flag, stops the
// timer, logs the exit. Once the flag flips the controller ticks
// resume their normal curve evaluation on the next interval — restore
// does NOT write PWM here, because:
//
//   - The next controller tick will push each fan to its curve-derived
//     value within one interval (~2s default). Any intermediate write
//     we emitted would just be overwritten.
//   - Writing during restore risks racing with the controller's own
//     write path.
//
// The trigger argument is logged so journals distinguish "timer
// expired" from "operator cancelled".
func (s *Server) restorePanic(trigger string) {
	s.panic.mu.Lock()
	wasActive := s.panic.active
	if s.panic.timer != nil {
		s.panic.timer.Stop()
		s.panic.timer = nil
	}
	s.panic.active = false
	s.panic.startedAt = time.Time{}
	s.panic.endAt = time.Time{}
	s.panic.mu.Unlock()
	if wasActive {
		s.logger.Info("panic: restored control to curves", "trigger", trigger)
	}
}

// writeMaxPWMToAllFans performs the direct PWM=MaxPWM write per fan at
// panic start. Logs failures but does not return them — a single bad
// fan should not prevent the rest from reaching max. Same branch
// logic as controller.go's manual path so pumps and rpm_target fans
// get the right write verb.
//
// MaxPWM is authoritative: config.validate already enforces that every
// fan's MaxPWM is within the hwmon-safety bounds (not zero, not below
// pump floor, etc.), so no additional clamp is needed here.
func (s *Server) writeMaxPWMToAllFans(cfg *config.Config) {
	for _, fan := range cfg.Fans {
		pwm := fan.MaxPWM
		var err error
		switch fan.Type {
		case "nvidia":
			// nvidia fan paths encode the GPU index as a decimal string;
			// on ill-formed values we skip this fan rather than panic
			// (parse errors at this point would already have been caught
			// at config.validate, but we guard anyway because panic is a
			// safety surface).
			var idx uint64
			if _, sErr := fmtSscanUint(fan.PWMPath, &idx); sErr != nil {
				s.logger.Warn("panic: invalid nvidia fan index, skipping",
					"fan", fan.Name, "path", fan.PWMPath, "err", sErr)
				continue
			}
			err = nvidia.WriteFanSpeed(uint(idx), pwm)
		default:
			if fan.ControlKind == "rpm_target" {
				maxRPM := hwmon.ReadFanMaxRPM(fan.PWMPath)
				rpm := int(math.Round(float64(pwm) / 255.0 * float64(maxRPM)))
				err = hwmon.WriteFanTarget(fan.PWMPath, rpm)
			} else {
				err = hwmon.WritePWM(fan.PWMPath, pwm)
			}
		}
		if err != nil {
			// ENOENT means the device disappeared between config load and
			// the panic write — hwmon-safety rule 5 says log and skip.
			if errors.Is(err, fs.ErrNotExist) {
				s.logger.Warn("panic: fan disappeared between config load and write",
					"fan", fan.Name, "path", fan.PWMPath)
				continue
			}
			s.logger.Error("panic: PWM write failed",
				slog.String("fan", fan.Name),
				slog.String("path", fan.PWMPath),
				slog.Any("err", err))
		}
	}
}

// fmtSscanUint is a thin shim around fmt.Sscanf so the nvidia path
// stays self-contained. Returning (n, err) keeps callers symmetric
// with fmt.Sscanf but the count is unused.
func fmtSscanUint(s string, dst *uint64) (int, error) {
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	*dst = n
	return 1, nil
}
