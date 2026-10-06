package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"watcloud-cli/internal/subscription"
)

// Value of a bare --discord: read the webhook from the environment.
const discordFromEnv = "__use_env__"

const discordWebhookEnv = "WATCLOUD_DISCORD_WEBHOOK"

var discordWebhook string

var subscriptionCmd = &cobra.Command{
	Use:   "subscription [job_id] [email]",
	Short: "Get notified when a SLURM job finishes",
	Long: "Subscribe to a specific SLURM job by its ID. When the job completes you will be " +
		"notified by email, or on Discord with --discord. Pass --discord <webhook_url> directly, " +
		"or set " + discordWebhookEnv + " (e.g. in ~/.bashrc) and pass a bare --discord.",

	Args: cobra.RangeArgs(1, 2),

	Run: func(cmd *cobra.Command, args []string) {
		jobID := args[0]

		var channel, target string
		switch {
		case cmd.Flags().Changed("discord"):
			channel = "discord"
			if discordWebhook == discordFromEnv {
				target = os.Getenv(discordWebhookEnv)
				if target == "" {
					fmt.Println("No Discord webhook set. Either pass it directly:")
					fmt.Println("  watcloud subscription", jobID, "--discord <webhook_url>")
					fmt.Println("or set it once in your shell profile (e.g. ~/.bashrc):")
					fmt.Println("  export " + discordWebhookEnv + "=<webhook_url>")
					return
				}
			} else {
				target = discordWebhook
			}
		case len(args) == 2:
			channel = "email"
			target = args[1]
		default:
			fmt.Println("Error: provide an email address, or use --discord (with a webhook URL or " + discordWebhookEnv + " set)")
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
		"Notify via Discord: pass a webhook URL, or omit the value to use $"+discordWebhookEnv)
	// Allow a bare --discord.
	subscriptionCmd.Flags().Lookup("discord").NoOptDefVal = discordFromEnv
	rootCmd.AddCommand(subscriptionCmd)
}
