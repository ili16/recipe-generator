package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DeleteKeycloakUser removes the identity itself, so a deleted account cannot be signed back
// into. It uses the backend client's service account (needs manage-users on realm-management);
// without a client secret there is nothing to authenticate with and it reports that.
func (a *Authenticator) DeleteKeycloakUser(ctx context.Context, subject string) error {
	if a.cfg.ClientSecret == "" {
		return fmt.Errorf("KEYCLOAK_CLIENT_SECRET not set")
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {a.cfg.ClientID}, "client_secret": {a.cfg.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.KeycloakURL+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&tok) != nil || tok.AccessToken == "" {
		return fmt.Errorf("service-account token: status %d", resp.StatusCode)
	}

	adminURL := strings.Replace(a.cfg.KeycloakURL, "/realms/", "/admin/realms/", 1) + "/users/" + url.PathEscape(subject)
	del, err := http.NewRequestWithContext(ctx, http.MethodDelete, adminURL, nil)
	if err != nil {
		return err
	}
	del.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	dr, err := a.client.Do(del)
	if err != nil {
		return err
	}
	dr.Body.Close()
	if dr.StatusCode != http.StatusNoContent && dr.StatusCode != http.StatusNotFound {
		return fmt.Errorf("keycloak delete user: status %d", dr.StatusCode)
	}
	return nil
}
