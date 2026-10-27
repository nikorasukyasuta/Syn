package domain

import "testing"

func TestTicketType_ValidationAndHelpers(t *testing.T) {
	t.Parallel()

	for _, tt := range AllTicketTypes {
		if !tt.IsValid() {
			t.Fatalf("expected %s to be valid", tt)
		}
		if tt.DisplayName() == "" || tt.DisplayName() == "Ticket" {
			t.Fatalf("expected specific DisplayName for %s, got %s", tt, tt.DisplayName())
		}
		if tt.Emoji() == "" {
			t.Fatalf("expected non-empty Emoji for %s", tt)
		}
		if tt.ChannelPrefix() == "" {
			t.Fatalf("expected non-empty ChannelPrefix for %s", tt)
		}

		parsed, err := ParseTicketType(string(tt))
		if err != nil || parsed != tt {
			t.Fatalf("ParseTicketType(%q) failed: got %s, err=%v", tt, parsed, err)
		}
	}

	invalid := TicketType("invalid_type")
	if invalid.IsValid() {
		t.Fatal("expected invalid TicketType to return false")
	}
	if invalid.DisplayName() != "invalid_type" {
		t.Fatalf("expected fallback DisplayName 'invalid_type', got %s", invalid.DisplayName())
	}
	if invalid.Emoji() != "🎫" {
		t.Fatalf("expected fallback Emoji '🎫', got %s", invalid.Emoji())
	}
	if invalid.ChannelPrefix() != "ticket" {
		t.Fatalf("expected fallback ChannelPrefix 'ticket', got %s", invalid.ChannelPrefix())
	}

	if _, err := ParseTicketType("unknown"); err != ErrInvalidTicketType {
		t.Fatalf("expected ErrInvalidTicketType, got %v", err)
	}
}

func TestTicketPanelType_AndDefaultPanelPost(t *testing.T) {
	t.Parallel()

	for idx, pt := range OrderedTicketPanelTypes {
		if !pt.IsValid() {
			t.Fatalf("expected panel type %s to be valid", pt)
		}
		if pt.CustomID() == "" {
			t.Fatalf("expected non-empty CustomID for %s", pt)
		}

		parsed, err := ParseTicketPanelType(string(pt))
		if err != nil || parsed != pt {
			t.Fatalf("ParseTicketPanelType(%q) failed: got %s, err=%v", pt, parsed, err)
		}

		post := DefaultTicketPanelPost("guild_1", pt)
		if post.GuildID != "guild_1" || post.PanelType != pt {
			t.Fatalf("unexpected default post metadata: %+v", post)
		}
		if post.DisplayOrder != idx+1 {
			t.Fatalf("expected DisplayOrder %d for %s, got %d", idx+1, pt, post.DisplayOrder)
		}
		if post.Title == "" || post.Description == "" || post.ButtonLabel == "" {
			t.Fatalf("expected populated default post fields for %s: %+v", pt, post)
		}
	}

	invalidPT := TicketPanelType("bad_panel")
	if invalidPT.IsValid() {
		t.Fatal("expected invalid TicketPanelType to return false")
	}
	if _, err := ParseTicketPanelType("bad_panel"); err != ErrInvalidTicketType {
		t.Fatalf("expected ErrInvalidTicketType, got %v", err)
	}

	fallbackPost := DefaultTicketPanelPost("guild_1", invalidPT)
	if fallbackPost != nil {
		t.Fatalf("expected nil DefaultTicketPanelPost for invalid type, got %+v", fallbackPost)
	}
}
