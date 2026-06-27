package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"watcloud-cli/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage watcloud CLI configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a config value (supported keys: discord-webhook)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		key, value := args[0], args[1]

		cfg, err := config.Load()
		if err != nil {
			fmt.Printf("Failed to read config: %v\n", err)
			os.Exit(1)
		}

		switch key {
		case "discord-webhook":
			cfg.DiscordWebhook = value
		default:
			fmt.Printf("Unknown config key %q (supported: discord-webhook)\n", key)
			os.Exit(1)
		}

		if err := config.Save(cfg); err != nil {
			fmt.Printf("Failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Saved %s.\n", key)
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Print a config value (supported keys: discord-webhook)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load()
		if err != nil {
			fmt.Printf("Failed to read config: %v\n", err)
			os.Exit(1)
		}

		switch args[0] {
		case "discord-webhook":
			fmt.Println(cfg.DiscordWebhook)
		default:
			fmt.Printf("Unknown config key %q (supported: discord-webhook)\n", args[0])
			os.Exit(1)
		}
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	rootCmd.AddCommand(configCmd)
}
