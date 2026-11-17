// Command smoke runs a short end-to-end check against a running stack.
//
// It creates one notification and a small batch, waits for acceptance, and
// reads /api/v1/stats. It expects a working provider endpoint (webhook.site or
// a local mock) and a ready gateway.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type notification struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	AttemptCount int     `json:"attempt_count"`
	LastError    *string `json:"last_error"`
}

type batch struct {
	ID     string         `json:"batch_id"`
	Total  int            `json:"total"`
	Status string         `json:"status"`
	Counts map[string]int `json:"counts"`
}

func main() {
	baseURL := strings.TrimRight(os.Getenv("BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	client := &http.Client{Timeout: 10 * time.Second}

	if err := waitReady(client, baseURL); err != nil {
		fail("readiness: %v", err)
	}

	id, err := createNotification(client, baseURL)
	if err != nil {
		fail("create notification: %v", err)
	}
	if err := waitNotification(client, baseURL, id); err != nil {
		fail("notification %s: %v", id, err)
	}
	fmt.Printf("single notification accepted: %s\n", id)

	batchID, total, err := createBatch(client, baseURL)
	if err != nil {
		fail("create batch: %v", err)
	}
	if err := waitBatch(client, baseURL, batchID, total); err != nil {
		fail("batch %s: %v", batchID, err)
	}
	fmt.Printf("batch accepted: %s (%d notifications)\n", batchID, total)

	if err := checkStats(client, baseURL); err != nil {
		fail("stats: %v", err)
	}

	fmt.Println("smoke passed")
}

func waitReady(client *http.Client, baseURL string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("readyz did not return 200 within 30s")
}

func createNotification(client *http.Client, baseURL string) (string, error) {
	body := map[string]any{
		"recipient": "+15555550100",
		"channel":   "sms",
		"content":   "Smoke test single",
		"priority":  "high",
	}

	var created notification
	if err := post(client, baseURL+"/api/v1/notifications", body, uniqueKey("smoke-single"), &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("response has no id")
	}
	return created.ID, nil
}

func createBatch(client *http.Client, baseURL string) (string, int, error) {
	items := make([]map[string]any, 0, 3)
	for i, recipient := range []string{"+15555550101", "+15555550102", "+15555550103"} {
		items = append(items, map[string]any{
			"recipient": recipient,
			"channel":   "sms",
			"content":   fmt.Sprintf("Smoke test batch %d", i+1),
		})
	}

	var created batch
	if err := post(client, baseURL+"/api/v1/notifications/batch",
		map[string]any{"notifications": items}, uniqueKey("smoke-batch"), &created); err != nil {
		return "", 0, err
	}
	if created.ID == "" {
		return "", 0, fmt.Errorf("response has no batch_id")
	}
	return created.ID, created.Total, nil
}

func waitNotification(client *http.Client, baseURL, id string) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var current notification
		if err := get(client, baseURL+"/api/v1/notifications/"+id, &current); err != nil {
			return err
		}

		switch current.Status {
		case "accepted", "delivered":
			return nil
		case "failed", "dead_letter", "cancelled":
			message := current.Status
			if current.LastError != nil {
				message += ": " + *current.LastError
			}
			return fmt.Errorf("terminal status %s", message)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("did not reach accepted within 60s")
}

func waitBatch(client *http.Client, baseURL, id string, total int) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var current batch
		if err := get(client, baseURL+"/api/v1/batches/"+id, &current); err != nil {
			return err
		}

		switch current.Status {
		case "completed":
			if current.Counts["accepted"]+current.Counts["delivered"] != total {
				return fmt.Errorf("accepted %d of %d", current.Counts["accepted"], total)
			}
			return nil
		case "failed", "partial_failure", "mixed", "cancelled":
			return fmt.Errorf("batch reached %s: %+v", current.Status, current.Counts)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("batch did not complete within 60s")
}

func checkStats(client *http.Client, baseURL string) error {
	response, err := client.Get(baseURL + "/api/v1/stats")
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}

	var stats map[string]any
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		return fmt.Errorf("decode stats: %w", err)
	}
	if _, ok := stats["counts"]; !ok {
		return fmt.Errorf("stats response has no counts")
	}
	return nil
}

func post(client *http.Client, url string, body any, idempotencyKey string, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return json.Unmarshal(payload, target)
}

func get(client *http.Client, url string, target any) error {
	response, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return json.Unmarshal(payload, target)
}

func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "smoke failed: "+format+"\n", args...)
	os.Exit(1)
}
