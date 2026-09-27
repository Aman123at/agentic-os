package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/code"
	"github.com/Aman123at/agentic-os/internal/files"
	"github.com/Aman123at/agentic-os/internal/policy"
)

// ---------------------------------------------------------------- Sensitive System Files (M8.5)

// sensitiveReason is FileInfo's sensitive_reason: in Root Mode only, since in
// Standard Mode aos can't write any of them.
func (f fileService) sensitiveReason(p string) string {
	why, _ := f.sensitive(p, false)
	return why
}

// sensitive reports whether changing p, in Root Mode, changes a Sensitive
// System File, and what can go wrong. With tree, p's contents count too: a
// folder moved or deleted takes them along.
func (f fileService) sensitive(p string, tree bool) (string, bool) {
	if !f.s.rootMode() {
		return "", false
	}
	if f.s.sensitivity != nil {
		return f.s.sensitivity(p, tree)
	}
	if why, ok := policy.Sensitive(p); ok || !tree {
		return why, ok
	}
	if why, ok := policy.SensitiveUnder(p); ok {
		return why, true
	}
	// authorized_keys are found by name: a home folder, or the folder of them.
	for _, pattern := range []string{filepath.Join(p, ".ssh", "authorized_keys"), filepath.Join(p, "*", ".ssh", "authorized_keys")} {
		if m, _ := filepath.Glob(pattern); len(m) > 0 {
			return policy.Sensitive(m[0])
		}
	}
	return "", false
}

// refuseSensitive is the refusal of an unconfirmed change, with what the
// client needs to ask.
func refuseSensitive(p, why string) error {
	err := connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s is a Sensitive System File. %s Confirm to change it", p, why))
	if d, derr := connect.NewErrorDetail(&aosv1.SensitivePath{Path: p, Reason: why}); derr == nil {
		err.AddDetail(d)
	}
	return err
}

// keep saves the version a confirmed write is about to replace.
func (f fileService) keep(p string) error {
	if f.s.Backups == nil {
		return nil
	}
	return f.s.Backups.Save(p)
}

// auditTool names a confirmed change to a Sensitive System File in the Audit
// Log apart from ordinary ones.
func auditTool(sensitive bool, tool string) string {
	if sensitive {
		return "write_sensitive"
	}
	return tool
}

func (f fileService) CheckSensitive(ctx context.Context, req *connect.Request[aosv1.CheckSensitiveRequest]) (*connect.Response[aosv1.CheckSensitiveResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	if _, ok := f.sensitive(p, false); !ok {
		return connect.NewResponse(&aosv1.CheckSensitiveResponse{}), nil
	}
	c, err := f.s.Checker.Check(ctx, p, req.Msg.Content)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&aosv1.CheckSensitiveResponse{Checker: c.Checker, Passed: c.Passed, Output: c.Output, Unavailable: c.Unavailable}), nil
}

func (f fileService) ListSensitiveBackups(_ context.Context, req *connect.Request[aosv1.ListSensitiveBackupsRequest]) (*connect.Response[aosv1.ListSensitiveBackupsResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	resp := &aosv1.ListSensitiveBackupsResponse{}
	if f.s.Backups == nil || !f.s.rootMode() {
		return connect.NewResponse(resp), nil
	}
	list, err := f.s.Backups.List(p)
	if err != nil {
		return nil, connectError(err)
	}
	for _, b := range list {
		resp.Backups = append(resp.Backups, &aosv1.SensitiveBackup{Id: b.ID, SavedAt: timestamppb.New(b.SavedAt), Size: b.Size})
	}
	return connect.NewResponse(resp), nil
}

func (f fileService) RestoreSensitiveBackup(ctx context.Context, req *connect.Request[aosv1.RestoreSensitiveBackupRequest]) (*connect.Response[aosv1.RestoreSensitiveBackupResponse], error) {
	p, err := f.abs(req.Msg.Path)
	if err != nil {
		return nil, err
	}
	if f.s.Backups == nil || !f.s.rootMode() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("kept versions of Sensitive System Files are restored in Root Mode"))
	}
	data, err := f.s.Backups.Read(p, req.Msg.Id)
	if errors.Is(err, code.ErrNoBackup) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connectError(err)
	}
	if err := f.keep(p); err != nil {
		return nil, connectError(err)
	}
	err = f.s.UserFiles.Run(ctx, files.OpWrite, files.WriteArgs{Path: p, Content: string(data), Overwrite: true}, nil, nil)
	f.audit(ctx, "write_sensitive", p+" (restored the version kept "+req.Msg.Id+")", err)
	return connect.NewResponse(&aosv1.RestoreSensitiveBackupResponse{}), connectError(err)
}
