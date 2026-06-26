package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"watcloud-cli/internal/subscription"
)

var discordWebhook string

var subscriptionCmd = &cobra.Command{
	Use:   "subscription [job_id] [email]",
	Short: "Get notified when a SLURM job finishes",
	Long: "Subscribe to a specific SLURM job by its ID. When the job completes you will be " +
		"notified by email, or on Discord with --discord <webhook_url>.",

	Args: cobra.RangeArgs(1, 2),

	Run: func(cmd *cobra.Command, args []string) {
		jobID := args[0]

		var channel, target string
		switch {
		case discordWebhook != "":
			channel = "discord"
			target = discordWebhook
		case len(args) == 2:
			channel = "email"
			target = args[1]
		default:
			fmt.Println("Error: provide an email address or use --discord <webhook_url>")
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
	subscriptionCmd.Flags().StringVar(&discordWebhook, "discord", "", "Discord webhook URL to notify instead of email")
	rootCmd.AddCommand(subscriptionCmd)
}
