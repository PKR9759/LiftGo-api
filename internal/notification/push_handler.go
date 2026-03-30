package notification

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/utils"
)

type SubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

func SubscribeHandler(pushClient *PushClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
		claims := auth.GetUserFromContext(r)

		slog.Info("SubscribeHandler push entry", "request_id", reqID, "user_id", claims.UserID)

		var req SubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Error("invalid request body in subscription", "error", err, "request_id", reqID)
			utils.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.Endpoint == "" || req.P256dh == "" || req.Auth == "" {
			slog.Warn("missing subscription fields", "request_id", reqID)
			utils.WriteError(w, http.StatusBadRequest, "missing subscription fields")
			return
		}

		if err := pushClient.SaveSubscription(claims.UserID, req.Endpoint, req.P256dh, req.Auth); err != nil {
			slog.Error("failed to save push subscription in handler", "error", err, "request_id", reqID)
			utils.WriteError(w, http.StatusInternalServerError, "failed to save subscription")
			return
		}

		slog.Info("push subscription saved successfully", "user_id", claims.UserID, "request_id", reqID)
		utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "subscribed recursively"})
	}
}
