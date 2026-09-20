package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// detachKey is Ctrl-].
const detachKey = 0x1d

func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <task-id | session-id>",
		Short: "Watch or type into a Task's Session (Ctrl-] detaches; the Agent is told what you typed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTTY(os.Stdin) {
				return fmt.Errorf("aos attach needs a terminal")
			}
			c := newClient()
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			printRealmBanner(ctx, c, os.Stderr)
			conn, _, err := websocket.Dial(ctx, "ws://aosd/ws/session/"+args[0], &websocket.DialOptions{HTTPClient: c.http})
			if err != nil {
				return fmt.Errorf("attach %s: %w (is the Task running? aos tasks)", args[0], explain(err))
			}
			defer func() { _ = conn.CloseNow() }()
			conn.SetReadLimit(-1)

			fd := int(os.Stdin.Fd())
			old, err := term.MakeRaw(fd)
			if err != nil {
				return err
			}
			defer func() { _ = term.Restore(fd, old) }()
			fmt.Fprint(os.Stderr, "[attached; Ctrl-] detaches]\r\n")

			resize := func() {
				if w, h, err := term.GetSize(fd); err == nil {
					msg, _ := json.Marshal(map[string]any{"type": "resize", "cols": w, "rows": h})
					_ = conn.Write(ctx, websocket.MessageText, msg)
				}
			}
			resize()
			winch := make(chan os.Signal, 1)
			signal.Notify(winch, syscall.SIGWINCH)
			defer signal.Stop(winch)
			go func() {
				for range winch {
					resize()
				}
			}()

			go func() {
				defer cancel()
				buf := make([]byte, 4096)
				for {
					n, err := os.Stdin.Read(buf)
					if err != nil {
						return
					}
					for i := 0; i < n; i++ {
						if buf[i] == detachKey {
							if i > 0 {
								_ = conn.Write(ctx, websocket.MessageBinary, buf[:i])
							}
							return
						}
					}
					if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
						return
					}
				}
			}()
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					fmt.Fprint(os.Stderr, "\r\n[detached]\r\n")
					return nil
				}
				if _, err := os.Stdout.Write(data); err != nil {
					return nil
				}
			}
		},
	}
}
