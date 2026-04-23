// Command ventd-ipmi is the privilege-separated IPMI sidecar for ventd.
// It holds the only process-level access to /dev/ipmi0 (via CAP_SYS_RAWIO)
// and serves the main ventd daemon over a Unix socket using the
// internal/hal/ipmi/proto wire protocol.
//
// The main ventd daemon runs without any IPMI privilege; all BMC commands are
// forwarded through this sidecar, which is the only process that needs
// DeviceAllow=/dev/ipmi0 and AmbientCapabilities=CAP_SYS_RAWIO.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/ipmi"
	"github.com/ventd/ventd/internal/hal/ipmi/proto"
)

const (
	maxConns        = 4
	shutdownTimeout = 5 * time.Second
	socketBaseDir   = "/run/ventd"
)

func main() {
	socketPath := flag.String("socket", "/run/ventd/ipmi.sock", "Unix socket path to listen on")
	flag.Parse()

	if err := validateSocketPath(*socketPath); err != nil {
		fmt.Fprintf(os.Stderr, "ventd-ipmi: %v\n", err)
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	b := ipmi.NewBackend(log)
	s := &sidecar{backend: b, log: log}

	// Remove stale socket file from a previous run.
	_ = os.Remove(*socketPath)

	ln, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Error("listen failed", "path", *socketPath, "err", err)
		os.Exit(1)
	}

	// Close listener when context is cancelled so Accept unblocks.
	go func() {
		<-ctx.Done()
		if err := ln.Close(); err != nil {
			log.Error("listener close", "err", err)
		}
	}()

	sdNotifyReady()
	log.Info("ventd-ipmi ready", "socket", *socketPath)

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConns)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break // normal shutdown
			}
			log.Error("accept", "err", err)
			continue
		}

		select {
		case sem <- struct{}{}:
			wg.Go(func() {
				defer func() { <-sem }()
				s.handleConn(ctx, conn)
			})
		default:
			log.Warn("connection limit reached, rejecting", "max", maxConns)
			_ = conn.Close()
		}
	}

	// Drain in-flight connections, hard-cap at shutdownTimeout.
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(shutdownTimeout):
		log.Warn("shutdown timeout elapsed with connections still active")
	}

	s.restoreOnExit()
	if err := b.Close(); err != nil {
		log.Error("backend close", "err", err)
	}
}

// sidecar dispatches proto requests to the IPMI backend.
type sidecar struct {
	backend *ipmi.Backend
	log     *slog.Logger

	mu      sync.Mutex
	lastChs []hal.Channel // updated on each ENUMERATE, used for restoreOnExit
}

func (s *sidecar) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	codec := proto.NewCodec(conn, conn)
	for {
		req, err := codec.ReadRequest()
		if err != nil {
			if !isNormalConnClose(err) {
				s.log.Debug("read request", "err", err)
			}
			return
		}
		resp := s.dispatch(ctx, req)
		if err := codec.WriteResponse(resp); err != nil {
			s.log.Debug("write response", "err", err)
			return
		}
	}
}

func (s *sidecar) dispatch(ctx context.Context, req *proto.Request) *proto.Response {
	switch req.Op {
	case proto.OpEnumerate:
		return s.opEnumerate(ctx, req)
	case proto.OpReadSensors:
		return s.opRead(req)
	case proto.OpWriteDuty:
		return s.opWrite(req)
	case proto.OpRestore:
		return s.opRestore(req)
	case proto.OpSetManualMode:
		// IPMI takes manual control implicitly on the first Write; no separate step.
		return &proto.Response{ReqID: req.ReqID, OK: true}
	default:
		return errResponse(req.ReqID, fmt.Errorf("unknown op %q", req.Op))
	}
}

func (s *sidecar) opEnumerate(ctx context.Context, req *proto.Request) *proto.Response {
	channels, err := s.backend.Enumerate(ctx)
	if err != nil {
		return errResponse(req.ReqID, err)
	}

	wires := make([]proto.ChannelWire, 0, len(channels))
	for _, ch := range channels {
		opaque, err := json.Marshal(ch.Opaque)
		if err != nil {
			s.log.Warn("marshal channel opaque", "id", ch.ID, "err", err)
			continue
		}
		wires = append(wires, proto.ChannelWire{
			ID:     ch.ID,
			Role:   string(ch.Role),
			Caps:   uint32(ch.Caps),
			Opaque: json.RawMessage(opaque),
		})
	}

	s.mu.Lock()
	s.lastChs = channels
	s.mu.Unlock()

	data, err := json.Marshal(wires)
	if err != nil {
		return errResponse(req.ReqID, err)
	}
	return &proto.Response{ReqID: req.ReqID, OK: true, Data: json.RawMessage(data)}
}

func (s *sidecar) opRead(req *proto.Request) *proto.Response {
	ch, err := unmarshalChannel(req.Channel)
	if err != nil {
		return errResponse(req.ReqID, err)
	}
	reading, err := s.backend.Read(ch)
	if err != nil {
		return errResponse(req.ReqID, err)
	}
	data, _ := json.Marshal(proto.ReadingWire{
		PWM:  reading.PWM,
		RPM:  reading.RPM,
		Temp: reading.Temp,
		OK:   reading.OK,
	})
	return &proto.Response{ReqID: req.ReqID, OK: true, Data: json.RawMessage(data)}
}

func (s *sidecar) opWrite(req *proto.Request) *proto.Response {
	ch, err := unmarshalChannel(req.Channel)
	if err != nil {
		return errResponse(req.ReqID, err)
	}
	if err := s.backend.Write(ch, req.Duty); err != nil {
		return errResponse(req.ReqID, err)
	}
	return &proto.Response{ReqID: req.ReqID, OK: true}
}

func (s *sidecar) opRestore(req *proto.Request) *proto.Response {
	ch, err := unmarshalChannel(req.Channel)
	if err != nil {
		return errResponse(req.ReqID, err)
	}
	if err := s.backend.Restore(ch); err != nil {
		return errResponse(req.ReqID, err)
	}
	return &proto.Response{ReqID: req.ReqID, OK: true}
}

// restoreOnExit sends the vendor-specific "return to auto" command on sidecar
// shutdown. Uses the first channel from the last ENUMERATE result. If no
// ENUMERATE was ever called (no client connected), this is a no-op.
func (s *sidecar) restoreOnExit() {
	s.mu.Lock()
	chs := s.lastChs
	s.mu.Unlock()
	if len(chs) == 0 {
		return
	}
	if err := s.backend.Restore(chs[0]); err != nil {
		s.log.Error("restore on exit", "err", err)
	}
}

// unmarshalChannel decodes a proto.ChannelWire and its embedded ipmi.State
// from the JSON raw message carried in a proto.Request.Channel field.
func unmarshalChannel(raw json.RawMessage) (hal.Channel, error) {
	var wire proto.ChannelWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return hal.Channel{}, fmt.Errorf("unmarshal channel wire: %w", err)
	}
	var state ipmi.State
	if err := json.Unmarshal(wire.Opaque, &state); err != nil {
		return hal.Channel{}, fmt.Errorf("unmarshal ipmi state: %w", err)
	}
	return hal.Channel{
		ID:     wire.ID,
		Role:   hal.ChannelRole(wire.Role),
		Caps:   hal.Caps(wire.Caps),
		Opaque: state,
	}, nil
}

// validateSocketPath rejects paths that are not absolute or are not under
// socketBaseDir. The sidecar must not bind a socket outside /run/ventd/.
func validateSocketPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("socket path must be absolute: %q", p)
	}
	if p != socketBaseDir && !strings.HasPrefix(p, socketBaseDir+"/") {
		return fmt.Errorf("socket path must be under %s: %q", socketBaseDir, p)
	}
	return nil
}

// sdNotifyReady sends READY=1 to the systemd notify socket if present.
// Implemented without coreos/go-systemd to avoid adding a dependency.
func sdNotifyReady() {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return
	}
	// An abstract socket address starts with '@'; replace with the null byte.
	if strings.HasPrefix(sock, "@") {
		sock = "\x00" + sock[1:]
	}
	conn, err := net.Dial("unixgram", sock)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("READY=1\n"))
}

func errResponse(reqID int64, err error) *proto.Response {
	return &proto.Response{ReqID: reqID, OK: false, Err: err.Error()}
}

func isNormalConnClose(err error) bool {
	return errors.Is(err, io.EOF) ||
		strings.Contains(err.Error(), "use of closed network connection")
}
