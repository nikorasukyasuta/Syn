package handlers_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/discord/handlers"
	"syn/internal/service"
)

func TestTemplateHandler_ExportAndApplyFlow(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "server_template.json")
	devIDsPath := filepath.Join(tmpDir, "discord_ids.dev.json")

	// 1. Production Server Configuration
	prodCfg := &config.Config{
		Environment:         config.EnvironmentProduction,
		GuildID:             "prod_guild_100",
		VerifiedRoleID:      "prod_role_verified",
		IDVerifiedRoleID:    "prod_role_id_verified",
		UnverifiedRoleID:    "prod_role_unverified",
		StaffRoleIDs:        []string{"prod_role_staff"},
		TicketCategoryID:    "prod_cat_tickets",
		TicketLogChannelID:  "prod_ch_ticket_log",
		StaffAlertChannelID: "prod_ch_staff_alert",
		ServerTemplatePath:  templatePath,
		IDsFilePath:         filepath.Join(tmpDir, "discord_ids.json"),
	}

	templateSvc := service.NewServerTemplateService(silentLogger)
	resCache := cache.NewResourceCache(silentLogger)
	prodHandler := handlers.NewTemplateHandler(templateSvc, prodCfg, resCache, silentLogger)

	// Mock Production Discord API state
	var mu sync.Mutex
	var lastEditBody string

	prodSession, _ := discordgo.New("Bot mock_prod_token")
	prodSession.State.User = &discordgo.User{ID: "bot_app_1"}
	prodSession.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()

		path := req.URL.Path
		method := req.Method

		if req.Body != nil {
			b, _ := io.ReadAll(req.Body)
			if strings.Contains(path, "/webhooks/") && method == http.MethodPatch {
				lastEditBody = string(b)
			}
		}

		// Guild metadata
		if method == http.MethodGet && strings.HasSuffix(path, "/guilds/prod_guild_100") {
			return jsonResponse(http.StatusOK, `{"id":"prod_guild_100","name":"Syn Production Server","owner_id":"owner_1"}`), nil
		}

		// Guild roles (including two duplicate-named roles with different colors!)
		if method == http.MethodGet && strings.HasSuffix(path, "/guilds/prod_guild_100/roles") {
			return jsonResponse(http.StatusOK, `[
				{"id":"prod_guild_100","name":"@everyone","position":0,"permissions":"104324673"},
				{"id":"prod_role_unverified","name":"Unverified","color":9807270,"position":1,"permissions":"0"},
				{"id":"prod_role_verified","name":"Verified","color":3066993,"position":2,"permissions":"0"},
				{"id":"prod_role_id_verified","name":"18+ ID Verified","color":15844367,"position":3,"permissions":"0"},
				{"id":"prod_role_dup_red","name":"Switch","color":16711680,"position":4,"permissions":"0"},
				{"id":"prod_role_dup_blue","name":"Switch","color":255,"position":5,"permissions":"0"},
				{"id":"prod_role_staff","name":"Staff","color":10181046,"position":10,"permissions":"8","hoist":true}
			]`), nil
		}

		// Guild channels
		if method == http.MethodGet && strings.HasSuffix(path, "/guilds/prod_guild_100/channels") {
			return jsonResponse(http.StatusOK, `[
				{
					"id":"prod_cat_tickets",
					"guild_id":"prod_guild_100",
					"name":"Support Tickets",
					"type":4,
					"position":1,
					"permission_overwrites":[
						{"id":"prod_guild_100","type":0,"allow":"0","deny":"1024"},
						{"id":"prod_role_staff","type":0,"allow":"1024","deny":"0"}
					]
				},
				{
					"id":"prod_ch_ticket_log",
					"guild_id":"prod_guild_100",
					"parent_id":"prod_cat_tickets",
					"name":"ticket-logs",
					"type":0,
					"position":1,
					"topic":"Audit logs for tickets"
				},
				{
					"id":"prod_ch_staff_alert",
					"guild_id":"prod_guild_100",
					"parent_id":"prod_cat_tickets",
					"name":"staff-alerts",
					"type":0,
					"position":2
				}
			]`), nil
		}

		return jsonResponse(http.StatusOK, `{}`), nil
	})

	// Test A: Non-Admin caller is blocked from /template-export
	nonAdminExport := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_non_admin",
			Token:   "tok_non_admin",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "prod_guild_100",
			Member: &discordgo.Member{
				User:        &discordgo.User{ID: "regular_staff", Username: "mod"},
				Permissions: discordgo.PermissionManageMessages,
				Roles:       []string{"prod_role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "template-export",
			},
		},
	}
	prodHandler.HandleExportCommand(prodSession, nonAdminExport)
	if _, err := os.Stat(templatePath); !os.IsNotExist(err) {
		t.Fatal("expected non-admin caller to be blocked from exporting server template")
	}

	// Test B: Admin caller exports production server template
	adminExport := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_admin_export",
			Token:   "tok_admin_export",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "prod_guild_100",
			Member: &discordgo.Member{
				User:        &discordgo.User{ID: "admin_1", Username: "admin"},
				Permissions: discordgo.PermissionAdministrator,
				Roles:       []string{"prod_role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "template-export",
			},
		},
	}
	prodHandler.HandleExportCommand(prodSession, adminExport)

	loadedTpl, err := templateSvc.LoadTemplate(templatePath)
	if err != nil {
		t.Fatalf("expected server_template.json to be written and loadable, got error: %v", err)
	}
	if loadedTpl.SourceGuildID != "prod_guild_100" {
		t.Fatalf("expected SourceGuildID prod_guild_100, got %s", loadedTpl.SourceGuildID)
	}
	if len(loadedTpl.Roles) != 7 {
		t.Fatalf("expected 7 roles (@everyone + 6 custom roles) in template, got %d", len(loadedTpl.Roles))
	}
	if len(loadedTpl.Categories) != 1 || loadedTpl.Categories[0].Name != "Support Tickets" {
		t.Fatalf("expected 1 category 'Support Tickets', got %+v", loadedTpl.Categories)
	}
	if len(loadedTpl.Channels) != 2 {
		t.Fatalf("expected 2 channels in template, got %d", len(loadedTpl.Channels))
	}
	if loadedTpl.BotMapping.IDVerifiedRoleOriginalID != "prod_role_id_verified" {
		t.Fatalf("expected BotMapping.IDVerifiedRoleOriginalID 'prod_role_id_verified', got %q", loadedTpl.BotMapping.IDVerifiedRoleOriginalID)
	}

	// Test C: /template-apply MUST be blocked when bot is in production mode
	prodHandler.HandleApplyCommand(prodSession, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_apply_in_prod",
			Token:   "tok_apply_in_prod",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "dev_guild_999",
			Member: &discordgo.Member{
				User:        &discordgo.User{ID: "admin_1", Username: "admin"},
				Permissions: discordgo.PermissionAdministrator,
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "template-apply",
			},
		},
	})
	if _, err := os.Stat(devIDsPath); !os.IsNotExist(err) {
		t.Fatal("expected /template-apply to be blocked in production mode")
	}

	// Test D: /template-apply in local DEVELOPMENT mode on a development Discord server
	devCfg := &config.Config{
		Environment:        config.EnvironmentDevelopment,
		GuildID:            "dev_guild_999",
		ServerTemplatePath: templatePath,
		IDsFilePath:        devIDsPath,
	}
	devCache := cache.NewResourceCache(silentLogger)
	devHandler := handlers.NewTemplateHandler(templateSvc, devCfg, devCache, silentLogger)

	createdRoleCounter := 0
	createdChannelCounter := 0

	devSession, _ := discordgo.New("Bot mock_dev_token")
	devSession.State.User = &discordgo.User{ID: "bot_app_1"}
	devSession.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()

		path := req.URL.Path
		method := req.Method

		if req.Body != nil {
			b, _ := io.ReadAll(req.Body)
			bodyStr := string(b)
			if strings.Contains(path, "/webhooks/") && method == http.MethodPatch {
				lastEditBody = bodyStr
			}

			// Creating a role on dev guild
			if method == http.MethodPost && strings.HasSuffix(path, "/guilds/dev_guild_999/roles") {
				createdRoleCounter++
				var params struct {
					Name  string `json:"name"`
					Color int    `json:"color"`
				}
				_ = json.Unmarshal(b, &params)
				newID := fmt.Sprintf("dev_role_%d", createdRoleCounter)
				return jsonResponse(http.StatusOK, fmt.Sprintf(`{"id":%q,"name":%q,"color":%d}`, newID, params.Name, params.Color)), nil
			}

			// Creating a category or channel on dev guild
			if method == http.MethodPost && strings.HasSuffix(path, "/guilds/dev_guild_999/channels") {
				createdChannelCounter++
				var params struct {
					Name     string `json:"name"`
					Type     int    `json:"type"`
					ParentID string `json:"parent_id"`
				}
				_ = json.Unmarshal(b, &params)
				newID := fmt.Sprintf("dev_ch_%d", createdChannelCounter)
				return jsonResponse(http.StatusOK, fmt.Sprintf(`{"id":%q,"guild_id":"dev_guild_999","name":%q,"type":%d,"parent_id":%q}`, newID, params.Name, params.Type, params.ParentID)), nil
			}
		}

		if method == http.MethodGet && strings.HasSuffix(path, "/guilds/dev_guild_999/roles") {
			return jsonResponse(http.StatusOK, `[{"id":"dev_guild_999","name":"@everyone","position":0,"permissions":"104324673"}]`), nil
		}

		if method == http.MethodGet && strings.HasSuffix(path, "/guilds/dev_guild_999/channels") {
			return jsonResponse(http.StatusOK, `[]`), nil
		}

		return jsonResponse(http.StatusOK, `{}`), nil
	})

	devApplyInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_dev_apply",
			Token:   "tok_dev_apply",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "dev_guild_999",
			Member: &discordgo.Member{
				User:        &discordgo.User{ID: "admin_1", Username: "admin"},
				Permissions: discordgo.PermissionAdministrator,
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "template-apply",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "confirm",
						Type:  discordgo.ApplicationCommandOptionBoolean,
						Value: true,
					},
				},
			},
		},
	}

	devHandler.HandleApplyCommand(devSession, devApplyInteraction)

	mu.Lock()
	editBodySnapshot := lastEditBody
	roleCount := createdRoleCounter
	chCount := createdChannelCounter
	mu.Unlock()

	if roleCount != 6 {
		t.Fatalf("expected 6 roles to be created on dev guild, got %d (response: %s)", roleCount, editBodySnapshot)
	}
	if chCount != 3 {
		t.Fatalf("expected 1 category + 2 channels = 3 created on dev guild, got %d", chCount)
	}

	// Verify runtime devCfg was updated with newly created Dev Server IDs
	if devCfg.VerifiedRoleID == "" || devCfg.IDVerifiedRoleID == "" || devCfg.UnverifiedRoleID == "" {
		t.Fatalf("expected devCfg role IDs to be populated after /template-apply, got %+v", devCfg)
	}
	if devCfg.TicketCategoryID == "" || devCfg.TicketLogChannelID == "" || devCfg.StaffAlertChannelID == "" {
		t.Fatalf("expected devCfg channel IDs to be populated after /template-apply, got %+v", devCfg)
	}

	// Verify discord_ids.dev.json was written to disk
	devIDsBytes, err := os.ReadFile(devIDsPath)
	if err != nil {
		t.Fatalf("expected %s to be written, got error: %v", devIDsPath, err)
	}
	if !strings.Contains(string(devIDsBytes), `"environment": "development"`) || !strings.Contains(string(devIDsBytes), `"guild_id": "dev_guild_999"`) {
		t.Fatalf("unexpected discord_ids.dev.json content: %s", string(devIDsBytes))
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     header,
	}
}
