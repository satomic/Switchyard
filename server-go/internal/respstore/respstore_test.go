package respstore

import (
	"testing"
	"time"

	"github.com/satomic/model-router/server-go/internal/omap"
)

func item(id, role, text string) *omap.Map {
	m := omap.New()
	m.Set("id", id)
	m.Set("type", "message")
	m.Set("role", role)
	m.Set("content", text)
	return m
}

func TestResponsesItemsAndOwnership(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	response := omap.New()
	response.Set("id", "resp_0123456789abcdef01234567")
	response.Set("output", []any{item("msg_out00001", "assistant", "hello")})
	s.PutResponse(&Response{
		ID: response.Str("id"), User: "alice", CreatedAt: time.Now(), Response: response,
		InputItems: []any{item("msg_in000001", "user", "hi")},
		Context:    []any{item("msg_in000001", "user", "hi"), item("msg_out00001", "assistant", "hello")},
	})

	got, err := New(dir).GetResponse(response.Str("id"), "alice") // a fresh store: a restart
	if err != nil || len(got.Context) != 2 || got.Context[1].(*omap.Map).Str("content") != "hello" {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := s.GetResponse(response.Str("id"), "bob"); err == nil {
		t.Error("another user must not see alice's response")
	}
	for _, id := range []string{"msg_in000001", "msg_out00001"} {
		if found, err := s.Item(id, "alice"); err != nil || found.Str("id") != id {
			t.Errorf("item %s must be resolvable by id: %v", id, err)
		}
		if _, err := s.Item(id, "bob"); err == nil {
			t.Errorf("item %s must not resolve for another user", id)
		}
	}
	if _, err := s.GetResponse("../../etc/passwd", "alice"); err == nil {
		t.Error("an id that is not an id must never become a path")
	}
	if err := s.DeleteResponse(response.Str("id"), "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetResponse(response.Str("id"), "alice"); err == nil {
		t.Error("a deleted response must be gone")
	}
}

func TestConversations(t *testing.T) {
	s := New(t.TempDir())
	c, err := s.CreateConversation("alice", nil, []any{item("msg_c0000001", "user", "first")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateConversation(c.ID, "alice", func(c *Conversation) {
		c.Items = append(c.Items, item("msg_c0000002", "assistant", "second"))
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConversation(c.ID, "alice")
	if err != nil || len(got.Items) != 2 {
		t.Fatalf("append failed: %v", err)
	}
	if found, err := s.Item("msg_c0000002", "alice"); err != nil || found.Str("content") != "second" {
		t.Errorf("a conversation item must be resolvable by id: %v", err)
	}
	if _, err := s.GetConversation(c.ID, "bob"); err == nil {
		t.Error("another user must not see alice's conversation")
	}
	if err := s.DeleteConversation(c.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetConversation(c.ID, "alice"); err == nil {
		t.Error("a deleted conversation must be gone")
	}
}
