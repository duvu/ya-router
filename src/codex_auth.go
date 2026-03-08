// codex_auth.go — OpenAI device-code authentication for Codex.
// Uses the same endpoints and client_id as the official Codex CLI:
//   1. POST /api/accounts/deviceauth/usercode → {device_auth_id, user_code}
//   2. User visits auth.openai.com/codex/device → enters code
//   3. Poll /api/accounts/deviceauth/token → {authorization_code, code_verifier}
//   4. Exchange auth code (PKCE) at /oauth/token → {id_token, access_token, refresh_token}
// Tokens are persisted in the project config folder, not ~/.codex.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	codexAuthIssuer    = "https://auth.openai.com"
	codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexUserAgent     = "github-copilot-svcs/1.0"
)

// Step 1 response: device auth user code.
type codexUserCodeResp struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
	ExpiresAt    string `json:"expires_at"`
}

// Step 3 response: authorization code + PKCE from server.
type codexDeviceTokenResp struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

// Step 4 response: OAuth tokens.
type codexOAuthTokenResp struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Refresh response.
type codexRefreshResp struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
}

// codexAuthenticate runs the official Codex device-code flow and
// persists the resulting tokens via save.
func codexAuthenticate(auth *CodexAuthState, save func() error) error {
	now := time.Now().Unix()
	if auth.AccessToken != "" && auth.ExpiresAt > now+60 {
		log.Printf("Codex token still valid: expires in %d seconds",
			auth.ExpiresAt-now)
		return nil
	}
	if auth.AccessToken != "" {
		log.Printf("Codex token expiring soon (%d s), re-authenticating",
			auth.ExpiresAt-now)
	} else {
		log.Printf("No Codex token found, starting authentication")
	}

	// Step 1: request device code via OpenAI's custom endpoint.
	apiBase := codexAuthIssuer + "/api/accounts"
	ucBody := map[string]string{
		"client_id": codexOAuthClientID,
		"scope":     "openid profile email offline_access",
	}
	ucJSON, _ := json.Marshal(ucBody)

	req, err := http.NewRequest("POST", apiBase+"/deviceauth/usercode",
		strings.NewReader(string(ucJSON)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("device code request returned %d: %s",
			resp.StatusCode, string(body))
	}

	var uc codexUserCodeResp
	if err := json.NewDecoder(resp.Body).Decode(&uc); err != nil {
		return err
	}

	interval := 5
	if v, err := parseInt(uc.Interval); err == nil && v > 0 {
		interval = v
	}

	verifyURL := codexAuthIssuer + "/codex/device"
	fmt.Printf("\n"+
		"Follow these steps to sign in with ChatGPT:\n\n"+
		"1. Open this link in your browser and sign in:\n"+
		"   %s\n\n"+
		"2. Enter this one-time code (expires in 15 minutes):\n"+
		"   %s\n\n"+
		"Device codes are a common phishing target. Never share this code.\n\n",
		verifyURL, uc.UserCode)

	// Step 2: poll for authorization code.
	deadline := time.Now().Add(15 * time.Minute)
	var codeResp codexDeviceTokenResp

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		cr, status, pollErr := pollCodexDeviceToken(
			apiBase, uc.DeviceAuthID, uc.UserCode)
		if pollErr != nil {
			return pollErr
		}
		if status == http.StatusForbidden ||
			status == http.StatusNotFound {
			// Authorization pending — user hasn't entered code yet.
			continue
		}
		if status != http.StatusOK {
			return fmt.Errorf("device auth poll returned status %d", status)
		}
		codeResp = *cr
		break
	}
	if codeResp.AuthorizationCode == "" {
		return fmt.Errorf("authentication timed out after 15 minutes")
	}

	// Step 3: exchange authorization code for tokens (with PKCE).
	tokens, err := exchangeCodexAuthCode(
		codeResp.AuthorizationCode, codeResp.CodeVerifier)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	auth.AccessToken = tokens.AccessToken
	auth.RefreshToken = tokens.RefreshToken
	if tokens.ExpiresIn > 0 {
		auth.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	} else {
		auth.ExpiresAt = time.Now().Unix() + 3600
	}

	if err := save(); err != nil {
		return err
	}
	fmt.Println("Codex authentication successful!")
	return nil
}

// pollCodexDeviceToken polls the device auth token endpoint.
// Returns (response, httpStatus, error).
func pollCodexDeviceToken(
	apiBase, deviceAuthID, userCode string,
) (*codexDeviceTokenResp, int, error) {
	body := map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", apiBase+"/deviceauth/token",
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	var cr codexDeviceTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, resp.StatusCode, err
	}
	return &cr, http.StatusOK, nil
}

// exchangeCodexAuthCode exchanges an authorization code for tokens
// using the PKCE code_verifier provided by the device auth server.
func exchangeCodexAuthCode(
	authCode, codeVerifier string,
) (*codexOAuthTokenResp, error) {
	redirectURI := codexAuthIssuer + "/deviceauth/callback"
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {redirectURI},
		"client_id":     {codexOAuthClientID},
		"code_verifier": {codeVerifier},
		"audience":      {"https://api.openai.com/v1"},
	}
	req, err := http.NewRequest("POST", codexAuthIssuer+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", codexUserAgent)

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange returned %d: %s",
			resp.StatusCode, string(body))
	}

	var tokens codexOAuthTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, err
	}
	return &tokens, nil
}

// parseInt parses a string as an integer (for the interval field).
func parseInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

// codexRefreshToken uses the stored refresh_token to obtain a new
// access_token. Retries with exponential backoff.
func codexRefreshToken(auth *CodexAuthState, save func() error) error {
	if auth.RefreshToken == "" {
		return errors.New("no refresh token available for Codex")
	}
	for attempt := 1; attempt <= maxRefreshRetries; attempt++ {
		log.Printf("Refreshing Codex token (attempt %d/%d)",
			attempt, maxRefreshRetries)

		refreshReq := map[string]string{
			"client_id":     codexOAuthClientID,
			"grant_type":    "refresh_token",
			"refresh_token": auth.RefreshToken,
			"audience":      "https://api.openai.com/v1",
		}
		reqJSON, _ := json.Marshal(refreshReq)

		req, err := http.NewRequest("POST",
			codexAuthIssuer+"/oauth/token",
			strings.NewReader(string(reqJSON)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)

		resp, err := sharedHTTPClient.Do(req)
		if err != nil {
			if attempt == maxRefreshRetries {
				return fmt.Errorf("refresh failed after %d attempts: %w",
					maxRefreshRetries, err)
			}
			wait := time.Duration(baseRetryDelay*attempt*attempt) * time.Second
			log.Printf("Refresh failed (attempt %d), retrying in %v: %v",
				attempt, wait, err)
			time.Sleep(wait)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			errMsg := string(body)
			if attempt == maxRefreshRetries {
				return fmt.Errorf("refresh error (status %d): %s",
					resp.StatusCode, errMsg)
			}
			wait := time.Duration(baseRetryDelay*attempt*attempt) * time.Second
			log.Printf("Refresh error (attempt %d, status %d): %s, retrying in %v",
				attempt, resp.StatusCode, errMsg, wait)
			time.Sleep(wait)
			continue
		}

		var rr codexRefreshResp
		if decErr := json.NewDecoder(resp.Body).Decode(&rr); decErr != nil {
			resp.Body.Close()
			return decErr
		}
		resp.Body.Close()

		log.Printf("Codex token refreshed: expires in %d s", rr.ExpiresIn)
		auth.AccessToken = rr.AccessToken
		if rr.RefreshToken != "" {
			auth.RefreshToken = rr.RefreshToken
		}
		if rr.ExpiresIn > 0 {
			auth.ExpiresAt = time.Now().Unix() + rr.ExpiresIn
		} else {
			auth.ExpiresAt = time.Now().Unix() + 3600
		}
		return save()
	}
	return errors.New("maximum retry attempts exceeded")
}
