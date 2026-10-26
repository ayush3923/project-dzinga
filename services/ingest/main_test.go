package main

import (
	"testing"

	"github.com/ayush3923/project-dzinga/services/ingest/internal/classify"
	"github.com/ayush3923/project-dzinga/services/ingest/internal/source"
)

func TestOwnMessageSkipsSentMail(t *testing.T) {
	sent := source.Message{Account: "me@gmail.com", Message: classify.Message{FromAddr: "Me@Gmail.com", Subject: "Re: Interview schedule"}}
	received := source.Message{Account: "me@gmail.com", Message: classify.Message{FromAddr: "jobs@acme.example"}}
	if !ownMessage(sent) || ownMessage(received) {
		t.Errorf("sent=%v received=%v", ownMessage(sent), ownMessage(received))
	}
}
