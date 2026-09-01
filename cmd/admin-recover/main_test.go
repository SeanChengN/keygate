package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadMatchingPassword(t *testing.T) {
	responses := [][]byte{[]byte("correct horse battery staple"), []byte("correct horse battery staple")}
	index := 0
	password, err := readMatchingPassword(func(string) ([]byte, error) {
		result := responses[index]
		index++
		return result, nil
	})
	if err != nil {
		t.Fatalf("readMatchingPassword() error = %v", err)
	}
	if string(password) != "correct horse battery staple" {
		t.Fatal("password mismatch")
	}
	clear(password)
}

func TestReadMatchingPasswordRejectsMismatch(t *testing.T) {
	responses := [][]byte{[]byte("correct horse battery staple"), []byte("different horse battery staple")}
	index := 0
	_, err := readMatchingPassword(func(string) ([]byte, error) {
		result := responses[index]
		index++
		return result, nil
	})
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("error = %v, want password mismatch", err)
	}
}

func TestRunRejectsNonRootBeforeReadingSecrets(t *testing.T) {
	called := false
	err := run([]string{"--email", "owner@example.com"}, 100, func(string) string { return "" }, func(string) ([]byte, error) {
		called = true
		return nil, errors.New("unexpected")
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must run as root") {
		t.Fatalf("error = %v, want root requirement", err)
	}
	if called {
		t.Fatal("password reader called for non-root invocation")
	}
}
