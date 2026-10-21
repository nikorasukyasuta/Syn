package cache

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// ResourceCache maintains a thread-safe, dynamically updating cache mapping Discord
// role and channel IDs to their current server names (and vice-versa).
type ResourceCache struct {
	mu             sync.RWMutex
	rolesByID      map[string]string
	rolesByName    map[string]string // normalized lowercase name -> ID
	channelsByID   map[string]string
	channelsByName map[string]string // normalized lowercase name -> ID
	logger         *slog.Logger
}

// NewResourceCache initializes an empty thread-safe resource cache.
func NewResourceCache(logger *slog.Logger) *ResourceCache {
	return &ResourceCache{
		rolesByID:      make(map[string]string),
		rolesByName:    make(map[string]string),
		channelsByID:   make(map[string]string),
		channelsByName: make(map[string]string),
		logger:         logger,
	}
}

func normalize(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// SetRole stores or updates a role mapping in the cache.
func (c *ResourceCache) SetRole(id, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If the role was previously known under a different name, delete old reverse mapping
	if oldName, exists := c.rolesByID[id]; exists {
		delete(c.rolesByName, normalize(oldName))
	}

	c.rolesByID[id] = name
	c.rolesByName[normalize(name)] = id
}

// RemoveRole removes a role mapping from the cache.
func (c *ResourceCache) RemoveRole(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if name, exists := c.rolesByID[id]; exists {
		delete(c.rolesByName, normalize(name))
		delete(c.rolesByID, id)
	}
}

// GetRoleID returns the Role ID for a given role name (case-insensitive).
func (c *ResourceCache) GetRoleID(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	id, exists := c.rolesByName[normalize(name)]
	return id, exists
}

// GetRoleName returns the current name for a given Role ID.
func (c *ResourceCache) GetRoleName(id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	name, exists := c.rolesByID[id]
	return name, exists
}

// CompareRole checks whether the role with the given ID currently matches the expectedName.
// Returns the actual live name and whether it matches (case-insensitive).
func (c *ResourceCache) CompareRole(id, expectedName string) (actualName string, matches bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	name, exists := c.rolesByID[id]
	if !exists {
		return "", false
	}
	return name, normalize(name) == normalize(expectedName)
}

// ResolveRole resolves an identifier that might be either a Role Name or a Role ID.
// Returns the authoritative ID, live name, and true if found.
func (c *ResourceCache) ResolveRole(nameOrID string) (id string, name string, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check if identifier is an existing Role ID
	if n, ok := c.rolesByID[nameOrID]; ok {
		return nameOrID, n, true
	}

	// Check if identifier is an existing Role Name
	if i, ok := c.rolesByName[normalize(nameOrID)]; ok {
		return i, c.rolesByID[i], true
	}

	return "", "", false
}

// GetAllRoles returns a snapshot copy of all cached roles (ID -> Name).
func (c *ResourceCache) GetAllRoles() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make(map[string]string, len(c.rolesByID))
	for k, v := range c.rolesByID {
		res[k] = v
	}
	return res
}

// SetChannel stores or updates a channel mapping in the cache.
func (c *ResourceCache) SetChannel(id, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if oldName, exists := c.channelsByID[id]; exists {
		delete(c.channelsByName, normalize(oldName))
	}

	c.channelsByID[id] = name
	c.channelsByName[normalize(name)] = id
}

// RemoveChannel removes a channel mapping from the cache.
func (c *ResourceCache) RemoveChannel(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if name, exists := c.channelsByID[id]; exists {
		delete(c.channelsByName, normalize(name))
		delete(c.channelsByID, id)
	}
}

// GetChannelID returns the Channel ID for a given channel name (case-insensitive).
func (c *ResourceCache) GetChannelID(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	id, exists := c.channelsByName[normalize(name)]
	return id, exists
}

// GetChannelName returns the current name for a given Channel ID.
func (c *ResourceCache) GetChannelName(id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	name, exists := c.channelsByID[id]
	return name, exists
}

// CompareChannel checks whether the channel with the given ID currently matches the expectedName.
// Returns the actual live name and whether it matches (case-insensitive).
func (c *ResourceCache) CompareChannel(id, expectedName string) (actualName string, matches bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	name, exists := c.channelsByID[id]
	if !exists {
		return "", false
	}
	return name, normalize(name) == normalize(expectedName)
}

// ResolveChannel resolves an identifier that might be either a Channel Name or a Channel ID.
// Returns the authoritative ID, live name, and true if found.
func (c *ResourceCache) ResolveChannel(nameOrID string) (id string, name string, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if n, ok := c.channelsByID[nameOrID]; ok {
		return nameOrID, n, true
	}

	if i, ok := c.channelsByName[normalize(nameOrID)]; ok {
		return i, c.channelsByID[i], true
	}

	return "", "", false
}

// GetAllChannels returns a snapshot copy of all cached channels (ID -> Name).
func (c *ResourceCache) GetAllChannels() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make(map[string]string, len(c.channelsByID))
	for k, v := range c.channelsByID {
		res[k] = v
	}
	return res
}

// SyncRoles batch-populates or updates roles in the cache.
func (c *ResourceCache) SyncRoles(roles []*discordgo.Role) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range roles {
		if r == nil {
			continue
		}
		if oldName, exists := c.rolesByID[r.ID]; exists {
			delete(c.rolesByName, normalize(oldName))
		}
		c.rolesByID[r.ID] = r.Name
		c.rolesByName[normalize(r.Name)] = r.ID
	}
	c.logger.Info("synchronized guild roles into resource cache", "count", len(roles))
}

// SyncChannels batch-populates or updates channels in the cache.
func (c *ResourceCache) SyncChannels(channels []*discordgo.Channel) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, ch := range channels {
		if ch == nil {
			continue
		}
		if oldName, exists := c.channelsByID[ch.ID]; exists {
			delete(c.channelsByName, normalize(oldName))
		}
		c.channelsByID[ch.ID] = ch.Name
		c.channelsByName[normalize(ch.Name)] = ch.ID
	}
	c.logger.Info("synchronized guild channels into resource cache", "count", len(channels))
}

// SyncGuild batch-populates roles and channels from a Discord Guild object.
func (c *ResourceCache) SyncGuild(guild *discordgo.Guild) {
	if guild == nil {
		return
	}
	c.SyncRoles(guild.Roles)
	c.SyncChannels(guild.Channels)
}

// SyncFromSession queries the Discord REST API to populate the cache with the guild's current state.
func (c *ResourceCache) SyncFromSession(s *discordgo.Session, guildID string) error {
	roles, err := s.GuildRoles(guildID)
	if err != nil {
		return fmt.Errorf("fetching guild roles: %w", err)
	}
	c.SyncRoles(roles)

	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return fmt.Errorf("fetching guild channels: %w", err)
	}
	c.SyncChannels(channels)

	return nil
}

// Live Discord Event Listeners below keep the cache in sync with Discord server mutations.

// HandleGuildRoleCreate dynamically registers a new server role.
func (c *ResourceCache) HandleGuildRoleCreate(_ *discordgo.Session, r *discordgo.GuildRoleCreate) {
	if r == nil || r.Role == nil {
		return
	}
	c.SetRole(r.Role.ID, r.Role.Name)
	c.logger.Debug("resource cache: role created", "role_id", r.Role.ID, "name", r.Role.Name)
}

// HandleGuildRoleUpdate dynamically updates an edited or renamed role.
func (c *ResourceCache) HandleGuildRoleUpdate(_ *discordgo.Session, r *discordgo.GuildRoleUpdate) {
	if r == nil || r.Role == nil {
		return
	}
	c.SetRole(r.Role.ID, r.Role.Name)
	c.logger.Debug("resource cache: role updated", "role_id", r.Role.ID, "name", r.Role.Name)
}

// HandleGuildRoleDelete dynamically unregisters a deleted role.
func (c *ResourceCache) HandleGuildRoleDelete(_ *discordgo.Session, r *discordgo.GuildRoleDelete) {
	if r == nil {
		return
	}
	c.RemoveRole(r.RoleID)
	c.logger.Debug("resource cache: role deleted", "role_id", r.RoleID)
}

// HandleChannelCreate dynamically registers a new server channel.
func (c *ResourceCache) HandleChannelCreate(_ *discordgo.Session, ch *discordgo.ChannelCreate) {
	if ch == nil || ch.Channel == nil {
		return
	}
	c.SetChannel(ch.Channel.ID, ch.Channel.Name)
	c.logger.Debug("resource cache: channel created", "channel_id", ch.Channel.ID, "name", ch.Channel.Name)
}

// HandleChannelUpdate dynamically updates an edited or renamed channel.
func (c *ResourceCache) HandleChannelUpdate(_ *discordgo.Session, ch *discordgo.ChannelUpdate) {
	if ch == nil || ch.Channel == nil {
		return
	}
	c.SetChannel(ch.Channel.ID, ch.Channel.Name)
	c.logger.Debug("resource cache: channel updated", "channel_id", ch.Channel.ID, "name", ch.Channel.Name)
}

// HandleChannelDelete dynamically unregisters a deleted channel.
func (c *ResourceCache) HandleChannelDelete(_ *discordgo.Session, ch *discordgo.ChannelDelete) {
	if ch == nil || ch.Channel == nil {
		return
	}
	c.RemoveChannel(ch.Channel.ID)
	c.logger.Debug("resource cache: channel deleted", "channel_id", ch.Channel.ID)
}

