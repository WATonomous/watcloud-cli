package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type JobSubscriptionData struct {
	JobID string `json:"job_id"`
	Email string `json:"email"`
}

func SubscribeToJobAPI(jobID string, email string) error {
	
	// Create data package
	payload := JobSubscriptionData{
		JobID: jobID,
		Email: email, 
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to format data: %v", err)
	}

	apiURL := "http://localhost:8080/subscribe" 

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}
	
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