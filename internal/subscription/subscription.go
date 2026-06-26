package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type JobSubscriptionData struct {
	JobID   string `json:"job_id"`
	Channel string `json:"channel"`
	Target  string `json:"target"`
}

// SubscribeToJobAPI registers a subscription so the user is notified when the
// SLURM job finishes. channel is "email" or "discord"; target is the email
// address or the Discord webhook URL respectively.
func SubscribeToJobAPI(jobID string, target string, channel string) error {

	// Create data package
	payload := JobSubscriptionData{
		JobID:   jobID,
		Channel: channel,
		Target:  target,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to format data: %v", err)
	}

	apiURL := "http://slurm-email-monitor.cluster.watonomous.ca/subscribe"

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Host = "slurm-email-monitor.cluster.watonomous.ca"
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server rejected the request with status: %d", resp.StatusCode)
	}

	return nil
}
