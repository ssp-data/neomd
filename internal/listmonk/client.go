// Package listmonk provides an API client for creating and scheduling
// campaigns on a Listmonk newsletter server.
package listmonk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Config holds Listmonk connection settings.
type Config struct {
	URL          string
	APIUser      string
	APIToken     string
	DelayMinutes int // default 30
}

// Client wraps Listmonk API calls.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient creates a Listmonk API client.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// campaignRequest is the JSON payload for POST /api/campaigns.
type campaignRequest struct {
	Name        string `json:"name"`
	Subject     string `json:"subject"`
	Lists       []int  `json:"lists"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
	Type        string `json:"type"`
	SendAt      string `json:"send_at,omitempty"`
}

// statusRequest is the JSON payload for PUT /api/campaigns/{id}/status.
type statusRequest struct {
	Status string `json:"status"`
}

// campaignResponse wraps the Listmonk API response for campaign creation.
type campaignResponse struct {
	Data struct {
		ID int `json:"id"`
	} `json:"data"`
}

// CreateAndSchedule creates a campaign in DRAFT status, then schedules it.
// Returns the campaign ID.
func (c *Client) CreateAndSchedule(subject, markdownBody string, listIDs []int, delay time.Duration) (int, error) {
	if delay == 0 {
		delay = 30 * time.Minute
	}
	sendAt := time.Now().UTC().Add(delay)

	id, err := c.createCampaign(subject, markdownBody, listIDs, sendAt)
	if err != nil {
		return 0, err
	}
	if err := c.setStatus(id, "scheduled"); err != nil {
		return id, fmt.Errorf("created campaign #%d but failed to schedule: %w", id, err)
	}
	return id, nil
}

func (c *Client) createCampaign(subject, body string, listIDs []int, sendAt time.Time) (int, error) {
	name := fmt.Sprintf("%s - %s", subject, sendAt.Format("2006-01-02 15:04"))
	payload := campaignRequest{
		Name:        name,
		Subject:     subject,
		Lists:       listIDs,
		Body:        body,
		ContentType: "markdown",
		Type:        "regular",
		SendAt:      sendAt.Format(time.RFC3339),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal campaign: %w", err)
	}

	req, err := http.NewRequest("POST", c.cfg.URL+"/api/campaigns", bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.cfg.APIUser, c.cfg.APIToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("create campaign: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return 0, fmt.Errorf("create campaign: HTTP %d: %s", resp.StatusCode, buf.String())
	}

	var result campaignResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("parse campaign response: %w", err)
	}
	return result.Data.ID, nil
}

func (c *Client) setStatus(campaignID int, status string) error {
	data, _ := json.Marshal(statusRequest{Status: status})

	url := fmt.Sprintf("%s/api/campaigns/%d/status", c.cfg.URL, campaignID)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.cfg.APIUser, c.cfg.APIToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("set campaign status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return fmt.Errorf("set campaign status: HTTP %d: %s", resp.StatusCode, buf.String())
	}
	return nil
}
