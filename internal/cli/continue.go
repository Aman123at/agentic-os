package cli

import (
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
)

// ---------------------------------------------------------------- follow-up, resume, reply

func followUpCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: `follow-up <id> "<text>"`, Short: "Continue a finished Task with a further instruction", Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			interactive := isTTY(os.Stdin) && isTTY(os.Stdout) && !asJSON
			c := newClient()
			resp, err := c.tasks.SendFollowUp(cmd.Context(), connect.NewRequest(&aosv1.SendFollowUpRequest{Id: args[0], Text: strings.Join(args[1:], " "), Interactive: interactive}))
			if err != nil {
				return explain(err)
			}
			return followTask(cmd.Context(), c, resp.Msg.Task.Id, interactive, asJSON)
		}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print events and the final Task as JSON lines")
	return cmd
}

func resumeCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "resume <id>", Short: "Continue a Task that AOS stopping interrupted", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			interactive := isTTY(os.Stdin) && isTTY(os.Stdout) && !asJSON
			c := newClient()
			resp, err := c.tasks.ResumeTask(cmd.Context(), connect.NewRequest(&aosv1.ResumeTaskRequest{Id: args[0], Interactive: interactive}))
			if err != nil {
				return explain(err)
			}
			return followTask(cmd.Context(), c, resp.Msg.Task.Id, interactive, asJSON)
		}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print events and the final Task as JSON lines")
	return cmd
}

func replyCmd() *cobra.Command {
	return &cobra.Command{Use: `reply <id> "<text>"`, Short: "Answer a Task that is waiting for you: its question, or a hint after its Retries ran out", Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := newClient().tasks.AnswerQuestion(cmd.Context(), connect.NewRequest(&aosv1.AnswerQuestionRequest{Id: args[0], Text: strings.Join(args[1:], " ")})); err != nil {
				return explain(err)
			}
			fmt.Println("Sent to", args[0])
			return nil
		}}
}
