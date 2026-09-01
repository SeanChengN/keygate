package handler

import (
	"testing"

	keycrypto "github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
)

func TestVerifyCurrentAdminPassword(t *testing.T) {
	hash, err := keycrypto.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		user     *model.User
		password string
		want     bool
	}{
		{name: "missing user", want: false},
		{name: "non-admin", user: &model.User{Role: model.RoleUser}, want: false},
		{name: "existing admin may establish first password", user: &model.User{Role: model.RoleAdmin}, want: true},
		{name: "existing owner may establish first password", user: &model.User{Role: model.RoleOwner}, want: true},
		{name: "password required after initialization", user: &model.User{Role: model.RoleAdmin, PasswordHash: hash}, want: false},
		{name: "wrong password", user: &model.User{Role: model.RoleAdmin, PasswordHash: hash}, password: "wrong-horse-battery-staple", want: false},
		{name: "correct password", user: &model.User{Role: model.RoleAdmin, PasswordHash: hash}, password: "correct-horse-battery-staple", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifyCurrentAdminPassword(tt.user, tt.password); got != tt.want {
				t.Fatalf("verifyCurrentAdminPassword() = %v, want %v", got, tt.want)
			}
		})
	}
}
