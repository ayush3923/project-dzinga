// Package secrets stores mailbox credentials (app passwords, Microsoft refresh
// tokens) outside the database: in the macOS Keychain when running locally, and
// in AWS Secrets Manager in production. Secrets are never logged or returned by an API.
package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("no saved credential")

type Store interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// Keychain uses the macOS `security` tool. Secrets are written through its
// interactive mode on stdin, so they never appear in a process's arguments.
type Keychain struct {
	Service string // e.g. "dzinga-mail"
}

func valid(s string) error {
	if s == "" || len(s) > 8192 || strings.ContainsAny(s, "\"\\\n\r\x00") {
		return errors.New("credential contains unsupported characters")
	}
	return nil
}

func (k Keychain) Get(account string) (string, error) {
	if err := valid(account); err != nil {
		return "", err
	}
	out, err := exec.Command("security", "find-generic-password", "-s", k.Service, "-a", account, "-w").Output()
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (k Keychain) Set(account, secret string) error {
	if err := valid(account); err != nil {
		return err
	}
	if err := valid(secret); err != nil {
		return err
	}
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s \"%s\" -a \"%s\" -w \"%s\"\n", k.Service, account, secret))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() > 0 {
		return errors.New("could not save to the Keychain") // never echo stderr: it could contain the command
	}
	return nil
}

func (k Keychain) Delete(account string) error {
	if err := valid(account); err != nil {
		return err
	}
	if err := exec.Command("security", "delete-generic-password", "-s", k.Service, "-a", account).Run(); err != nil {
		return ErrNotFound
	}
	return nil
}

// Memory is an in-process store for tests.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func (m *Memory) Get(account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) Set(account, secret string) error {
	if err := valid(secret); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]string{}
	}
	m.m[account] = secret
	return nil
}

func (m *Memory) Delete(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.m[account]; !ok {
		return ErrNotFound
	}
	delete(m.m, account)
	return nil
}
