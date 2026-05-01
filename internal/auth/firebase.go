package auth

import (
	"context"
	"net/http"
	"strings"

	firebaseauth "firebase.google.com/go/v4/auth"

	"chat-ui-go-backend/internal/types"
)

type Middleware struct {
	client *firebaseauth.Client
}

func NewMiddleware(client *firebaseauth.Client) *Middleware {
	return &Middleware{client: client}
}

func (m *Middleware) RequireFirebaseAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			types.WriteError(w, r, http.StatusUnauthorized, "missing bearer token")
			return
		}

		prefix := "Bearer "
		if !strings.HasPrefix(header, prefix) {
			types.WriteError(w, r, http.StatusUnauthorized, "invalid authorization header")
			return
		}

		idToken := strings.TrimSpace(strings.TrimPrefix(header, prefix))
		if idToken == "" {
			types.WriteError(w, r, http.StatusUnauthorized, "empty bearer token")
			return
		}

		// تحقق أمني مهم: السيرفر يقبل Firebase ID Token فقط ولا يثق بأي uid قادم من العميل.
		token, err := m.client.VerifyIDToken(r.Context(), idToken)
		if err != nil {
			types.WriteError(w, r, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		user := types.FirebaseUser{UID: token.UID}
		if email, ok := token.Claims["email"].(string); ok {
			user.Email = email
		}

		ctx := context.WithValue(r.Context(), types.UserContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserFromContext(ctx context.Context) (types.FirebaseUser, bool) {
	user, ok := ctx.Value(types.UserContextKey).(types.FirebaseUser)
	return user, ok
}
