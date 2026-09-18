package api

import (
	"bytes"
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/files"
)

// watchInterval is how often Watch lists a folder again. Polling sees changes
// on every Host, where file notifications miss those made on a Windows Shared
// Folder (PLAN.md §15); it costs one listing per open window every 2 s.
const watchInterval = 2 * time.Second

// Watch sends a folder's listing, then again each time it changes, until the
// client goes away. It lists through the same worker as List, as the aos user;
// a folder that cannot be listed ends the stream with the reason.
func (f fileService) Watch(ctx context.Context, req *connect.Request[aosv1.WatchRequest], stream *connect.ServerStream[aosv1.WatchResponse]) error {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return err
	}
	interval := f.s.WatchInterval
	if interval <= 0 {
		interval = watchInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var last []byte
	for {
		var entries []files.Info
		if err := f.s.UserFiles.Run(ctx, files.OpList, files.PathArgs{Path: p}, &entries, nil); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return connectError(err)
		}
		resp := &aosv1.WatchResponse{}
		for _, e := range entries {
			resp.Entries = append(resp.Entries, f.info(e))
		}
		// Send only a listing that differs from the last one sent.
		key, err := proto.MarshalOptions{Deterministic: true}.Marshal(resp)
		if err != nil {
			return err
		}
		if last == nil || !bytes.Equal(key, last) {
			last = key
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
