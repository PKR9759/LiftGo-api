// internal/auth/service.go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func (s *Service) Register(ctx context.Context, req RegisterRequest, reqID string) (*AuthResponse, error) {
	if req.Name == "" || req.Email == "" || req.Password == "" {
		return nil, errors.New("name, email and password are required")
	}
	if len(req.Password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if req.Role == "" {
		req.Role = "both"
	}

	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, req.Email,
	).Scan(&exists)
	if err != nil {
		slog.Error("query error checking email existence", "error", err, "request_id", reqID)
		return nil, err
	}
	if exists {
		return nil, errors.New("email already registered")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("password hashing error", "error", err, "request_id", reqID)
		return nil, err
	}

	var id, name, email, role string
	err = s.db.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, phone, role)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, email, role`,
		req.Name, req.Email, string(hash), req.Phone, req.Role,
	).Scan(&id, &name, &email, &role)
	if err != nil {
		slog.Error("insert user error", "error", err, "request_id", reqID)
		return nil, err
	}

	return s.createSession(ctx, id, name, email, role, reqID)
}

func (s *Service) Login(ctx context.Context, req LoginRequest, reqID string) (*AuthResponse, error) {
	if req.Email == "" || req.Password == "" {
		return nil, errors.New("email and password are required")
	}

	var id, name, email, role, passHash string
	err := s.db.QueryRow(ctx,
		`SELECT id, name, email, role, password_hash
		 FROM users WHERE email = $1`, req.Email,
	).Scan(&id, &name, &email, &role, &passHash)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("invalid email or password")
	}
	if err != nil {
		slog.Error("query error fetching user by email", "error", err, "request_id", reqID)
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passHash), []byte(req.Password)); err != nil {
		return nil, errors.New("invalid email or password")
	}

	return s.createSession(ctx, id, name, email, role, reqID)
}

func (s *Service) RefreshSession(ctx context.Context, rawRefresh, reqID string) (*AuthResponse, error) {
	// Hash the raw refresh token to look it up
	hashRaw := sha256.Sum256([]byte(rawRefresh))
	tokenHash := hex.EncodeToString(hashRaw[:])

	var userID, name, email, role string
	var expiresAt time.Time
	var revoked bool

	err := s.db.QueryRow(ctx, `
		SELECT r.user_id, r.expires_at, r.revoked, u.name, u.email, u.role
		FROM refresh_tokens r
		JOIN users u ON r.user_id = u.id
		WHERE r.token_hash = $1
	`, tokenHash).Scan(&userID, &expiresAt, &revoked, &name, &email, &role)

	if errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("refresh token not found", "request_id", reqID)
		return nil, errors.New("invalid refresh token")
	}
	if err != nil {
		slog.Error("db error verifying refresh token", "error", err, "request_id", reqID)
		return nil, err
	}

	if revoked {
		slog.Warn("refresh token revoked", "user_id", userID, "request_id", reqID)
		return nil, errors.New("refresh token revoked")
	}

	if time.Now().After(expiresAt) {
		slog.Warn("refresh token expired", "user_id", userID, "request_id", reqID)
		return nil, errors.New("refresh token expired")
	}

	// Revoke the old token (token rotation)
	_, err = s.db.Exec(ctx, `UPDATE refresh_tokens SET revoked = true WHERE token_hash = $1`, tokenHash)
	if err != nil {
		slog.Error("db error revoking old refresh token", "error", err, "request_id", reqID)
		return nil, err
	}

	// Create a new session
	return s.createSession(ctx, userID, name, email, role, reqID)
}

func (s *Service) Logout(ctx context.Context, rawRefresh string) error {
	hashRaw := sha256.Sum256([]byte(rawRefresh))
	tokenHash := hex.EncodeToString(hashRaw[:])

	_, err := s.db.Exec(ctx, `UPDATE refresh_tokens SET revoked = true WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Service) createSession(ctx context.Context, userID, name, email, role, reqID string) (*AuthResponse, error) {
	// 1. Generate Access Token
	accessToken, err := generateToken(userID, email, role)
	if err != nil {
		slog.Error("token generation error", "error", err, "request_id", reqID)
		return nil, err
	}

	// 2. Generate Refresh Token
	refreshToken, tokenHash := generateRefreshToken()
	expiresAt := time.Now().Add(7 * 24 * time.Hour) // 7 days

	// 3. Store Refresh Token Hash
	_, err = s.db.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)
	`, tokenHash, userID, expiresAt)

	if err != nil {
		slog.Error("failed to store refresh token", "error", err, "request_id", reqID)
		return nil, err
	}

	return &AuthResponse{
		User:         UserPublic{ID: userID, Name: name, Email: email, Role: role},
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func generateToken(userID, email, role string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	claims := Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func ValidateToken(tokenStr string) (*Claims, error) {
	secret := os.Getenv("JWT_SECRET")
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(secret), nil
		},
	)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func generateRefreshToken() (string, string) {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	token := hex.EncodeToString(bytes)

	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])

	return token, tokenHash
}
