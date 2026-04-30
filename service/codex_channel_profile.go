package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

type CodexChannelProfile struct {
	Email      string
	AccountID  string
	EncodedKey string
	Usage      any
}

func ResolveCodexChannelProfile(ctx context.Context, ch *model.Channel) (*CodexChannelProfile, error) {
	if ch == nil {
		return nil, fmt.Errorf("nil channel")
	}
	if ch.Type != constant.ChannelTypeCodex {
		return nil, fmt.Errorf("channel type is not Codex")
	}
	if ch.ChannelInfo.IsMultiKey {
		return nil, fmt.Errorf("multi-key channel is not supported")
	}

	oauthKey, err := parseCodexOAuthKey(strings.TrimSpace(ch.Key))
	if err != nil {
		return nil, err
	}
	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	if accessToken == "" {
		return nil, fmt.Errorf("codex channel: access_token is required")
	}
	if accountID == "" {
		if extracted, ok := ExtractCodexAccountIDFromJWT(accessToken); ok {
			accountID = extracted
			oauthKey.AccountID = extracted
		}
	}
	if accountID == "" {
		return nil, fmt.Errorf("codex channel: account_id is required")
	}

	client, err := NewProxyHttpClient(ch.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	statusCode, body, err := FetchCodexWhamUsage(fetchCtx, client, ch.GetBaseURL(), accessToken, accountID)
	if err != nil {
		return nil, err
	}

	refreshed := false
	if (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) && strings.TrimSpace(oauthKey.RefreshToken) != "" {
		refreshCtx, refreshCancel := context.WithTimeout(ctx, 10*time.Second)
		defer refreshCancel()
		res, refreshErr := RefreshCodexOAuthTokenWithProxy(refreshCtx, oauthKey.RefreshToken, ch.GetSetting().Proxy)
		if refreshErr != nil {
			return nil, refreshErr
		}

		oauthKey.AccessToken = res.AccessToken
		oauthKey.RefreshToken = res.RefreshToken
		oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
		oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
		if strings.TrimSpace(oauthKey.Type) == "" {
			oauthKey.Type = "codex"
		}
		if extracted, ok := ExtractCodexAccountIDFromJWT(oauthKey.AccessToken); ok {
			oauthKey.AccountID = extracted
			accountID = extracted
		}
		if email, ok := ExtractEmailFromJWT(oauthKey.AccessToken); ok {
			oauthKey.Email = email
		}

		fetchCtx2, cancel2 := context.WithTimeout(ctx, 15*time.Second)
		defer cancel2()
		statusCode, body, err = FetchCodexWhamUsage(fetchCtx2, client, ch.GetBaseURL(), oauthKey.AccessToken, accountID)
		if err != nil {
			return nil, err
		}
		refreshed = true
	}

	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("upstream status: %d", statusCode)
	}

	var payload any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	email := strings.TrimSpace(extractCodexUsageString(payload, "email"))
	if email == "" {
		email = strings.TrimSpace(oauthKey.Email)
	}
	if email == "" {
		if extracted, ok := ExtractEmailFromJWT(oauthKey.AccessToken); ok {
			email = extracted
		}
	}
	if email == "" {
		return nil, fmt.Errorf("codex channel: email is missing")
	}

	payloadAccountID := strings.TrimSpace(extractCodexUsageString(payload, "account_id"))
	if payloadAccountID != "" {
		oauthKey.AccountID = payloadAccountID
		accountID = payloadAccountID
	}
	oauthKey.Email = email
	if strings.TrimSpace(oauthKey.Type) == "" {
		oauthKey.Type = "codex"
	}

	encodedKey := ""
	if refreshed || strings.TrimSpace(oauthKey.Email) != "" || strings.TrimSpace(oauthKey.AccountID) != "" {
		if encoded, err := common.Marshal(oauthKey); err == nil {
			encodedKey = string(encoded)
		}
	}

	return &CodexChannelProfile{
		Email:      email,
		AccountID:  accountID,
		EncodedKey: encodedKey,
		Usage:      payload,
	}, nil
}

func extractCodexUsageString(payload any, key string) string {
	obj, ok := payload.(map[string]any)
	if !ok || obj == nil {
		return ""
	}
	value, ok := obj[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}
