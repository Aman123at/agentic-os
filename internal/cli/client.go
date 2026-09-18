package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"

	"github.com/Aman123at/agentic-os/gen/go/aos/v1/aosv1connect"
)

// socketPath is aosd's Unix socket inside the Machine.
var socketPath = envOr("AOS_SOCKET", "/run/aos/aosd.sock")

// client talks to aosd over its Unix socket.
type client struct {
	http       *http.Client
	auth       aosv1connect.AuthServiceClient
	tasks      aosv1connect.TaskServiceClient
	approvals  aosv1connect.ApprovalServiceClient
	events     aosv1connect.EventServiceClient
	files      aosv1connect.FileServiceClient
	trash      aosv1connect.TrashServiceClient
	sessions   aosv1connect.SessionServiceClient
	system     aosv1connect.SystemServiceClient
	software   aosv1connect.SoftwareServiceClient
	supervisor aosv1connect.SupervisorServiceClient
	settings   aosv1connect.SettingsServiceClient
}

const baseURL = "http://aosd"

func newClient() *client {
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
	return &client{
		http:       hc,
		auth:       aosv1connect.NewAuthServiceClient(hc, baseURL),
		tasks:      aosv1connect.NewTaskServiceClient(hc, baseURL),
		approvals:  aosv1connect.NewApprovalServiceClient(hc, baseURL),
		events:     aosv1connect.NewEventServiceClient(hc, baseURL),
		files:      aosv1connect.NewFileServiceClient(hc, baseURL),
		trash:      aosv1connect.NewTrashServiceClient(hc, baseURL),
		sessions:   aosv1connect.NewSessionServiceClient(hc, baseURL),
		system:     aosv1connect.NewSystemServiceClient(hc, baseURL),
		software:   aosv1connect.NewSoftwareServiceClient(hc, baseURL),
		supervisor: aosv1connect.NewSupervisorServiceClient(hc, baseURL),
		settings:   aosv1connect.NewSettingsServiceClient(hc, baseURL),
	}
}

// explain turns connection and API errors into messages for people.
func explain(err error) error {
	if err == nil {
		return nil
	}
	var ne *net.OpError
	if errors.As(err, &ne) {
		if _, statErr := os.Stat(socketPath); statErr != nil {
			return fmt.Errorf("aosd is not running (no %s). Start the Machine: docker compose up -d", socketPath)
		}
		return fmt.Errorf("cannot reach aosd at %s: %v", socketPath, ne.Err)
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		msg := ce.Message()
		if ce.Code() == connect.CodeUnknown && strings.Contains(msg, "403") {
			return errors.New("aosd refuses requests from Agents: this command can't be run from an Agent's Session")
		}
		return errors.New(msg)
	}
	return err
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
