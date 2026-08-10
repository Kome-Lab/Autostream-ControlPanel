package store

import (
	"testing"
	"time"
)

func TestMemoryDiscordYouTubeLiveNotificationBotDispatchLeaseFencesUnknownDelivery(t *testing.T) {
	streams, notification := testMemoryDiscordYouTubeLiveNotification(t)
	now := time.Now().UTC()
	claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), now, 2*time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim due = %#v, %v", claims, err)
	}
	begun, err := streams.BeginDiscordYouTubeLiveNotificationBotDispatch(t.Context(), claims[0].Notification.ID, claims[0].LeaseToken)
	if err != nil {
		t.Fatalf("begin bot dispatch: %v", err)
	}
	if begun.State != DiscordYouTubeLiveNotificationStateBotDispatching || begun.DispatchAttemptCount != 1 {
		t.Fatalf("unexpected bot dispatch fence: %#v", begun)
	}
	if claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), now.Add(3*time.Minute), 2*time.Minute, 1); err != nil || len(claims) != 0 {
		t.Fatalf("expired bot dispatch must not be re-claimed: claims=%#v err=%v", claims, err)
	}
	fenced, err := streams.FenceExpiredDiscordYouTubeLiveNotificationDispatches(t.Context(), now.Add(3*time.Minute), 1)
	if err != nil || len(fenced) != 1 {
		t.Fatalf("fence expired bot dispatch = %#v, %v", fenced, err)
	}
	if fenced[0].State != DiscordYouTubeLiveNotificationStateDeliveryUnknown || fenced[0].LastError != "discord_dispatch_lease_expired_unknown" {
		t.Fatalf("unexpected fenced notification: %#v", fenced[0])
	}
	if claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), now.Add(4*time.Minute), 2*time.Minute, 1); err != nil || len(claims) != 0 {
		t.Fatalf("delivery_unknown must never auto-retry: claims=%#v err=%v", claims, err)
	}
	recovery, err := streams.CreateDiscordYouTubeLiveNotificationRecovery(t.Context(), notification.StreamID, notification.ID)
	if err != nil {
		t.Fatalf("operator recovery: %v", err)
	}
	if recovery.EventID == notification.EventID || recovery.RecoveryOfID != notification.ID {
		t.Fatalf("recovery must use a new event id and retain its audit parent: original=%#v recovery=%#v", notification, recovery)
	}
}

func TestMemoryDiscordYouTubeLiveNotificationPreBotLeaseCanBeSafelyReclaimed(t *testing.T) {
	streams, _ := testMemoryDiscordYouTubeLiveNotification(t)
	now := time.Now().UTC()
	claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), now, 2*time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("initial claim = %#v, %v", claims, err)
	}
	// A delayed lifecycle lookup plus Bot dispatch still fits inside the two
	// minute lease. A second worker must not take the same event meanwhile.
	if claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), now.Add(75*time.Second), 2*time.Minute, 1); err != nil || len(claims) != 0 {
		t.Fatalf("unexpired pre-Bot lease was re-claimed: claims=%#v err=%v", claims, err)
	}
	if claims, err := streams.ClaimDueDiscordYouTubeLiveNotifications(t.Context(), now.Add(2*time.Minute+time.Second), 2*time.Minute, 1); err != nil || len(claims) != 1 {
		t.Fatalf("expired pre-Bot lease should be safely re-claimable: claims=%#v err=%v", claims, err)
	}
}

func testMemoryDiscordYouTubeLiveNotification(t *testing.T) (*MemoryStreamStore, DiscordYouTubeLiveNotification) {
	t.Helper()
	streams := NewMemoryStreamStore()
	stream, err := streams.CreateStream(t.Context(), "Discord outbox test")
	if err != nil {
		t.Fatal(err)
	}
	_, notification, transitioned, err := streams.TransitionStreamStatusAndEnqueueDiscordYouTubeLiveNotification(t.Context(), stream.ID, "created", "live", DiscordYouTubeLiveNotification{
		WatchURL:             "https://www.youtube.com/watch?v=video_01",
		DiscordServiceID:     "discord-service-01",
		DiscordTextChannelID: "discord-channel-01",
		YouTubeMode:          "stream_key",
	})
	if err != nil || !transitioned {
		t.Fatalf("enqueue transition: transitioned=%t err=%v", transitioned, err)
	}
	return streams, notification
}
