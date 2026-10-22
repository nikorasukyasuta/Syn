package ratelimit

import (
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// DefaultAutoDeferDelay is the maximum duration to wait before automatically deferring
// an interaction to satisfy Discord's strict 3-second acknowledgement deadline.
const DefaultAutoDeferDelay = 1200 * time.Millisecond

// Responder wraps a Discord interaction with automatic SLA timeout protection.
// If a handler does not respond within autoDeferDelay, the interaction is automatically
// acknowledged with a deferred response, extending the completion window to 15 minutes.
type Responder struct {
	session     *discordgo.Session
	interaction *discordgo.InteractionCreate
	mu          sync.Mutex
	isDeferred  bool
	hasReplied  bool
	timer       *time.Timer
}

// NewResponder creates a new Responder and starts the anti-timeout protection timer.
func NewResponder(s *discordgo.Session, i *discordgo.InteractionCreate, autoDeferDelay time.Duration) *Responder {
	if autoDeferDelay <= 0 {
		autoDeferDelay = DefaultAutoDeferDelay
	}

	r := &Responder{
		session:     s,
		interaction: i,
	}

	// Auto-defer if the handler hasn't responded before the timeout threshold
	r.timer = time.AfterFunc(autoDeferDelay, func() {
		r.mu.Lock()
		defer r.mu.Unlock()

		if r.hasReplied || r.isDeferred {
			return
		}

		err := r.session.InteractionRespond(r.interaction.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags: discordgo.MessageFlagsEphemeral,
			},
		})
		if err == nil {
			r.isDeferred = true
		}
	})

	return r
}

// Defer immediately acknowledges the interaction as deferred (ephemeral), stopping the timer.
func (r *Responder) Defer() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.timer != nil {
		r.timer.Stop()
	}
	if r.isDeferred || r.hasReplied {
		return nil
	}

	err := r.session.InteractionRespond(r.interaction.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err == nil {
		r.isDeferred = true
	}
	return err
}

// RespondEphemeral sends an ephemeral message. If the interaction was already deferred,
// it seamlessly dispatches as a followup webhook message.
func (r *Responder) RespondEphemeral(content string) error {
	return r.send(content, nil, nil, true)
}

// RespondPublic sends a public message visible to everyone in the channel.
func (r *Responder) RespondPublic(content string) error {
	return r.send(content, nil, nil, false)
}

// RespondEmbed sends an embed. If deferred, it dispatches as a followup.
func (r *Responder) RespondEmbed(embed *discordgo.MessageEmbed, ephemeral bool) error {
	var embeds []*discordgo.MessageEmbed
	if embed != nil {
		embeds = []*discordgo.MessageEmbed{embed}
	}
	return r.send("", embeds, nil, ephemeral)
}

// RespondComplex sends a message with custom embeds and interactive components.
func (r *Responder) RespondComplex(content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, ephemeral bool) error {
	return r.send(content, embeds, components, ephemeral)
}

func (r *Responder) send(content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, ephemeral bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.timer != nil {
		r.timer.Stop()
	}

	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	if r.isDeferred {
		// Interaction was already deferred; send via Followup webhook
		_, err := r.session.FollowupMessageCreate(r.interaction.Interaction, true, &discordgo.WebhookParams{
			Content:    content,
			Embeds:     embeds,
			Components: components,
			Flags:      flags,
		})
		if err != nil {
			return fmt.Errorf("sending followup message after deferral: %w", err)
		}
		r.hasReplied = true
		return nil
	}

	// Send direct immediate response
	err := r.session.InteractionRespond(r.interaction.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Embeds:     embeds,
			Components: components,
			Flags:      flags,
		},
	})
	if err != nil {
		return fmt.Errorf("responding directly to interaction: %w", err)
	}
	r.hasReplied = true
	return nil
}

