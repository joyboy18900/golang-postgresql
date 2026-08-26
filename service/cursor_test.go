package service

import (
	"errors"
	"testing"
	"time"

	"golang-postgresql/errs"
	"golang-postgresql/repository"
)

func TestEncodeDecodeCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		cursor repository.AuditLogCursor
	}{
		{
			name:   "typical timestamp and id",
			cursor: repository.AuditLogCursor{CreatedAt: time.UnixMicro(1756201503906123), ID: 42},
		},
		{
			name:   "zero id",
			cursor: repository.AuditLogCursor{CreatedAt: time.UnixMicro(0), ID: 0},
		},
		{
			name:   "large id",
			cursor: repository.AuditLogCursor{CreatedAt: time.UnixMicro(9999999999999999), ID: 9223372036854775807},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeCursor(tt.cursor)

			decoded, err := decodeCursor(encoded)
			if err != nil {
				t.Fatalf("decodeCursor() error = %v", err)
			}

			if !decoded.CreatedAt.Equal(tt.cursor.CreatedAt) {
				t.Errorf("CreatedAt = %v, want %v", decoded.CreatedAt, tt.cursor.CreatedAt)
			}
			if decoded.ID != tt.cursor.ID {
				t.Errorf("ID = %d, want %d", decoded.ID, tt.cursor.ID)
			}
		})
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
	}{
		{name: "malformed base64", encoded: "not-valid-base64!!!"},
		{name: "valid base64 but not a cursor", encoded: "aGVsbG8gd29ybGQ"},
		{name: "valid base64 with only one part", encoded: "MTIz"},
		{name: "empty string", encoded: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeCursor(tt.encoded)
			if err == nil {
				t.Fatal("decodeCursor() error = nil, want an error")
			}

			var appErr errs.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("error = %v, want errs.AppError", err)
			}
			if appErr.Message != "invalid cursor" {
				t.Errorf("message = %q, want %q", appErr.Message, "invalid cursor")
			}
		})
	}
}
