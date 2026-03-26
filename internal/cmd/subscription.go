package cmd

import ( 
	"fmt"
	"github.com/spf13/cobra"
	"watcloud-cli/internal/subscription"
)

var subscriptionCmd = &cobra.Command{
	Use:   "subscription [job_id] [email]",
	Short: "Get an email notification when a SLURM job finishes", 
	Long: "Subscribe to a specific SLURM job by its ID. When the job completes, you will receive an email notification.", 
 
	Args: cobra.ExactArgs(2), 

	Run: func(cmd *cobra.Command, args []string) {
		jobID := args[0]
		email := args[1]

		fmt.Printf("Attempting to subscribe %s to job %s...\n", email, jobID) 
		err := subscription.SubscribeToJobAPI(jobID, email)

		if err != nil {
			fmt.Printf("Failed to subscribe to job: %v\n", err)
		} else { 
			fmt.Printf("Success! %s You will be emailed when job %s completes \n", email, jobID)
		}
	},
}

func init() {
	rootCmd.AddCommand(subscriptionCmd)
}
