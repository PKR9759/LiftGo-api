package notification

import (
	"encoding/json"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/utils"
)

type SubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

func SubscribeHandler(pushClient *PushClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetUserFromContext(r)

		var req SubscribeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			utils.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.Endpoint == "" || req.P256dh == "" || req.Auth == "" {
			utils.WriteError(w, http.StatusBadRequest, "missing subscription fields")
			return
		}

		if err := pushClient.SaveSubscription(claims.UserID, req.Endpoint, req.P256dh, req.Auth); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to save subscription")
			return
		}

		utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "subscribed recursively"})
	}
}
