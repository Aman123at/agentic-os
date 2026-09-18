package cli

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// modeCmd shows or switches the Machine's Mode. One image carries both Modes and
// mode: lives in config.yml (M6.10), so switching is a runtime key change plus a
// restart, not a rebuild: `aos mode ui` / `aos mode cli` write the key and
// restart the Daemon, and bare `aos mode` prints the Mode in force — one word,
// matching `aos status`.
func modeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mode [ui|cli]",
		Short: "Show or switch the Machine's Mode (ui or cli)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := newClient().system.Info(cmd.Context(), connect.NewRequest(&aosv1.InfoRequest{}))
			if err != nil {
				return explain(err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), resp.Msg.Mode)
			return nil
		},
	}
	cmd.AddCommand(modeUICmd(), modeCLICmd())
	return cmd
}

// modeUICmd switches to the Desktop. The first time it also creates the one
// account, generating a single-use password shown once and replaced on first
// sign-in, so the account is never left on a secret that lingered in a terminal
// (M6.5). Creating a second account is refused; a repeat switch keeps the
// existing account and only restarts into ui Mode.
func modeUICmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Switch on the Desktop: create the account if needed, write mode: ui and restart",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if user == "" {
				return fmt.Errorf("--user cannot be empty")
			}
			c := newClient()
			password, err := generatePassword()
			if err != nil {
				return err
			}
			switch _, err := c.auth.CreateInitialUser(cmd.Context(),
				connect.NewRequest(&aosv1.CreateInitialUserRequest{Username: user, Password: password})); {
			case err == nil:
				fmt.Print("The Desktop account is ready. Sign in with this password once; the Desktop\n" +
					"then asks you to set your own.\n\n")
				fmt.Printf("  Username: %s\n", user)
				fmt.Printf("  Password: %s\n\n", password)
				fmt.Print("It is shown only now.\n\n")
			case connect.CodeOf(err) == connect.CodeAlreadyExists:
				fmt.Print("The Desktop account already exists; keeping it. Switching to ui Mode.\n\n")
			default:
				return explain(err)
			}
			return applyMode(cmd, c, "ui", "Open the Desktop at http://<this-server>:7700 and sign in.")
		},
	}
	cmd.Flags().StringVar(&user, "user", "admin", "the username to sign in with")
	return cmd
}

// modeCLICmd switches off the Desktop: the Daemon restarts with the control
// socket only and no TCP listener, so `bind` and `port` go inert (M6.4/M6.10).
// The account is left in place for a later switch back.
func modeCLICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cli",
		Short: "Switch off the Desktop: write mode: cli and restart (control socket only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return applyMode(cmd, newClient(), "cli", "The Desktop is off; aos talks over the control socket.")
		},
	}
}

// applyMode writes mode: to config.yml through aosd, then restarts the unit so
// the new Mode takes effect (a startup-only key needs a restart, ADR-0010). The
// restart uses systemctl, like `aos daemon restart`, so it needs root.
func applyMode(cmd *cobra.Command, c *client, mode, after string) error {
	if _, err := c.settings.Update(cmd.Context(),
		connect.NewRequest(&aosv1.UpdateSettingRequest{Key: "mode", Value: mode})); err != nil {
		return explain(err)
	}
	fmt.Printf("Restarting the Daemon to apply %s Mode ...\n", mode)
	if err := systemctl("restart", "aos"); err != nil {
		return err
	}
	fmt.Println(after)
	return nil
}

// passwordAlphabet omits look-alike characters (0/O, 1/l/I) so a generated
// password survives being read off a terminal once and typed into the login
// screen.
const passwordAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// generatePassword returns a strong, single-use password for a new account,
// well over the model's minimum length and drawn uniformly from the alphabet.
func generatePassword() (string, error) {
	const n = 24
	b := make([]byte, n)
	size := big.NewInt(int64(len(passwordAlphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, size)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[k.Int64()]
	}
	return string(b), nil
}
