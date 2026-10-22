package ratelimit

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type mockRoundTripper struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Header:     make(http.Header),
	}, nil
}

func newMockSession() (*discordgo.Session, *mockRoundTripper) {
	rt := &mockRoundTripper{}
	s, _ := discordgo.New("Bot mock_token")
	s.Client.Transport = rt
	return s, rt
}

func TestResponder_ImmediateResponse(t *testing.T) {
	t.Parallel()

	session, rt := newMockSession()
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_1",
			Token: "tok_1",
		},
	}

	responder := NewResponder(session, interaction, 500*time.Millisecond)
	err := responder.RespondEphemeral("Instant reply")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rt.mu.Lock()
	reqCount := len(rt.requests)
	rt.mu.Unlock()

	if reqCount != 1 {
		t.Fatalf("expected 1 direct response request, got %d", reqCount)
	}
}

func TestResponder_AutoDeferral(t *testing.T) {
	t.Parallel()

	session, rt := newMockSession()
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_2",
			Token: "tok_2",
		},
	}

	// Set a very short auto-defer delay (50ms)
	responder := NewResponder(session, interaction, 50*time.Millisecond)

	// Simulate slow operation: wait 100ms
	time.Sleep(100 * time.Millisecond)

	rt.mu.Lock()
	autoDeferSent := len(rt.requests) == 1
	rt.mu.Unlock()

	if !autoDeferSent {
		t.Fatal("expected auto-defer request to be sent after delay threshold")
	}

	// Now complete the handler
	err := responder.RespondEphemeral("Slow operation finished")
	if err != nil {
		t.Fatalf("unexpected error sending followup: %v", err)
	}

	rt.mu.Lock()
	totalReqs := len(rt.requests)
	rt.mu.Unlock()

	if totalReqs != 2 {
		t.Fatalf("expected 2 requests (auto-defer + followup), got %d", totalReqs)
	}
}

func TestResponder_ExplicitDefer(t *testing.T) {
	t.Parallel()

	session, rt := newMockSession()
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_3",
			Token: "tok_3",
		},
	}

	responder := NewResponder(session, interaction, 1*time.Second)
	if err := responder.Defer(); err != nil {
		t.Fatalf("unexpected defer error: %v", err)
	}

	if err := responder.RespondEphemeral("Followup after explicit defer"); err != nil {
		t.Fatalf("unexpected followup error: %v", err)
	}

	rt.mu.Lock()
	totalReqs := len(rt.requests)
	rt.mu.Unlock()

	if totalReqs != 2 {
		t.Fatalf("expected 2 requests (explicit defer + followup), got %d", totalReqs)
	}
}

