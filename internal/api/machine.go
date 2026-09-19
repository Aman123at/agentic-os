package api

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/service"
	"github.com/Aman123at/agentic-os/internal/software"
)

// ---------------------------------------------------------------- Software (PLAN.md §11)

type softwareService struct{ s *Server }

func (sw softwareService) manager() (*software.Manager, error) {
	if sw.s.Software == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("software management is not available"))
	}
	return sw.s.Software, nil
}

func (sw softwareService) ListPackages(ctx context.Context, _ *connect.Request[aosv1.ListPackagesRequest]) (*connect.Response[aosv1.ListPackagesResponse], error) {
	m, err := sw.manager()
	if err != nil {
		return nil, err
	}
	pkgs, err := m.Ledger.Packages(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListPackagesResponse{Packages: pkgs}), nil
}

func (sw softwareService) ListLedger(ctx context.Context, req *connect.Request[aosv1.ListLedgerRequest]) (*connect.Response[aosv1.ListLedgerResponse], error) {
	m, err := sw.manager()
	if err != nil {
		return nil, err
	}
	ops, err := m.Ledger.List(ctx, int(req.Msg.Limit), req.Msg.BeforeId)
	if err != nil {
		return nil, connectError(err)
	}
	resp := &aosv1.ListLedgerResponse{}
	for _, op := range ops {
		resp.Ops = append(resp.Ops, op.Proto())
	}
	return connect.NewResponse(resp), nil
}

func (sw softwareService) ListCheckpoints(ctx context.Context, _ *connect.Request[aosv1.ListCheckpointsRequest]) (*connect.Response[aosv1.ListCheckpointsResponse], error) {
	m, err := sw.manager()
	if err != nil {
		return nil, err
	}
	cps, err := m.Ledger.Checkpoints(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.ListCheckpointsResponse{Checkpoints: cps}), nil
}

func (sw softwareService) CreateCheckpoint(ctx context.Context, req *connect.Request[aosv1.CreateCheckpointRequest]) (*connect.Response[aosv1.CreateCheckpointResponse], error) {
	m, err := sw.manager()
	if err != nil {
		return nil, err
	}
	cp, err := m.Checkpoint(ctx, software.Call{Actor: ActorFrom(ctx)}, req.Msg.Name)
	if err != nil {
		return nil, connectError(err)
	}
	_ = sw.s.Audit.Record(ctx, audit.Entry{Tool: "create_checkpoint", Arguments: `{"id":` + quote(cp.Id) + `}`, Result: cp.Name,
		Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	return connect.NewResponse(&aosv1.CreateCheckpointResponse{Checkpoint: cp}), nil
}

func (sw softwareService) RestoreCheckpoint(ctx context.Context, req *connect.Request[aosv1.RestoreCheckpointRequest]) (*connect.Response[aosv1.RestoreCheckpointResponse], error) {
	m, err := sw.manager()
	if err != nil {
		return nil, err
	}
	out, before, err := m.Restore(ctx, software.Call{Actor: ActorFrom(ctx)}, req.Msg.Id)
	resp := &aosv1.RestoreCheckpointResponse{Before: before, Notes: out.Notes}
	result := "nothing needed changing"
	if out.Op != nil {
		resp.Op = out.Op.Proto()
		result = fmt.Sprintf("Ledger #%d: %d changes", out.Op.ID, len(out.Op.Changes))
	}
	if err != nil {
		result = err.Error()
	}
	_ = sw.s.Audit.Record(ctx, audit.Entry{Tool: "restore_checkpoint", Arguments: `{"id":` + quote(req.Msg.Id) + `}`, Result: result,
		Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(resp), nil
}

// ---------------------------------------------------------------- Services (PLAN.md §12)

type supervisorService struct{ s *Server }

func (sv supervisorService) supervisor() (*service.Supervisor, error) {
	if sv.s.Supervisor == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the Service supervisor is not available"))
	}
	return sv.s.Supervisor, nil
}

func (sv supervisorService) ListServices(ctx context.Context, _ *connect.Request[aosv1.ListServicesRequest]) (*connect.Response[aosv1.ListServicesResponse], error) {
	s, err := sv.supervisor()
	if err != nil {
		return nil, err
	}
	s.Scan() // ports as they are now, not as of the last 2 s scan
	resp := &aosv1.ListServicesResponse{Services: s.List()}
	for _, l := range s.Listeners() {
		if l.Internal() {
			continue
		}
		resp.Listeners = append(resp.Listeners, &aosv1.Listener{Port: int32(l.Port), Pid: int32(l.PID), Process: l.Process, Service: l.Service, Address: l.Address, Reachable: l.Reachable()})
	}
	return connect.NewResponse(resp), nil
}

// act runs a user's action on a Service and records it in the Audit Log.
func (sv supervisorService) act(ctx context.Context, tool, name string, do func(*service.Supervisor) (*aosv1.ServiceInfo, error)) (*aosv1.ServiceInfo, error) {
	s, err := sv.supervisor()
	if err != nil {
		return nil, err
	}
	info, err := do(s)
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	_ = sv.s.Audit.Record(ctx, audit.Entry{Tool: tool, Arguments: `{"name":` + quote(name) + `}`, Result: result,
		Decision: "allow", DecidedBy: ActorFrom(ctx), Actor: ActorFrom(ctx)})
	return info, connectError(err)
}

func (sv supervisorService) StartService(ctx context.Context, req *connect.Request[aosv1.StartServiceRequest]) (*connect.Response[aosv1.StartServiceResponse], error) {
	info, err := sv.act(ctx, "service_start", req.Msg.Name, func(s *service.Supervisor) (*aosv1.ServiceInfo, error) { return s.Start(req.Msg.Name) })
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&aosv1.StartServiceResponse{Service: info}), nil
}

func (sv supervisorService) StopService(ctx context.Context, req *connect.Request[aosv1.StopServiceRequest]) (*connect.Response[aosv1.StopServiceResponse], error) {
	info, err := sv.act(ctx, "service_stop", req.Msg.Name, func(s *service.Supervisor) (*aosv1.ServiceInfo, error) { return s.Stop(req.Msg.Name) })
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&aosv1.StopServiceResponse{Service: info}), nil
}

func (sv supervisorService) RestartService(ctx context.Context, req *connect.Request[aosv1.RestartServiceRequest]) (*connect.Response[aosv1.RestartServiceResponse], error) {
	info, err := sv.act(ctx, "service_restart", req.Msg.Name, func(s *service.Supervisor) (*aosv1.ServiceInfo, error) { return s.Restart(req.Msg.Name) })
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&aosv1.RestartServiceResponse{Service: info}), nil
}

func (sv supervisorService) RemoveService(ctx context.Context, req *connect.Request[aosv1.RemoveServiceRequest]) (*connect.Response[aosv1.RemoveServiceResponse], error) {
	_, err := sv.act(ctx, "service_remove", req.Msg.Name, func(s *service.Supervisor) (*aosv1.ServiceInfo, error) {
		return nil, s.Remove(ctx, req.Msg.Name, ActorFrom(ctx), "")
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&aosv1.RemoveServiceResponse{}), nil
}

func (sv supervisorService) StreamLogs(ctx context.Context, req *connect.Request[aosv1.StreamLogsRequest], stream *connect.ServerStream[aosv1.StreamLogsResponse]) error {
	s, err := sv.supervisor()
	if err != nil {
		return err
	}
	logs, err := s.Logs(req.Msg.Name)
	if err != nil {
		return connectError(err)
	}
	tail := int(req.Msg.TailBytes)
	if tail <= 0 {
		tail = 64 << 10
	}
	recent, feed, stop := logs.Watch(tail)
	defer stop()
	if len(recent) > 0 {
		if err := stream.Send(&aosv1.StreamLogsResponse{Data: recent}); err != nil {
			return err
		}
	}
	if !req.Msg.Follow {
		return nil
	}
	for {
		select {
		case chunk, ok := <-feed:
			if !ok {
				return nil // the Service was removed
			}
			if err := stream.Send(&aosv1.StreamLogsResponse{Data: chunk}); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}
