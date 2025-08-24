package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	copilotDeviceCodeURL = "https://github.com/login/device/code"
	copilotTokenURL      = "https://github.com/login/oauth/access_token"
	copilotAPIKeyURL     = "https://api.github.com/copilot_internal/v2/token"
	copilotClientID      = "Iv1.b507a08c87ecfe98"
	copilotScope         = "read:user"
	userAgent            = "GitHubCopilotChat/0.26.7"

	// Retry configuration
	maxRefreshRetries = 3
	baseRetryDelay    = 2 // seconds
)

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int64  `json:"refresh_in"`
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
}

func authenticate(cfg *Config) error {
	now := time.Now().Unix()
	if cfg.CopilotToken != "" && cfg.ExpiresAt > now+60 {
		log.Printf("Token still valid: expires in %d seconds", cfg.ExpiresAt-now)
		return nil // Already authenticated
	}

	if cfg.CopilotToken != "" {
		log.Printf("Token expired or expiring soon: expires in %d seconds, triggering re-auth", cfg.ExpiresAt-now)
	} else {
		log.Printf("No token found, starting authentication flow")
	}

	// Step 1: Get device code
	req, err := http.NewRequest("POST", copilotDeviceCodeURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	body := fmt.Sprintf(`{"client_id":"%s","scope":"%s"}`, copilotClientID, copilotScope)
	req.Body = io.NopCloser(strings.NewReader(body))

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var dc deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return err
	}

	fmt.Printf("\nTo authenticate, visit: %s\nEnter code: %s\n", dc.VerificationURI, dc.UserCode)

	// Step 2: Poll for GitHub token
	githubToken, err := pollForGitHubToken(dc.DeviceCode, dc.Interval)
	if err != nil {
		return err
	}
	cfg.GitHubToken = githubToken

	// Step 3: Exchange GitHub token for Copilot token
	copilotToken, expiresAt, refreshIn, err := getCopilotToken(githubToken)
	if err != nil {
		return err
	}

	cfg.CopilotToken = copilotToken
	cfg.ExpiresAt = expiresAt
	cfg.RefreshIn = refreshIn

	if err := saveConfig(cfg); err != nil {
		return err
	}

	fmt.Println("Authentication successful!")
	return nil
}

func pollForGitHubToken(deviceCode string, interval int) (string, error) {
	for i := 0; i < 120; i++ { // Poll for 2 minutes max
		time.Sleep(time.Duration(interval) * time.Second)

		req, err := http.NewRequest("POST", copilotTokenURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)

		body := fmt.Sprintf(`{"client_id":"%s","device_code":"%s","grant_type":"urn:ietf:params:oauth:grant-type:device_code"}`,
			copilotClientID, deviceCode)
		req.Body = io.NopCloser(strings.NewReader(body))

		resp, err := sharedHTTPClient.Do(req)
		if err != nil {
			continue
		}

		var tr tokenResponse
		json.NewDecoder(resp.Body).Decode(&tr)
		resp.Body.Close()

		if tr.Error != "" {
			if tr.Error == "authorization_pending" {
				continue
			}
			return "", fmt.Errorf("authorization error: %s - %s", tr.Error, tr.ErrorDesc)
		}

		if tr.AccessToken != "" {
			return tr.AccessToken, nil
		}
	}

	return "", fmt.Errorf("authentication timed out")
}

func getCopilotToken(githubToken string) (string, int64, int64, error) {
	req, err := http.NewRequest("GET", copilotAPIKeyURL, nil)
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("User-Agent", userAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return "", 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", 0, 0, fmt.Errorf("failed to get Copilot token: %d", resp.StatusCode)
	}

	var ctr copilotTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&ctr); err != nil {
		return "", 0, 0, err
	}

	return ctr.Token, ctr.ExpiresAt, ctr.RefreshIn, nil
}

func refreshToken(cfg *Config) error {
	if cfg.GitHubToken == "" {
		log.Printf("Cannot refresh token: no GitHub token available")
		return errors.New("no GitHub token available for refresh")
	}

	// Retry with exponential backoff
	for attempt := 1; attempt <= maxRefreshRetries; attempt++ {
		log.Printf("Attempting to refresh Copilot token (attempt %d/%d)", attempt, maxRefreshRetries)

		copilotToken, expiresAt, refreshIn, err := getCopilotToken(cfg.GitHubToken)
		if err != nil {
			if attempt == maxRefreshRetries {
				log.Printf("Token refresh failed after %d attempts: %v", maxRefreshRetries, err)
				return err
			}

			// Wait before retry with exponential backoff
			waitTime := time.Duration(baseRetryDelay*attempt*attempt) * time.Second
			log.Printf("Token refresh failed (attempt %d), retrying in %v: %v", attempt, waitTime, err)
			time.Sleep(waitTime)
			continue
		}

		log.Printf("Token refresh successful: new token expires in %d seconds", expiresAt-time.Now().Unix())
		cfg.CopilotToken = copilotToken
		cfg.ExpiresAt = expiresAt
		cfg.RefreshIn = refreshIn

		return saveConfig(cfg)
	}

	return errors.New("maximum retry attempts exceeded")
}
