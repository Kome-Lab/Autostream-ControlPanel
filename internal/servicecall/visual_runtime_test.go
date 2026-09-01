package servicecall

import (
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestDiscordResolvedTargetV2PayloadRequiresExactReportedCapability(t *testing.T) {
	baseRequest := StartRequest{
		DiscordGuildID: "1001", DiscordTextChannelID: "1002", DiscordVoiceChannelID: "1003",
		DiscordTargetRevision: 9, WorkerJobGeneration: 4,
	}
	for _, test := range []struct {
		name         string
		capabilities map[string]any
		wantV2       bool
	}{
		{name: "actual true", capabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}, wantV2: true},
		{name: "absent", capabilities: map[string]any{}},
		{name: "false", capabilities: map[string]any{CapabilityDiscordResolvedTargetV2: false}},
		{name: "wrong type", capabilities: map[string]any{CapabilityDiscordResolvedTargetV2: "true"}},
		{name: "version only", capabilities: map[string]any{"service_version": "v2.0.0"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := store.RegisteredService{ServiceType: "discord_bot", ReportedCapabilities: test.capabilities}
			_, value, ok := (Client{}).startPayload(store.Stream{ID: "stream-1"}, service, baseRequest, "https://encoder.example.com", store.RegisteredService{}, time.Unix(1, 0))
			if !ok {
				t.Fatal("Discord start payload was not selected")
			}
			payload := value.(map[string]any)
			_, hasVersion := payload["schema_version"]
			_, hasTarget := payload["discord_target"]
			_, hasGuild := payload["guild_id"]
			_, hasVoice := payload["voice_channel_id"]
			_, hasText := payload["text_channel_id"]
			if test.wantV2 {
				if !hasVersion || !hasTarget || hasGuild || hasVoice || hasText {
					t.Fatalf("v2 payload mixed protocol fields: %#v", payload)
				}
				target := payload["discord_target"].(DiscordTargetSnapshot)
				if target.Revision != 9 || target.Resolved.GuildID != "1001" || target.Resolved.TextChannelID != "1002" || target.Resolved.VoiceChannelID != "1003" {
					t.Fatalf("resolved target mismatch: %#v", target)
				}
				return
			}
			if hasVersion || hasTarget || !hasGuild || !hasVoice || !hasText {
				t.Fatalf("legacy payload mixed protocol fields: %#v", payload)
			}
		})
	}
}

func TestVisualStartFieldsAreNeverSentWithoutActualRuntimeCapability(t *testing.T) {
	request := StartRequest{
		SceneAppearance: &SceneAppearance{Generation: 1, Revision: 1},
		VideoCoverStart: &VideoCoverStartSnapshot{JobGeneration: 1, Revision: 1},
	}
	_, encoderValue, _ := (Client{}).startPayload(store.Stream{ID: "stream-1"}, store.RegisteredService{ServiceType: "encoder_recorder"}, request, "", store.RegisteredService{}, time.Unix(1, 0))
	if _, present := encoderValue.(map[string]any)["video_cover_start"]; present {
		t.Fatal("old Encoder received unknown video_cover_start")
	}
	_, workerValue, _ := (Client{}).startPayload(store.Stream{ID: "stream-1"}, store.RegisteredService{ServiceType: "worker"}, request, "https://encoder.example.com", store.RegisteredService{}, time.Unix(1, 0))
	if _, present := workerValue.(map[string]any)["scene_appearance"]; present {
		t.Fatal("old Worker received unknown scene_appearance")
	}

	encoder := store.RegisteredService{ServiceType: "encoder_recorder", ReportedCapabilities: map[string]any{CapabilityLiveVideoCoverV1: true}}
	_, encoderValue, _ = (Client{}).startPayload(store.Stream{ID: "stream-1"}, encoder, request, "", store.RegisteredService{}, time.Unix(1, 0))
	if _, present := encoderValue.(map[string]any)["video_cover_start"]; !present {
		t.Fatal("new Encoder did not receive negotiated video_cover_start")
	}
	worker := store.RegisteredService{ServiceType: "worker", ReportedCapabilities: map[string]any{CapabilitySceneAppearanceV1: true}}
	_, workerValue, _ = (Client{}).startPayload(store.Stream{ID: "stream-1"}, worker, request, "https://encoder.example.com", store.RegisteredService{}, time.Unix(1, 0))
	if _, present := workerValue.(map[string]any)["scene_appearance"]; !present {
		t.Fatal("new Worker did not receive negotiated scene_appearance")
	}
}

func TestVisualCapabilityAndResolvedTargetMismatchFailBeforeAnyDispatch(t *testing.T) {
	client := Client{}
	services := []store.RegisteredService{
		{ServiceID: "encoder-1", ServiceType: "encoder_recorder"},
		{ServiceID: "worker-1", ServiceType: "worker"},
		{ServiceID: "discord-1", ServiceType: "discord_bot", ReportedCapabilities: map[string]any{CapabilityDiscordResolvedTargetV2: true}},
	}
	result := client.Start(t.Context(), store.Stream{ID: "stream-1"}, services, StartRequest{SceneAppearance: &SceneAppearance{Generation: 1, Revision: 1}})
	if len(result) != 1 || result[0].Code != "scene_appearance_capability_unavailable" || result[0].FailurePhase != "pre_dispatch" {
		t.Fatalf("scene mismatch did not fail before dispatch: %#v", result)
	}
	result = client.Start(t.Context(), store.Stream{ID: "stream-1"}, services, StartRequest{DiscordTargetRevision: 1, DiscordGuildID: "1001", DiscordVoiceChannelID: "1003"})
	if len(result) != 1 || result[0].Code != "discord_target_invalid" || result[0].FailurePhase != "pre_dispatch" {
		t.Fatalf("target mismatch did not fail before dispatch: %#v", result)
	}
}
