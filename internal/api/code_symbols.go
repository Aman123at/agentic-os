package api

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"

	"connectrpc.com/connect"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/code"
	"github.com/Aman123at/agentic-os/internal/files"
)

// ---------------------------------------------------------------- navigation (M8.8)

// maxReferences bounds ⇧F12's lines.
const maxReferences = 1000

// errInstallCtags is what navigation says on a Machine without universal-ctags.
var errInstallCtags = errors.New("universal-ctags is not installed on this Machine; Go to Definition and symbols need it: sudo apt install universal-ctags")

// symbols brings a Workspace's index up to date for a question asked from
// path (absolute, or ""), and returns it with the Workspace.
func (c codeService) symbols(ctx context.Context, id, from string) (code.Workspace, *code.Index, string, *aosv1.IndexState, error) {
	ws, tools, err := c.workspace(id)
	if err != nil {
		return ws, nil, "", nil, err
	}
	near := ""
	if from != "" {
		if r, err := filepath.Rel(ws.Root, filepath.Clean(from)); err == nil && filepath.IsLocal(r) {
			near = filepath.ToSlash(r)
		}
	}
	list := func(ctx context.Context) ([]string, error) {
		paths, _, err := c.paths(ctx, ws, tools.Files)
		return paths, err
	}
	tag := func(ctx context.Context, rel []string) ([]files.Tag, error) {
		var tags []files.Tag
		err := c.s.UserFiles.Run(ctx, files.OpTags, files.TagsArgs{Root: ws.Root, Files: rel}, &tags, nil)
		return tags, err
	}
	st, err := tools.Symbols.Ensure(ctx, list, tag, near)
	if errors.Is(err, files.ErrNoCtags) {
		return ws, nil, "", nil, connect.NewError(connect.CodeFailedPrecondition, errInstallCtags)
	}
	if err != nil {
		return ws, nil, "", nil, connectError(err)
	}
	return ws, tools.Symbols, near, &aosv1.IndexState{Files: uint32(st.Files), Total: uint32(st.Total), Partial: st.Partial}, nil
}

func symbolsOut(root string, tags []files.Tag) []*aosv1.Symbol {
	out := make([]*aosv1.Symbol, 0, len(tags))
	for _, t := range tags {
		out = append(out, &aosv1.Symbol{
			Name: t.Name, Kind: t.Kind, Path: filepath.Join(root, filepath.FromSlash(t.Path)), Line: uint32(t.Line), EndLine: uint32(t.End),
			Scope: t.Scope, Signature: t.Signature, Language: t.Language,
		})
	}
	return out
}

func (c codeService) Definition(ctx context.Context, req *connect.Request[aosv1.DefinitionRequest]) (*connect.Response[aosv1.DefinitionResponse], error) {
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no name to find"))
	}
	ws, idx, near, st, err := c.symbols(ctx, req.Msg.WorkspaceId, req.Msg.Path)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&aosv1.DefinitionResponse{Symbols: symbolsOut(ws.Root, idx.Definitions(req.Msg.Name, near)), Index: st}), nil
}

func (c codeService) DocumentSymbols(ctx context.Context, req *connect.Request[aosv1.DocumentSymbolsRequest]) (*connect.Response[aosv1.DocumentSymbolsResponse], error) {
	ws, idx, near, st, err := c.symbols(ctx, req.Msg.WorkspaceId, req.Msg.Path)
	if err != nil {
		return nil, err
	}
	if near == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(req.Msg.Path+" is not in the Workspace"))
	}
	return connect.NewResponse(&aosv1.DocumentSymbolsResponse{Symbols: symbolsOut(ws.Root, idx.FileSymbols(near)), Index: st}), nil
}

func (c codeService) WorkspaceSymbols(ctx context.Context, req *connect.Request[aosv1.WorkspaceSymbolsRequest]) (*connect.Response[aosv1.WorkspaceSymbolsResponse], error) {
	ws, idx, _, st, err := c.symbols(ctx, req.Msg.WorkspaceId, req.Msg.Path)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.Limit)
	switch {
	case limit <= 0:
		limit = 100
	case limit > 500:
		limit = 500
	}
	return connect.NewResponse(&aosv1.WorkspaceSymbolsResponse{Symbols: symbolsOut(ws.Root, idx.Search(req.Msg.Query, limit)), Index: st}), nil
}

// References is a whole-word search for the name in the files of the asking
// file's language; it needs no index.
func (c codeService) References(ctx context.Context, req *connect.Request[aosv1.ReferencesRequest]) (*connect.Response[aosv1.ReferencesResponse], error) {
	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no name to find"))
	}
	ws, _, err := c.workspace(req.Msg.WorkspaceId)
	if err != nil {
		return nil, err
	}
	searcher, ok := c.s.UserFiles.(files.TextSearcher)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this aosd cannot search text"))
	}
	resp := &aosv1.ReferencesResponse{}
	res, err := searcher.SearchText(ctx, files.TextSearchArgs{
		Root: ws.Root, Query: `\b` + regexp.QuoteMeta(req.Msg.Name) + `\b`, Regex: true, Case: true,
		Include: code.LanguageGlobs(req.Msg.Path), Max: maxReferences,
	}, func(f files.TextFile) error {
		resp.Files = append(resp.Files, textFileOut(f))
		return nil
	})
	if errors.Is(err, files.ErrBadQuery) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return nil, connectError(err)
	}
	resp.Truncated = res.Truncated
	return connect.NewResponse(resp), nil
}
