package cli

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

// modeCmd groups the Machine's Mode. In M6.5 it carries the `ui` setup — the
// account-creation moment (PLAN.md §18 M6.5). Switching Mode at runtime (writing
// mode: ui to config.yml and restarting) lands with the runtime Mode key in
// M6.10; until then Mode is fixed by the install's environment.
func modeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mode", Short: "The Machine's Mode (cli or ui)"}
	cmd.AddCommand(modeUICmd())
	return cmd
}

// modeUICmd creates the one Desktop account. The password it generates is shown
// once and must be replaced on first sign-in, so the account is never left on a
// secret that lingered in a terminal (M6.5). Creating a second account is
// refused, so this is genuinely a one-time setup.
func modeUICmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Create the Desktop account and print a first password to sign in with",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if user == "" {
				return fmt.Errorf("--user cannot be empty")
			}
			password, err := generatePassword()
			if err != nil {
				return err
			}
			if _, err := newClient().auth.CreateInitialUser(cmd.Context(),
				connect.NewRequest(&aosv1.CreateInitialUserRequest{Username: user, Password: password})); err != nil {
				return explain(err)
			}
			fmt.Print("The Desktop account is ready. Sign in with this password once; the Desktop\n" +
				"then asks you to set your own.\n\n")
			fmt.Printf("  Username: %s\n", user)
			fmt.Printf("  Password: %s\n\n", password)
			fmt.Print("It is shown only now. Open the Desktop at http://<this-server>:7700\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&user, "user", "admin", "the username to sign in with")
	return cmd
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
