package cmd

import (
	"watcloud-cli/internal/du"

	"github.com/spf13/cobra"
)

var duCmd = &cobra.Command{
	Use:   "du",
	Short: "Find what's taking up space in your home folder",
	Long: "Browse your home folder, largest items first.\n" +
		"See each item's size and share of your home storage limit.\n\n" +
		"Use the arrow keys to move, Enter to open a folder, and Left to go back.\n" +
		"Press r to refresh or q to quit.",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return du.Run(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(duCmd)
}
