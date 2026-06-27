package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"watcloud-cli/internal/config"
	"watcloud-cli/internal/subscription"
)

// Sentinel assigned to --discord when it's passed with no value, meaning
// "use the webhook saved in config". A real webhook URL can never equal this.
const discordFromConfig = "__use_saved_config__"

var discordWebhook string

var subscriptionCmd = &cobra.Command{
	Use:   "subscription [job_id] [email]",
	Short: "Get notified when a SLURM job finishes",
	Long: "Subscribe to a specific SLURM job by its ID. When the job completes you will be " +
		"notified by email, or on Discord with --discord. Save a webhook once via " +
		"'watcloud config set discord-webhook <url>', or pass --discord <webhook_url> directly.",

	Args: cobra.RangeArgs(1, 2),

	Run: func(cmd *cobra.Command, args []string) {
		jobID := args[0]

		var channel, target string
		switch {
		case cmd.Flags().Changed("discord"):
			channel = "discord"
			if discordWebhook == discordFromConfig {
				// Bare --discord: fall back to the saved webhook.
				cfg, err := config.Load()
				if err != nil {
					fmt.Printf("Failed to read config: %v\n", err)
					return
				}
				if cfg.DiscordWebhook == "" {
					fmt.Println("No Discord webhook configured. Either pass it directly:")
					fmt.Println("  watcloud subscription", jobID, "--discord <webhook_url>")
					fmt.Println("or save it once:")
					fmt.Println("  watcloud config set discord-webhook <webhook_url>")
					return
				}
				target = cfg.DiscordWebhook
			} else {
				target = discordWebhook
			}
		case len(args) == 2:
			channel = "email"
			target = args[1]
		default:
			fmt.Println("Error: provide an email address, or use --discord (with a saved or explicit webhook)")
			return
		}

		fmt.Printf("Attempting to subscribe job %s (%s)...\n", jobID, channel)
		err := subscription.SubscribeToJobAPI(jobID, target, channel)

		if err != nil {
			fmt.Printf("Failed to subscribe to job: %v\n", err)
		} else {
			fmt.Printf("Success! You will be notified via %s when job %s completes.\n", channel, jobID)
		}
	},
}

func init() {
	subscriptionCmd.Flags().StringVar(&discordWebhook, "discord", "",
		"Notify via Discord: pass a webhook URL, or omit the value to use the one saved with 'watcloud config set discord-webhook'")
	// Allow `--discord` with no value (uses the saved webhook).
	subscriptionCmd.Flags().Lookup("discord").NoOptDefVal = discordFromConfig
	rootCmd.AddCommand(subscriptionCmd)
}
